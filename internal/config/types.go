// Package config implements Wormgate's layered configuration system.
//
// Precedence (highest first): CLI flags > environment variables > project
// configuration file > global configuration file > built-in defaults.
package config

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a YAML-friendly time.Duration. It accepts "15s", "1m30s" and
// plain integers (interpreted as seconds) so hand-written configs stay terse.
type Duration time.Duration

// D returns the value as a standard time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar string such as \"15s\"")
	}
	if value.Tag == "!!int" {
		var seconds int64
		if err := value.Decode(&seconds); err != nil {
			return fmt.Errorf("invalid duration %q: %w", value.Value, err)
		}
		if seconds < 0 {
			return fmt.Errorf("duration must not be negative")
		}
		*d = Duration(time.Duration(seconds) * time.Second)
		return nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value.Value))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use forms like 500ms, 15s, 2m): %w", value.Value, err)
	}
	if parsed < 0 {
		return fmt.Errorf("duration %q must not be negative", value.Value)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Config is the root configuration shared by the server and the client.
//
// // CONFIG: A single file can configure both roles; each binary reads the
// section that applies to it. Defaults are always merged first.
type Config struct {
	// Profile selects an overlay from the top-level "profiles" map.
	Profile string       `yaml:"profile,omitempty"`
	Log     LogConfig    `yaml:"log"`
	Server  ServerConfig `yaml:"server"`
	Client  ClientConfig `yaml:"client"`
}

// LogConfig controls structured logging.
type LogConfig struct {
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level"`
	// Format is "console" (human friendly) or "json" (production).
	Format string `yaml:"format"`
	// AddCaller appends file:line to each record.
	AddCaller bool `yaml:"add_caller"`
}

// ServerConfig configures the wormgate server role.
type ServerConfig struct {
	ControlAddr       string            `yaml:"control_addr"`
	HeartbeatInterval Duration          `yaml:"heartbeat_interval"`
	SessionTimeout    Duration          `yaml:"session_timeout"`
	TLS               TLSServerConfig   `yaml:"tls"`
	HTTP              HTTPConfig        `yaml:"http"`
	TCP               TCPConfig         `yaml:"tcp"`
	WebUI             ServerWebUIConfig `yaml:"webui"`
	Metrics           MetricsConfig     `yaml:"metrics"`
	Auth              AuthConfig        `yaml:"auth"`
	Quota             QuotaConfig       `yaml:"quota"`
	// StreamSecret keys the optional per-tunnel AES-256-GCM layer. Leave empty
	// to disable it: tunnels that request encryption are then rejected at
	// registration instead of running unencrypted by accident.
	StreamSecret string `yaml:"stream_secret"`
	// DataDir stores persisted users, audit logs and the zero-config secrets.
	DataDir string `yaml:"data_dir"`
}

// TLSServerConfig configures the control/data TLS listener.
type TLSServerConfig struct {
	// CertFile and KeyFile enable a user-provided certificate. When both are
	// empty and AutoSelfSigned is true, a short-lived self-signed certificate
	// is generated at boot (development only).
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// ClientCAFile enables mutual TLS: every client must present a
	// certificate signed by this CA. Combined with the token this is the
	// documented two-factor setup.
	ClientCAFile string `yaml:"client_ca_file"`
	// AutoSelfSigned is ignored when CertFile/KeyFile are set.
	AutoSelfSigned bool `yaml:"auto_self_signed"`
}

// HTTPConfig configures the public HTTP ingress used by http tunnels.
type HTTPConfig struct {
	Addr string `yaml:"addr"`
	// Domain is appended to every assigned subdomain, e.g. "wormgate.local".
	Domain string `yaml:"domain"`
	// Scheme is used when rendering public URLs ("http" or "https" when a
	// TLS-terminating load balancer sits in front of Wormgate).
	Scheme string `yaml:"scheme"`
	// PublicPort overrides the port advertised in public URLs. Zero derives
	// it from the bound listener (omitting 80/443 for the matching scheme).
	PublicPort        int      `yaml:"public_port"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	IdleTimeout       Duration `yaml:"idle_timeout"`
}

// TCPConfig configures public TCP ingress.
type TCPConfig struct {
	BindAddr string `yaml:"bind_addr"`
	// AdvertiseHost is shown in public TCP addresses. When empty the server's
	// control-plane host is used.
	AdvertiseHost string `yaml:"advertise_host"`
	// PortRange is the inclusive range used for automatic remote port
	// assignment, e.g. "20000-30000".
	PortRange string `yaml:"port_range"`
}

// ServerWebUIConfig configures the embedded admin console.
type ServerWebUIConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Addr      string `yaml:"addr"`
	AdminUser string `yaml:"admin_user"`
	// AdminPassword is only used to bootstrap the first admin. Prefer
	// AdminPasswordHash. Plain passwords are never written back by Wormgate.
	AdminPassword     string       `yaml:"admin_password"`
	AdminPasswordHash string       `yaml:"admin_password_hash"`
	SessionTTL        Duration     `yaml:"session_ttl"`
	SecureCookie      bool         `yaml:"secure_cookie"`
	OAuth2            OAuth2Config `yaml:"oauth2"`
}

// OAuth2Config optionally enables single sign-on for the admin console.
// Wormgate implements the authorization-code flow against a userinfo endpoint.
type OAuth2Config struct {
	Enabled       bool     `yaml:"enabled"`
	ClientID      string   `yaml:"client_id"`
	ClientSecret  string   `yaml:"client_secret"`
	AuthURL       string   `yaml:"auth_url"`
	TokenURL      string   `yaml:"token_url"`
	UserInfoURL   string   `yaml:"userinfo_url"`
	RedirectURL   string   `yaml:"redirect_url"`
	Scopes        []string `yaml:"scopes"`
	UsernameField string   `yaml:"username_field"`
	DefaultRole   string   `yaml:"default_role"`
}

// MetricsConfig configures the Prometheus endpoint.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// AuthConfig holds static client tokens. Dynamic, per-user tokens live in the
// data store and are managed from the admin console.
type AuthConfig struct {
	// Tokens are bootstrap tokens accepted by the control plane. They are
	// intended for development and small deployments. A client presenting a
	// static token is owned by the reserved user "static".
	Tokens []string `yaml:"tokens"`
}

// QuotaConfig defines server-side limits. Zero means "unlimited" except for
// the per-user tunnel limit where 0 also means unlimited.
type QuotaConfig struct {
	MaxTunnelsPerUser       int `yaml:"max_tunnels_per_user"`
	MaxConnectionsPerTunnel int `yaml:"max_connections_per_tunnel"`
	// DailyTrafficBytes triggers an alert and blocks new public connections
	// once the owner's daily traffic exceeds the limit. 0 disables it.
	DailyTrafficBytes int64 `yaml:"daily_traffic_bytes"`
}

// ClientConfig configures the wormgate client role.
type ClientConfig struct {
	// Server is the control-plane address as host:port. TLS is mandatory;
	// the client derives wss:// from this address.
	Server            string            `yaml:"server"`
	Token             string            `yaml:"token"`
	ClientName        string            `yaml:"client_name"`
	TLS               TLSClientConfig   `yaml:"tls"`
	HeartbeatInterval Duration          `yaml:"heartbeat_interval"`
	DialTimeout       Duration          `yaml:"dial_timeout"`
	Reconnect         ReconnectConfig   `yaml:"reconnect"`
	Tunnels           []TunnelConfig    `yaml:"tunnels"`
	WebUI             ClientWebUIConfig `yaml:"webui"`
	// StreamSecret must match the server's value to enable encrypted tunnels.
	// Required only for tunnels that set `encryption: true`.
	StreamSecret string `yaml:"stream_secret"`
}

// TLSClientConfig configures the client side of the control/data channels.
type TLSClientConfig struct {
	CAFile   string `yaml:"ca_file"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// InsecureSkipVerify disables certificate verification. Development only.
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	ServerName         string `yaml:"server_name"`
}

// ReconnectConfig controls exponential reconnect backoff.
type ReconnectConfig struct {
	MinBackoff Duration `yaml:"min_backoff"`
	MaxBackoff Duration `yaml:"max_backoff"`
}

// ClientWebUIConfig configures the local client console. It must stay bound
// to loopback unless the operator deliberately exposes it.
type ClientWebUIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	// Token authenticates the console. When empty, Wormgate generates one on
	// first use and stores it with 0600 permissions in DataDir.
	Token string `yaml:"token"`
	// PasswordHash optionally adds password login (argon2id encoded hash).
	PasswordHash string   `yaml:"password_hash"`
	DataDir      string   `yaml:"data_dir"`
	SessionTTL   Duration `yaml:"session_ttl"`
}

// TunnelConfig declares a tunnel exposed by the client.
type TunnelConfig struct {
	Name string `yaml:"name"`
	// Type is "http" (subdomain routing) or "tcp" (dedicated remote port).
	Type  string `yaml:"type"`
	Local string `yaml:"local"`
	// Subdomain is optional; the server assigns one when empty.
	Subdomain string `yaml:"subdomain"`
	// RemotePort is optional for TCP tunnels; the server allocates from its
	// configured range when zero.
	RemotePort int `yaml:"remote_port"`
	// Encryption adds an AES-256-GCM layer inside the tunnel stream, keyed by
	// the shared stream secret. It is independent of the TLS hop, so it still
	// protects traffic when certificate verification is skipped. Optional and
	// off by default: both ends must configure the same secret.
	Encryption bool `yaml:"encryption,omitempty"`
	// Enabled defaults to true when omitted.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// IsEnabled reports whether the tunnel should be active.
func (t TunnelConfig) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }

// Bool returns a pointer suitable for TunnelConfig.Enabled.
func Bool(v bool) *bool { return &v }

// Default returns the built-in configuration. It aims to be safe: control
// TLS is always on and both consoles bind to loopback.
func Default() *Config {
	return &Config{
		Log: LogConfig{Level: "info", Format: "console", AddCaller: false},
		Server: ServerConfig{
			ControlAddr:       "0.0.0.0:7000",
			HeartbeatInterval: Duration(15 * time.Second),
			SessionTimeout:    Duration(45 * time.Second),
			TLS:               TLSServerConfig{AutoSelfSigned: true},
			HTTP: HTTPConfig{
				Addr:              "0.0.0.0:8080",
				Domain:            "wormgate.local",
				Scheme:            "http",
				ReadHeaderTimeout: Duration(10 * time.Second),
				IdleTimeout:       Duration(90 * time.Second),
			},
			TCP: TCPConfig{BindAddr: "0.0.0.0", PortRange: "20000-30000"},
			WebUI: ServerWebUIConfig{
				Enabled:    true,
				Addr:       "0.0.0.0:7100",
				AdminUser:  "admin",
				SessionTTL: Duration(12 * time.Hour),
			},
			Metrics: MetricsConfig{Enabled: true, Addr: "0.0.0.0:9090"},
			Quota: QuotaConfig{
				MaxTunnelsPerUser:       20,
				MaxConnectionsPerTunnel: 200,
			},
			DataDir: "data",
		},
		Client: ClientConfig{
			Server:            "127.0.0.1:7000",
			HeartbeatInterval: Duration(15 * time.Second),
			DialTimeout:       Duration(5 * time.Second),
			Reconnect: ReconnectConfig{
				MinBackoff: Duration(time.Second),
				MaxBackoff: Duration(30 * time.Second),
			},
			WebUI: ClientWebUIConfig{
				Enabled:    true,
				Addr:       "127.0.0.1:7200",
				DataDir:    "data",
				SessionTTL: Duration(12 * time.Hour),
			},
		},
	}
}
