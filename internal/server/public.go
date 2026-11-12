package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/proto"
)

type proxyContextKey struct{}

type proxyMeta struct {
	reg    *Registration
	host   string
	scheme string
	source string
	trace  string
}

func (s *Server) publicHandler() http.Handler {
	return http.HandlerFunc(s.servePublic)
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	trace := idgen.Trace()
	host := stripHostPort(r.Host)
	cfg := s.cfg.Current()
	domain := strings.ToLower(cfg.Server.HTTP.Domain)
	s.httpRequests.Add(1)

	reg := s.matchHost(host, domain)
	if reg == nil {
		s.metrics.httpResponses.WithLabelValues("404").Inc()
		s.log.Debug("no tunnel for host",
			zap.String("trace", trace),
			zap.String("host", host),
			zap.String("remote", r.RemoteAddr))
		http.NotFound(w, r)
		return
	}
	if reg.IsClosed() {
		s.metrics.httpResponses.WithLabelValues("404").Inc()
		http.NotFound(w, r)
		return
	}

	meta := &proxyMeta{
		reg:    reg,
		host:   r.Host,
		scheme: cfg.Server.HTTP.Scheme,
		source: r.RemoteAddr,
		trace:  trace,
	}
	s.log.Debug("http request",
		zap.String("trace", trace),
		zap.String("method", r.Method),
		zap.String("host", host),
		zap.String("path", r.URL.Path),
		zap.String("tunnel", reg.Name),
		zap.String("remote", r.RemoteAddr))
	ctx := context.WithValue(r.Context(), proxyContextKey{}, meta)
	s.proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) matchHost(host, domain string) *Registration {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || domain == "" {
		return nil
	}
	suffix := "." + domain
	if !strings.HasSuffix(host, suffix) {
		return nil
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") || !subdomainRE.MatchString(sub) {
		return nil
	}
	return s.registry.BySubdomain(sub)
}

func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// parseTrustedProxies expands the configured CIDRs, accepting a bare address
// as a single-host network. A bad entry fails startup instead of silently
// trusting nothing: a typo would otherwise surface later as every visitor
// sharing the load balancer's address.
func parseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(entries))
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			ip := net.ParseIP(raw)
			if ip == nil {
				return nil, fmt.Errorf("server.trusted_proxies: %q is not an address or CIDR", raw)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			raw = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("server.trusted_proxies: %w", err)
		}
		out = append(out, network)
	}
	return out, nil
}

func (s *Server) isTrustedProxy(ip net.IP) bool {
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP decides which address a tunnel backend should see as its caller.
//
// The connection address is the answer unless the peer is a configured trusted
// proxy — behind a load balancer every request would otherwise arrive as the
// balancer itself. When the peer is trusted, the rightmost untrusted entry in
// X-Forwarded-For wins: anything further left was appended by the client and
// proves nothing.
func (s *Server) clientIP(source string, h http.Header) string {
	direct := stripHostPort(source)
	ip := net.ParseIP(direct)
	if ip == nil || !s.isTrustedProxy(ip) {
		return direct
	}
	var chain []string
	for _, line := range h.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			if p := strings.TrimSpace(part); p != "" {
				chain = append(chain, p)
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		cand := net.ParseIP(stripHostPort(chain[i]))
		if cand == nil || s.isTrustedProxy(cand) {
			continue
		}
		return cand.String()
	}
	return direct
}

// directProxy rewrites the outbound request. The fake URL host is unique per
// registration, which keeps net/http's connection pool isolated per tunnel.
func (s *Server) directProxy(req *http.Request) {
	meta, _ := req.Context().Value(proxyContextKey{}).(*proxyMeta)
	if meta == nil {
		return
	}
	req.URL.Scheme = "http"
	req.URL.Host = "tunnel-" + meta.reg.ID + ".invalid"
	req.Host = meta.host
	// SECURITY: never trust client-supplied forwarding headers on the public
	// ingress. They are replaced with a value Wormgate can stand behind: the
	// address it observed itself, or — when the peer is a configured trusted
	// proxy — the client address that proxy reported.
	client := s.clientIP(meta.source, req.Header)
	req.Header.Del("X-Forwarded-For")
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-Proto")
	req.Header.Del("X-Real-IP")
	req.Header.Del("X-Wormgate-Tunnel")
	req.Header.Del("X-Wormgate-Trace")
	req.Header.Del("X-Wormgate-Proxy")
	req.Header.Set("X-Forwarded-For", client)
	req.Header.Set("X-Forwarded-Host", meta.host)
	req.Header.Set("X-Forwarded-Proto", meta.scheme)
	req.Header.Set("X-Real-IP", client)
	req.Header.Set("X-Wormgate-Tunnel", meta.reg.Name)
	req.Header.Set("X-Wormgate-Trace", meta.trace)
	if peer := stripHostPort(meta.source); peer != client {
		// Keep the proxy hop visible so a backend can tell a direct caller from
		// one that arrived through a load balancer.
		req.Header.Set("X-Wormgate-Proxy", peer)
	}
}

// dialTunnel is the http.Transport dialer. Each physical HTTP connection to a
// backend becomes one yamux stream, opened by the server for low latency.
func (s *Server) dialTunnel(ctx context.Context, _, _ string) (net.Conn, error) {
	meta, _ := ctx.Value(proxyContextKey{}).(*proxyMeta)
	if meta == nil {
		return nil, fmt.Errorf("missing tunnel proxy context")
	}
	if meta.reg.IsClosed() {
		return nil, fmt.Errorf("tunnel %q is closed", meta.reg.Name)
	}
	if err := s.acquireSlot(meta.reg); err != nil {
		return nil, err
	}
	connID := idgen.Conn()
	stream, err := meta.reg.Session.OpenStream(ctx, meta.reg.Name, connID, meta.source, "http", meta.reg.StreamKey)
	if err != nil {
		s.releaseSlot(meta.reg)
		return nil, fmt.Errorf("open tunnel stream: %w", err)
	}
	metered := newMeteredConn(s, meta.reg, stream)
	if err := meta.reg.Track(metered); err != nil {
		_ = metered.Close()
		return nil, err
	}
	meta.reg.Session.notifyNewConnection(proto.NewConnection{
		Tunnel:     meta.reg.Name,
		ConnID:     connID,
		RemoteAddr: meta.source,
		Kind:       "http",
		Timestamp:  time.Now().UnixMilli(),
	})
	return metered, nil
}

func (s *Server) proxyResponse(res *http.Response) error {
	meta, _ := res.Request.Context().Value(proxyContextKey{}).(*proxyMeta)
	if meta == nil {
		return nil
	}
	res.Header.Set("X-Wormgate-Tunnel", meta.reg.Name)
	res.Header.Set("X-Wormgate-Trace", meta.trace)
	s.metrics.httpResponses.WithLabelValues(strconv.Itoa(res.StatusCode)).Inc()
	s.log.Debug("http response",
		zap.String("trace", meta.trace),
		zap.String("tunnel", meta.reg.Name),
		zap.Int("status", res.StatusCode))
	return nil
}

func (s *Server) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, errConnQuota) || errors.Is(err, errDailyQuota) {
		status = http.StatusTooManyRequests
	}
	meta, _ := r.Context().Value(proxyContextKey{}).(*proxyMeta)
	if meta != nil {
		w.Header().Set("X-Wormgate-Trace", meta.trace)
		s.metrics.httpResponses.WithLabelValues(strconv.Itoa(status)).Inc()
		s.log.Warn("tunnel proxy error",
			zap.String("trace", meta.trace),
			zap.String("tunnel", meta.reg.Name),
			zap.Int("status", status),
			zap.Error(err))
	}
	if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
		return
	}
	http.Error(w, http.StatusText(status), status)
}
