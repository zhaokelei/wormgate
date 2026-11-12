package server

import (
	"net/http"
	"testing"
)

func newTestServer(t *testing.T, cidrs ...string) *Server {
	t.Helper()
	networks, err := parseTrustedProxies(cidrs)
	if err != nil {
		t.Fatalf("parseTrustedProxies(%v): %v", cidrs, err)
	}
	return &Server{trustedProxies: networks}
}

func TestClientIPWithoutTrustedProxy(t *testing.T) {
	s := newTestServer(t)
	h := http.Header{}
	h.Set("X-Forwarded-For", "203.0.113.9")
	// A public caller must never be able to name itself: the header is only
	// read when the immediate peer is a configured proxy.
	if got := s.clientIP("198.51.100.7:44321", h); got != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want connection address 198.51.100.7", got)
	}
}

func TestClientIPFromTrustedProxy(t *testing.T) {
	s := newTestServer(t, "172.19.0.0/16", "127.0.0.1/32")

	cases := []struct {
		name   string
		source string
		xff    string
		want   string
	}{
		{"single hop", "172.19.0.3:51234", "203.0.113.9", "203.0.113.9"},
		{"proxy chain", "172.19.0.3:51234", "203.0.113.9, 172.19.0.4", "203.0.113.9"},
		{"ipv6 client", "172.19.0.3:51234", "2001:db8::1", "2001:db8::1"},
		{"padded entries", "172.19.0.3:51234", " 203.0.113.9 ,  10.0.0.1 ", "10.0.0.1"},
		{"no header", "172.19.0.3:51234", "", "172.19.0.3"},
		{"only proxies", "172.19.0.3:51234", "172.19.0.9", "172.19.0.3"},
		{"garbage entry", "172.19.0.3:51234", "not-an-ip", "172.19.0.3"},
		{"loopback proxy", "127.0.0.1:9999", "203.0.113.9", "203.0.113.9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.xff != "" {
				h.Set("X-Forwarded-For", tc.xff)
			}
			if got := s.clientIP(tc.source, h); got != tc.want {
				t.Fatalf("clientIP(%q, %q) = %q, want %q", tc.source, tc.xff, got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxiesBareAddress(t *testing.T) {
	networks, err := parseTrustedProxies([]string{"127.0.0.1", "::1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(networks) != 2 {
		t.Fatalf("got %d networks, want 2", len(networks))
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	if _, err := parseTrustedProxies([]string{"172.19.0.0/33"}); err == nil {
		t.Fatal("expected an error for an out-of-range prefix")
	}
	if _, err := parseTrustedProxies([]string{"caddy"}); err == nil {
		t.Fatal("expected an error for a non-address entry")
	}
}
