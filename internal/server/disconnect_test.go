package server

import "testing"

// TestBenignDisconnect pins down which session-close reasons must stay out of
// the alert centre. Everything an operator triggered on purpose (or that fpc
// retries by itself) belongs here; genuine transport failures do not.
func TestBenignDisconnect(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"server shutting down", true},
		{"replaced by a newer session with the same client name", true},
		{"control channel closed", true},
		{"data channel closed", true},
		// Admin-initiated kicks: audited with the acting principal, not alerts.
		{"account disabled by administrator", true},
		{"account deleted by administrator", true},
		// Real problems must still alert.
		{"read tcp 10.0.0.1:5000: connection reset by peer", false},
		{"control channel handshake timeout", false},
		{"quota exceeded", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := benignDisconnect(tc.reason); got != tc.want {
			t.Errorf("benignDisconnect(%q) = %v, want %v", tc.reason, got, tc.want)
		}
	}
}
