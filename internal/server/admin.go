package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/version"
	"github.com/wormgate/wormgate/internal/webui"
	"github.com/wormgate/wormgate/internal/webui/adminnext"
	"github.com/wormgate/wormgate/internal/webui/adminui"
)

const (
	adminSessionCookie = "wg_admin_session"
	adminCSRFCookie    = "wg_admin_csrf"
	adminCSRFHeader    = "X-CSRF-Token"
	oauthStateCookie   = "wg_oauth_state"
)

type adminPrincipalKey struct{}

type adminPrincipal struct {
	User   *auth.User
	Claims *auth.Claims
}

func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /api/meta", s.handleAdminMeta)
	mux.HandleFunc("POST /api/login", s.handleAdminLogin)
	mux.HandleFunc("GET /api/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("GET /api/oauth/callback", s.handleOAuthCallback)

	// Session.
	mux.HandleFunc("POST /api/logout", s.adminAuth(s.handleAdminLogout))
	mux.HandleFunc("GET /api/me", s.adminAuth(s.handleAdminMe))
	// Self-service password change. Unlike /api/users/{id}/password (admin
	// reset), this verifies the caller's current password.
	mux.HandleFunc("POST /api/me/password", s.adminAuth(s.handleChangeOwnPassword))

	// Dashboard.
	mux.HandleFunc("GET /api/overview", s.adminAuth(s.handleOverview))
	mux.HandleFunc("GET /api/stats", s.adminAuth(s.handleAdminStats))
	mux.HandleFunc("GET /api/tunnels", s.adminAuth(s.handleAdminTunnels))
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.adminAuth(s.handleRevokeTunnel))
	mux.HandleFunc("GET /api/clients", s.adminAuth(s.handleAdminClients))
	mux.HandleFunc("POST /api/clients/{id}/kick", s.adminAuth(s.handleKickClient))
	mux.HandleFunc("GET /api/nodes", s.adminAuth(s.handleNodes))
	mux.HandleFunc("GET /api/alerts", s.adminAuth(s.handleAlerts))
	mux.HandleFunc("POST /api/alerts/{id}/ack", s.adminAuth(s.handleAckAlert, auth.RoleAdmin))
	mux.HandleFunc("GET /api/audit", s.adminAuth(s.handleAudit, auth.RoleAdmin, auth.RoleViewer))
	// Exporting a client profile hands out the account's token, so it stays
	// admin-only and is audited.
	mux.HandleFunc("GET /api/users/{id}/export", s.adminAuth(s.handleExportClientConfig, auth.RoleAdmin))
	// Stream encryption: reading the state needs admin, because the response
	// contains the shared secret itself.
	mux.HandleFunc("GET /api/encryption", s.adminAuth(s.handleGetEncryption, auth.RoleAdmin))
	mux.HandleFunc("PUT /api/encryption", s.adminAuth(s.handlePutEncryption, auth.RoleAdmin))
	mux.HandleFunc("GET /api/ws", s.handleAdminWS)

	// User and tenant management.
	mux.HandleFunc("GET /api/users", s.adminAuth(s.handleListUsers, auth.RoleAdmin))
	mux.HandleFunc("POST /api/users", s.adminAuth(s.handleCreateUser, auth.RoleAdmin))
	mux.HandleFunc("PATCH /api/users/{id}", s.adminAuth(s.handleUpdateUser, auth.RoleAdmin))
	mux.HandleFunc("DELETE /api/users/{id}", s.adminAuth(s.handleDeleteUser, auth.RoleAdmin))
	mux.HandleFunc("POST /api/users/{id}/rotate-token", s.adminAuth(s.handleRotateToken, auth.RoleAdmin))
	mux.HandleFunc("GET /api/users/{id}/token", s.adminAuth(s.handleRevealToken, auth.RoleAdmin))
	mux.HandleFunc("POST /api/users/{id}/password", s.adminAuth(s.handleSetPassword, auth.RoleAdmin))

	// Console assets. Compressed: the chart vendor chunk dominates first load.
	mux.Handle("GET /admin/", webui.Compress(webui.SPAHandler(adminui.FS(), "/admin")))
	// Grey-release console: React 19 + Ant Design 6 on the same API, so an
	// operator can A/B the two UIs page by page before the legacy one retires.
	mux.Handle("GET /next/", webui.Compress(webui.SPAHandler(adminnext.FS(), "/next")))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			writeError(w, http.StatusNotFound, "API endpoint not found")
		case r.URL.Path == "/admin":
			http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
		case r.URL.Path == "/next":
			http.Redirect(w, r, "/next/", http.StatusTemporaryRedirect)
		case r.Method == http.MethodGet && r.URL.Path == "/":
			http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
		default:
			http.NotFound(w, r)
		}
	})
	return webui.SecurityHeaders(adminNoStore(mux))
}

func adminNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminUser(r *http.Request) (*adminPrincipal, error) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, auth.ErrUnauthorized
	}
	claims, err := s.users.ParseJWT(cookie.Value)
	if err != nil {
		return nil, err
	}
	user, ok := s.users.GetUser(claims.Subject)
	if !ok || user.Disabled {
		return nil, auth.ErrUnauthorized
	}
	// A password change invalidates JWTs issued before it. The browser that
	// changed the password is re-issued a fresh token by the endpoint itself,
	// so only stale sessions (other browsers, leaked cookies) are refused.
	// One second of slack covers clock/serialisation rounding.
	if !user.PasswordChangedAt.IsZero() && claims.IssuedAt != nil &&
		claims.IssuedAt.Time.Before(user.PasswordChangedAt.Add(-time.Second)) {
		return nil, auth.ErrUnauthorized
	}
	return &adminPrincipal{User: user, Claims: claims}, nil
}

// refreshAdminSession keeps the caller signed in after their own password
// change while every older JWT is rejected by adminUser.
func (s *Server) refreshAdminSession(w http.ResponseWriter, user *auth.User) string {
	if user == nil {
		return ""
	}
	token, err := s.issueAdminSession(user)
	if err != nil {
		s.log.Warn("could not refresh admin session after password change", zap.Error(err))
		return ""
	}
	csrf := idgen.Hex(24)
	s.setAdminCookies(w, token, csrf)
	return csrf
}

// adminAuth enforces JWT auth, role membership and CSRF for writes. Admin
// bypasses an explicit role list.
func (s *Server) adminAuth(next http.HandlerFunc, roles ...auth.Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.adminUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		if len(roles) > 0 && principal.User.Role != auth.RoleAdmin {
			allowed := false
			for _, role := range roles {
				if principal.User.Role == role {
					allowed = true
					break
				}
			}
			if !allowed {
				writeError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
		}
		if isUnsafeMethod(r.Method) {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			cookie, err := r.Cookie(adminCSRFCookie)
			header := r.Header.Get(adminCSRFHeader)
			if err != nil || cookie.Value == "" ||
				subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				writeError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		ctx := context.WithValue(r.Context(), adminPrincipalKey{}, principal)
		next(w, r.WithContext(ctx))
	}
}

func principalFrom(r *http.Request) *adminPrincipal {
	p, _ := r.Context().Value(adminPrincipalKey{}).(*adminPrincipal)
	return p
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin login rejected")
		return
	}
	ip := requestIP(r)
	if !s.adminLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts; try again later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, err := s.users.AuthenticatePassword(body.Username, body.Password)
	if err != nil {
		s.log.Warn("admin login failed",
			zap.String("username", body.Username),
			zap.String("ip", ip))
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.adminLimiter.Reset(ip)
	token, err := s.issueAdminSession(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	csrf := idgen.Hex(24)
	s.setAdminCookies(w, token, csrf)
	if _, err := s.audit.Add(user.Username, user.ID, "admin.login", "console", "", ip, "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": toUserView(*user),
		"csrf": csrf,
	})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "admin.logout", "console", "", requestIP(r), "info"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	expire := -1
	http.SetCookie(w, &http.Cookie{Name: adminSessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: expire})
	http.SetCookie(w, &http.Cookie{Name: adminCSRFCookie, Value: "", Path: "/", SameSite: http.SameSiteStrictMode, MaxAge: expire})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	csrf := ""
	if cookie, err := r.Cookie(adminCSRFCookie); err == nil {
		csrf = cookie.Value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": toUserView(*p.User),
		"csrf": csrf,
	})
}

func (s *Server) handleAdminMeta(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg.Current()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       version.Version,
		"server_time":   time.Now().UTC(),
		"oauth_enabled": cfg.Server.WebUI.OAuth2.Enabled,
		"roles":         []string{"admin", "user", "viewer"},
	})
}

func (s *Server) issueAdminSession(user *auth.User) (string, error) {
	ttl := s.cfg.Current().Server.WebUI.SessionTTL.D()
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return s.users.IssueJWT(user, ttl)
}

// setAdminCookies issues the HttpOnly JWT cookie and the readable CSRF
// double-submit cookie (the SPA mirrors the latter into X-CSRF-Token).
func (s *Server) setAdminCookies(w http.ResponseWriter, token, csrf string) {
	cfg := s.cfg.Current()
	ttl := cfg.Server.WebUI.SessionTTL.D()
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	maxAge := int(ttl.Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Server.WebUI.SecureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     adminCSRFCookie,
		Value:    csrf,
		Path:     "/",
		Secure:   cfg.Server.WebUI.SecureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// lastCookieValue is a small helper for tests and login responses. Set-Cookie
// is only visible after headers flush, so the CSRF value is returned directly
// by callers that need it; this function reads a request cookie, not the
// response.
func (s *Server) handleAdminWS(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "login required")
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
	send := func() error {
		return write(map[string]any{
			"type":      "snapshot",
			"timestamp": time.Now().UnixMilli(),
			"data":      s.adminSnapshot(p),
		})
	}
	if err := send(); err != nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
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
