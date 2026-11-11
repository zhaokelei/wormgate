package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/idgen"
	"github.com/wormgate/wormgate/internal/netpipe"
	"github.com/wormgate/wormgate/internal/proto"
	"github.com/wormgate/wormgate/internal/streamcipher"
)

var tunnelNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)
var subdomainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// registerTunnel validates a client request, assigns public resources and
// records the tunnel. It is idempotent for an already-registered name.
func (s *Server) registerTunnel(sess *ClientSession, spec proto.TunnelSpec) proto.TunnelResult {
	result := proto.TunnelResult{Name: spec.Name, Type: spec.Type, OK: false}
	fail := func(msg string) proto.TunnelResult {
		s.metrics.registrationsFail.Inc()
		result.Error = msg
		s.log.Warn("tunnel registration rejected",
			zap.String("client", sess.ClientName),
			zap.String("tunnel", spec.Name),
			zap.String("reason", msg))
		return result
	}
	if !tunnelNameRE.MatchString(spec.Name) {
		return fail("tunnel name must start with a letter/digit and contain only letters, digits, _ or - (max 40)")
	}
	spec.Type = strings.ToLower(strings.TrimSpace(spec.Type))
	if spec.Type != "http" && spec.Type != "tcp" {
		return fail("tunnel type must be http or tcp")
	}
	// Stream encryption is opt-in and must never silently degrade: a client
	// that asked for it gets an error when this server cannot honour it.
	spec.Encryption = strings.ToLower(strings.TrimSpace(spec.Encryption))
	var streamKey []byte
	switch spec.Encryption {
	case "":
	case streamcipher.Scheme:
		if s.cfg.Current().Server.StreamSecret == "" {
			return fail("stream encryption requested but this server has no stream_secret configured")
		}
		key, err := streamcipher.DeriveKey(s.cfg.Current().Server.StreamSecret, sess.ID, spec.Name)
		if err != nil {
			return fail(err.Error())
		}
		streamKey = key
	default:
		return fail(fmt.Sprintf("unsupported encryption scheme %q", spec.Encryption))
	}
	sess.tunnelsMu.Lock()
	if existing, ok := sess.tunnels[spec.Name]; ok {
		sess.tunnelsMu.Unlock()
		result.OK = true
		result.Type = existing.Type
		result.PublicAddr = existing.PublicAddr
		result.Subdomain = existing.Subdomain
		result.RemotePort = existing.RemotePort
		return result
	}
	sess.tunnelsMu.Unlock()

	quota := s.users.EffectiveQuota(sess.Principal.UserID)
	if quota.MaxTunnelsPerUser > 0 && s.registry.CountByOwner(sess.Principal.UserID) >= quota.MaxTunnelsPerUser {
		return fail(fmt.Sprintf("tunnel quota reached (%d per user)", quota.MaxTunnelsPerUser))
	}

	cfg := s.cfg.Current()
	reg := newRegistration()
	reg.ID = idgen.Hex(8)
	reg.OwnerID = sess.Principal.UserID
	reg.OwnerName = sess.Principal.Username
	reg.ClientName = sess.ClientName
	reg.Name = spec.Name
	reg.Type = spec.Type
	reg.Session = sess
	reg.CreatedAt = time.Now().UTC()
	reg.StreamKey = streamKey
	reg.Encrypted = len(streamKey) > 0

	s.regMu.Lock()
	defer s.regMu.Unlock()

	switch spec.Type {
	case "http":
		sub, err := s.assignSubdomainLocked(spec, sess, cfg)
		if err != nil {
			return fail(err.Error())
		}
		reg.Subdomain = sub
		reg.PublicAddr = s.publicHTTPAddr(cfg, sub)
	case "tcp":
		if spec.RemotePort != 0 && (spec.RemotePort < 1 || spec.RemotePort > 65535) {
			return fail("remote_port must be between 1 and 65535")
		}
		if err := s.listenTCPLocked(cfg, reg, spec.RemotePort); err != nil {
			return fail(err.Error())
		}
		reg.PublicAddr = s.publicTCPAddr(cfg, reg.RemotePort)
	}

	if err := s.registry.Add(reg); err != nil {
		reg.Close()
		return fail(err.Error())
	}
	sess.addTunnel(reg)
	s.metrics.tunnels.Inc()
	result.OK = true
	result.Type = reg.Type
	result.PublicAddr = reg.PublicAddr
	result.Subdomain = reg.Subdomain
	result.RemotePort = reg.RemotePort

	if reg.Type == "tcp" {
		s.wg.Add(1)
		go s.serveTCPRegistration(reg)
	}
	s.log.Info("tunnel registered",
		zap.String("client", sess.ClientName),
		zap.String("owner", reg.OwnerName),
		zap.String("tunnel", reg.Name),
		zap.String("type", reg.Type),
		zap.String("public", reg.PublicAddr))
	if _, err := s.audit.Add(reg.OwnerName, reg.OwnerID, "tunnel.register", reg.Name, reg.PublicAddr, sess.RemoteAddr, "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	return result
}

func (s *Server) assignSubdomainLocked(spec proto.TunnelSpec, sess *ClientSession, cfg *config.Config) (string, error) {
	requested := strings.ToLower(strings.TrimSpace(spec.Subdomain))
	if requested != "" {
		if !subdomainRE.MatchString(requested) {
			return "", fmt.Errorf("subdomain must be a lowercase DNS label")
		}
		if s.registry.SubdomainTaken(requested) {
			return "", fmt.Errorf("subdomain %q is already in use", requested)
		}
		return requested, nil
	}
	base := sanitizeLabel(spec.Name)
	if base == "" {
		base = sanitizeLabel(sess.ClientName)
	}
	if base == "" {
		base = "t"
	}
	candidate := base
	for i := 0; i < 12; i++ {
		if !s.registry.SubdomainTaken(candidate) {
			return candidate, nil
		}
		candidate = base + "-" + idgen.Hex(2)
	}
	return "", fmt.Errorf("could not allocate a subdomain; try an explicit one")
}

func (s *Server) publicHTTPAddr(cfg *config.Config, sub string) string {
	port := cfg.Server.HTTP.PublicPort
	if port == 0 {
		if _, p, err := net.SplitHostPort(s.HTTPAddr()); err == nil {
			port, _ = strconv.Atoi(p)
		}
	}
	host := sub + "." + cfg.Server.HTTP.Domain
	defaultPort := 80
	if cfg.Server.HTTP.Scheme == "https" {
		defaultPort = 443
	}
	if port != 0 && port != defaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return cfg.Server.HTTP.Scheme + "://" + host
}

func (s *Server) listenTCPLocked(cfg *config.Config, reg *Registration, requested int) error {
	bindHost := cfg.Server.TCP.BindAddr
	if bindHost == "" {
		bindHost = "0.0.0.0"
	}
	listen := func(port int) error {
		ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(port)))
		if err != nil {
			return err
		}
		reg.Listener = ln
		reg.RemotePort = port
		return nil
	}
	if requested != 0 {
		if s.registry.PortTaken(requested) {
			return fmt.Errorf("remote port %d is already in use", requested)
		}
		if err := listen(requested); err != nil {
			return fmt.Errorf("bind remote port %d: %v", requested, err)
		}
		return nil
	}
	start, end, err := config.ParsePortRange(cfg.Server.TCP.PortRange)
	if err != nil {
		return err
	}
	attempts := 0
	for port := start; port <= end && attempts < 4096; port++ {
		if s.registry.PortTaken(port) {
			continue
		}
		attempts++
		if err := listen(port); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no free remote port in %s", cfg.Server.TCP.PortRange)
}

func (s *Server) publicTCPAddr(cfg *config.Config, port int) string {
	host := cfg.Server.TCP.AdvertiseHost
	if host == "" {
		controlHost, _, err := net.SplitHostPort(s.ControlAddr())
		if err == nil && controlHost != "" && controlHost != "0.0.0.0" && controlHost != "::" {
			host = controlHost
		} else {
			host = "localhost"
		}
	}
	return "tcp://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func (s *Server) unregisterTunnel(sess *ClientSession, name string) {
	reg := sess.removeTunnel(name)
	if reg == nil {
		return
	}
	if !s.revokeRegistration(reg, sess.Principal.Username, sess.Principal.UserID, "tunnel.unregister", "") {
		return
	}
	s.log.Info("tunnel unregistered",
		zap.String("client", sess.ClientName),
		zap.String("owner", sess.Principal.Username),
		zap.String("tunnel", name))
}

// revokeRegistration removes a tunnel from routing, closes its listener and
// live connections, and writes an audit event. It is idempotent.
func (s *Server) revokeRegistration(reg *Registration, actor, actorID, action, detail string) bool {
	if !reg.markRevoked() {
		return false
	}
	if reg.Session != nil {
		reg.Session.removeTunnel(reg.Name)
	}
	s.registry.Remove(reg)
	reg.Close()
	s.metrics.tunnels.Dec()
	if _, err := s.audit.Add(actor, actorID, action, reg.Name, detail, "", "info"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	return true
}

func (s *Server) serveTCPRegistration(reg *Registration) {
	defer s.wg.Done()
	for {
		conn, err := reg.Listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("tcp accept failed",
				zap.String("tunnel", reg.Name),
				zap.Error(err))
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		go s.handleTCPConn(reg, conn)
	}
}

func (s *Server) handleTCPConn(reg *Registration, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if reg.IsClosed() {
		return
	}
	if err := s.acquireSlot(reg); err != nil {
		s.log.Debug("tcp connection rejected by quota",
			zap.String("tunnel", reg.Name),
			zap.Error(err))
		return
	}
	source := conn.RemoteAddr().String()
	connID := idgen.Conn()
	opCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	stream, err := reg.Session.OpenStream(opCtx, reg.Name, connID, source, "tcp", reg.StreamKey)
	cancel()
	if err != nil {
		s.releaseSlot(reg)
		s.log.Debug("open tunnel stream failed", zap.String("tunnel", reg.Name), zap.Error(err))
		return
	}
	metered := newMeteredConn(s, reg, stream)
	if err := reg.Track(metered); err != nil {
		_ = metered.Close()
		return
	}
	defer func() { _ = metered.Close() }()
	if err := reg.Track(conn); err != nil {
		return
	}
	defer reg.Untrack(conn)

	reg.Session.notifyNewConnection(proto.NewConnection{
		Tunnel:     reg.Name,
		ConnID:     connID,
		RemoteAddr: source,
		Kind:       "tcp",
		Timestamp:  time.Now().UnixMilli(),
	})
	s.log.Debug("tcp tunnel connection",
		zap.String("tunnel", reg.Name),
		zap.String("source", source))
	netpipe.Pipe(conn, metered)
}
