package client

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/logx"
	"github.com/wormgate/wormgate/internal/webui"
)

// Connection states exposed to the console.
const (
	StateDisconnected = "disconnected"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateReconnecting = "reconnecting"
	StateStopped      = "stopped"
)

// Agent owns the client tunnel runtime and its local control console.
type Agent struct {
	cfg       *config.Manager
	log       *logx.Logger
	startedAt time.Time

	mu           sync.RWMutex
	tunnels      map[string]*Tunnel
	state        string
	stateSince   time.Time
	lastError    string
	sessionID    string
	connectedAt  time.Time
	reconnects   uint64
	clientName   string
	webToken     string
	webTokenPath string

	wake      chan struct{}
	reconnect chan struct{}

	tunnelEditMu sync.Mutex
	totalIn      atomic.Uint64
	totalOut     atomic.Uint64

	stats    *StatsStore
	conns    *ConnRegistry
	sessions *webui.SessionStore
	limiter  *webui.LoginLimiter
}

// New loads the runtime state and subscribes to configuration changes.
func New(mgr *config.Manager, logger *logx.Logger) (*Agent, error) {
	if mgr == nil {
		return nil, fmt.Errorf("config manager is required")
	}
	cfg := mgr.Current()
	webToken, webTokenPath, err := resolveWebToken(cfg)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg:          mgr,
		log:          logger,
		startedAt:    time.Now(),
		tunnels:      map[string]*Tunnel{},
		state:        StateDisconnected,
		stateSince:   time.Now(),
		clientName:   resolveClientName(cfg),
		webToken:     webToken,
		webTokenPath: webTokenPath,
		wake:         make(chan struct{}, 1),
		reconnect:    make(chan struct{}, 1),
		stats:        newStatsStore(),
		conns:        newConnRegistry(),
		sessions:     webui.NewSessionStore(cfg.Client.WebUI.SessionTTL.D()),
		limiter:      webui.NewLoginLimiter(10, time.Minute),
	}
	a.applyTunnelConfig(cfg.Client.Tunnels, false)
	mgr.Subscribe(a.onConfigChange)
	return a, nil
}

// Run supervises control/data sessions and reconnects with exponential
// backoff. It returns when ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.stats.Run(runCtx, a.tunnelPointers)

	backoff := a.cfg.Current().Client.Reconnect.MinBackoff.D()
	maxBackoff := a.cfg.Current().Client.Reconnect.MaxBackoff.D()
	if backoff <= 0 {
		backoff = time.Second
	}
	if maxBackoff < backoff {
		maxBackoff = 30 * time.Second
	}

	for {
		if runCtx.Err() != nil {
			a.setState(StateStopped, "")
			return runCtx.Err()
		}
		// No server token yet: the local console must still be reachable so the
		// operator can enter it in the configuration page. Wait quietly instead
		// of hammering the control plane with guaranteed-401 attempts.
		if strings.TrimSpace(a.cfg.Current().Client.Token) == "" {
			a.setState(StateDisconnected, "server token is not configured yet")
			select {
			case <-runCtx.Done():
				a.setState(StateStopped, "")
				return runCtx.Err()
			case <-a.reconnect:
			case <-time.After(3 * time.Second):
			}
			continue
		}
		started := time.Now()
		err := a.runSession(runCtx)
		a.clearSession()
		if runCtx.Err() != nil {
			a.setState(StateStopped, "")
			return runCtx.Err()
		}
		if err == nil {
			// A manual reconnect or hot reload requested a fresh session.
			a.setState(StateConnecting, "")
			backoff = a.cfg.Current().Client.Reconnect.MinBackoff.D()
			continue
		}
		a.log.Warn("tunnel session ended", zap.Error(err))
		a.setState(StateReconnecting, err.Error())
		a.mu.Lock()
		a.reconnects++
		a.mu.Unlock()
		if time.Since(started) > time.Minute {
			backoff = a.cfg.Current().Client.Reconnect.MinBackoff.D()
		}
		delay := jitter(backoff)
		timer := time.NewTimer(delay)
		select {
		case <-runCtx.Done():
			timer.Stop()
			a.setState(StateStopped, "")
			return runCtx.Err()
		case <-a.reconnect:
			timer.Stop()
			backoff = a.cfg.Current().Client.Reconnect.MinBackoff.D()
			a.setState(StateConnecting, "")
		case <-timer.C:
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 100 * time.Millisecond
	}
	return d + time.Duration(rand.Int63n(int64(d/5)+1))
}

// onConfigChange applies hot-reloadable settings. Changes that need a fresh
// TLS/control connection force a reconnect.
func (a *Agent) onConfigChange(old, next *config.Config) error {
	// client.webui.password_hash is the one exception to the restart rule: the
	// Security page writes it and must take effect immediately. Compare the
	// rest of the section field-by-field so a stray edit still needs a restart.
	oldWeb, nextWeb := old.Client.WebUI, next.Client.WebUI
	oldWeb.PasswordHash, nextWeb.PasswordHash = "", ""
	if oldWeb != nextWeb {
		return fmt.Errorf("client.webui changes require a restart")
	}
	if old.Client.WebUI.PasswordHash != next.Client.WebUI.PasswordHash {
		// The hash supersedes the startup token; drop the in-memory copy so an
		// old secret can never be accepted after a password change.
		a.mu.Lock()
		a.webToken = ""
		a.mu.Unlock()
	}
	if old.Log.Format != next.Log.Format || old.Log.AddCaller != next.Log.AddCaller {
		return fmt.Errorf("log format changes require a restart")
	}
	if err := a.log.SetLevel(next.Log.Level); err != nil {
		return err
	}
	name := resolveClientName(next)
	a.mu.Lock()
	a.clientName = name
	a.mu.Unlock()
	a.applyTunnelConfig(next.Client.Tunnels, true)
	if old.Client.Server != next.Client.Server ||
		old.Client.Token != next.Client.Token ||
		old.Client.TLS != next.Client.TLS ||
		old.Client.ClientName != next.Client.ClientName ||
		// The stream key is derived from session id + secret, so a new secret
		// only takes effect on a fresh session. Without this the client would
		// keep sealing with the old key while the server unseals with the new
		// one, and every encrypted stream would fail authentication.
		old.Client.StreamSecret != next.Client.StreamSecret {
		a.requestReconnect()
	}
	return nil
}

func (a *Agent) applyTunnelConfig(list []config.TunnelConfig, wake bool) {
	a.mu.Lock()
	next := make(map[string]*Tunnel, len(list))
	for _, cfg := range list {
		if existing, ok := a.tunnels[cfg.Name]; ok {
			old := existing.config()
			existing.setConfig(cfg)
			if old.Type != cfg.Type || old.Subdomain != cfg.Subdomain || old.RemotePort != cfg.RemotePort {
				existing.clearAssignment()
			}
			next[cfg.Name] = existing
			continue
		}
		next[cfg.Name] = newTunnel(cfg)
	}
	a.tunnels = next
	a.mu.Unlock()
	if wake {
		a.wakeUp()
	}
}

func (a *Agent) desiredSpecs() map[string]config.TunnelConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]config.TunnelConfig, len(a.tunnels))
	for name, t := range a.tunnels {
		cfg := t.config()
		if cfg.IsEnabled() {
			out[name] = cfg
		}
	}
	return out
}

func (a *Agent) tunnelPointers() []*Tunnel {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Tunnel, 0, len(a.tunnels))
	for _, t := range a.tunnels {
		out = append(out, t)
	}
	return out
}

func (a *Agent) findTunnel(name string) *Tunnel {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tunnels[name]
}

func (a *Agent) clearSession() {
	a.mu.Lock()
	a.sessionID = ""
	a.connectedAt = time.Time{}
	for _, t := range a.tunnels {
		t.clearAssignment()
	}
	a.mu.Unlock()
}

func (a *Agent) markConnected(sessionID string) {
	now := time.Now()
	a.mu.Lock()
	a.state = StateConnected
	a.stateSince = now
	a.connectedAt = now
	a.sessionID = sessionID
	a.lastError = ""
	a.mu.Unlock()
}

func (a *Agent) setState(state, errMsg string) {
	a.mu.Lock()
	if a.state != state {
		a.stateSince = time.Now()
	}
	a.state = state
	if errMsg != "" {
		a.lastError = errMsg
	}
	a.mu.Unlock()
}

func (a *Agent) wakeUp() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Reconnect interrupts the current session or backoff immediately.
func (a *Agent) Reconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

func (a *Agent) requestReconnect() { a.Reconnect() }

// MutateTunnels applies fn to a copy of the tunnel list and persists it
// transactionally through the config manager.
func (a *Agent) MutateTunnels(fn func([]config.TunnelConfig) ([]config.TunnelConfig, error)) error {
	a.tunnelEditMu.Lock()
	defer a.tunnelEditMu.Unlock()
	current := append([]config.TunnelConfig(nil), a.cfg.Current().Client.Tunnels...)
	next, err := fn(current)
	if err != nil {
		return err
	}
	return a.cfg.UpdateTunnels(next)
}

// StatusView is the console's status payload.
type StatusView struct {
	State       string       `json:"state"`
	StateSince  time.Time    `json:"state_since"`
	Server      string       `json:"server"`
	ClientName  string       `json:"client_name"`
	SessionID   string       `json:"session_id,omitempty"`
	ConnectedAt time.Time    `json:"connected_at,omitempty"`
	LastError   string       `json:"last_error,omitempty"`
	Reconnects  uint64       `json:"reconnects"`
	UptimeSec   int64        `json:"uptime_sec"`
	TotalIn     uint64       `json:"total_in"`
	TotalOut    uint64       `json:"total_out"`
	Tunnels     []TunnelView `json:"tunnels"`
}

// Status returns a consistent snapshot for the console.
func (a *Agent) Status() StatusView {
	a.mu.RLock()
	view := StatusView{
		State:       a.state,
		StateSince:  a.stateSince,
		Server:      a.cfg.Current().Client.Server,
		ClientName:  a.clientName,
		SessionID:   a.sessionID,
		ConnectedAt: a.connectedAt,
		LastError:   a.lastError,
		Reconnects:  a.reconnects,
		UptimeSec:   int64(time.Since(a.startedAt).Seconds()),
		TotalIn:     a.totalIn.Load(),
		TotalOut:    a.totalOut.Load(),
		Tunnels:     make([]TunnelView, 0, len(a.tunnels)),
	}
	tunnels := make([]*Tunnel, 0, len(a.tunnels))
	for _, t := range a.tunnels {
		tunnels = append(tunnels, t)
	}
	a.mu.RUnlock()
	for _, t := range tunnels {
		view.Tunnels = append(view.Tunnels, t.view())
	}
	return view
}

// Tunnels returns the tunnel views.
func (a *Agent) Tunnels() []TunnelView { return a.Status().Tunnels }

// Connections returns a snapshot of live connections.
func (a *Agent) Connections() []ConnView { return a.conns.snapshot() }

// StatsView bundles chart series with current counters.
type StatsView struct {
	Timestamp int64                   `json:"timestamp"`
	Global    []RateSample            `json:"global"`
	PerTunnel map[string][]RateSample `json:"per_tunnel"`
}

// Stats returns the sampled series.
func (a *Agent) Stats() StatsView {
	global, per := a.stats.Snapshot()
	return StatsView{Timestamp: time.Now().UnixMilli(), Global: global, PerTunnel: per}
}

// WebToken returns the console token (printed once at startup). Once a
// password hash is configured, the plain startup token is no longer a valid
// credential and must not be printed again.
func (a *Agent) WebToken() string {
	if a.cfg.Current().Client.WebUI.PasswordHash != "" {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.webToken
}

// SetConsolePassword changes the LOCAL browser-login password from the
// Security page. The argon2id hash is written into the writable configuration
// file (0600) as client.webui.password_hash and becomes the only accepted
// credential — this also beats --webui-password, which would otherwise restore
// the old secret on every restart. The legacy plaintext file is kept in sync
// on a best-effort basis for operators who inspect it or remove the hash.
func (a *Agent) SetConsolePassword(password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := a.cfg.UpdateClientSettings(map[string]any{
		"webui": map[string]any{"password_hash": hash},
	}); err != nil {
		return fmt.Errorf("persist password hash: %w", err)
	}
	a.mu.RLock()
	path := a.webTokenPath
	a.mu.RUnlock()
	if path == "" {
		dir := a.cfg.Current().Client.WebUI.DataDir
		if strings.TrimSpace(dir) == "" {
			dir = "data"
		}
		path = filepath.Join(dir, "client-console-password")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
			a.log.Warn("could not sync the console password file", zap.String("path", path), zap.Error(err))
		} else {
			a.mu.Lock()
			a.webToken, a.webTokenPath = password, path
			a.mu.Unlock()
		}
	}
	return nil
}

// StartSessionGC expires console sessions in the background. It blocks until
// ctx is cancelled, so callers must start it in its own goroutine.
func (a *Agent) StartSessionGC(ctx context.Context) { a.sessions.StartGC(ctx) }

// ConfigYAML renders the effective client configuration.
func (a *Agent) ConfigYAML() ([]byte, error) { return a.cfg.YAML() }

func sanitizeName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func resolveClientName(cfg *config.Config) string {
	raw := cfg.Client.ClientName
	if strings.TrimSpace(raw) == "" {
		raw, _ = os.Hostname()
	}
	name := sanitizeName(raw)
	if name == "" {
		name = "client-" + idgen.Hex(3)
	}
	return name
}

// resolveWebToken returns the LOCAL console password. It is unrelated to the
// tx_ client token that authenticates fpc to fps: that one belongs in the
// configuration page, while this one only unlocks the browser UI on :7200.
// A explicitly configured value (--webui-token / --webui-password) always wins.
func resolveWebToken(cfg *config.Config) (string, string, error) {
	if tok := strings.TrimSpace(cfg.Client.WebUI.Token); tok != "" {
		return tok, "", nil
	}
	dir := cfg.Client.WebUI.DataDir
	if strings.TrimSpace(dir) == "" {
		dir = "data"
	}
	path := filepath.Join(dir, "client-console-password")
	legacy := filepath.Join(dir, "client-webui-token")
	if raw, err := os.ReadFile(path); err == nil {
		tok := strings.TrimSpace(string(raw))
		if len(tok) < 8 {
			return "", "", fmt.Errorf("console password file %s is too short; delete it to regenerate", path)
		}
		return tok, path, nil
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("read console password: %w", err)
	}
	// Older installs used a wx_ token file; keep them working.
	if raw, err := os.ReadFile(legacy); err == nil {
		if tok := strings.TrimSpace(string(raw)); tok != "" {
			return tok, legacy, nil
		}
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("read legacy console token: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create data dir: %w", err)
	}
	password := idgen.URL(12)
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		return "", "", fmt.Errorf("write console password: %w", err)
	}
	return password, path, nil
}
