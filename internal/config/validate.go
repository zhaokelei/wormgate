package config

import (
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Issue is a single configuration problem with a precise location and an
// optional suggestion. Keeping this structured (instead of only an error
// string) lets the CLI render friendly diagnostics.
type Issue struct {
	File    string
	Path    string
	Line    int
	Message string
	Hint    string
}

// Error implements error.
func (i Issue) Error() string { return i.String() }

// String renders a diagnostic line such as:
//
//	wormgate.yaml:12: server.control_addr: not a valid host:port (did you mean 0.0.0.0:7000?)
func (i Issue) String() string {
	var b strings.Builder
	if i.File != "" {
		b.WriteString(i.File)
		if i.Line > 0 {
			fmt.Fprintf(&b, ":%d", i.Line)
		}
		b.WriteString(": ")
	}
	if i.Path != "" {
		b.WriteString(i.Path)
		b.WriteString(": ")
	}
	b.WriteString(i.Message)
	if i.Hint != "" {
		b.WriteString(" (")
		b.WriteString(i.Hint)
		b.WriteString(")")
	}
	return b.String()
}

// ValidationResult groups errors and warnings. Warnings never block startup.
type ValidationResult struct {
	Errors   []Issue
	Warnings []Issue
}

// HasErrors reports whether validation failed.
func (v *ValidationResult) HasErrors() bool { return len(v.Errors) > 0 }

// ErrorList is returned by Load when one or more issues block startup.
type ErrorList struct {
	Issues []Issue
}

// Error implements error.
func (e *ErrorList) Error() string {
	if len(e.Issues) == 0 {
		return "configuration is invalid"
	}
	lines := make([]string, 0, len(e.Issues))
	for _, iss := range e.Issues {
		lines = append(lines, iss.String())
	}
	return "configuration is invalid:\n  " + strings.Join(lines, "\n  ")
}

var (
	tunnelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)
	dnsLabelRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Validate performs semantic validation on a decoded configuration. It checks
// both the server and client sections and requires a client token.
func Validate(c *Config) *ValidationResult {
	return validateSections(c, true, true, true)
}

// ValidateRole validates only the sections relevant to one role. An empty
// role checks both sections but treats a missing client token as a warning.
func ValidateRole(c *Config, role string) *ValidationResult {
	switch role {
	case "server":
		return validateSections(c, true, false, false)
	case "client":
		// A missing token is not fatal: fpc starts its local console first and
		// the operator enters the server token in the configuration page. The
		// agent waits for that value instead of exiting at startup.
		return validateSections(c, false, true, false)
	default:
		return validateSections(c, true, true, false)
	}
}

func validateSections(c *Config, checkServer, checkClient, requireToken bool) *ValidationResult {
	v := &ValidationResult{}
	addErr := func(path, msg string) { v.Errors = append(v.Errors, Issue{Path: path, Message: msg}) }
	addWarn := func(path, msg string) { v.Warnings = append(v.Warnings, Issue{Path: path, Message: msg}) }

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		addErr("log.level", fmt.Sprintf("must be one of debug|info|warn|error, got %q", c.Log.Level))
	}
	switch c.Log.Format {
	case "console", "json":
	default:
		addErr("log.format", fmt.Sprintf("must be console or json, got %q", c.Log.Format))
	}

	if checkServer {
		s := &c.Server
		if err := checkListenAddr(s.ControlAddr, false); err != nil {
			addErr("server.control_addr", err.Error())
		}
		if s.HeartbeatInterval.D() <= 0 {
			addErr("server.heartbeat_interval", "must be greater than zero")
		}
		if s.SessionTimeout.D() < s.HeartbeatInterval.D() {
			addErr("server.session_timeout", "must be greater than or equal to heartbeat_interval")
		}
		if (s.TLS.CertFile == "") != (s.TLS.KeyFile == "") {
			addErr("server.tls", "cert_file and key_file must be configured together")
		}
		if s.TLS.CertFile == "" && !s.TLS.AutoSelfSigned {
			addErr("server.tls", "no certificate configured and auto_self_signed is disabled")
		}
		if s.TLS.ClientCAFile != "" && !s.TLS.AutoSelfSigned && s.TLS.CertFile == "" {
			addErr("server.tls.client_ca_file", "mTLS requires a server certificate")
		}
		if err := checkListenAddr(s.HTTP.Addr, true); err != nil {
			addErr("server.http.addr", err.Error())
		}
		if err := validateDomain(s.HTTP.Domain); err != nil {
			addErr("server.http.domain", err.Error())
		}
		if s.HTTP.Scheme != "http" && s.HTTP.Scheme != "https" {
			addErr("server.http.scheme", "must be http or https")
		}
		if s.HTTP.PublicPort < 0 || s.HTTP.PublicPort > 65535 {
			addErr("server.http.public_port", "must be 0-65535")
		}
		if s.HTTP.ReadHeaderTimeout.D() <= 0 {
			addErr("server.http.read_header_timeout", "must be greater than zero")
		}
		if s.HTTP.IdleTimeout.D() <= 0 {
			addErr("server.http.idle_timeout", "must be greater than zero")
		}
		if err := checkListenAddr(s.TCP.BindAddr+":0", true); err != nil {
			addErr("server.tcp.bind_addr", err.Error())
		}
		if s.TCP.AdvertiseHost != "" && net.ParseIP(s.TCP.AdvertiseHost) == nil && !dnsLabelRe.MatchString(strings.ToLower(s.TCP.AdvertiseHost)) {
			addErr("server.tcp.advertise_host", "must be a hostname or IP address")
		}
		if _, _, err := ParsePortRange(s.TCP.PortRange); err != nil {
			addErr("server.tcp.port_range", err.Error())
		}

		if s.WebUI.Enabled {
			if err := checkListenAddr(s.WebUI.Addr, true); err != nil {
				addErr("server.webui.addr", err.Error())
			}
			if strings.TrimSpace(s.WebUI.AdminUser) == "" {
				addErr("server.webui.admin_user", "must not be empty")
			}
			if s.WebUI.AdminPasswordHash != "" && !strings.HasPrefix(s.WebUI.AdminPasswordHash, "$argon2id$") {
				addErr("server.webui.admin_password_hash", "must be an argon2id encoded hash")
			}
			if s.WebUI.AdminPassword != "" {
				addWarn("server.webui.admin_password", "plain-text password in config; prefer admin_password_hash")
			}
			if s.WebUI.SessionTTL.D() <= 0 {
				addErr("server.webui.session_ttl", "must be greater than zero")
			}
			if s.WebUI.OAuth2.Enabled {
				o := &s.WebUI.OAuth2
				if o.ClientID == "" || o.ClientSecret == "" {
					addErr("server.webui.oauth2", "client_id and client_secret are required")
				}
				for name, raw := range map[string]string{"auth_url": o.AuthURL, "token_url": o.TokenURL, "userinfo_url": o.UserInfoURL, "redirect_url": o.RedirectURL} {
					if _, err := url.ParseRequestURI(raw); err != nil {
						addErr("server.webui.oauth2."+name, "must be an absolute URL")
					}
				}
				if o.UsernameField == "" {
					addErr("server.webui.oauth2.username_field", "must name a field in the userinfo response")
				}
				if o.DefaultRole != "" && !validRole(o.DefaultRole) {
					addErr("server.webui.oauth2.default_role", "must be admin, user or viewer")
				}
			}
		}
		if s.Metrics.Enabled {
			if err := checkListenAddr(s.Metrics.Addr, true); err != nil {
				addErr("server.metrics.addr", err.Error())
			}
		}
		seenTokens := map[string]bool{}
		for i, tok := range s.Auth.Tokens {
			if strings.TrimSpace(tok) == "" {
				addErr(fmt.Sprintf("server.auth.tokens[%d]", i), "must not be empty")
				continue
			}
			if seenTokens[tok] {
				addErr(fmt.Sprintf("server.auth.tokens[%d]", i), "duplicate token")
			}
			seenTokens[tok] = true
			if len(tok) < 16 {
				addWarn(fmt.Sprintf("server.auth.tokens[%d]", i), "tokens shorter than 16 characters are easy to guess")
			}
		}
		if s.Quota.MaxTunnelsPerUser < 0 || s.Quota.MaxConnectionsPerTunnel < 0 || s.Quota.DailyTrafficBytes < 0 {
			addErr("server.quota", "limits must not be negative")
		}
		if strings.TrimSpace(s.DataDir) == "" {
			addErr("server.data_dir", "must not be empty")
		}
		if sec := strings.TrimSpace(s.StreamSecret); sec != "" && len(sec) < 16 {
			addWarn("server.stream_secret", "shorter than 16 characters is easy to brute-force; use a long random value")
		}
	}

	if checkClient {
		cl := &c.Client
		if err := checkServerAddr(cl.Server); err != nil {
			addErr("client.server", err.Error())
		}
		token := strings.TrimSpace(cl.Token)
		if token == "" {
			if requireToken {
				addErr("client.token", "required: read the token printed by `wormgate server` or create one in the admin console")
			} else {
				addWarn("client.token", "not set; client commands will require --token or WORMGATE_CLIENT_TOKEN")
			}
		} else if len(token) < 16 {
			addWarn("client.token", "token shorter than 16 characters is easy to guess")
		}
		if cl.TLS.InsecureSkipVerify {
			addWarn("client.tls.insecure_skip_verify", "certificate verification is disabled; use only for local development")
		}
		if (cl.TLS.CertFile == "") != (cl.TLS.KeyFile == "") {
			addErr("client.tls", "cert_file and key_file must be configured together")
		}
		if cl.HeartbeatInterval.D() <= 0 {
			addErr("client.heartbeat_interval", "must be greater than zero")
		}
		if cl.DialTimeout.D() <= 0 {
			addErr("client.dial_timeout", "must be greater than zero")
		}
		if cl.Reconnect.MinBackoff.D() <= 0 || cl.Reconnect.MaxBackoff.D() < cl.Reconnect.MinBackoff.D() {
			addErr("client.reconnect", "require 0 < min_backoff <= max_backoff")
		}
		if cl.ClientName != "" && !dnsLabelRe.MatchString(strings.ToLower(cl.ClientName)) {
			addErr("client.client_name", "must be a DNS label (letters, digits, dashes)")
		}
		nameSeen := map[string]bool{}
		for i := range cl.Tunnels {
			t := &cl.Tunnels[i]
			prefix := fmt.Sprintf("client.tunnels[%d]", i)
			if !tunnelNameRe.MatchString(t.Name) {
				addErr(prefix+".name", "must start with a letter/digit and contain only letters, digits, _ or - (max 40)")
			} else if nameSeen[t.Name] {
				addErr(prefix+".name", fmt.Sprintf("duplicate tunnel name %q", t.Name))
			}
			nameSeen[t.Name] = true
			switch t.Type {
			case "http":
				if t.RemotePort != 0 {
					addWarn(prefix+".remote_port", "ignored for http tunnels; use subdomain routing")
				}
				if t.Subdomain != "" && !dnsLabelRe.MatchString(t.Subdomain) {
					addErr(prefix+".subdomain", "must be a lowercase DNS label")
				}
			case "tcp":
				if t.Subdomain != "" {
					addWarn(prefix+".subdomain", "ignored for tcp tunnels")
				}
				if t.RemotePort != 0 && (t.RemotePort < 1 || t.RemotePort > 65535) {
					addErr(prefix+".remote_port", "must be between 1 and 65535")
				}
			default:
				addErr(prefix+".type", "must be http or tcp")
			}
			if err := checkLocalAddr(t.Local); err != nil {
				addErr(prefix+".local", err.Error())
			}
			// A tunnel cannot encrypt without a key. Fail loudly here rather
			// than letting registration succeed and every stream then die.
			if t.Encryption && strings.TrimSpace(cl.StreamSecret) == "" {
				addErr(prefix+".encryption", "requires client.stream_secret (must match the server value)")
			}
		}
		if s := strings.TrimSpace(cl.StreamSecret); s != "" && len(s) < 16 {
			addWarn("client.stream_secret", "shorter than 16 characters is easy to brute-force; use a long random value")
		}
		if cl.WebUI.Enabled {
			if err := checkListenAddr(cl.WebUI.Addr, true); err != nil {
				addErr("client.webui.addr", err.Error())
			}
			if cl.WebUI.PasswordHash != "" && !strings.HasPrefix(cl.WebUI.PasswordHash, "$argon2id$") {
				addErr("client.webui.password_hash", "must be an argon2id encoded hash")
			}
			if cl.WebUI.SessionTTL.D() <= 0 {
				addErr("client.webui.session_ttl", "must be greater than zero")
			}
			if cl.WebUI.Token != "" && len(cl.WebUI.Token) < 16 {
				addWarn("client.webui.token", "token shorter than 16 characters is easy to guess")
			}
			if !isLoopbackAddr(cl.WebUI.Addr) {
				addWarn("client.webui.addr", "the client console is reachable off-host; keep it on loopback unless you understand the risk")
			}
			if strings.TrimSpace(cl.WebUI.DataDir) == "" {
				addErr("client.webui.data_dir", "must not be empty")
			}
		}
	}
	return v
}

func validRole(r string) bool { return r == "admin" || r == "user" || r == "viewer" }

func checkListenAddr(addr string, allowZeroPort bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("not a valid host:port (%q)", addr)
	}
	if port == "" {
		return fmt.Errorf("port is required")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("port must be 0-65535, got %q", port)
	}
	if n == 0 && !allowZeroPort {
		return fmt.Errorf("port must not be 0")
	}
	if host != "" && net.ParseIP(host) == nil && !dnsLabelRe.MatchString(strings.ToLower(host)) {
		return fmt.Errorf("invalid host %q", host)
	}
	return nil
}

func checkServerAddr(addr string) error {
	if strings.Contains(addr, "://") {
		return fmt.Errorf("use host:port without a scheme (%q); TLS is always enabled", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return fmt.Errorf("not a valid host:port (%q)", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535, got %q", port)
	}
	return nil
}

func checkLocalAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("not a valid host:port (%q)", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535, got %q", port)
	}
	_ = host
	return nil
}

func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("must not be empty")
	}
	if domain != strings.ToLower(domain) {
		return fmt.Errorf("must be lowercase")
	}
	if ip := net.ParseIP(domain); ip != nil {
		return fmt.Errorf("must be a DNS name, not an IP address")
	}
	for _, label := range strings.Split(domain, ".") {
		if !dnsLabelRe.MatchString(label) {
			return fmt.Errorf("invalid DNS label %q", label)
		}
	}
	return nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ParsePortRange parses an inclusive "start-end" port range.
func ParsePortRange(raw string) (int, int, error) {
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("must look like 20000-30000, got %q", raw)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start port in %q", raw)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end port in %q", raw)
	}
	if start < 1 || end > 65535 || start > end {
		return 0, 0, fmt.Errorf("range %q must satisfy 1 <= start <= end <= 65535", raw)
	}
	return start, end, nil
}

// validateKnownKeys walks a YAML document against the Config schema and
// reports unknown fields with a spelling suggestion. It runs on the raw file
// so diagnostics can include line numbers.
func validateKnownKeys(n *yaml.Node, t reflect.Type, path, file string, out *[]Issue) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) > 0 {
			validateKnownKeys(n.Content[0], t, path, file, out)
		}
		return
	case yaml.AliasNode:
		return
	}
	if t.Kind() == reflect.Struct {
		if n.Kind != yaml.MappingNode {
			return
		}
		fields := yamlFields(t)
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if path == "" && key.Value == "profiles" {
				if val.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(val.Content); j += 2 {
						validateKnownKeys(val.Content[j+1], t, "profiles."+val.Content[j].Value, file, out)
					}
				}
				continue
			}
			ft, ok := fields[key.Value]
			if !ok {
				iss := Issue{
					File:    file,
					Path:    joinPath(path, key.Value),
					Line:    key.Line,
					Message: fmt.Sprintf("unknown field %q", key.Value),
				}
				if suggestion := nearest(key.Value, names); suggestion != "" {
					iss.Hint = "did you mean " + strconv.Quote(suggestion) + "?"
				}
				*out = append(*out, iss)
				continue
			}
			validateKnownKeys(val, ft, joinPath(path, key.Value), file, out)
		}
		return
	}
	if t.Kind() == reflect.Slice && n.Kind == yaml.SequenceNode {
		for i, item := range n.Content {
			validateKnownKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i), file, out)
		}
	}
}

func yamlFields(t reflect.Type) map[string]reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		tag := f.Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if name == "-" {
			continue
		}
		fields[name] = f.Type
	}
	return fields
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// nearest returns the closest candidate within a small edit distance, giving
// the "did you mean" experience expected from polished CLIs.
func nearest(input string, candidates []string) string {
	best, bestDist := "", 1<<30
	limit := len(input) / 3
	if limit < 2 {
		limit = 2
	}
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(input), strings.ToLower(c))
		if d < bestDist && d <= limit {
			best, bestDist = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
