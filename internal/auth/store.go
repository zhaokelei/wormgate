package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/idgen"
)

// Role is an admin-console authorization role.
type Role string

// Supported roles, from most to least privileged.
const (
	RoleAdmin  Role = "admin"
	RoleUser   Role = "user"
	RoleViewer Role = "viewer"
)

// Valid reports whether the role is known.
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleUser || r == RoleViewer
}

// CanWrite reports whether the role may mutate server state.
func (r Role) CanWrite() bool { return r == RoleAdmin }

// Quota stores per-user overrides. Zero means "inherit the server default".
type Quota struct {
	MaxTunnels        int   `json:"max_tunnels"`
	MaxConnections    int   `json:"max_connections"`
	DailyTrafficBytes int64 `json:"daily_traffic_bytes"`
}

// User is an account that can own tunnels and/or log into the admin console.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name,omitempty"`
	Role         Role   `json:"role"`
	PasswordHash string `json:"password_hash,omitempty"`
	// PasswordChangedAt is bumped on every password change. The admin console
	// rejects JWTs issued before it, so a credential change logs out sessions
	// that may still hold the old password.
	PasswordChangedAt time.Time `json:"password_changed_at,omitempty"`
	TokenHash         string    `json:"token_hash,omitempty"`
	// TokenSealed is an AES-GCM copy of the token, encrypted with a key derived
	// from the server's jwt-secret. It exists only so an administrator can look
	// up a forgotten token in the console; TokenHash stays the source of truth
	// for authentication, so losing this field never blocks a client.
	TokenSealed string `json:"token_sealed,omitempty"`
	// ExternalID links an OAuth identity (provider userinfo URL + sub).
	ExternalID string    `json:"external_id,omitempty"`
	Quota      Quota     `json:"quota"`
	Disabled   bool      `json:"disabled"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}

// Principal is an authenticated tunnel client.
type Principal struct {
	UserID   string
	Username string
	Role     Role
	// Static is true for tokens supplied through the config file.
	Static bool
}

// Bootstrap reports credentials generated during first start.
type Bootstrap struct {
	AdminUsername          string
	GeneratedAdminPassword string
	ClientToken            string
	TokenReprinted         bool
}

// ErrUnauthorized is returned for unknown or disabled credentials.
var ErrUnauthorized = errors.New("unauthorized")

// ErrTokenNotRecoverable means the account predates sealed token storage, so
// the plaintext token is gone and only a rotation can restore console access
// to it.
var ErrTokenNotRecoverable = errors.New("token was issued before sealed storage was enabled; rotate it to make it viewable")

type persisted struct {
	Users []*User `json:"users"`
}

// Store is a small, file-backed user directory. Traffic data stays in memory;
// only account metadata is durable.
type Store struct {
	mu         sync.RWMutex
	path       string
	users      map[string]*User
	byName     map[string]string
	byToken    map[string]string
	byExternal map[string]string
	static     map[string]Principal
	jwtSecret  []byte
	defaults   config.QuotaConfig
}

// OpenOptions configures the store.
type OpenOptions struct {
	DataDir           string
	AdminUser         string
	AdminPassword     string
	AdminPasswordHash string
	Defaults          config.QuotaConfig
	StaticTokens      []string
}

// Open loads or creates the user store. It never wipes a corrupt store: an
// unreadable file is a hard error so an admin can recover it manually.
func Open(opts OpenOptions) (*Store, *Bootstrap, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, nil, fmt.Errorf("data_dir must not be empty")
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create data dir: %w", err)
	}
	secret, err := loadOrCreateSecret(filepath.Join(opts.DataDir, "jwt-secret"), 32)
	if err != nil {
		return nil, nil, err
	}
	s := &Store{
		path:       filepath.Join(opts.DataDir, "users.json"),
		users:      map[string]*User{},
		byName:     map[string]string{},
		byToken:    map[string]string{},
		byExternal: map[string]string{},
		static:     map[string]Principal{},
		jwtSecret:  secret,
		defaults:   opts.Defaults,
	}
	if err := s.load(); err != nil {
		return nil, nil, err
	}
	s.SetStaticTokens(opts.StaticTokens)

	boot := &Bootstrap{AdminUsername: strings.TrimSpace(opts.AdminUser)}
	if boot.AdminUsername == "" {
		boot.AdminUsername = "admin"
	}

	s.mu.Lock()
	admin := s.userByNameLocked(boot.AdminUsername)
	changed := false
	if admin == nil {
		admin = &User{
			ID:        idgen.Hex(12),
			Username:  boot.AdminUsername,
			Role:      RoleAdmin,
			CreatedAt: time.Now().UTC(),
		}
		s.addUserLocked(admin)
		changed = true
	}
	if admin.Role != RoleAdmin {
		admin.Role = RoleAdmin
		changed = true
	}
	switch {
	case opts.AdminPasswordHash != "":
		if admin.PasswordHash != opts.AdminPasswordHash {
			admin.PasswordHash = opts.AdminPasswordHash
			changed = true
		}
	case opts.AdminPassword != "":
		hash, err := HashPassword(opts.AdminPassword)
		if err != nil {
			s.mu.Unlock()
			return nil, nil, fmt.Errorf("hash admin password: %w", err)
		}
		admin.PasswordHash = hash
		changed = true
	case admin.PasswordHash == "":
		generated := idgen.URL(18)
		hash, err := HashPassword(generated)
		if err != nil {
			s.mu.Unlock()
			return nil, nil, fmt.Errorf("hash admin password: %w", err)
		}
		admin.PasswordHash = hash
		boot.GeneratedAdminPassword = generated
		changed = true
	}

	demo := s.userByNameLocked("demo")
	if demo == nil {
		demo = &User{
			ID:          idgen.Hex(12),
			Username:    "demo",
			DisplayName: "Demo tunnel client",
			Role:        RoleUser,
			CreatedAt:   time.Now().UTC(),
		}
		s.addUserLocked(demo)
		changed = true
	}
	if demo.Role != RoleUser {
		demo.Role = RoleUser
		changed = true
	}
	tokenPath := filepath.Join(opts.DataDir, "demo-token")
	if demo.TokenHash == "" {
		token := idgen.Token("wg_")
		demo.TokenHash = HashToken(token)
		demo.TokenSealed = s.sealToken(token)
		// The user was registered by addUserLocked above while TokenHash was
		// still empty, so that call could not index the token. Register the
		// hash explicitly or AuthenticateToken would reject the bootstrap
		// token with 401 until the next process restart.
		s.byToken[demo.TokenHash] = demo.ID
		if err := writeSecret(tokenPath, []byte(token)); err != nil {
			s.mu.Unlock()
			return nil, nil, err
		}
		boot.ClientToken = token
		changed = true
	} else if raw, err := os.ReadFile(tokenPath); err == nil {
		// Only reprint when the stored hash still matches; rotation from the
		// console must never be undone by a stale file.
		candidate := strings.TrimSpace(string(raw))
		if HashToken(candidate) == demo.TokenHash {
			boot.ClientToken = candidate
			boot.TokenReprinted = true
			// Backfill the sealed copy for installs created before the console
			// could show a token. The plaintext is already on disk here, so
			// this adds no new exposure.
			if demo.TokenSealed == "" {
				demo.TokenSealed = s.sealToken(candidate)
				changed = changed || demo.TokenSealed != ""
			}
		}
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			s.mu.Unlock()
			return nil, nil, err
		}
	}
	s.mu.Unlock()
	return s, boot, nil
}

// SetStaticTokens replaces config-provided tokens. They authenticate as the
// reserved "static" owner and never grant console access.
func (s *Store) SetStaticTokens(tokens []string) {
	static := make(map[string]Principal, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		static[HashToken(token)] = Principal{UserID: "static", Username: "static", Role: RoleUser, Static: true}
	}
	s.mu.Lock()
	s.static = static
	s.mu.Unlock()
}

// AuthenticateToken resolves a tunnel-client token.
func (s *Store) AuthenticateToken(token string) (*Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUnauthorized
	}
	hash := HashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byToken[hash]; ok {
		u := s.users[id]
		if u == nil || u.Disabled {
			return nil, ErrUnauthorized
		}
		u.LastSeenAt = time.Now().UTC()
		return &Principal{UserID: u.ID, Username: u.Username, Role: u.Role}, nil
	}
	if p, ok := s.static[hash]; ok {
		return &p, nil
	}
	return nil, ErrUnauthorized
}

// AuthenticatePassword validates admin-console credentials.
func (s *Store) AuthenticatePassword(username, password string) (*User, error) {
	s.mu.RLock()
	id, ok := s.byName[strings.ToLower(strings.TrimSpace(username))]
	var user User
	if ok {
		if u := s.users[id]; u != nil {
			user = *u
		}
	}
	s.mu.RUnlock()
	if !ok || user.Disabled || user.PasswordHash == "" {
		// Run a dummy hash to keep timing roughly uniform for unknown users.
		_ = VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return nil, ErrUnauthorized
	}
	if !VerifyPassword(user.PasswordHash, password) {
		return nil, ErrUnauthorized
	}
	s.mu.Lock()
	if u := s.users[user.ID]; u != nil {
		u.LastSeenAt = time.Now().UTC()
	}
	s.mu.Unlock()
	return &user, nil
}

// GetUser returns a copy of a user by ID.
func (s *Store) GetUser(id string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, false
	}
	cp := *u
	return &cp, true
}

// UserByName returns a copy of a user by username.
func (s *Store) UserByName(username string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byName[strings.ToLower(strings.TrimSpace(username))]
	if !ok {
		return nil, false
	}
	u := s.users[id]
	if u == nil {
		return nil, false
	}
	cp := *u
	return &cp, true
}

// ListUsers returns users sorted by creation time.
func (s *Store) ListUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// CreateUser creates an account and returns its one-time client token.
func (s *Store) CreateUser(username, displayName string, role Role, quota Quota) (*User, string, error) {
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return nil, "", fmt.Errorf("username must be 3-32 characters of letters, digits, dot, dash or underscore")
	}
	if !role.Valid() {
		return nil, "", fmt.Errorf("invalid role %q", role)
	}
	token := idgen.Token("wg_")
	u := &User{
		ID:          idgen.Hex(12),
		Username:    username,
		DisplayName: strings.TrimSpace(displayName),
		Role:        role,
		TokenHash:   HashToken(token),
		TokenSealed: s.sealToken(token),
		Quota:       quota,
		CreatedAt:   time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userByNameLocked(username) != nil {
		return nil, "", fmt.Errorf("user %q already exists", username)
	}
	s.addUserLocked(u)
	if err := s.saveLocked(); err != nil {
		delete(s.users, u.ID)
		delete(s.byName, strings.ToLower(username))
		if u.TokenHash != "" {
			delete(s.byToken, u.TokenHash)
		}
		return nil, "", err
	}
	cp := *u
	return &cp, token, nil
}

// UpdateUser patches display name, role, quota and disabled state.
func (s *Store) UpdateUser(id, displayName string, role Role, quota Quota, disabled bool) (*User, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("invalid role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	if u == nil {
		return nil, fmt.Errorf("user not found")
	}
	u.DisplayName = strings.TrimSpace(displayName)
	u.Role = role
	u.Quota = quota
	u.Disabled = disabled
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := *u
	return &cp, nil
}

// SetPassword updates a user's console password and invalidates every admin
// console session issued before the change.
func (s *Store) SetPassword(id, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	if u == nil {
		return fmt.Errorf("user not found")
	}
	u.PasswordHash = hash
	u.PasswordChangedAt = time.Now().UTC()
	return s.saveLocked()
}

// RotateToken issues a new client token for a user.
func (s *Store) RotateToken(id string) (string, error) {
	token := idgen.Token("wg_")
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	if u == nil {
		return "", fmt.Errorf("user not found")
	}
	if u.TokenHash != "" {
		delete(s.byToken, u.TokenHash)
	}
	u.TokenHash = HashToken(token)
	u.TokenSealed = s.sealToken(token)
	s.byToken[u.TokenHash] = u.ID
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return token, nil
}

// DeleteUser removes an account and invalidates its token.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	if u == nil {
		return fmt.Errorf("user not found")
	}
	if u.TokenHash != "" {
		delete(s.byToken, u.TokenHash)
	}
	if u.ExternalID != "" {
		delete(s.byExternal, u.ExternalID)
	}
	delete(s.byName, strings.ToLower(u.Username))
	delete(s.users, id)
	return s.saveLocked()
}

// FindByExternalID resolves an OAuth identity.
func (s *Store) FindByExternalID(externalID string) (*User, bool) {
	if externalID == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byExternal[externalID]
	if !ok {
		return nil, false
	}
	u := s.users[id]
	if u == nil {
		return nil, false
	}
	cp := *u
	return &cp, true
}

// CreateOAuthUser provisions an OAuth-only account. The caller must supply a
// trusted externalID (already namespaced by provider).
func (s *Store) CreateOAuthUser(preferredUsername, displayName, externalID string, role Role) (*User, error) {
	if externalID == "" {
		return nil, fmt.Errorf("external identity is required")
	}
	if !role.Valid() {
		role = RoleViewer
	}
	username := normalizeUsername(preferredUsername)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byExternal[externalID]; ok {
		u := s.users[id]
		if u == nil {
			return nil, fmt.Errorf("identity links to a missing user")
		}
		cp := *u
		return &cp, nil
	}
	if username == "" {
		username = "oauth-" + idgen.Hex(3)
	}
	base := username
	for i := 0; i < 20; i++ {
		if s.userByNameLocked(username) == nil {
			break
		}
		username = base + "-" + idgen.Hex(2)
	}
	if s.userByNameLocked(username) != nil {
		return nil, fmt.Errorf("could not allocate a unique username")
	}
	u := &User{
		ID:          idgen.Hex(12),
		Username:    username,
		DisplayName: strings.TrimSpace(displayName),
		Role:        role,
		ExternalID:  externalID,
		CreatedAt:   time.Now().UTC(),
	}
	s.addUserLocked(u)
	if err := s.saveLocked(); err != nil {
		delete(s.users, u.ID)
		delete(s.byName, strings.ToLower(username))
		delete(s.byExternal, externalID)
		return nil, err
	}
	cp := *u
	return &cp, nil
}

// EffectiveQuota resolves user overrides against server defaults.
func (s *Store) EffectiveQuota(userID string) config.QuotaConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := s.defaults
	if u := s.users[userID]; u != nil {
		if u.Quota.MaxTunnels > 0 {
			q.MaxTunnelsPerUser = u.Quota.MaxTunnels
		}
		if u.Quota.MaxConnections > 0 {
			q.MaxConnectionsPerTunnel = u.Quota.MaxConnections
		}
		if u.Quota.DailyTrafficBytes > 0 {
			q.DailyTrafficBytes = u.Quota.DailyTrafficBytes
		}
	}
	return q
}

// DefaultQuota returns the server-wide defaults.
func (s *Store) DefaultQuota() config.QuotaConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults
}

// SetDefaults updates server-wide quota defaults (hot reload).
func (s *Store) SetDefaults(q config.QuotaConfig) {
	s.mu.Lock()
	s.defaults = q
	s.mu.Unlock()
}

// HashToken returns the hex SHA-256 used for token-at-rest storage.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenAEAD derives the token-sealing key from the server secret. A separate
// label keeps it independent from the JWT signing key even though both are
// derived from the same file.
func (s *Store) tokenAEAD() (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, s.jwtSecret)
	// "tunnelx/user-token/v1" is a HMAC domain separator predating the rename;
	// changing it would invalidate every existing token hash in users.json.
	mac.Write([]byte("tunnelx/user-token/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealToken encrypts a token for console display. A nil result means the token
// is still perfectly usable; only the "view current token" feature degrades.
func (s *Store) sealToken(token string) string {
	aead, err := s.tokenAEAD()
	if err != nil {
		return ""
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}
	sealed := aead.Seal(nonce, nonce, []byte(token), nil)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

func (s *Store) openSealedToken(sealed string) (string, error) {
	aead, err := s.tokenAEAD()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("decode sealed token: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("sealed token is truncated")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("open sealed token: %w", err)
	}
	return string(plain), nil
}

// RevealToken returns the plaintext client token for the console. It is the
// only path that hands a stored credential back to an administrator, and the
// caller is expected to be admin-scoped.
func (s *Store) RevealToken(id string) (string, error) {
	s.mu.RLock()
	u := s.users[id]
	var sealed string
	if u != nil {
		sealed = u.TokenSealed
	}
	s.mu.RUnlock()
	if u == nil {
		return "", fmt.Errorf("user not found")
	}
	if sealed == "" {
		return "", ErrTokenNotRecoverable
	}
	return s.openSealedToken(sealed)
}

func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 32 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (s *Store) addUserLocked(u *User) {
	s.users[u.ID] = u
	s.byName[strings.ToLower(u.Username)] = u.ID
	if u.TokenHash != "" {
		s.byToken[u.TokenHash] = u.ID
	}
	if u.ExternalID != "" {
		s.byExternal[u.ExternalID] = u.ID
	}
}

func normalizeUsername(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '-' || r == '_':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) < 3 {
		return ""
	}
	return out
}

func (s *Store) userByNameLocked(name string) *User {
	id, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil
	}
	return s.users[id]
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read user store: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("parse user store %s: %w", s.path, err)
	}
	for _, u := range p.Users {
		if u == nil || u.ID == "" || u.Username == "" {
			continue
		}
		if !u.Role.Valid() {
			u.Role = RoleViewer
		}
		s.addUserLocked(u)
	}
	return nil
}

func (s *Store) saveLocked() error {
	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		cp := *u
		users = append(users, &cp)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	raw, err := json.Marshal(persisted{Users: users})
	if err != nil {
		return fmt.Errorf("encode user store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".users-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func loadOrCreateSecret(path string, size int) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(raw))
		if len(secret) < 16 {
			return nil, fmt.Errorf("invalid secret file %s: too short", path)
		}
		return []byte(secret), nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read secret %s: %w", path, err)
	}
	secret := idgen.URL(size)
	if err := writeSecret(path, []byte(secret)); err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func writeSecret(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write secret %s: %w", path, err)
	}
	return nil
}
