package client

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/logx"
	"github.com/wormgate/wormgate/internal/version"
	"github.com/wormgate/wormgate/internal/webui"
	"github.com/wormgate/wormgate/internal/webui/clientui"
)

const (
	clientSessionCookie = "wg_client_session"
	clientCSRFCookie    = "wg_client_csrf"
	csrfHeader          = "X-CSRF-Token"
)

type sessionContextKey struct{}

// WebUIHandler builds the local console HTTP handler.
func (a *Agent) WebUIHandler() http.Handler {
	mux := http.NewServeMux()

	// Authentication.
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.protect(a.handleLogout))
	mux.HandleFunc("POST /api/password", a.protect(a.handleChangePassword))
	mux.HandleFunc("GET /api/meta", a.handleMeta)
	mux.HandleFunc("GET /api/session", a.protect(a.handleSession))

	// Tunnels and runtime.
	mux.HandleFunc("GET /api/status", a.protect(a.handleStatus))
	mux.HandleFunc("GET /api/tunnels", a.protect(a.handleListTunnels))
	mux.HandleFunc("POST /api/tunnels", a.protect(a.handleCreateTunnel))
	mux.HandleFunc("PUT /api/tunnels/{name}", a.protect(a.handleUpdateTunnel))
	mux.HandleFunc("DELETE /api/tunnels/{name}", a.protect(a.handleDeleteTunnel))
	mux.HandleFunc("POST /api/tunnels/{name}/start", a.protect(a.handleTunnelEnable(true)))
	mux.HandleFunc("POST /api/tunnels/{name}/stop", a.protect(a.handleTunnelEnable(false)))
	mux.HandleFunc("POST /api/reconnect", a.protect(a.handleReconnect))
	mux.HandleFunc("GET /api/connections", a.protect(a.handleConnections))
	mux.HandleFunc("GET /api/stats", a.protect(a.handleStats))

	// Configuration and observability.
	mux.HandleFunc("GET /api/config", a.protect(a.handleGetConfig))
	mux.HandleFunc("PUT /api/config", a.protect(a.handlePutConfig))
	mux.HandleFunc("GET /api/settings", a.protect(a.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", a.protect(a.handlePutSettings))
	mux.HandleFunc("GET /api/logs", a.protect(a.handleLogsSSE))
	mux.HandleFunc("GET /api/ws", a.protect(a.handleWS))

	// Assets are gzipped: the echarts vendor chunk is ~550 KB raw and ~150 KB
	// compressed, which is the difference between a snappy and a sluggish
	// first load on a remote admin link.
	mux.Handle("/", webui.Compress(webui.SPAHandler(clientui.FS(), "")))
	return webui.SecurityHeaders(noStoreAPI(mux))
}

func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// protect enforces session auth, same-origin writes and CSRF. The session is
// placed in the request context.
func (a *Agent) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.sessionFromRequest(r)
		if !ok {
			apiError(w, http.StatusUnauthorized, "login required")
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !sameOrigin(r) {
				apiError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			header := r.Header.Get(csrfHeader)
			cookie, err := r.Cookie(clientCSRFCookie)
			if err != nil || cookie.Value == "" ||
				subtle.ConstantTimeCompare([]byte(header), []byte(sess.CSRF)) != 1 ||
				subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(sess.CSRF)) != 1 {
				apiError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionContextKey{}, sess)
		next(w, r.WithContext(ctx))
	}
}

func (a *Agent) sessionFromRequest(r *http.Request) (webui.Session, bool) {
	cookie, err := r.Cookie(clientSessionCookie)
	if err != nil {
		return webui.Session{}, false
	}
	return a.sessions.Get(cookie.Value)
}

// consoleSecretValid checks the LOCAL console password. Once a password hash
// is configured (which is what the Security page writes), it is the only
// accepted credential: the startup token and --webui-password must stop
// working as soon as the operator changes the password from the UI.
func (a *Agent) consoleSecretValid(secret string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	if hash := a.cfg.Current().Client.WebUI.PasswordHash; hash != "" {
		return auth.VerifyPassword(hash, secret)
	}
	return secureEqual(secret, a.WebToken())
}

// handleChangePassword changes the browser login password for this console.
// It has nothing to do with the tx_ token used by fpc to reach fps.
func (a *Agent) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !a.consoleSecretValid(body.CurrentPassword) {
		// 400, not 401: the session is still valid. A 401 would make the SPA
		// treat this as an expired login and bounce the user to /login.
		apiError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	if len(body.NewPassword) < 8 {
		apiError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if body.NewPassword == body.CurrentPassword {
		apiError(w, http.StatusBadRequest, "new password must differ from the current password")
		return
	}
	if err := a.SetConsolePassword(body.NewPassword); err != nil {
		apiError(w, http.StatusInternalServerError, "save console password: "+err.Error())
		return
	}
	sess, _ := r.Context().Value(sessionContextKey{}).(webui.Session)
	a.sessions.DeleteExcept(sess.ID)
	a.log.Info("console password changed", zap.String("ip", requestIP(r)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "password_managed": true})
}

func (a *Agent) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apiError(w, http.StatusForbidden, "cross-origin login rejected")
		return
	}
	ip := requestIP(r)
	if !a.limiter.Allow(ip) {
		apiError(w, http.StatusTooManyRequests, "too many login attempts; try again later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		Secret string `json:"secret"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	secret := strings.TrimSpace(body.Secret)
	if secret == "" {
		apiError(w, http.StatusBadRequest, "secret is required")
		return
	}

	if !a.consoleSecretValid(secret) {
		// SECURITY: never log the attempted secret.
		a.log.Warn("console login failed", zap.String("ip", ip))
		// A wg_ (or legacy tx_) token authenticates fpc to fps, not a browser
		// to this console. Operators paste it here surprisingly often, so tell
		// them what to use instead of replying with a generic "invalid
		// credentials".
		if strings.HasPrefix(strings.ToLower(secret), "wg_") || strings.HasPrefix(strings.ToLower(secret), "tx_") {
			apiError(w, http.StatusUnauthorized,
				"this is a client token (wg_) for connecting fpc to fps; sign in with the local console password (Security page)")
			return
		}
		apiError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	a.limiter.Reset(ip)
	sess := a.sessions.Create()
	ttl := a.cfg.Current().Client.WebUI.SessionTTL.D()
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	maxAge := int(ttl.Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     clientSessionCookie,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     clientCSRFCookie,
		Value:    sess.CSRF,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"csrf":       sess.CSRF,
		"expires_at": sess.Expires,
	})
}

func (a *Agent) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(clientSessionCookie); err == nil {
		a.sessions.Delete(cookie.Value)
	}
	expire := -1
	http.SetCookie(w, &http.Cookie{Name: clientSessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: expire})
	http.SetCookie(w, &http.Cookie{Name: clientCSRFCookie, Value: "", Path: "/", SameSite: http.SameSiteStrictMode, MaxAge: expire})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Agent) handleMeta(w http.ResponseWriter, _ *http.Request) {
	cfg := a.cfg.Current()
	status := a.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     version.Version,
		"client_name": status.ClientName,
		"server":      cfg.Client.Server,
		"state":       status.State,
		"uptime_sec":  status.UptimeSec,
		"csrf_cookie": clientCSRFCookie,
		"csrf_header": csrfHeader,
		"started_at":  a.startedAt,
		"config_path": a.cfg.ProjectPath(),
	})
}

func (a *Agent) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := r.Context().Value(sessionContextKey{}).(webui.Session)
	if !ok {
		apiError(w, http.StatusUnauthorized, "login required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"csrf": sess.CSRF, "expires_at": sess.Expires})
}

func (a *Agent) handleWS(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.sessionFromRequest(r); !ok {
		apiError(w, http.StatusUnauthorized, "login required")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	write := func(payload any) error {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
		defer cancelWrite()
		return ws.Write(writeCtx, websocket.MessageText, raw)
	}
	if err := write(a.realtimeSnapshot()); err != nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ts := <-ticker.C:
			snap := a.realtimeSnapshot()
			snap.Timestamp = ts.UnixMilli()
			if err := write(snap); err != nil {
				return
			}
		}
	}
}

func (a *Agent) handleLogsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	minRank := levelRank(strings.ToLower(r.URL.Query().Get("level")))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	send := func(entry logx.Entry) bool {
		if levelRank(entry.Level) < minRank {
			return true
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", entry.Seq, raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for _, entry := range a.log.Buffer.Snapshot(200) {
		if !send(entry) {
			return
		}
	}
	ch, cancel := a.log.Buffer.Subscribe(128)
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			if !send(entry) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func levelRank(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return 0
	case "info", "":
		return 1
	case "warn", "warning":
		return 2
	case "error", "fatal", "panic":
		return 3
	default:
		return 1
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func secureEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// realtimeSnapshot is pushed over the console WebSocket.
type realtimeSnapshot struct {
	Type        string       `json:"type"`
	Timestamp   int64        `json:"timestamp"`
	Status      StatusView   `json:"status"`
	Connections []ConnView   `json:"connections"`
	Recent      []RateSample `json:"recent"`
}

func (a *Agent) realtimeSnapshot() realtimeSnapshot {
	stats := a.Stats()
	recent := stats.Global
	if len(recent) > 120 {
		recent = recent[len(recent)-120:]
	}
	return realtimeSnapshot{
		Type:        "snapshot",
		Timestamp:   time.Now().UnixMilli(),
		Status:      a.Status(),
		Connections: a.Connections(),
		Recent:      recent,
	}
}
