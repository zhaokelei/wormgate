// Package streamcipher adds an authenticated encryption layer on top of a
// tunnel stream.
//
// It exists because the TLS hop between fpc and fps is not always
// authenticated: a client running with --insecure (or with a mismatched CA)
// will happily talk to an impostor. A shared secret configured on both ends
// gives that traffic confidentiality and integrity that does not depend on
// certificate validation at all.
//
// The layer is opt-in per tunnel. Nothing here is used unless a tunnel sets
// `encryption: true` and both ends configure the same stream secret.
package streamcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/hkdf"
)

// Scheme is the value carried on the wire in a tunnel spec. An empty scheme
// means "no stream encryption", which is the default.
const Scheme = "aes-256-gcm"

// maxFrame bounds one plaintext chunk. It matches the pipe buffer size used by
// internal/netpipe so a typical read maps to a single frame.
const maxFrame = 32 * 1024

// tagLen is the AES-GCM authentication tag length appended by Seal.
const tagLen = 16

var (
	// ErrNoSecret reports a tunnel that asked for encryption while the local
	// side has no stream secret configured. It is surfaced as a registration
	// failure rather than silently downgrading to plaintext.
	ErrNoSecret = errors.New("stream encryption requested but no stream secret is configured")
	// ErrFrame reports a corrupt or oversized length prefix.
	ErrFrame = errors.New("stream cipher: invalid frame length")
	// ErrAuth reports a frame that failed authentication: wrong key, tampered
	// data, or a reordered stream.
	ErrAuth = errors.New("stream cipher: authentication failed")
)

// DeriveKey expands the shared secret into a 32-byte AES key bound to one
// session and one tunnel name. Binding to the session id means a key captured
// from one connection cannot decrypt another, and binding to the tunnel name
// keeps sibling tunnels on the same client isolated from each other.
func DeriveKey(secret, sessionID, tunnel string) ([]byte, error) {
	if secret == "" {
		return nil, ErrNoSecret
	}
	// NOTE: "tunnelx/stream/v1" is a wire-format domain separator. It predates
	// the Wormgate rename and MUST NOT change: a client and a server built
	// from different generations would derive different session keys and fail
	// to understand each other.
	reader := hkdf.New(sha256.New, []byte(secret), []byte(sessionID), []byte("tunnelx/stream/v1/"+tunnel))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive stream key: %w", err)
	}
	return key, nil
}

// direction derives a per-direction subkey. Both peers derive both keys, so
// the read and write nonce counters can start at zero without colliding.
func direction(key []byte, label string) ([]byte, error) {
	// Same domain separator as DeriveKey — see the note there.
	reader := hkdf.New(sha256.New, key, nil, []byte("tunnelx/stream/v1/"+label))
	out := make([]byte, 32)
	if _, err := io.ReadFull(reader, out); err != nil {
		return nil, fmt.Errorf("derive %s key: %w", label, err)
	}
	return out, nil
}

// Conn is a net.Conn whose payload is framed and sealed with AES-256-GCM.
//
// Framing: [4-byte big-endian length][ciphertext]. The length covers the
// ciphertext including the 16-byte tag. Nonces are implicit per direction
// counters, which is safe because yamux guarantees an ordered, reliable
// stream: any gap shows up as an authentication failure and kills the tunnel
// rather than corrupting traffic.
type Conn struct {
	net.Conn

	readAEAD  cipher.AEAD
	writeAEAD cipher.AEAD

	readMu  sync.Mutex
	writeMu sync.Mutex
	rnonce  uint64
	wnonce  uint64
	readBuf []byte
	readErr error
}

// NewConn wraps conn. initiator reports whether this side opened the stream;
// it selects which direction subkey encrypts outgoing bytes.
func NewConn(conn net.Conn, key []byte, initiator bool) (*Conn, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("stream cipher: key must be 32 bytes, got %d", len(key))
	}
	s2c, err := direction(key, "server-to-client")
	if err != nil {
		return nil, err
	}
	c2s, err := direction(key, "client-to-server")
	if err != nil {
		return nil, err
	}
	readKey, writeKey := s2c, c2s
	if !initiator {
		readKey, writeKey = c2s, s2c
	}
	readBlock, err := aes.NewCipher(readKey)
	if err != nil {
		return nil, fmt.Errorf("stream cipher: read cipher: %w", err)
	}
	writeBlock, err := aes.NewCipher(writeKey)
	if err != nil {
		return nil, fmt.Errorf("stream cipher: write cipher: %w", err)
	}
	readAEAD, err := cipher.NewGCM(readBlock)
	if err != nil {
		return nil, fmt.Errorf("stream cipher: read aead: %w", err)
	}
	writeAEAD, err := cipher.NewGCM(writeBlock)
	if err != nil {
		return nil, fmt.Errorf("stream cipher: write aead: %w", err)
	}
	return &Conn{Conn: conn, readAEAD: readAEAD, writeAEAD: writeAEAD}, nil
}

func nonceFor(counter uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], counter)
	return nonce
}

// Read decrypts the next frame, buffering whatever the caller's slice cannot
// take in one go.
func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}
	if c.readErr != nil {
		return 0, c.readErr
	}

	var header [4]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		c.readErr = err
		return 0, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size <= tagLen || size > maxFrame+tagLen {
		c.readErr = ErrFrame
		return 0, ErrFrame
	}

	frame := make([]byte, size)
	if _, err := io.ReadFull(c.Conn, frame); err != nil {
		c.readErr = err
		return 0, err
	}
	plain, err := c.readAEAD.Open(frame[:0], nonceFor(c.rnonce), frame, nil)
	if err != nil {
		c.readErr = ErrAuth
		return 0, ErrAuth
	}
	c.rnonce++

	n := copy(p, plain)
	if n < len(plain) {
		c.readBuf = append(c.readBuf[:0], plain[n:]...)
	}
	return n, nil
}

// Write seals p in frames of at most maxFrame plaintext bytes.
func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	written := 0
	var header [4]byte
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxFrame {
			chunk = chunk[:maxFrame]
		}
		sealed := c.writeAEAD.Seal(nil, nonceFor(c.wnonce), chunk, nil)
		c.wnonce++
		binary.BigEndian.PutUint32(header[:], uint32(len(sealed)))
		// One buffer per frame keeps the length prefix and payload in a single
		// write, which matters on the yamux write path.
		out := make([]byte, 4+len(sealed))
		copy(out, header[:])
		copy(out[4:], sealed)
		if _, err := c.Conn.Write(out); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// CloseWrite forwards the half-close so protocols that rely on EOF (HTTP/1.1,
// netcat-style TCP) still see it. Without this, netpipe's half-close handling
// would block until the 5s grace period expired on every request.
func (c *Conn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
