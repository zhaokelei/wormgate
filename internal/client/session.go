package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/netpipe"
	"github.com/wormgate/wormgate/internal/proto"
	"github.com/wormgate/wormgate/internal/streamcipher"
	"github.com/wormgate/wormgate/internal/tlsutil"
	"github.com/wormgate/wormgate/internal/version"
)

type pendingSpec struct {
	spec proto.TunnelSpec
	at   time.Time
}

// liveSession is one control+data connection pair.
type liveSession struct {
	id      string
	control *websocket.Conn
	mux     *yamux.Session
	sendMu  sync.Mutex
	results chan proto.TunnelResult
	dead    chan error
}

func (s *liveSession) send(msgType string, v any) error {
	raw, err := proto.Encode(msgType, v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if err := s.control.Write(ctx, websocket.MessageText, raw); err != nil {
		return fmt.Errorf("write %s: %w", msgType, err)
	}
	return nil
}

func (s *liveSession) fail(err error) {
	select {
	case s.dead <- err:
	default:
	}
}

// runSession establishes one session and serves it until it fails or a
// reconnect is requested. A nil return means "reconnect immediately".
func (a *Agent) runSession(ctx context.Context) (err error) {
	cfg := a.cfg.Current()
	a.setState(StateConnecting, "")
	tlsCfg, err := tlsutil.ClientConfig(cfg.Client.TLS, serverHost(cfg.Client.Server))
	if err != nil {
		return fmt.Errorf("configure client TLS: %w", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			TLSClientConfig:     tlsCfg,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   false,
			DisableKeepAlives:   true,
		},
	}
	defer httpClient.CloseIdleConnections()

	baseURL := "wss://" + cfg.Client.Server
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Client.Token)
	header.Set("User-Agent", "wormgate/"+version.Version)
	header.Set("X-Wormgate-Client", a.Status().ClientName)

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	control, _, err := websocket.Dial(dialCtx, baseURL+"/control", &websocket.DialOptions{
		HTTPClient:      httpClient,
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("dial control channel %s: %w", cfg.Client.Server, err)
	}
	control.SetReadLimit(64 << 10)
	defer control.CloseNow()

	// SECURITY: the server closes the socket unless the first control frame is
	// a hello within 10s (see internal/server/control.go), so the handshake must
	// be written before we block on the reply. Version is the protocol version,
	// not the build version: the server only warns on a mismatch.
	helloCtx, cancelHello := context.WithTimeout(ctx, 10*time.Second)
	helloRaw, err := proto.Encode(proto.TypeHello, proto.Hello{
		Version:    version.Protocol,
		ClientName: a.Status().ClientName,
	})
	if err != nil {
		cancelHello()
		return fmt.Errorf("encode hello: %w", err)
	}
	if err := control.Write(helloCtx, websocket.MessageText, helloRaw); err != nil {
		cancelHello()
		return fmt.Errorf("write hello: %w", err)
	}
	cancelHello()

	readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
	msgType, raw, err := control.Read(readCtx)
	cancelRead()
	if err != nil {
		return fmt.Errorf("read hello_ok: %w", err)
	}
	if msgType != websocket.MessageText {
		return fmt.Errorf("server sent a non-text handshake")
	}
	var hello proto.HelloOK
	typ, err := proto.Decode(raw, &hello)
	if err != nil {
		return fmt.Errorf("decode hello_ok: %w", err)
	}
	if typ != proto.TypeHelloOK || hello.SessionID == "" {
		return fmt.Errorf("unexpected handshake message %q", typ)
	}

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	dataHeader := header.Clone()
	dataHeader.Set("X-Wormgate-Session", hello.SessionID)
	dataDialCtx, cancelData := context.WithTimeout(sessCtx, 15*time.Second)
	dataWS, _, err := websocket.Dial(dataDialCtx, baseURL+"/data", &websocket.DialOptions{
		HTTPClient:      httpClient,
		HTTPHeader:      dataHeader,
		CompressionMode: websocket.CompressionDisabled,
	})
	cancelData()
	if err != nil {
		return fmt.Errorf("dial data channel: %w", err)
	}
	dataWS.SetReadLimit(8 << 20)
	defer dataWS.CloseNow()
	dataConn := websocket.NetConn(sessCtx, dataWS, websocket.MessageBinary)

	mux, err := yamux.Client(dataConn, clientYamuxConfig())
	if err != nil {
		return fmt.Errorf("start yamux client: %w", err)
	}
	defer mux.Close()

	live := &liveSession{
		id:      hello.SessionID,
		control: control,
		mux:     mux,
		results: make(chan proto.TunnelResult, 32),
		dead:    make(chan error, 1),
	}
	go a.readControl(sessCtx, live)
	go a.acceptStreams(sessCtx, mux, hello.SessionID)
	go func() {
		<-mux.CloseChan()
		live.fail(fmt.Errorf("data channel closed"))
	}()

	registered := map[string]proto.TunnelSpec{}
	pending := map[string]pendingSpec{}
	hbInterval := time.Duration(hello.HeartbeatIntervalSec) * time.Second
	if hbInterval <= 0 {
		hbInterval = cfg.Client.HeartbeatInterval.D()
	}
	if hbInterval < 5*time.Second {
		hbInterval = 5 * time.Second
	}
	heartbeat := time.NewTicker(hbInterval)
	defer heartbeat.Stop()
	retry := time.NewTicker(10 * time.Second)
	defer retry.Stop()

	if _, err := a.reconcile(live, registered, pending); err != nil {
		return err
	}
	a.markConnected(hello.SessionID)
	a.log.Info("connected to server",
		zap.String("server", cfg.Client.Server),
		zap.String("session", hello.SessionID),
		zap.String("client", a.Status().ClientName))

	for {
		select {
		case <-sessCtx.Done():
			return sessCtx.Err()
		case <-a.reconnect:
			a.log.Info("reconnect requested")
			return nil
		case <-a.wake:
			if _, err := a.reconcile(live, registered, pending); err != nil {
				return err
			}
		case res := <-live.results:
			if err := a.handleTunnelResult(live, res, registered, pending); err != nil {
				return err
			}
		case err := <-live.dead:
			return err
		case <-retry.C:
			// Expire registrations the server never acknowledged so they can
			// be retried instead of blocking forever.
			for name, p := range pending {
				if time.Since(p.at) > 20*time.Second {
					delete(pending, name)
					a.log.Warn("tunnel registration timed out", zap.String("tunnel", name))
				}
			}
			if _, err := a.reconcile(live, registered, pending); err != nil {
				return err
			}
		case <-heartbeat.C:
			if err := live.send(proto.TypeHeartbeat, proto.Heartbeat{Timestamp: time.Now().UnixMilli()}); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) readControl(ctx context.Context, live *liveSession) {
	for {
		_, raw, err := live.control.Read(ctx)
		if err != nil {
			live.fail(fmt.Errorf("control channel closed: %w", err))
			return
		}
		typ, err := proto.Decode(raw, nil)
		if err != nil {
			a.log.Warn("invalid control message", zap.Error(err))
			continue
		}
		switch typ {
		case proto.TypeTunnelResult:
			var res proto.TunnelResult
			if _, err := proto.Decode(raw, &res); err != nil {
				a.log.Warn("decode tunnel_result", zap.Error(err))
				continue
			}
			select {
			case live.results <- res:
			case <-ctx.Done():
				return
			}
		case proto.TypeKicked:
			var k proto.Kicked
			_, _ = proto.Decode(raw, &k)
			live.fail(fmt.Errorf("server kicked client: %s", k.Reason))
			return
		case proto.TypePing:
			var p proto.Ping
			_, _ = proto.Decode(raw, &p)
			if err := live.send(proto.TypePong, proto.Pong{Timestamp: p.Timestamp}); err != nil {
				live.fail(err)
				return
			}
		case proto.TypeError:
			var e proto.Error
			_, _ = proto.Decode(raw, &e)
			a.log.Warn("server protocol error", zap.String("code", e.Code), zap.String("message", e.Message))
		case proto.TypeNewConnection:
			var n proto.NewConnection
			_, _ = proto.Decode(raw, &n)
			a.log.Debug("new public connection",
				zap.String("tunnel", n.Tunnel),
				zap.String("conn_id", n.ConnID),
				zap.String("source", n.RemoteAddr))
		}
	}
}

func (a *Agent) reconcile(live *liveSession, registered map[string]proto.TunnelSpec, pending map[string]pendingSpec) (map[string]config.TunnelConfig, error) {
	desiredCfg := a.desiredSpecs()
	desired := make(map[string]proto.TunnelSpec, len(desiredCfg))
	for name, cfg := range desiredCfg {
		desired[name] = toSpec(cfg)
	}
	for name, old := range registered {
		want, ok := desired[name]
		if ok && specEqual(want, old) {
			continue
		}
		if err := live.send(proto.TypeUnregister, proto.Unregister{Names: []string{name}}); err != nil {
			return desiredCfg, err
		}
		delete(registered, name)
		if t := a.findTunnel(name); t != nil {
			t.clearAssignment()
		}
	}
	for name, want := range desired {
		if _, ok := registered[name]; ok {
			continue
		}
		if _, ok := pending[name]; ok {
			continue
		}
		if err := live.send(proto.TypeRegister, proto.Register{Tunnels: []proto.TunnelSpec{want}}); err != nil {
			return desiredCfg, err
		}
		pending[name] = pendingSpec{spec: want, at: time.Now()}
		a.log.Info("registering tunnel", zap.String("tunnel", name), zap.String("type", want.Type))
	}
	return desiredCfg, nil
}

func (a *Agent) handleTunnelResult(live *liveSession, res proto.TunnelResult, registered map[string]proto.TunnelSpec, pending map[string]pendingSpec) error {
	req, wasPending := pending[res.Name]
	delete(pending, res.Name)
	desiredCfg := a.desiredSpecs()
	wantCfg, wanted := desiredCfg[res.Name]
	want := proto.TunnelSpec{}
	if wanted {
		want = toSpec(wantCfg)
	}
	tunnel := a.findTunnel(res.Name)

	if !res.OK {
		if tunnel != nil && wanted {
			tunnel.setError(res.Error)
		}
		a.log.Warn("tunnel registration failed",
			zap.String("tunnel", res.Name),
			zap.String("error", res.Error))
		return nil
	}
	stale := !wasPending || !wanted || !specEqual(req.spec, want)
	if stale {
		if err := live.send(proto.TypeUnregister, proto.Unregister{Names: []string{res.Name}}); err != nil {
			return err
		}
		if tunnel != nil {
			tunnel.clearAssignment()
		}
		return nil
	}
	registered[res.Name] = req.spec
	if tunnel != nil {
		tunnel.setAssignment(res.PublicAddr, res.Subdomain, res.RemotePort)
	}
	a.log.Info("tunnel is live",
		zap.String("tunnel", res.Name),
		zap.String("type", res.Type),
		zap.String("public", res.PublicAddr))
	return nil
}

func (a *Agent) acceptStreams(ctx context.Context, mux *yamux.Session, sessionID string) {
	for {
		stream, err := mux.AcceptStream()
		if err != nil {
			return
		}
		go a.handleStream(ctx, stream, sessionID)
	}
}

func (a *Agent) handleStream(ctx context.Context, stream net.Conn, sessionID string) {
	defer func() { _ = stream.Close() }()
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	header, err := proto.ReadStreamHeader(stream)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil {
		a.log.Warn("invalid tunnel stream header", zap.Error(err))
		return
	}
	if ctx.Err() != nil {
		return
	}
	tunnel := a.findTunnel(header.Tunnel)
	if tunnel == nil {
		a.log.Warn("stream for unknown tunnel", zap.String("tunnel", header.Tunnel))
		return
	}
	cfg := tunnel.config()
	if !cfg.IsEnabled() {
		return
	}
	// The header is plaintext by design (the client needs the tunnel name to
	// select a key); the payload is sealed from here on.
	payload := stream
	if cfg.Encryption {
		key, err := streamcipher.DeriveKey(a.cfg.Current().Client.StreamSecret, sessionID, cfg.Name)
		if err != nil {
			a.log.Error("cannot derive stream key",
				zap.String("tunnel", cfg.Name),
				zap.Error(err))
			return
		}
		sealed, err := streamcipher.NewConn(stream, key, false)
		if err != nil {
			a.log.Error("cannot start stream cipher",
				zap.String("tunnel", cfg.Name),
				zap.Error(err))
			return
		}
		payload = sealed
	}
	timeout := a.cfg.Current().Client.DialTimeout.D()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	local, err := net.DialTimeout("tcp", cfg.Local, timeout)
	if err != nil {
		a.log.Warn("local dial failed",
			zap.String("tunnel", cfg.Name),
			zap.String("local", cfg.Local),
			zap.String("conn_id", header.ConnID),
			zap.Error(err))
		return
	}
	defer func() { _ = local.Close() }()

	info := &ConnInfo{
		ID:        header.ConnID,
		Tunnel:    cfg.Name,
		Source:    header.Source,
		Local:     cfg.Local,
		StartedAt: time.Now().UTC(),
	}
	a.conns.add(info)
	tunnel.connections.Add(1)
	tunnel.active.Add(1)
	metered := &clientMeteredConn{Conn: payload, agent: a, tunnel: tunnel, conn: info}
	defer func() { _ = metered.Close() }()

	a.log.Debug("tunnel stream established",
		zap.String("conn_id", header.ConnID),
		zap.String("tunnel", cfg.Name),
		zap.String("source", header.Source),
		zap.String("local", cfg.Local))
	netpipe.Pipe(local, metered)
}

func toSpec(cfg config.TunnelConfig) proto.TunnelSpec {
	spec := proto.TunnelSpec{
		Name:       cfg.Name,
		Type:       cfg.Type,
		Local:      cfg.Local,
		Subdomain:  cfg.Subdomain,
		RemotePort: cfg.RemotePort,
	}
	if cfg.Encryption {
		spec.Encryption = streamcipher.Scheme
	}
	return spec
}

func specEqual(a, b proto.TunnelSpec) bool {
	return a.Name == b.Name && a.Type == b.Type && a.Local == b.Local &&
		a.Subdomain == b.Subdomain && a.RemotePort == b.RemotePort &&
		a.Encryption == b.Encryption
}

func serverHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return strings.Trim(host, "[]")
}

func clientYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 15 * time.Second
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.AcceptBacklog = 512
	return cfg
}
