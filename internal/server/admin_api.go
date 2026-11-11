package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/alerts"
	"github.com/wormgate/wormgate/internal/audit"
	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/version"
)

type userView struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name,omitempty"`
	Role        auth.Role  `json:"role"`
	Disabled    bool       `json:"disabled"`
	Quota       auth.Quota `json:"quota"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  time.Time  `json:"last_seen_at,omitempty"`
	HasToken    bool       `json:"has_token"`
	HasPassword bool       `json:"has_password"`
	OAuth       bool       `json:"oauth"`
	// TokenViewable reports whether GET /api/users/{id}/token can return the
	// plaintext token. Accounts created before sealed storage existed can only
	// be recovered by rotating.
	TokenViewable bool `json:"token_viewable"`
}

func toUserView(u auth.User) userView {
	return userView{
		ID:            u.ID,
		Username:      u.Username,
		DisplayName:   u.DisplayName,
		Role:          u.Role,
		Disabled:      u.Disabled,
		Quota:         u.Quota,
		CreatedAt:     u.CreatedAt,
		LastSeenAt:    u.LastSeenAt,
		HasToken:      u.TokenHash != "",
		HasPassword:   u.PasswordHash != "",
		OAuth:         u.ExternalID != "",
		TokenViewable: u.TokenSealed != "",
	}
}

type tunnelAdminView struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	OwnerID    string    `json:"owner_id"`
	Client     string    `json:"client"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	PublicAddr string    `json:"public_addr"`
	Subdomain  string    `json:"subdomain,omitempty"`
	RemotePort int       `json:"remote_port,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// Encryption mirrors Registration.Encrypted so the console can show which
	// tunnels carry the extra AEAD layer.
	Encryption  bool   `json:"encryption"`
	Active      int64  `json:"active_connections"`
	Connections uint64 `json:"connections"`
	BytesIn     uint64 `json:"bytes_in"`
	BytesOut    uint64 `json:"bytes_out"`
}

type clientAdminView struct {
	SessionID   string    `json:"session_id"`
	ClientName  string    `json:"client_name"`
	Owner       string    `json:"owner"`
	OwnerID     string    `json:"owner_id"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen    time.Time `json:"last_seen"`
	DataReady   bool      `json:"data_ready"`
	Tunnels     int       `json:"tunnels"`
}

type nodeView struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	UptimeSec  int64  `json:"uptime_sec"`
	Goroutines int    `json:"goroutines"`
	HeapAlloc  uint64 `json:"heap_alloc"`
	HeapSys    uint64 `json:"heap_sys"`
	NumGC      uint32 `json:"num_gc"`
	Clients    int    `json:"clients"`
	Tunnels    int    `json:"tunnels"`

	// Host resources. HostKnown is false on platforms without a sampling
	// implementation, so the console can hide the rows instead of showing 0%.
	HostKnown  bool    `json:"host_known"`
	CPUPercent float64 `json:"cpu_percent"`
	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
}

type overviewView struct {
	Timestamp         int64             `json:"timestamp"`
	UptimeSec         int64             `json:"uptime_sec"`
	Clients           int               `json:"clients"`
	Tunnels           int               `json:"tunnels"`
	ActiveConnections int64             `json:"active_connections"`
	ConnectionsTotal  uint64            `json:"connections_total"`
	TotalIn           uint64            `json:"total_in"`
	TotalOut          uint64            `json:"total_out"`
	RateIn            uint64            `json:"rate_in"`
	RateOut           uint64            `json:"rate_out"`
	HTTPRequests      uint64            `json:"http_requests"`
	TopTunnels        []tunnelAdminView `json:"top_tunnels"`
	Alerts            []alerts.Alert    `json:"alerts"`
	Node              nodeView          `json:"node"`
}

type adminSnapshotView struct {
	Overview overviewView `json:"overview"`
	Recent   []RateSample `json:"recent"`
}

func (s *Server) adminSnapshot(p *adminPrincipal) adminSnapshotView {
	overview := s.overviewFor(p)
	recent := s.recentFor(p)
	if len(recent) > 120 {
		recent = recent[len(recent)-120:]
	}
	return adminSnapshotView{Overview: overview, Recent: recent}
}

func (s *Server) visibleRegistrations(p *adminPrincipal) []*Registration {
	regs := s.registry.List()
	if p == nil || p.User.Role == auth.RoleAdmin || p.User.Role == auth.RoleViewer {
		return regs
	}
	out := make([]*Registration, 0, len(regs))
	for _, reg := range regs {
		if reg.OwnerID == p.User.ID {
			out = append(out, reg)
		}
	}
	return out
}

func (s *Server) visibleSessions(p *adminPrincipal) []*ClientSession {
	sessions := s.sessionSnapshot()
	if p == nil || p.User.Role == auth.RoleAdmin || p.User.Role == auth.RoleViewer {
		return sessions
	}
	out := make([]*ClientSession, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Principal.UserID == p.User.ID {
			out = append(out, sess)
		}
	}
	return out
}

func (s *Server) overviewFor(p *adminPrincipal) overviewView {
	regs := s.visibleRegistrations(p)
	sessions := s.visibleSessions(p)
	global, series := s.stats.Snapshot()

	allowed := map[string]bool{}
	for _, reg := range regs {
		allowed[reg.ID] = true
	}
	rateIn, rateOut := latestRates(global, 10)
	totalIn, totalOut := s.totalIn.Load(), s.totalOut.Load()
	if p != nil && p.User.Role == auth.RoleUser {
		var in, out uint64
		for _, reg := range regs {
			in += reg.Counters.BytesIn.Load()
			out += reg.Counters.BytesOut.Load()
		}
		totalIn, totalOut = in, out
		rateIn, rateOut = sumRecentRates(series, allowed, 10)
	}

	var active int64
	var connections uint64
	top := make([]tunnelAdminView, 0, len(regs))
	for _, reg := range regs {
		act := reg.Counters.Active.Load()
		conns := reg.Counters.Connections.Load()
		active += act
		connections += conns
		top = append(top, tunnelToAdminView(reg))
	}
	sort.Slice(top, func(i, j int) bool {
		return top[i].BytesIn+top[i].BytesOut > top[j].BytesIn+top[j].BytesOut
	})
	if len(top) > 8 {
		top = top[:8]
	}

	var alertList []alerts.Alert
	if p == nil || p.User.Role != auth.RoleUser {
		alertList = s.alerts.List(20)
	}
	if alertList == nil {
		alertList = []alerts.Alert{}
	}
	if top == nil {
		top = []tunnelAdminView{}
	}

	node := s.nodeInfo()
	node.Clients = len(sessions)
	node.Tunnels = len(regs)

	return overviewView{
		Timestamp:         time.Now().UnixMilli(),
		UptimeSec:         int64(time.Since(s.startedAt).Seconds()),
		Clients:           len(sessions),
		Tunnels:           len(regs),
		ActiveConnections: active,
		ConnectionsTotal:  connections,
		TotalIn:           totalIn,
		TotalOut:          totalOut,
		RateIn:            rateIn,
		RateOut:           rateOut,
		HTTPRequests:      s.httpRequests.Load(),
		TopTunnels:        top,
		Alerts:            alertList,
		Node:              node,
	}
}

func (s *Server) recentFor(p *adminPrincipal) []RateSample {
	global, series := s.stats.Snapshot()
	if p == nil || p.User.Role != auth.RoleUser {
		return global
	}
	allowed := map[string]bool{}
	for _, reg := range s.visibleRegistrations(p) {
		allowed[reg.ID] = true
	}
	byTime := map[int64]*RateSample{}
	for _, ts := range series {
		if !allowed[ts.ID] {
			continue
		}
		for _, sample := range ts.Samples {
			point := byTime[sample.Timestamp]
			if point == nil {
				point = &RateSample{Timestamp: sample.Timestamp}
				byTime[sample.Timestamp] = point
			}
			point.BytesIn += sample.BytesIn
			point.BytesOut += sample.BytesOut
		}
	}
	out := make([]RateSample, 0, len(byTime))
	for _, point := range byTime {
		out = append(out, *point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

func sumRecentRates(series []TunnelSeries, allowed map[string]bool, n int) (uint64, uint64) {
	byTime := map[int64][2]uint64{}
	for _, ts := range series {
		if !allowed[ts.ID] || len(ts.Samples) == 0 {
			continue
		}
		start := len(ts.Samples) - n
		if start < 0 {
			start = 0
		}
		for _, sample := range ts.Samples[start:] {
			acc := byTime[sample.Timestamp]
			acc[0] += sample.BytesIn
			acc[1] += sample.BytesOut
			byTime[sample.Timestamp] = acc
		}
	}
	if len(byTime) == 0 {
		return 0, 0
	}
	var in, out uint64
	for _, acc := range byTime {
		in += acc[0]
		out += acc[1]
	}
	divisor := uint64(len(byTime))
	return in / divisor, out / divisor
}

func (s *Server) nodeInfo() nodeView {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	host, _ := os.Hostname()
	host0 := s.sys.Snapshot()
	return nodeView{
		Name:       host,
		Kind:       "control+data",
		Version:    version.Version,
		UptimeSec:  int64(time.Since(s.startedAt).Seconds()),
		Goroutines: runtime.NumGoroutine(),
		HeapAlloc:  mem.HeapAlloc,
		HeapSys:    mem.HeapSys,
		NumGC:      mem.NumGC,
		HostKnown:  host0.Available,
		CPUPercent: host0.CPUPercent,
		MemTotal:   host0.MemTotal,
		MemUsed:    host0.MemUsed,
	}
}

func tunnelToAdminView(reg *Registration) tunnelAdminView {
	return tunnelAdminView{
		ID:          reg.ID,
		Owner:       reg.OwnerName,
		OwnerID:     reg.OwnerID,
		Client:      reg.ClientName,
		Name:        reg.Name,
		Type:        reg.Type,
		PublicAddr:  reg.PublicAddr,
		Subdomain:   reg.Subdomain,
		RemotePort:  reg.RemotePort,
		CreatedAt:   reg.CreatedAt,
		Encryption:  reg.Encrypted,
		Active:      reg.Counters.Active.Load(),
		Connections: reg.Counters.Connections.Load(),
		BytesIn:     reg.Counters.BytesIn.Load(),
		BytesOut:    reg.Counters.BytesOut.Load(),
	}
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	writeJSON(w, http.StatusOK, s.overviewFor(p))
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	global, series := s.stats.Snapshot()
	if p != nil && p.User.Role == auth.RoleUser {
		allowed := map[string]bool{}
		for _, reg := range s.visibleRegistrations(p) {
			allowed[reg.ID] = true
		}
		filtered := make([]TunnelSeries, 0, len(series))
		for _, ts := range series {
			if allowed[ts.ID] {
				filtered = append(filtered, ts)
			}
		}
		series = filtered
	}
	if series == nil {
		series = []TunnelSeries{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timestamp": time.Now().UnixMilli(),
		"global":    global,
		"tunnels":   series,
	})
}

func (s *Server) handleAdminTunnels(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	regs := s.visibleRegistrations(p)
	out := make([]tunnelAdminView, 0, len(regs))
	for _, reg := range regs {
		out = append(out, tunnelToAdminView(reg))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out, "timestamp": time.Now().UnixMilli()})
}

// ---------------------------------------------------------------------------
// Stream encryption (server.stream_secret)
//
// The secret keys the optional AES-256-GCM layer that sits inside each tunnel
// stream. It must match the value each fpc uses, so this endpoint exists to
// hand the operator a value they can copy into a client config. The secret is
// returned in full: it is already readable from the config file by the same
// administrator, and hiding it here would only force a shell session.
// ---------------------------------------------------------------------------

type encryptionView struct {
	Enabled          bool   `json:"enabled"`
	Secret           string `json:"secret"`
	Fingerprint      string `json:"fingerprint"`
	ConfigPath       string `json:"config_path"`
	TunnelsEncrypted int    `json:"tunnels_encrypted"`
	TunnelsPlain     int    `json:"tunnels_plain"`
}

func (s *Server) encryptionState() encryptionView {
	secret := strings.TrimSpace(s.cfg.Current().Server.StreamSecret)
	out := encryptionView{
		Enabled:    secret != "",
		Secret:     secret,
		ConfigPath: s.cfg.WritablePathOrEmpty(),
	}
	if secret != "" {
		sum := sha256.Sum256([]byte(secret))
		out.Fingerprint = hex.EncodeToString(sum[:])[:16]
	}
	for _, reg := range s.registry.List() {
		if reg.Encrypted {
			out.TunnelsEncrypted++
		} else {
			out.TunnelsPlain++
		}
	}
	return out
}

func (s *Server) handleGetEncryption(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil || p.User.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "administrator role required")
		return
	}
	writeJSON(w, http.StatusOK, s.encryptionState())
}

func (s *Server) handlePutEncryption(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil || p.User.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "administrator role required")
		return
	}
	var body struct {
		Generate bool   `json:"generate"`
		Secret   string `json:"secret"`
		Disable  bool   `json:"disable"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	secret := strings.TrimSpace(body.Secret)
	action := "encryption.set"
	switch {
	case body.Disable:
		secret = ""
		action = "encryption.disable"
	case body.Generate:
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			writeError(w, http.StatusInternalServerError, "cannot generate secret: "+err.Error())
			return
		}
		secret = base64.RawURLEncoding.EncodeToString(buf)
		action = "encryption.rotate"
	case secret == "":
		writeError(w, http.StatusBadRequest, "secret is required (send disable to turn encryption off)")
		return
	}
	if secret != "" && len(secret) < 16 {
		writeError(w, http.StatusBadRequest, "secret must be at least 16 characters")
		return
	}

	// Persist first: SetServerScalar validates the resulting document and rolls
	// back if the reload is rejected, so a failed write leaves the old secret
	// both on disk and in memory.
	if err := s.cfg.SetServerScalar("stream_secret", secret); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	detail := "stream encryption disabled"
	if secret != "" {
		detail = "stream encryption enabled"
	}
	if _, err := s.audit.Add(p.User.Username, p.User.ID, action, "server.stream_secret", detail, requestIP(r), "warning"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	s.log.Info("stream encryption updated",
		zap.Bool("enabled", secret != ""),
		zap.String("by", p.User.Username))
	writeJSON(w, http.StatusOK, s.encryptionState())
}

func (s *Server) handleRevokeTunnel(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if p.User.Role == auth.RoleViewer {
		writeError(w, http.StatusForbidden, "read-only role")
		return
	}
	reg := s.registry.ByID(r.PathValue("id"))
	if reg == nil {
		writeError(w, http.StatusNotFound, "tunnel not found")
		return
	}
	if p.User.Role != auth.RoleAdmin && reg.OwnerID != p.User.ID {
		writeError(w, http.StatusForbidden, "tunnel belongs to another user")
		return
	}
	detail := "revoked from admin console by " + p.User.Username
	if !s.revokeRegistration(reg, p.User.Username, p.User.ID, "tunnel.revoke", detail) {
		writeError(w, http.StatusConflict, "tunnel already closed")
		return
	}
	s.log.Info("tunnel revoked by admin",
		zap.String("tunnel", reg.Name),
		zap.String("owner", reg.OwnerName),
		zap.String("actor", p.User.Username))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminClients(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	sessions := s.visibleSessions(p)
	out := make([]clientAdminView, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, clientAdminView{
			SessionID:   sess.ID,
			ClientName:  sess.ClientName,
			Owner:       sess.Principal.Username,
			OwnerID:     sess.Principal.UserID,
			RemoteAddr:  sess.RemoteAddr,
			ConnectedAt: sess.CreatedAt,
			LastSeen:    sess.LastSeen(),
			DataReady:   sess.DataReady(),
			Tunnels:     s.registry.CountByClient(sess.ID),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.After(out[j].ConnectedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"clients": out, "timestamp": time.Now().UnixMilli()})
}

func (s *Server) handleKickClient(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if p.User.Role == auth.RoleViewer {
		writeError(w, http.StatusForbidden, "read-only role")
		return
	}
	sess := s.sessionByID(r.PathValue("id"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	if p.User.Role != auth.RoleAdmin && sess.Principal.UserID != p.User.ID {
		writeError(w, http.StatusForbidden, "client belongs to another user")
		return
	}
	sess.Kick("removed by administrator " + p.User.Username)
	if _, err := s.audit.Add(p.User.Username, p.User.ID, "client.kick", sess.ClientName, sess.ID, requestIP(r), "warning"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	node := s.nodeInfo()
	node.Clients = len(s.visibleSessions(p))
	node.Tunnels = len(s.visibleRegistrations(p))
	writeJSON(w, http.StatusOK, map[string]any{"nodes": []nodeView{node}})
}

func (s *Server) handleAlerts(w http.ResponseWriter, _ *http.Request) {
	items := s.alerts.List(50)
	if items == nil {
		items = []alerts.Alert{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !s.alerts.Ack(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "alert.ack", r.PathValue("id"), "", requestIP(r), "info"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	events := s.audit.List(limit)
	if events == nil {
		events = []audit.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	users := s.users.ListUsers()
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, toUserView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

type createUserRequest struct {
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Password    string     `json:"password"`
	Role        auth.Role  `json:"role"`
	Quota       auth.Quota `json:"quota"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body createUserRequest
	if err := decodeAdminJSON(w, r, &body); err != nil {
		return
	}
	if body.Role == "" {
		body.Role = auth.RoleUser
	}
	user, token, err := s.users.CreateUser(body.Username, body.DisplayName, body.Role, body.Quota)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Password != "" {
		if err := s.users.SetPassword(user.ID, body.Password); err != nil {
			_ = s.users.DeleteUser(user.ID)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if fresh, ok := s.users.GetUser(user.ID); ok {
			user = fresh
		}
	}
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.create", user.Username, string(user.Role), requestIP(r), "info"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":  toUserView(*user),
		"token": token,
		"note":  "the client token is shown only once",
	})
}

type updateUserRequest struct {
	DisplayName *string     `json:"display_name"`
	Role        *auth.Role  `json:"role"`
	Disabled    *bool       `json:"disabled"`
	Quota       *auth.Quota `json:"quota"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	current, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var body updateUserRequest
	if err := decodeAdminJSON(w, r, &body); err != nil {
		return
	}
	display := current.DisplayName
	if body.DisplayName != nil {
		display = *body.DisplayName
	}
	role := current.Role
	if body.Role != nil {
		role = *body.Role
	}
	disabled := current.Disabled
	if body.Disabled != nil {
		disabled = *body.Disabled
	}
	quota := current.Quota
	if body.Quota != nil {
		quota = *body.Quota
	}
	if p != nil && p.User.ID == id && disabled {
		writeError(w, http.StatusBadRequest, "you cannot disable your own account")
		return
	}
	if current.Role == auth.RoleAdmin && (role != auth.RoleAdmin || disabled) {
		if s.countAdmins() <= 1 {
			writeError(w, http.StatusBadRequest, "cannot remove the last active administrator")
			return
		}
	}
	updated, err := s.users.UpdateUser(id, display, role, quota, disabled)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if disabled {
		s.kickOwner(id, "account disabled by administrator")
	}
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.update", updated.Username, string(updated.Role), requestIP(r), "info"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserView(*updated)})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	user, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if p != nil && p.User.ID == id {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	if user.Role == auth.RoleAdmin && s.countAdmins() <= 1 {
		writeError(w, http.StatusBadRequest, "cannot delete the last administrator")
		return
	}
	if err := s.users.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.kickOwner(id, "account deleted by administrator")
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.delete", user.Username, "", requestIP(r), "warning"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	user, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	token, err := s.users.RotateToken(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.rotate_token", user.Username, "", requestIP(r), "warning"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "note": "shown only once"})
}

// handleRevealToken returns the stored client token so an administrator can
// recover one a user has lost. Rotation stays the cheaper option for accounts
// that predate sealed storage.
func (s *Server) handleRevealToken(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	user, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	token, err := s.users.RevealToken(id)
	if err != nil {
		if errors.Is(err, auth.ErrTokenNotRecoverable) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p != nil {
		// Reading a credential is auditable: record who looked at whose token.
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.reveal_token", user.Username, "", requestIP(r), "warning"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

// handleChangeOwnPassword is the self-service form on the Security page. It is
// deliberately separate from the admin reset endpoint: here the caller must
// prove knowledge of the current password, and stale sessions are revoked.
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if p == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if p.User.PasswordHash == "" {
		writeError(w, http.StatusBadRequest,
			"this account has no local password; ask an administrator to set one")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeAdminJSON(w, r, &body); err != nil {
		return
	}
	if len(body.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if body.NewPassword == body.CurrentPassword {
		writeError(w, http.StatusBadRequest, "new password must differ from the current password")
		return
	}
	if _, err := s.users.AuthenticatePassword(p.User.Username, body.CurrentPassword); err != nil {
		// 400: the JWT is still valid; a 401 would log the operator out of
		// the console they are actively using.
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	if err := s.users.SetPassword(p.User.ID, body.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.change_own_password", p.User.Username, "", requestIP(r), "warning"); err != nil {
		s.log.Warn("audit write failed", zap.Error(err))
	}
	user := p.User
	if fresh, ok := s.users.GetUser(p.User.ID); ok {
		user = fresh
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"csrf": s.refreshAdminSession(w, user),
	})
}

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	user, ok := s.users.GetUser(id)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeAdminJSON(w, r, &body); err != nil {
		return
	}
	if err := s.users.SetPassword(id, body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p != nil {
		if _, err := s.audit.Add(p.User.Username, p.User.ID, "user.set_password", user.Username, "", requestIP(r), "warning"); err != nil {
			s.log.Warn("audit write failed", zap.Error(err))
		}
		// Resetting your own password from the Users page also invalidates the
		// current JWT; re-issue it so the operator is not kicked out mid-task.
		if p.User.ID == id {
			if fresh, ok := s.users.GetUser(id); ok {
				s.refreshAdminSession(w, fresh)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) countAdmins() int {
	count := 0
	for _, u := range s.users.ListUsers() {
		if u.Role == auth.RoleAdmin && !u.Disabled {
			count++
		}
	}
	return count
}

func (s *Server) kickOwner(ownerID, reason string) {
	for _, sess := range s.sessionSnapshot() {
		if sess.Principal.UserID == ownerID {
			sess.Kick(reason)
		}
	}
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return err
	}
	return nil
}
