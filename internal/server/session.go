package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/proto"
	"github.com/wormgate/wormgate/internal/streamcipher"
)

// ClientSession represents one authenticated client process. The control
// WebSocket carries JSON; the data WebSocket carries a yamux session. Both are
// required for traffic to flow.
type ClientSession struct {
	ID         string
	Principal  auth.Principal
	ClientName string
	RemoteAddr string
	CreatedAt  time.Time
	Heartbeat  time.Duration
	Server     *Server

	control   *websocket.Conn
	controlMu sync.Mutex

	muxMu sync.RWMutex
	mux   *yamux.Session

	dataOnce  sync.Once
	dataErr   error
	dataReady chan struct{}

	closed      chan struct{}
	closeOnce   sync.Once
	closeReason atomic.Value // string

	tunnelsMu sync.Mutex
	tunnels   map[string]*Registration

	notify   chan proto.NewConnection
	lastSeen atomic.Int64
}

func newClientSession(srv *Server, principal auth.Principal, clientName, remoteAddr string, control *websocket.Conn, heartbeat time.Duration) *ClientSession {
	s := &ClientSession{
		ID:         idgen.Session(),
		Principal:  principal,
		ClientName: clientName,
		RemoteAddr: remoteAddr,
		CreatedAt:  time.Now().UTC(),
		Heartbeat:  heartbeat,
		Server:     srv,
		control:    control,
		dataReady:  make(chan struct{}),
		closed:     make(chan struct{}),
		tunnels:    map[string]*Registration{},
		notify:     make(chan proto.NewConnection, 64),
	}
	s.touch()
	go s.notificationLoop()
	return s
}

// Key uniquely identifies a client identity slot for replacement detection.
func (s *ClientSession) Key() string {
	return s.Principal.UserID + "\x00" + s.ClientName
}

func (s *ClientSession) touch() { s.lastSeen.Store(time.Now().UnixNano()) }

func (s *ClientSession) idleFor() time.Duration {
	return time.Since(time.Unix(0, s.lastSeen.Load()))
}

// LastSeen returns the last heartbeat timestamp.
func (s *ClientSession) LastSeen() time.Time { return time.Unix(0, s.lastSeen.Load()).UTC() }

// DataReady reports whether the yamux channel is attached.
func (s *ClientSession) DataReady() bool {
	select {
	case <-s.dataReady:
		return true
	default:
		return false
	}
}

// Send writes a control message. Writers are serialized because WebSocket
// writes must not interleave.
func (s *ClientSession) Send(msgType string, v any) error {
	if s.control == nil {
		return fmt.Errorf("control channel is closed")
	}
	raw, err := proto.Encode(msgType, v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	return s.control.Write(ctx, websocket.MessageText, raw)
}

// notifyNewConnection is best-effort observability; routing does not depend on
// it, so a full queue merely drops the notification.
func (s *ClientSession) notifyNewConnection(n proto.NewConnection) {
	select {
	case s.notify <- n:
	default:
	}
}

func (s *ClientSession) notificationLoop() {
	for {
		select {
		case n := <-s.notify:
			_ = s.Send(proto.TypeNewConnection, n)
		case <-s.closed:
			return
		}
	}
}

// attachData binds the single yamux session to this control session.
func (s *ClientSession) attachData(conn net.Conn) error {
	s.dataOnce.Do(func() {
		mux, err := yamux.Server(conn, yamuxConfig())
		if err != nil {
			s.dataErr = fmt.Errorf("start yamux server: %w", err)
			return
		}
		s.muxMu.Lock()
		s.mux = mux
		s.muxMu.Unlock()
		close(s.dataReady)
		go func() {
			<-mux.CloseChan()
			s.Close("data channel closed")
		}()
	})
	return s.dataErr
}

func (s *ClientSession) waitData(ctx context.Context) error {
	select {
	case <-s.dataReady:
		return nil
	case <-s.closed:
		return fmt.Errorf("session closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *ClientSession) currentMux() *yamux.Session {
	s.muxMu.RLock()
	defer s.muxMu.RUnlock()
	return s.mux
}

// OpenStream opens a stream and writes the tunnel header. The caller owns the
// returned connection and must close it. A non-nil streamKey seals everything
// after the header; the client decrypts it once it has read the same header.
func (s *ClientSession) OpenStream(ctx context.Context, tunnel, connID, source, kind string, streamKey []byte) (net.Conn, error) {
	mux := s.currentMux()
	if mux == nil {
		return nil, fmt.Errorf("data channel is not ready")
	}
	// SECURITY/NOTES: hashicorp/yamux v0.1.x has no context-aware open, so the
	// open is bounded by racing it against ctx. If ctx wins we still own the
	// stream that eventually appears and must close it to avoid a leak.
	type openResult struct {
		stream *yamux.Stream
		err    error
	}
	opened := make(chan openResult, 1)
	go func() {
		stream, err := mux.OpenStream()
		opened <- openResult{stream: stream, err: err}
	}()
	var stream *yamux.Stream
	select {
	case res := <-opened:
		if res.err != nil {
			return nil, fmt.Errorf("open yamux stream: %w", res.err)
		}
		stream = res.stream
	case <-ctx.Done():
		go func() {
			if res := <-opened; res.stream != nil {
				_ = res.stream.Close()
			}
		}()
		return nil, fmt.Errorf("open yamux stream: %w", ctx.Err())
	}
	_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := proto.WriteStreamHeader(stream, proto.StreamHeader{
		Tunnel: tunnel,
		ConnID: connID,
		Source: source,
		Kind:   kind,
	}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	_ = stream.SetWriteDeadline(time.Time{})
	if len(streamKey) == 0 {
		return stream, nil
	}
	// The header is deliberately sent in the clear: the client needs the
	// tunnel name to pick the right key. Everything after it is sealed.
	sealed, err := streamcipher.NewConn(stream, streamKey, true)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	return sealed, nil
}

// addTunnel records a registration on the session.
func (s *ClientSession) addTunnel(reg *Registration) {
	s.tunnelsMu.Lock()
	s.tunnels[reg.Name] = reg
	s.tunnelsMu.Unlock()
}

// removeTunnel detaches a registration by name.
func (s *ClientSession) removeTunnel(name string) *Registration {
	s.tunnelsMu.Lock()
	defer s.tunnelsMu.Unlock()
	reg := s.tunnels[name]
	delete(s.tunnels, name)
	return reg
}

// AllTunnels returns the session's registrations.
func (s *ClientSession) AllTunnels() []*Registration {
	s.tunnelsMu.Lock()
	defer s.tunnelsMu.Unlock()
	out := make([]*Registration, 0, len(s.tunnels))
	for _, reg := range s.tunnels {
		out = append(out, reg)
	}
	return out
}

// Close tears the session down once. Reason is recorded for audit/alerts.
func (s *ClientSession) Close(reason string) {
	s.closeOnce.Do(func() {
		s.closeReason.Store(reason)
		close(s.closed)
		if s.control != nil {
			s.control.CloseNow()
		}
		if mux := s.currentMux(); mux != nil {
			_ = mux.Close()
		}
		s.Server.onSessionClosed(s, reason)
	})
}

// Kick notifies the client and closes the session.
func (s *ClientSession) Kick(reason string) {
	_ = s.Send(proto.TypeKicked, proto.Kicked{Reason: reason})
	s.Close(reason)
}

// CloseReason returns the recorded close reason.
func (s *ClientSession) CloseReason() string {
	if v := s.closeReason.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 15 * time.Second
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.AcceptBacklog = 512
	return cfg
}
