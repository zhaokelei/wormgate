// Package idgen provides cryptographically random identifiers used for
// sessions, tokens, connection IDs and trace IDs.
package idgen

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Hex returns n random bytes encoded as 2n lowercase hex characters.
func Hex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable for security-sensitive IDs.
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

// URL returns n random bytes in URL-safe base64 without padding. Use 32 bytes
// for tokens (256 bits of entropy).
func URL(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Session returns a 128-bit session identifier.
func Session() string { return Hex(16) }

// Conn returns a 96-bit connection identifier.
func Conn() string { return Hex(12) }

// Trace returns a 64-bit trace identifier for request correlation.
func Trace() string { return Hex(8) }

// Token returns a 256-bit secret token with a readable prefix.
func Token(prefix string) string { return prefix + URL(32) }
