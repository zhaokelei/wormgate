package client

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wormgate/wormgate/internal/config"
)

// Tunnel is the client-side runtime state of one configured tunnel.
type Tunnel struct {
	mu         sync.RWMutex
	cfg        config.TunnelConfig
	registered bool
	publicAddr string
	subdomain  string
	remotePort int
	lastError  string

	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	connections atomic.Uint64
	active      atomic.Int64
}

func newTunnel(cfg config.TunnelConfig) *Tunnel { return &Tunnel{cfg: cfg} }

func (t *Tunnel) config() config.TunnelConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cfg
}

func (t *Tunnel) setConfig(cfg config.TunnelConfig) {
	t.mu.Lock()
	t.cfg = cfg
	t.mu.Unlock()
}

func (t *Tunnel) setAssignment(publicAddr, subdomain string, remotePort int) {
	t.mu.Lock()
	t.registered = true
	t.publicAddr = publicAddr
	t.subdomain = subdomain
	t.remotePort = remotePort
	t.lastError = ""
	t.mu.Unlock()
}

func (t *Tunnel) clearAssignment() {
	t.mu.Lock()
	t.registered = false
	t.publicAddr = ""
	t.subdomain = ""
	t.remotePort = 0
	t.mu.Unlock()
}

func (t *Tunnel) setError(msg string) {
	t.mu.Lock()
	t.registered = false
	t.lastError = msg
	t.mu.Unlock()
}

// TunnelView is the JSON representation used by the console API.
type TunnelView struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Local      string `json:"local"`
	Subdomain  string `json:"subdomain,omitempty"`
	RemotePort int    `json:"remote_port,omitempty"`
	Enabled    bool   `json:"enabled"`
	// Encryption mirrors client.tunnels[].encryption so the console can show a
	// lock badge without re-reading the YAML.
	Encryption  bool   `json:"encryption"`
	Registered  bool   `json:"registered"`
	PublicAddr  string `json:"public_addr,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	BytesIn     uint64 `json:"bytes_in"`
	BytesOut    uint64 `json:"bytes_out"`
	Connections uint64 `json:"connections"`
	Active      int64  `json:"active_connections"`
}

func (t *Tunnel) view() TunnelView {
	t.mu.RLock()
	cfg := t.cfg
	registered := t.registered
	publicAddr := t.publicAddr
	subdomain := t.subdomain
	remotePort := t.remotePort
	lastError := t.lastError
	t.mu.RUnlock()
	return TunnelView{
		Name:        cfg.Name,
		Type:        cfg.Type,
		Local:       cfg.Local,
		Subdomain:   subdomain,
		RemotePort:  remotePort,
		Enabled:     cfg.IsEnabled(),
		Encryption:  cfg.Encryption,
		Registered:  registered,
		PublicAddr:  publicAddr,
		LastError:   lastError,
		BytesIn:     t.bytesIn.Load(),
		BytesOut:    t.bytesOut.Load(),
		Connections: t.connections.Load(),
		Active:      t.active.Load(),
	}
}

// RateSample is one traffic bucket at one-second resolution.
type RateSample struct {
	Timestamp int64  `json:"timestamp"`
	BytesIn   uint64 `json:"bytes_in"`
	BytesOut  uint64 `json:"bytes_out"`
}

type counterPair struct{ in, out uint64 }

// StatsStore samples tunnel counters every second into bounded rings.
type StatsStore struct {
	mu     sync.Mutex
	global []RateSample
	per    map[string][]RateSample
	prev   map[string]counterPair
}

func newStatsStore() *StatsStore {
	return &StatsStore{per: map[string][]RateSample{}, prev: map[string]counterPair{}}
}

// Run samples until ctx ends.
func (s *StatsStore) Run(ctx context.Context, list func() []*Tunnel) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.sample(now, list())
		}
	}
}

func (s *StatsStore) sample(now time.Time, tunnels []*Tunnel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[string]bool, len(tunnels))
	var totalIn, totalOut uint64
	for _, t := range tunnels {
		name := t.config().Name
		live[name] = true
		in, out := t.bytesIn.Load(), t.bytesOut.Load()
		prev := s.prev[name]
		dIn, dOut := in-prev.in, out-prev.out
		if in < prev.in {
			dIn = in
		}
		if out < prev.out {
			dOut = out
		}
		s.prev[name] = counterPair{in: in, out: out}
		totalIn += dIn
		totalOut += dOut
		ring := s.per[name]
		appendSample(&ring, RateSample{Timestamp: now.UnixMilli(), BytesIn: dIn, BytesOut: dOut})
		s.per[name] = ring
	}
	for name := range s.per {
		if !live[name] {
			delete(s.per, name)
			delete(s.prev, name)
		}
	}
	appendSample(&s.global, RateSample{Timestamp: now.UnixMilli(), BytesIn: totalIn, BytesOut: totalOut})
}

func appendSample(ring *[]RateSample, sample RateSample) {
	const window = 300
	if len(*ring) == window {
		copy(*ring, (*ring)[1:])
		(*ring)[len(*ring)-1] = sample
		return
	}
	*ring = append(*ring, sample)
}

// Snapshot returns copies of the global and per-tunnel series.
func (s *StatsStore) Snapshot() ([]RateSample, map[string][]RateSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	global := append([]RateSample(nil), s.global...)
	per := make(map[string][]RateSample, len(s.per))
	for name, ring := range s.per {
		per[name] = append([]RateSample(nil), ring...)
	}
	return global, per
}

// ConnInfo is one live tunneled connection.
type ConnInfo struct {
	ID        string
	Tunnel    string
	Source    string
	Local     string
	StartedAt time.Time

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
}

// ConnView is the JSON representation of a connection.
type ConnView struct {
	ID        string    `json:"id"`
	Tunnel    string    `json:"tunnel"`
	Source    string    `json:"source"`
	Local     string    `json:"local"`
	StartedAt time.Time `json:"started_at"`
	BytesIn   uint64    `json:"bytes_in"`
	BytesOut  uint64    `json:"bytes_out"`
}

// ConnRegistry tracks active connections for the console.
type ConnRegistry struct {
	mu    sync.RWMutex
	conns map[string]*ConnInfo
}

func newConnRegistry() *ConnRegistry {
	return &ConnRegistry{conns: map[string]*ConnInfo{}}
}

func (r *ConnRegistry) add(info *ConnInfo) {
	r.mu.Lock()
	r.conns[info.ID] = info
	r.mu.Unlock()
}

func (r *ConnRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.conns, id)
	r.mu.Unlock()
}

func (r *ConnRegistry) snapshot() []ConnView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ConnView, 0, len(r.conns))
	for _, c := range r.conns {
		out = append(out, ConnView{
			ID:        c.ID,
			Tunnel:    c.Tunnel,
			Source:    c.Source,
			Local:     c.Local,
			StartedAt: c.StartedAt,
			BytesIn:   c.bytesIn.Load(),
			BytesOut:  c.bytesOut.Load(),
		})
	}
	return out
}

// clientMeteredConn accounts bytes on the yamux stream (server <-> client).
type clientMeteredConn struct {
	net.Conn
	agent  *Agent
	tunnel *Tunnel
	conn   *ConnInfo
	once   sync.Once
}

func (c *clientMeteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.tunnel.bytesIn.Add(uint64(n))
		c.agent.totalIn.Add(uint64(n))
		c.conn.bytesIn.Add(uint64(n))
	}
	return n, err
}

func (c *clientMeteredConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.tunnel.bytesOut.Add(uint64(n))
		c.agent.totalOut.Add(uint64(n))
		c.conn.bytesOut.Add(uint64(n))
	}
	return n, err
}

func (c *clientMeteredConn) Close() error {
	c.once.Do(func() {
		c.agent.conns.remove(c.conn.ID)
		c.tunnel.active.Add(-1)
	})
	return c.Conn.Close()
}

func (c *clientMeteredConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
