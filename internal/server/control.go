package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/proto"
	"github.com/wormgate/wormgate/internal/version"
)

const (
	controlReadLimit = 64 << 10
	dataReadLimit    = 8 << 20
)

func (s *Server) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control", s.handleControl)
	mux.HandleFunc("/data", s.handleData)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
	})
	return mux
}

// authenticate extracts and validates the bearer token. Tokens are compared
// via their SHA-256 keys, never in plaintext loops.
func (s *Server) authenticate(r *http.Request) (*auth.Principal, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return nil, auth.ErrUnauthorized
	}
	return s.users.AuthenticateToken(strings.TrimSpace(token))
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		// A stale token is the single most common cause of "all my tunnels are
		// gone", and the client only ever sees a 401 in its own log. Surface it
		// once per source address so the operator knows where to look.
		s.alertOnce("auth:"+requestIP(r), "warning", "client_auth_failed",
			fmt.Sprintf("客户端认证被拒绝：%s 的令牌无效或账号已停用。检查该客户端的令牌是否已被轮换（管理台「用户与租户 → 查看令牌」可核对），或账号是否被停用。", requestIP(r)))
		writeError(w, http.StatusUnauthorized, "invalid client token")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.log.Warn("control websocket accept failed", zap.Error(err), zap.String("remote", r.RemoteAddr))
		return
	}
	ws.SetReadLimit(controlReadLimit)

	readCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	msgType, data, err := ws.Read(readCtx)
	cancel()
	if err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	if msgType != websocket.MessageText {
		_ = ws.Close(websocket.StatusUnsupportedData, "control messages must be text JSON")
		return
	}
	var hello proto.Hello
	typ, err := proto.Decode(data, &hello)
	if err != nil || typ != proto.TypeHello {
		_ = ws.Close(websocket.StatusPolicyViolation, "first message must be hello")
		return
	}
	if hello.Version != "" && hello.Version != version.Protocol {
		s.log.Warn("client protocol version differs",
			zap.String("client_version", hello.Version),
			zap.String("server_protocol", version.Protocol))
	}

	clientName := sanitizeLabel(hello.ClientName)
	if clientName == "" {
		clientName = "client-" + idgen.Hex(3)
	}
	heartbeat := s.cfg.Current().Server.HeartbeatInterval.D()
	if heartbeat < 5*time.Second {
		heartbeat = 5 * time.Second
	}
	sess := newClientSession(s, *principal, clientName, r.RemoteAddr, ws, heartbeat)
	s.addSession(sess)
	defer sess.Close("control channel closed")

	if err := sess.Send(proto.TypeHelloOK, proto.HelloOK{
		SessionID:            sess.ID,
		ClientID:             sess.Key(),
		ServerVersion:        version.Version,
		HeartbeatIntervalSec: int(heartbeat.Seconds()),
	}); err != nil {
		s.log.Warn("send hello_ok failed", zap.Error(err))
		return
	}
	s.log.Info("client connected",
		zap.String("client", clientName),
		zap.String("owner", principal.Username),
		zap.String("session", sess.ID),
		zap.String("remote", r.RemoteAddr))
	if _, err := s.audit.Add(principal.Username, principal.UserID, "client.connect", clientName, "", r.RemoteAddr, "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}

	for {
		_, raw, err := ws.Read(context.Background())
		if err != nil {
			break
		}
		sess.touch()
		typ, err := proto.Decode(raw, nil)
		if err != nil {
			_ = sess.Send(proto.TypeError, proto.Error{Code: "bad_message", Message: err.Error()})
			continue
		}
		switch typ {
		case proto.TypeHeartbeat:
			// touch above is the heartbeat's real effect.
		case proto.TypeRegister:
			var payload proto.Register
			if _, err := proto.Decode(raw, &payload); err != nil {
				_ = sess.Send(proto.TypeError, proto.Error{Code: "bad_register", Message: err.Error()})
				continue
			}
			if len(payload.Tunnels) > 32 {
				_ = sess.Send(proto.TypeError, proto.Error{Code: "too_many_tunnels", Message: "at most 32 tunnels per register message"})
				continue
			}
			s.processRegister(sess, payload.Tunnels)
		case proto.TypeUnregister:
			var payload proto.Unregister
			if _, err := proto.Decode(raw, &payload); err != nil {
				_ = sess.Send(proto.TypeError, proto.Error{Code: "bad_unregister", Message: err.Error()})
				continue
			}
			for _, name := range payload.Names {
				s.unregisterTunnel(sess, name)
			}
		case proto.TypePing:
			var payload proto.Ping
			_, _ = proto.Decode(raw, &payload)
			_ = sess.Send(proto.TypePong, proto.Pong{Timestamp: payload.Timestamp})
		case proto.TypePong:
			// Liveness already updated.
		default:
			_ = sess.Send(proto.TypeError, proto.Error{Code: "unsupported", Message: "unsupported message type " + typ})
		}
	}
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid client token")
		return
	}
	sessionID := strings.TrimSpace(r.Header.Get("X-Wormgate-Session"))
	if sessionID == "" {
		// Compatibility: clients built before the Wormgate rename still send
		// the old header name. Accept it so a server upgrade does not force
		// every client to restart in lockstep.
		sessionID = strings.TrimSpace(r.Header.Get("X-TunnelX-Session"))
	}
	sess := s.sessionByID(sessionID)
	if sess == nil {
		writeError(w, http.StatusNotFound, "unknown control session")
		return
	}
	if sess.Principal.UserID != principal.UserID {
		writeError(w, http.StatusForbidden, "token does not own this session")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(dataReadLimit)
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	if err := sess.attachData(conn); err != nil {
		s.log.Warn("attach data channel failed", zap.Error(err), zap.String("session", sess.ID))
		_ = ws.Close(websocket.StatusPolicyViolation, "data channel already attached")
		return
	}
	s.log.Info("data channel attached",
		zap.String("client", sess.ClientName),
		zap.String("session", sess.ID))
	<-sess.closed
	_ = conn.Close()
	_ = ws.CloseNow()
}

func (s *Server) processRegister(sess *ClientSession, specs []proto.TunnelSpec) {
	if len(specs) == 0 {
		return
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	err := sess.waitData(waitCtx)
	cancel()
	for _, spec := range specs {
		var result proto.TunnelResult
		if err != nil {
			result = proto.TunnelResult{
				Name:  spec.Name,
				Type:  spec.Type,
				OK:    false,
				Error: "data channel is not ready; try again",
			}
		} else {
			result = s.registerTunnel(sess, spec)
		}
		if sendErr := sess.Send(proto.TypeTunnelResult, result); sendErr != nil {
			s.log.Warn("send tunnel result failed", zap.Error(sendErr))
			return
		}
	}
}
