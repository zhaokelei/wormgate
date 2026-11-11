package proto

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload, err := Encode(TypeRegister, Register{Tunnels: []TunnelSpec{{Name: "web", Type: "http", Local: "127.0.0.1:8080"}}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got Register
	typ, err := Decode(payload, &got)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if typ != TypeRegister {
		t.Fatalf("type = %q, want %q", typ, TypeRegister)
	}
	if len(got.Tunnels) != 1 || got.Tunnels[0].Name != "web" || got.Tunnels[0].Local != "127.0.0.1:8080" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestDecodeRejectsMissingType(t *testing.T) {
	if _, err := Decode([]byte(`{"data":{}}`), nil); err == nil {
		t.Fatal("expected an error for a message without a type")
	}
}

func TestStreamHeaderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := StreamHeader{Tunnel: "api", ConnID: "abc123", Source: "203.0.113.9:1234", Kind: "tcp"}
	if err := WriteStreamHeader(&buf, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadStreamHeader(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStreamHeaderRejectsBadMagic(t *testing.T) {
	raw := make([]byte, streamHeaderFixed)
	copy(raw, "BAD!")
	if _, err := ReadStreamHeader(bytes.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("expected magic error, got %v", err)
	}
}

func TestStreamHeaderRejectsOversize(t *testing.T) {
	h := StreamHeader{Tunnel: "web", ConnID: "c1", Source: strings.Repeat("a", 4000)}
	// Validation catches the source length first; the framing cap is a second
	// line of defence against hostile peers.
	if err := h.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
