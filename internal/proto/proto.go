// Package proto defines the control-plane JSON protocol and the binary
// stream header used on the multiplexed data channel.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Version is the control protocol version advertised during hello.
const Version = "1"

// Message types. Control messages are JSON objects shaped as
// {"type":"...","data":{...}}.
const (
	TypeHello         = "hello"
	TypeHelloOK       = "hello_ok"
	TypeRegister      = "register"
	TypeUnregister    = "unregister"
	TypeTunnelResult  = "tunnel_result"
	TypeHeartbeat     = "heartbeat"
	TypePing          = "ping"
	TypePong          = "pong"
	TypeKicked        = "kicked"
	TypeNewConnection = "new_connection"
	TypeError         = "error"
)

// Envelope is the wire representation of every control message.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// TunnelSpec is how a client describes a tunnel during registration. The
// Local address never leaves the client host except for display purposes.
type TunnelSpec struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Local      string `json:"local,omitempty"`
	Subdomain  string `json:"subdomain,omitempty"`
	RemotePort int    `json:"remote_port,omitempty"`
	// Encryption names the stream cipher applied inside the tunnel
	// ("aes-256-gcm"), or is empty for the default unencrypted stream. The
	// server rejects an unknown or unsupported scheme instead of downgrading.
	Encryption string `json:"encryption,omitempty"`
}

// Hello is sent by the client immediately after the control socket opens.
type Hello struct {
	ClientName string       `json:"client_name"`
	Version    string       `json:"version"`
	Tunnels    []TunnelSpec `json:"tunnels,omitempty"`
}

// HelloOK acknowledges the session and tells the client the heartbeat cadence.
type HelloOK struct {
	SessionID            string `json:"session_id"`
	ClientID             string `json:"client_id"`
	ServerVersion        string `json:"server_version"`
	HeartbeatIntervalSec int    `json:"heartbeat_interval_sec"`
}

// Register asks the server to expose one or more tunnels.
type Register struct {
	Tunnels []TunnelSpec `json:"tunnels"`
}

// Unregister removes tunnels by name.
type Unregister struct {
	Names []string `json:"names"`
}

// TunnelResult is the server's verdict for a single registration. PublicAddr
// is ready to paste into a browser or client.
type TunnelResult struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	OK         bool   `json:"ok"`
	PublicAddr string `json:"public_addr,omitempty"`
	Subdomain  string `json:"subdomain,omitempty"`
	RemotePort int    `json:"remote_port,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Heartbeat keeps the control session alive.
type Heartbeat struct {
	Timestamp int64 `json:"timestamp"`
}

// Ping/Pong measure control-plane liveness from server to client.
type Ping struct {
	Timestamp int64 `json:"timestamp"`
}

// Pong replies to Ping.
type Pong struct {
	Timestamp int64 `json:"timestamp"`
}

// Kicked tells a client it was disconnected administratively.
type Kicked struct {
	Reason string `json:"reason"`
}

// NewConnection is an observability notification. Routing itself happens on
// the data channel: the server opens a stream and writes a header, so a lost
// notification never loses traffic.
type NewConnection struct {
	Tunnel     string `json:"tunnel"`
	ConnID     string `json:"conn_id"`
	RemoteAddr string `json:"remote_addr"`
	Kind       string `json:"kind"`
	Timestamp  int64  `json:"timestamp"`
}

// Error reports an asynchronous protocol error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Encode marshals a control message.
func Encode(msgType string, value any) ([]byte, error) {
	var raw json.RawMessage
	if value != nil {
		b, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode %s message: %w", msgType, err)
		}
		raw = b
	}
	b, err := json.Marshal(Envelope{Type: msgType, Data: raw})
	if err != nil {
		return nil, fmt.Errorf("encode %s envelope: %w", msgType, err)
	}
	return b, nil
}

// Decode parses an envelope and optionally unmarshals its data into out.
func Decode(data []byte, out any) (string, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("decode control envelope: %w", err)
	}
	if strings.TrimSpace(env.Type) == "" {
		return "", fmt.Errorf("control message has no type")
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return env.Type, fmt.Errorf("decode %s data: %w", env.Type, err)
		}
	}
	return env.Type, nil
}

const (
	streamMagic       uint32 = 0x54584e4c // "TXNL"
	streamVersion     byte   = 1
	streamHeaderFixed        = 12
	// maxStreamHeader caps attacker-controlled JSON before it is decoded.
	maxStreamHeader = 4096
)

// StreamHeader prefixes every yamux stream. It identifies which tunnel the
// stream belongs to; ConnID lets server and client logs be correlated.
type StreamHeader struct {
	Tunnel string `json:"tunnel"`
	ConnID string `json:"conn_id"`
	Source string `json:"source,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

// WriteStreamHeader writes a length-prefixed binary header.
func WriteStreamHeader(w io.Writer, h StreamHeader) error {
	if err := h.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("encode stream header: %w", err)
	}
	if len(payload) > maxStreamHeader {
		return fmt.Errorf("stream header too large (%d bytes)", len(payload))
	}
	buf := make([]byte, streamHeaderFixed+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], streamMagic)
	buf[4] = streamVersion
	buf[5] = 0
	binary.BigEndian.PutUint16(buf[6:8], 0)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(payload)))
	copy(buf[streamHeaderFixed:], payload)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write stream header: %w", err)
	}
	return nil
}

// ReadStreamHeader validates and decodes a stream header.
func ReadStreamHeader(r io.Reader) (StreamHeader, error) {
	var h StreamHeader
	fixed := make([]byte, streamHeaderFixed)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return h, fmt.Errorf("read stream header: %w", err)
	}
	if binary.BigEndian.Uint32(fixed[0:4]) != streamMagic {
		return h, fmt.Errorf("invalid stream magic")
	}
	if fixed[4] != streamVersion {
		return h, fmt.Errorf("unsupported stream version %d", fixed[4])
	}
	size := binary.BigEndian.Uint32(fixed[8:12])
	if size == 0 || size > maxStreamHeader {
		return h, fmt.Errorf("invalid stream header size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return h, fmt.Errorf("read stream header body: %w", err)
	}
	if err := json.Unmarshal(payload, &h); err != nil {
		return h, fmt.Errorf("decode stream header: %w", err)
	}
	if err := h.Validate(); err != nil {
		return h, err
	}
	return h, nil
}

// Validate enforces tight bounds on header fields.
func (h StreamHeader) Validate() error {
	if h.Tunnel == "" || len(h.Tunnel) > 64 {
		return fmt.Errorf("invalid tunnel name in stream header")
	}
	if h.ConnID == "" || len(h.ConnID) > 64 {
		return fmt.Errorf("invalid conn_id in stream header")
	}
	if len(h.Source) > 128 {
		return fmt.Errorf("source address too long")
	}
	if len(h.Kind) > 16 {
		return fmt.Errorf("kind too long")
	}
	return nil
}
