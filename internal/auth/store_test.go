package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wormgate/wormgate/internal/config"
)

// newTestStore opens a store in a throwaway data directory.
func newTestStore(t *testing.T) (*Store, *Bootstrap) {
	t.Helper()
	store, boot, err := Open(OpenOptions{
		DataDir:   t.TempDir(),
		AdminUser: "admin",
		Defaults:  config.QuotaConfig{},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store, boot
}

// TestBootstrapDemoTokenAuthenticates is the regression test for a bug where the
// bootstrap demo user was indexed before its token hash existed, so the token
// printed at first start was rejected with 401 until the process restarted.
func TestBootstrapDemoTokenAuthenticates(t *testing.T) {
	store, boot := newTestStore(t)
	if boot.ClientToken == "" {
		t.Fatal("expected a bootstrap client token")
	}

	principal, err := store.AuthenticateToken(boot.ClientToken)
	if err != nil {
		t.Fatalf("bootstrap token rejected: %v", err)
	}
	if principal.Username != "demo" {
		t.Fatalf("principal = %q, want demo", principal.Username)
	}
	if principal.Role != RoleUser {
		t.Fatalf("role = %q, want %q", principal.Role, RoleUser)
	}

	// A rotated token must invalidate the old one and keep working itself.
	rotated, err := store.RotateToken(principal.UserID)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	if _, err := store.AuthenticateToken(boot.ClientToken); err == nil {
		t.Fatal("old token still accepted after rotation")
	}
	if _, err := store.AuthenticateToken(rotated); err != nil {
		t.Fatalf("rotated token rejected: %v", err)
	}
}

// TestBootstrapTokenSurvivesRestart verifies the token file is re-read and the
// persisted hash still authenticates on a fresh Store.
func TestBootstrapTokenSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := OpenOptions{DataDir: dir, AdminUser: "admin"}
	_, boot, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if boot.ClientToken == "" {
		t.Fatal("expected a bootstrap client token")
	}

	store2, boot2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if boot2.ClientToken != boot.ClientToken {
		t.Fatalf("token changed across restart: %q vs %q", boot2.ClientToken, boot.ClientToken)
	}
	if !boot2.TokenReprinted {
		t.Fatal("expected the token to be reprinted on restart")
	}
	if _, err := store2.AuthenticateToken(boot.ClientToken); err != nil {
		t.Fatalf("token rejected after restart: %v", err)
	}
}

// TestTokenFilePermissions documents the on-disk mode of the demo token.
//
// os.WriteFile applies the 0600 mode only when it creates the file; on a
// filesystem that does not implement POSIX modes (NTFS via Go's Windows layer
// reports 0666) the requirement cannot be met, so the assertion is limited to
// platforms where permissions are meaningful. The operator-facing mitigation on
// Windows is the per-user ACL on the data directory.
func TestTokenFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Open(OpenOptions{DataDir: dir, AdminUser: "admin"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "demo-token"))
	if err != nil {
		t.Fatalf("stat demo-token: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skipf("Windows reports POSIX modes as %o; ACLs govern access instead", info.Mode().Perm())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("demo-token permissions = %o, want group/other bits clear", perm)
	}
}

// TestSetPasswordRotatesCredential is the regression test for the self-service
// password change: the old password must stop working, the new one must work,
// and the change must be durable plus timestamped so stale console JWTs can be
// rejected by the server.
func TestSetPasswordRotatesCredential(t *testing.T) {
	dir := t.TempDir()
	opts := OpenOptions{DataDir: dir, AdminUser: "admin"}
	store, boot, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if boot.GeneratedAdminPassword == "" {
		t.Fatal("expected a generated admin password")
	}
	admin, ok := store.UserByName("admin")
	if !ok {
		t.Fatal("admin user missing")
	}
	if _, err := store.AuthenticatePassword("admin", boot.GeneratedAdminPassword); err != nil {
		t.Fatalf("bootstrap password rejected: %v", err)
	}
	if err := store.SetPassword(admin.ID, "new-password-123"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if _, err := store.AuthenticatePassword("admin", boot.GeneratedAdminPassword); err == nil {
		t.Fatal("old password still accepted after change")
	}
	if _, err := store.AuthenticatePassword("admin", "new-password-123"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	changed, _ := store.GetUser(admin.ID)
	if changed.PasswordChangedAt.IsZero() {
		t.Fatal("PasswordChangedAt not set")
	}

	store2, _, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := store2.AuthenticatePassword("admin", "new-password-123"); err != nil {
		t.Fatalf("new password rejected after restart: %v", err)
	}
	persisted, _ := store2.GetUser(admin.ID)
	if persisted.PasswordChangedAt.IsZero() {
		t.Fatal("PasswordChangedAt not persisted")
	}
	if err := store2.SetPassword(admin.ID, "short"); err == nil {
		t.Fatal("password shorter than 8 characters was accepted")
	}
}

// TestDisabledUserTokenRejected ensures disabling an account also blocks its
// client token (the same switch that blocks console login).
func TestDisabledUserTokenRejected(t *testing.T) {
	store, boot := newTestStore(t)
	principal, err := store.AuthenticateToken(boot.ClientToken)
	if err != nil {
		t.Fatalf("bootstrap token: %v", err)
	}
	user, ok := store.GetUser(principal.UserID)
	if !ok {
		t.Fatal("demo user missing")
	}
	if _, err := store.UpdateUser(user.ID, user.DisplayName, user.Role, user.Quota, true); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if _, err := store.AuthenticateToken(boot.ClientToken); err == nil {
		t.Fatal("disabled user's token is still accepted")
	}
}

// TestRevealTokenRoundTrip covers the console's "view current token" path: the
// sealed copy must decrypt to the token the client authenticates with, and it
// must survive a restart.
func TestRevealTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, boot, err := Open(OpenOptions{DataDir: dir, AdminUser: "admin", Defaults: config.QuotaConfig{}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.SetPassword("admin", "admin-password-123"); err == nil {
		t.Log("admin password set")
	}
	user, token, err := store.CreateUser("alice", "", RoleUser, Quota{})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := store.RevealToken(user.ID)
	if err != nil {
		t.Fatalf("RevealToken: %v", err)
	}
	if got != token {
		t.Fatalf("revealed token = %q, want %q", got, token)
	}
	if HashToken(got) != user.TokenHash {
		t.Fatal("revealed token does not match the stored hash")
	}

	rotated, err := store.RotateToken(user.ID)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	got, err = store.RevealToken(user.ID)
	if err != nil {
		t.Fatalf("RevealToken after rotate: %v", err)
	}
	if got != rotated {
		t.Fatalf("revealed token after rotate = %q, want %q", got, rotated)
	}

	// The demo token generated at bootstrap must also be recoverable.
	demoID, ok := store.byName["demo"]
	if !ok {
		t.Fatal("demo user missing")
	}
	demo, ok := store.GetUser(demoID)
	if !ok {
		t.Fatal("demo user missing")
	}
	revealed, err := store.RevealToken(demo.ID)
	if err != nil {
		t.Fatalf("RevealToken(demo): %v", err)
	}
	if revealed != boot.ClientToken {
		t.Fatalf("demo token = %q, want %q", revealed, boot.ClientToken)
	}

	// Reopen to prove the sealed copy is persisted, not just held in memory.
	again, _, err := Open(OpenOptions{DataDir: dir, AdminUser: "admin", Defaults: config.QuotaConfig{}})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err = again.RevealToken(user.ID)
	if err != nil {
		t.Fatalf("RevealToken after reopen: %v", err)
	}
	if got != rotated {
		t.Fatalf("revealed token after reopen = %q, want %q", got, rotated)
	}
}

// TestRevealTokenLegacyAccount makes sure accounts without a sealed copy report
// the recoverable error instead of panicking or leaking the hash.
func TestRevealTokenLegacyAccount(t *testing.T) {
	store, _ := newTestStore(t)
	legacy, _, err := store.CreateUser("bob", "", RoleUser, Quota{})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	store.mu.Lock()
	store.users[legacy.ID].TokenSealed = ""
	store.mu.Unlock()
	if _, err := store.RevealToken(legacy.ID); !errors.Is(err, ErrTokenNotRecoverable) {
		t.Fatalf("err = %v, want ErrTokenNotRecoverable", err)
	}
}
