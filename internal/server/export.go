package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/version"
)

// clientProfile is the portable connection profile the admin exports and the
// client console imports. It intentionally carries only what a client needs to
// dial in: address, token, identity, and the TLS trust hint. Tunnel definitions
// stay with the client, because the server never knows the local targets.
type clientProfile struct {
	Server     string
	Token      string
	ClientName string
	Insecure   bool
	ExportedAt time.Time
}

// handleExportClientConfig renders a ready-to-import client profile for one
// account. The token is per-user, so this is admin-only and audited: being able
// to read a token is exactly what an audit trail is for.
func (s *Server) handleExportClientConfig(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	id := r.PathValue("id")
	user, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	token, err := s.users.RevealToken(id)
	if err != nil {
		if errors.Is(err, auth.ErrTokenNotRecoverable) {
			// Signed before the reveal feature existed: only a rotation can
			// produce a token the server can hand out.
			writeError(w, http.StatusConflict, "token predates token export support; rotate it once and try again")
			return
		}
		s.log.Warn("reveal token for export failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "cannot read token")
		return
	}

	cfg := s.cfg.Current()
	profile := clientProfile{
		Server:     exportServerAddr(r, cfg.Server.ControlAddr),
		Token:      token,
		ClientName: user.Username,
		// TLS is always on; a self-signed certificate is the default, so the
		// client must skip verification until the operator installs a trusted
		// one. The console states this in the exported comment.
		Insecure:   cfg.Server.TLS.CertFile == "",
		ExportedAt: time.Now(),
	}

	if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.export_config", user.Username, profile.Server, requestIP(r), "warning"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"server":      profile.Server,
		"token":       profile.Token,
		"client_name": profile.ClientName,
		"filename":    fmt.Sprintf("wormgate-client-%s.yaml", user.Username),
		"yaml":        profile.YAML(),
	})
}

// YAML renders the profile in the same shape a client config file uses, so the
// imported settings land in the same fields the CLI would have read.
func (p clientProfile) YAML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Wormgate 客户端连接配置\n")
	fmt.Fprintf(&b, "# 导出时间 %s · 账号 %s · 版本 %s\n", p.ExportedAt.Format("2006-01-02 15:04:05"), p.ClientName, version.Version)
	fmt.Fprintf(&b, "# 用法：客户端控制台 → 配置 → 一键导入，粘贴本文件内容或直接选择本文件。\n")
	fmt.Fprintf(&b, "# 本文件含客户端令牌，等同于密码，请通过可信渠道传递。\n")
	fmt.Fprintf(&b, "client:\n")
	fmt.Fprintf(&b, "  server: %s\n", yamlScalar(p.Server))
	fmt.Fprintf(&b, "  token: %s\n", yamlScalar(p.Token))
	fmt.Fprintf(&b, "  client_name: %s\n", yamlScalar(p.ClientName))
	fmt.Fprintf(&b, "  tls:\n")
	fmt.Fprintf(&b, "    insecure_skip_verify: %t\n", p.Insecure)
	if p.Insecure {
		fmt.Fprintf(&b, "# 服务端用的是自签名证书，上面一行必须保留；换成受信任证书后可删除。\n")
	}
	return b.String()
}

// yamlScalar quotes a value when leaving it bare could be misread.
func yamlScalar(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, ":#{}[]&*!|>'\"%@`,\n\t ") {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

// exportServerAddr builds the address a client should dial. The port comes from
// the real control listener; the host comes from however the operator reached
// the console, falling back to the first non-loopback interface when they are
// browsing over an SSH tunnel.
func exportServerAddr(r *http.Request, controlAddr string) string {
	_, port, err := net.SplitHostPort(controlAddr)
	if err != nil || port == "" {
		port = "7001"
	}
	host := strings.Trim(r.Host, "[]")
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || isLoopbackHost(host) {
		if iface := firstNonLoopbackIPv4(); iface != "" {
			host = iface
		}
	}
	return net.JoinHostPort(host, port)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// firstNonLoopbackIPv4 returns the machine's primary IPv4 address, which is the
// one an operator would put in a client config.
func firstNonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
