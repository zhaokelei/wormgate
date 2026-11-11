package streamcipher

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// pair builds a server/client stream pair the way the tunnel does: the server
// side opens the stream (initiator), the client side accepts it.
func pair(t *testing.T, secret, session, tunnel string) (server, client net.Conn) {
	t.Helper()
	key, err := DeriveKey(secret, session, tunnel)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	rawServer, rawClient := net.Pipe()
	server, err = NewConn(rawServer, key, true)
	if err != nil {
		t.Fatalf("server conn: %v", err)
	}
	client, err = NewConn(rawClient, key, false)
	if err != nil {
		t.Fatalf("client conn: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return server, client
}

func TestRoundTripBothDirections(t *testing.T) {
	server, client := pair(t, "a-long-shared-secret-value", "sess-1", "tcpweb")

	payload := make([]byte, 300*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("random payload: %v", err)
	}

	// Server -> client.
	go func() {
		if _, err := server.Write(payload); err != nil {
			t.Errorf("server write: %v", err)
		}
		_ = server.(*Conn).CloseWrite()
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("server->client payload mismatch")
	}

	// Client -> server, exercising the opposite direction subkey.
	small := []byte("hello from the client side")
	go func() {
		if _, err := client.Write(small); err != nil {
			t.Errorf("client write: %v", err)
		}
	}()
	back := make([]byte, len(small))
	if _, err := io.ReadFull(server, back); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if !bytes.Equal(back, small) {
		t.Fatalf("client->server payload mismatch: %q", back)
	}
}

func TestReadAcrossFrameBoundary(t *testing.T) {
	server, client := pair(t, "a-long-shared-secret-value", "sess-1", "tcpweb")

	// One frame larger than the caller's buffer forces the internal read
	// buffer to split it, which is the common case for a 4 KB HTTP read.
	payload := bytes.Repeat([]byte("A"), maxFrame+1234)
	go func() {
		if _, err := server.Write(payload); err != nil {
			t.Errorf("write: %v", err)
		}
		_ = server.(*Conn).CloseWrite()
	}()

	var got bytes.Buffer
	buf := make([]byte, 1024)
	for got.Len() < len(payload) {
		n, err := client.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("read: %v", err)
		}
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", got.Len(), len(payload))
	}
}

func TestWrongSecretFailsAuthentication(t *testing.T) {
	rightKey, err := DeriveKey("right-secret-value-here", "sess-1", "tcpweb")
	if err != nil {
		t.Fatalf("derive right: %v", err)
	}
	wrongKey, err := DeriveKey("wrong-secret-value-here", "sess-1", "tcpweb")
	if err != nil {
		t.Fatalf("derive wrong: %v", err)
	}
	// One physical pipe, two ends with mismatched keys: exactly what happens
	// when the two sides configure different stream_secret values.
	rawServer, rawClient := net.Pipe()
	server, err := NewConn(rawServer, rightKey, true)
	if err != nil {
		t.Fatalf("server conn: %v", err)
	}
	other, err := NewConn(rawClient, wrongKey, false)
	if err != nil {
		t.Fatalf("client conn: %v", err)
	}
	defer server.Close()
	defer other.Close()

	go func() {
		_, _ = server.Write([]byte("secret payload"))
	}()

	_ = other.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	if _, err := other.Read(buf); !errors.Is(err, ErrAuth) {
		t.Fatalf("expected ErrAuth with mismatched secrets, got %v", err)
	}
}

func TestTamperedFrameRejected(t *testing.T) {
	key, err := DeriveKey("a-long-shared-secret-value", "sess-1", "tcpweb")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	// sealer -> tamper -> opener, so exactly one byte of the sealed frame can
	// be flipped in flight.
	sealSide, tamperIn := net.Pipe()
	tamperOut, openSide := net.Pipe()
	sealer, err := NewConn(sealSide, key, true)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	opener, err := NewConn(openSide, key, false)
	if err != nil {
		t.Fatalf("opener: %v", err)
	}
	defer sealer.Close()
	defer opener.Close()

	go func() {
		buf := make([]byte, 64*1024)
		n, err := tamperIn.Read(buf)
		if err != nil || n < tagLen {
			return
		}
		buf[n-1] ^= 0xff // corrupt the GCM tag
		_, _ = tamperOut.Write(buf[:n])
	}()
	go func() {
		_, _ = sealer.Write([]byte("authentic message"))
	}()

	_ = opener.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := opener.Read(make([]byte, 64)); !errors.Is(err, ErrAuth) {
		t.Fatalf("expected ErrAuth on a tampered tag, got %v", err)
	}
}

func TestDeriveKeyDiffersPerTunnelAndSession(t *testing.T) {
	base, err := DeriveKey("shared-value", "sess-1", "tcpweb")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	otherTunnel, _ := DeriveKey("shared-value", "sess-1", "webpage")
	otherSession, _ := DeriveKey("shared-value", "sess-2", "tcpweb")

	if bytes.Equal(base, otherTunnel) {
		t.Fatal("keys must differ per tunnel name")
	}
	if bytes.Equal(base, otherSession) {
		t.Fatal("keys must differ per session")
	}
	if len(base) != 32 {
		t.Fatalf("key length = %d, want 32", len(base))
	}
}

func TestDeriveKeyRequiresSecret(t *testing.T) {
	if _, err := DeriveKey("", "sess-1", "tcpweb"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("expected ErrNoSecret, got %v", err)
	}
}

func TestEmptyWriteIsNoop(t *testing.T) {
	server, client := pair(t, "a-long-shared-secret-value", "sess-1", "tcpweb")
	if n, err := server.Write(nil); n != 0 || err != nil {
		t.Fatalf("empty write = (%d, %v), want (0, nil)", n, err)
	}
	_ = client.Close()
}
