package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httputil"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/alerts"
	"github.com/wormgate/wormgate/internal/audit"
	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/logx"
	"github.com/wormgate/wormgate/internal/sysinfo"
	"github.com/wormgate/wormgate/internal/tlsutil"
	"github.com/wormgate/wormgate/internal/version"
	"github.com/wormgate/wormgate/internal/webui"
)

// Server is the Wormgate control-plane and data-plane service.
type Server struct {
	cfg     *config.Manager
	log     *logx.Logger
	users   *auth.Store
	audit   *audit.Log
	alerts  *alerts.Store
	metrics *Metrics

	registry *Registry
	stats    *Stats
	usage    *Usage

	tlsCfg    *tls.Config
	transport *http.Transport
	proxy     *httputil.ReverseProxy

	bootstrap    *auth.Bootstrap
	adminLimiter *webui.LoginLimiter

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	sessionsMu sync.RWMutex
	sessions   map[string]*ClientSession
	byClient   map[string]*ClientSession

	// regMu serializes subdomain/port assignment and registry mutation.
	regMu sync.Mutex

	totalIn      atomic.Uint64
	totalOut     atomic.Uint64
	httpRequests atomic.Uint64
	startedAt    time.Time

	// sys reports host CPU/memory for the console's node panel. It polls in the
	// background so a request never blocks on /proc.
	sys *sysinfo.Sampler

	// alertMu guards the de-duplication table for repeated failures that would
	// otherwise flood the alert centre once per retry.
	alertMu   sync.Mutex
	alertedAt map[string]time.Time

	addrMu          sync.RWMutex
	controlAddr     string
	httpAddr        string
	adminAddr       string
	metricsAddr     string
	controlServer   *http.Server
	publicServer    *http.Server
	adminServer     *http.Server
	metricsServer   *http.Server
	controlListener net.Listener
	publicListener  net.Listener
	adminListener   net.Listener
	metricsListener net.Listener
}

// New constructs the server. Network listeners are opened by Run so startup
// errors are reported to a single caller.
func New(mgr *config.Manager, logger *logx.Logger) (*Server, error) {
	cfg := mgr.Current()
	dataDir := cfg.Server.DataDir
	auditLog, err := audit.Open(filepath.Join(dataDir, "audit.jsonl"), 2000)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	users, boot, err := auth.Open(auth.OpenOptions{
		DataDir:           dataDir,
		AdminUser:         cfg.Server.WebUI.AdminUser,
		AdminPassword:     cfg.Server.WebUI.AdminPassword,
		AdminPasswordHash: cfg.Server.WebUI.AdminPasswordHash,
		Defaults:          cfg.Server.Quota,
		StaticTokens:      cfg.Server.Auth.Tokens,
	})
	if err != nil {
		return nil, fmt.Errorf("open user store: %w", err)
	}
	tlsCfg, err := tlsutil.ServerConfig(cfg.Server.TLS, []string{cfg.Server.HTTP.Domain})
	if err != nil {
		return nil, fmt.Errorf("configure TLS: %w", err)
	}
	s := &Server{
		cfg:          mgr,
		log:          logger,
		users:        users,
		audit:        auditLog,
		alerts:       alerts.New(200),
		metrics:      newMetrics(),
		registry:     newRegistry(),
		stats:        newStats(),
		usage:        newUsage(),
		tlsCfg:       tlsCfg,
		bootstrap:    boot,
		adminLimiter: webui.NewLoginLimiter(10, time.Minute),
		sessions:     map[string]*ClientSession{},
		byClient:     map[string]*ClientSession{},
		startedAt:    time.Now(),
		sys:          sysinfo.NewSampler(2 * time.Second),
	}
	s.sys.Start()
	s.transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           s.dialTunnel,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		ExpectContinueTimeout: time.Second,
	}
	s.proxy = &httputil.ReverseProxy{
		Director:       s.directProxy,
		Transport:      s.transport,
		FlushInterval:  100 * time.Millisecond,
		ErrorHandler:   s.proxyError,
		ModifyResponse: s.proxyResponse,
	}
	mgr.Subscribe(s.applyConfig)

	if boot.GeneratedAdminPassword != "" {
		// SECURITY: one-time credential delivery; printed once and never
		// persisted in plaintext.
		logger.Warn("generated initial admin password - store it now",
			zap.String("username", boot.AdminUsername),
			zap.String("password", boot.GeneratedAdminPassword),
			zap.String("note", "change it after first login"))
	}
	if boot.ClientToken != "" {
		logger.Info("demo client token (user: demo)",
			zap.String("token", boot.ClientToken),
			zap.Bool("reprinted", boot.TokenReprinted),
			zap.String("note", "rotate or create per-user tokens in the admin console"))
		// Usability: give the operator a command they can paste on the client
		// machine instead of having to write a config file first.
		addr := "127.0.0.1:7000"
		if h, p, err := net.SplitHostPort(cfg.Server.ControlAddr); err == nil {
			if h == "" || h == "0.0.0.0" || h == "::" {
				h = "127.0.0.1"
			}
			addr = net.JoinHostPort(h, p)
		}
		logger.Info("quick start - run this on the client machine",
			zap.String("command", fmt.Sprintf(
				"fpc --server %s --token %s --insecure --tunnel web=http://127.0.0.1:8080",
				addr, boot.ClientToken)),
			zap.String("note", "replace the host with this server's reachable IP; drop --insecure once a trusted certificate is configured"))
	}
	return s, nil
}

// applyConfig is the hot-reload subscriber. Listener-level changes cannot be
// applied in place; returning an error rolls the whole configuration back.
func (s *Server) applyConfig(old, next *config.Config) error {
	if reason := restartReason(old, next); reason != "" {
		return fmt.Errorf("%s changes require a restart", reason)
	}
	if err := s.log.SetLevel(next.Log.Level); err != nil {
		return err
	}
	s.users.SetStaticTokens(next.Server.Auth.Tokens)
	s.users.SetDefaults(next.Server.Quota)
	s.log.Info("configuration reloaded",
		zap.String("level", next.Log.Level),
		zap.Int("static_tokens", len(next.Server.Auth.Tokens)))
	return nil
}

func restartReason(old, next *config.Config) string {
	o, n := old.Server, next.Server
	switch {
	case o.ControlAddr != n.ControlAddr:
		return "server.control_addr"
	case o.HTTP.Addr != n.HTTP.Addr || o.HTTP.PublicPort != n.HTTP.PublicPort || o.HTTP.Scheme != n.HTTP.Scheme || o.HTTP.Domain != n.HTTP.Domain:
		return "server.http listener/domain"
	case o.TCP.BindAddr != n.TCP.BindAddr || o.TCP.PortRange != n.TCP.PortRange:
		return "server.tcp listener/range"
	case o.WebUI.Enabled != n.WebUI.Enabled || o.WebUI.Addr != n.WebUI.Addr:
		return "server.webui listener"
	case o.Metrics.Enabled != n.Metrics.Enabled || o.Metrics.Addr != n.Metrics.Addr:
		return "server.metrics listener"
	case o.TLS != n.TLS:
		return "server.tls"
	case o.DataDir != n.DataDir:
		return "server.data_dir"
	case o.WebUI.AdminUser != n.WebUI.AdminUser || o.WebUI.AdminPassword != n.WebUI.AdminPassword || o.WebUI.AdminPasswordHash != n.WebUI.AdminPasswordHash:
		return "server.webui admin credentials"
	}
	return ""
}

// Run starts listeners and blocks until ctx ends or a fatal serve error
// occurs.
func (s *Server) Run(parent context.Context) error {
	s.ctx, s.cancel = context.WithCancel(parent)
	defer s.cancel()
	if err := s.startListeners(); err != nil {
		s.shutdown()
		return err
	}
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.stats.Run(s.ctx, s.registry.List)
	}()
	go func() {
		defer s.wg.Done()
		s.janitor()
	}()

	s.log.Info("Wormgate server is running",
		zap.String("version", version.Version),
		zap.String("control", s.ControlAddr()),
		zap.String("http_ingress", s.HTTPAddr()),
		zap.String("admin", s.adminURL()),
		zap.String("metrics", s.MetricsAddr()))

	errCh := make(chan error, 4)
	serve := func(name string, srv *http.Server, ln net.Listener) {
		if srv == nil || ln == nil {
			return
		}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s listener: %w", name, err)
			}
		}()
	}
	serve("control", s.controlServer, s.controlListener)
	serve("http", s.publicServer, s.publicListener)
	serve("admin", s.adminServer, s.adminListener)
	serve("metrics", s.metricsServer, s.metricsListener)

	var runErr error
	select {
	case <-parent.Done():
		s.log.Info("shutdown requested")
	case err := <-errCh:
		runErr = err
		s.log.Error("server listener failed", zap.Error(err))
	}
	s.shutdown()
	return runErr
}

func (s *Server) startListeners() error {
	cfg := s.cfg.Current()

	controlLn, err := net.Listen("tcp", cfg.Server.ControlAddr)
	if err != nil {
		return fmt.Errorf("listen control %s: %w", cfg.Server.ControlAddr, err)
	}
	tlsLn := tls.NewListener(controlLn, s.tlsCfg)

	httpLn, err := net.Listen("tcp", cfg.Server.HTTP.Addr)
	if err != nil {
		_ = controlLn.Close()
		return fmt.Errorf("listen http %s: %w", cfg.Server.HTTP.Addr, err)
	}

	s.addrMu.Lock()
	s.controlListener = tlsLn
	s.publicListener = httpLn
	s.controlAddr = controlLn.Addr().String()
	s.httpAddr = httpLn.Addr().String()
	s.controlServer = &http.Server{
		Handler:           s.controlHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          s.stdLogger(),
		BaseContext:       func(net.Listener) context.Context { return s.ctx },
	}
	s.publicServer = &http.Server{
		Handler:           s.publicHandler(),
		ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout.D(),
		IdleTimeout:       cfg.Server.HTTP.IdleTimeout.D(),
		ErrorLog:          s.stdLogger(),
		BaseContext:       func(net.Listener) context.Context { return s.ctx },
	}
	s.addrMu.Unlock()

	if cfg.Server.WebUI.Enabled {
		adminLn, err := net.Listen("tcp", cfg.Server.WebUI.Addr)
		if err != nil {
			_ = controlLn.Close()
			_ = httpLn.Close()
			return fmt.Errorf("listen admin %s: %w", cfg.Server.WebUI.Addr, err)
		}
		s.addrMu.Lock()
		s.adminListener = adminLn
		s.adminAddr = adminLn.Addr().String()
		s.adminServer = &http.Server{
			Handler:           s.adminHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
			ErrorLog:          s.stdLogger(),
			BaseContext:       func(net.Listener) context.Context { return s.ctx },
		}
		s.addrMu.Unlock()
	}
	if cfg.Server.Metrics.Enabled {
		metricsLn, err := net.Listen("tcp", cfg.Server.Metrics.Addr)
		if err != nil {
			_ = controlLn.Close()
			_ = httpLn.Close()
			if s.adminListener != nil {
				_ = s.adminListener.Close()
			}
			return fmt.Errorf("listen metrics %s: %w", cfg.Server.Metrics.Addr, err)
		}
		mux := http.NewServeMux()
		mux.Handle("/metrics", s.metrics.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		s.addrMu.Lock()
		s.metricsListener = metricsLn
		s.metricsAddr = metricsLn.Addr().String()
		s.metricsServer = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ErrorLog: s.stdLogger()}
		s.addrMu.Unlock()
	}
	return nil
}

func (s *Server) shutdown() {
	if s.cancel != nil {
		s.cancel()
	}
	for _, sess := range s.sessionSnapshot() {
		sess.Kick("server shutting down")
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.addrMu.Lock()
	servers := []*http.Server{s.controlServer, s.publicServer, s.adminServer, s.metricsServer}
	listeners := []net.Listener{s.controlListener, s.publicListener, s.adminListener, s.metricsListener}
	s.addrMu.Unlock()
	for _, srv := range servers {
		if srv != nil {
			_ = srv.Shutdown(shCtx)
		}
	}
	for _, ln := range listeners {
		if ln != nil {
			_ = ln.Close()
		}
	}
	s.transport.CloseIdleConnections()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-shCtx.Done():
	}
}

// WaitReady waits until listeners are bound (useful for tests and probes).
func (s *Server) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.ControlAddr() != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ControlAddr returns the bound control-plane address.
func (s *Server) ControlAddr() string {
	s.addrMu.RLock()
	defer s.addrMu.RUnlock()
	return s.controlAddr
}

// HTTPAddr returns the bound public HTTP address.
func (s *Server) HTTPAddr() string { s.addrMu.RLock(); defer s.addrMu.RUnlock(); return s.httpAddr }

// AdminAddr returns the bound admin console address.
func (s *Server) AdminAddr() string { s.addrMu.RLock(); defer s.addrMu.RUnlock(); return s.adminAddr }

// MetricsAddr returns the bound metrics address.
func (s *Server) MetricsAddr() string {
	s.addrMu.RLock()
	defer s.addrMu.RUnlock()
	return s.metricsAddr
}

func (s *Server) adminURL() string {
	addr := s.AdminAddr()
	if addr == "" {
		return "disabled"
	}
	// A wildcard bind is not a usable browser URL. Show a placeholder so the
	// startup log tells operators to use the machine's reachable IP/hostname.
	if host, port, err := net.SplitHostPort(addr); err == nil &&
		(host == "" || host == "0.0.0.0" || host == "::") {
		return "http://<server-ip>:" + port + "/admin/"
	}
	return "http://" + addr + "/admin/"
}

func (s *Server) stdLogger() *stdlog.Logger {
	return stdlog.New(zapWriter{s.log}, "", 0)
}

type zapWriter struct{ log *logx.Logger }

func (w zapWriter) Write(p []byte) (int, error) {
	w.log.Warn("http server", zap.String("detail", strings.TrimSpace(string(p))))
	return len(p), nil
}

func (s *Server) janitor() {
	interval := s.cfg.Current().Server.HeartbeatInterval.D()
	if interval <= 0 {
		interval = 15 * time.Second
	}
	timeout := s.cfg.Current().Server.SessionTimeout.D()
	if timeout <= interval {
		timeout = 3 * interval
	}
	ticker := time.NewTicker(interval / 2)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			for _, sess := range s.sessionSnapshot() {
				if sess.idleFor() > timeout {
					s.log.Warn("closing idle control session",
						zap.String("client", sess.ClientName),
						zap.String("session", sess.ID),
						zap.Duration("idle", sess.idleFor()))
					sess.Kick("heartbeat timeout")
				}
			}
		}
	}
}

func (s *Server) sessionSnapshot() []*ClientSession {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	out := make([]*ClientSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

func (s *Server) sessionByID(id string) *ClientSession {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[id]
}

func (s *Server) addSession(sess *ClientSession) *ClientSession {
	s.sessionsMu.Lock()
	old := s.byClient[sess.Key()]
	s.sessions[sess.ID] = sess
	s.byClient[sess.Key()] = sess
	s.sessionsMu.Unlock()

	s.metrics.controlSessions.Inc()
	s.metrics.clientSessions.Inc()
	if old != nil && old.ID != sess.ID {
		old.Kick("replaced by a newer session with the same client name")
	}
	return old
}

func (s *Server) onSessionClosed(sess *ClientSession, reason string) {
	s.sessionsMu.Lock()
	if s.sessions[sess.ID] == sess {
		delete(s.sessions, sess.ID)
	}
	if s.byClient[sess.Key()] == sess {
		delete(s.byClient, sess.Key())
	}
	s.sessionsMu.Unlock()
	s.metrics.controlSessions.Dec()

	for _, reg := range sess.AllTunnels() {
		s.revokeRegistration(reg, "system", "", "tunnel.unregister", "client disconnected")
	}
	s.log.Info("client session closed",
		zap.String("client", sess.ClientName),
		zap.String("owner", sess.Principal.Username),
		zap.String("session", sess.ID),
		zap.String("reason", reason))
	if _, err := s.audit.Add(sess.Principal.Username, sess.Principal.UserID, "client.disconnect", sess.ClientName, reason, sess.RemoteAddr, "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	// A client closing its channels is routine: fpc reconnects on its own, so
	// alerting on it only buries the events that need attention. The audit
	// trail still keeps a record, and the Clients page shows live state.
	if !benignDisconnect(reason) {
		s.alerts.Add("warning", "client_disconnected",
			fmt.Sprintf("client %q (%s) disconnected: %s", sess.ClientName, sess.Principal.Username, reason))
	}
}

// benignDisconnect reports whether a session close is an expected lifecycle
// event rather than something an operator can act on.
func benignDisconnect(reason string) bool {
	switch reason {
	case "server shutting down", "replaced by a newer session with the same client name":
		return true
	}
	// Kicks issued by an administrator (account disabled or deleted from the
	// console) are deliberate actions the operator just took. They are already
	// in the audit trail with the acting principal, so surfacing them as alerts
	// only hides the events that actually need attention.
	if strings.Contains(reason, "by administrator") {
		return true
	}
	return strings.Contains(reason, "channel closed")
}

// alertOnce raises an alert at most once per key per window. A client retrying
// with a revoked token is one incident, not hundreds: without this the alert
// centre becomes unreadable within minutes and operators mute it.
func (s *Server) alertOnce(key, level, code, message string) {
	const window = 10 * time.Minute
	s.alertMu.Lock()
	if s.alertedAt == nil {
		s.alertedAt = map[string]time.Time{}
	}
	if last, seen := s.alertedAt[key]; seen && time.Since(last) < window {
		s.alertMu.Unlock()
		return
	}
	s.alertedAt[key] = time.Now()
	s.alertMu.Unlock()
	s.alerts.Add(level, code, message)
}

// recordTraffic updates per-tunnel, global, quota and Prometheus counters.
func (s *Server) recordTraffic(reg *Registration, in, out int64) {
	if in > 0 {
		u := uint64(in)
		reg.Counters.BytesIn.Add(u)
		s.totalIn.Add(u)
		s.metrics.traffic.WithLabelValues(reg.OwnerName, "in").Add(float64(in))
	}
	if out > 0 {
		u := uint64(out)
		reg.Counters.BytesOut.Add(u)
		s.totalOut.Add(u)
		s.metrics.traffic.WithLabelValues(reg.OwnerName, "out").Add(float64(out))
	}
	if in+out > 0 {
		s.usage.Add(reg.OwnerID, in+out)
	}
}

var (
	errConnQuota  = errors.New("per-tunnel connection quota exceeded")
	errDailyQuota = errors.New("daily traffic quota exceeded")
)

// acquireSlot enforces connection and daily-traffic quotas atomically.
func (s *Server) acquireSlot(reg *Registration) error {
	quota := s.users.EffectiveQuota(reg.OwnerID)
	if quota.DailyTrafficBytes > 0 && s.usage.Today(reg.OwnerID) >= quota.DailyTrafficBytes {
		return errDailyQuota
	}
	if quota.MaxConnectionsPerTunnel > 0 {
		active := reg.Counters.Active.Add(1)
		if active > int64(quota.MaxConnectionsPerTunnel) {
			reg.Counters.Active.Add(-1)
			return errConnQuota
		}
	} else {
		reg.Counters.Active.Add(1)
	}
	reg.Counters.Connections.Add(1)
	s.metrics.publicConns.WithLabelValues(reg.OwnerName).Inc()
	return nil
}

func (s *Server) releaseSlot(reg *Registration) {
	reg.Counters.Active.Add(-1)
}

// meteredConn counts bytes on yamux streams exactly once per direction.
type meteredConn struct {
	net.Conn
	srv  *Server
	reg  *Registration
	once sync.Once
}

func newMeteredConn(srv *Server, reg *Registration, conn net.Conn) *meteredConn {
	return &meteredConn{Conn: conn, srv: srv, reg: reg}
}

func (c *meteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.srv.recordTraffic(c.reg, 0, int64(n))
	}
	return n, err
}

func (c *meteredConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.srv.recordTraffic(c.reg, int64(n), 0)
	}
	return n, err
}

func (c *meteredConn) Close() error {
	c.once.Do(func() {
		c.reg.Untrack(c)
		c.reg.Counters.Active.Add(-1)
	})
	return c.Conn.Close()
}

func (c *meteredConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

func (s *Server) newConnID() string { return idgen.Conn() }
