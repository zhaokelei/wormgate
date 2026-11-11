package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/idgen"
)

const (
	oauthVerifierCookie = "wg_oauth_verifier"
	oauthMaxBody        = 1 << 20
)

type oauthTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Current()
	o := cfg.Server.WebUI.OAuth2
	if !o.Enabled {
		writeError(w, http.StatusNotFound, "OAuth is not enabled")
		return
	}
	target, err := url.Parse(o.AuthURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid OAuth authorization URL")
		return
	}
	state := idgen.Hex(24)
	verifier := idgen.URL(32)
	challenge := pkceChallenge(verifier)

	secure := cfg.Server.WebUI.SecureCookie
	stateCookie := &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/oauth/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	}
	verifierCookie := &http.Cookie{
		Name:     oauthVerifierCookie,
		Value:    verifier,
		Path:     "/api/oauth/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	}
	http.SetCookie(w, stateCookie)
	http.SetCookie(w, verifierCookie)

	q := target.Query()
	q.Set("response_type", "code")
	q.Set("client_id", o.ClientID)
	q.Set("redirect_uri", o.RedirectURL)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	scopes := o.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	q.Set("scope", strings.Join(scopes, " "))
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Current()
	o := cfg.Server.WebUI.OAuth2
	if !o.Enabled {
		writeError(w, http.StatusNotFound, "OAuth is not enabled")
		return
	}
	// Always clear the short-lived cookies.
	expire := -1
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/api/oauth/", MaxAge: expire, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: oauthVerifierCookie, Value: "", Path: "/api/oauth/", MaxAge: expire, HttpOnly: true, SameSite: http.SameSiteLaxMode})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		s.oauthRedirectError(w, r, "identity provider rejected the login")
		return
	}
	state := r.URL.Query().Get("state")
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || state == "" || subtleCompare(state, stateCookie.Value) != true {
		s.oauthRedirectError(w, r, "invalid OAuth state")
		return
	}
	verifierCookie, err := r.Cookie(oauthVerifierCookie)
	if err != nil || verifierCookie.Value == "" {
		s.oauthRedirectError(w, r, "missing PKCE verifier")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.oauthRedirectError(w, r, "missing authorization code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tokens, err := exchangeOAuthCode(ctx, o, code, verifierCookie.Value)
	if err != nil {
		s.log.Warn("OAuth code exchange failed", zap.Error(err))
		s.oauthRedirectError(w, r, "token exchange failed")
		return
	}
	info, err := fetchOAuthUserInfo(ctx, o, tokens.AccessToken)
	if err != nil {
		s.log.Warn("OAuth userinfo failed", zap.Error(err))
		s.oauthRedirectError(w, r, "could not read user profile")
		return
	}
	sub := lookupString(info, "sub")
	if sub == "" {
		s.log.Warn("OAuth userinfo has no sub claim")
		s.oauthRedirectError(w, r, "identity provider did not return a subject")
		return
	}
	externalID := o.UserInfoURL + "|" + sub
	preferred := lookupString(info, o.UsernameField)
	display := lookupString(info, "name")
	if display == "" {
		display = preferred
	}

	user, ok := s.users.FindByExternalID(externalID)
	if !ok {
		role := auth.RoleViewer
		if o.DefaultRole != "" && auth.Role(o.DefaultRole).Valid() {
			role = auth.Role(o.DefaultRole)
		}
		created, err := s.users.CreateOAuthUser(preferred, display, externalID, role)
		if err != nil {
			s.log.Warn("OAuth auto-provision failed", zap.Error(err))
			s.oauthRedirectError(w, r, "could not provision a local account")
			return
		}
		user = created
	}
	if user.Disabled {
		s.oauthRedirectError(w, r, "account is disabled")
		return
	}
	token, err := s.issueAdminSession(user)
	if err != nil {
		s.oauthRedirectError(w, r, "could not create a session")
		return
	}
	s.setAdminCookies(w, token, idgen.Hex(24))
	if _, err := s.audit.Add(user.Username, user.ID, "admin.oauth_login", "console", o.UserInfoURL, requestIP(r), "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

func (s *Server) oauthRedirectError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/admin/?oauth_error="+url.QueryEscape(message), http.StatusFound)
}

func exchangeOAuthCode(ctx context.Context, o config.OAuth2Config, code, verifier string) (*oauthTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.RedirectURL)
	form.Set("client_id", o.ClientID)
	form.Set("client_secret", o.ClientSecret)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBody))
	if err != nil {
		return nil, err
	}
	var tokens oauthTokenResponse
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || tokens.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned %d (%s)", resp.StatusCode, tokens.Error)
	}
	return &tokens, nil
}

func fetchOAuthUserInfo(ctx context.Context, o config.OAuth2Config, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("userinfo endpoint returned %d", resp.StatusCode)
	}
	var info map[string]any
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return info, nil
}

func lookupString(m map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var current any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = obj[part]
	}
	switch v := current.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return ""
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
