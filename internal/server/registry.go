package server

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counters tracks per-registration traffic. Counters are cumulative and are
// sampled by the stats collector once per second.
type Counters struct {
	BytesIn     atomic.Uint64
	BytesOut    atomic.Uint64
	Connections atomic.Uint64
	Active      atomic.Int64
}

// Registration is a server-side tunnel assignment. It owns either a
// subdomain (HTTP) or a listening port (TCP).
type Registration struct {
	ID         string
	OwnerID    string
	OwnerName  string
	ClientName string
	Name       string
	Type       string
	PublicAddr string
	Subdomain  string
	RemotePort int
	Session    *ClientSession
	CreatedAt  time.Time
	Listener   net.Listener
	Counters   Counters
	// StreamKey is the AES-256 key derived for this tunnel when the client
	// enabled stream encryption. Nil means the tunnel stream is plaintext (the
	// TLS hop still applies, if configured).
	StreamKey []byte
	// Encrypted reports whether the AEAD layer is active. Derived from
	// StreamKey so the two can never disagree.
	Encrypted bool

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	closed  bool
	revoked atomic.Bool
}

func newRegistration() *Registration {
	return &Registration{conns: map[net.Conn]struct{}{}}
}

// Track registers a live connection so it can be closed when the tunnel is
// revoked. It fails if the tunnel has already been closed.
func (r *Registration) Track(conn net.Conn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("tunnel %q is no longer active", r.Name)
	}
	r.conns[conn] = struct{}{}
	return nil
}

// Untrack removes a connection from the close set.
func (r *Registration) Untrack(conn net.Conn) {
	r.mu.Lock()
	delete(r.conns, conn)
	r.mu.Unlock()
}

// Close revokes the tunnel, stops its listener and closes live connections.
func (r *Registration) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	conns := make([]net.Conn, 0, len(r.conns))
	for c := range r.conns {
		conns = append(conns, c)
	}
	r.conns = map[net.Conn]struct{}{}
	r.mu.Unlock()

	if r.Listener != nil {
		_ = r.Listener.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

// IsClosed reports whether the registration was revoked.
func (r *Registration) IsClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// markRevoked returns true only for the first revocation, preventing double
// metric decrements when admin actions race with session teardown.
func (r *Registration) markRevoked() bool { return r.revoked.CompareAndSwap(false, true) }

// Registry indexes active registrations by subdomain, port and ID.
type Registry struct {
	mu     sync.RWMutex
	bySub  map[string]*Registration
	byPort map[int]*Registration
	byID   map[string]*Registration
}

func newRegistry() *Registry {
	return &Registry{
		bySub:  map[string]*Registration{},
		byPort: map[int]*Registration{},
		byID:   map[string]*Registration{},
	}
}

// SubdomainTaken reports whether a subdomain is already routed.
func (r *Registry) SubdomainTaken(sub string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.bySub[sub]
	return ok
}

// PortTaken reports whether a port is already assigned.
func (r *Registry) PortTaken(port int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byPort[port]
	return ok
}

// Add indexes a registration. It rejects conflicts, so assignment must be
// serialized by the server's registration mutex.
func (r *Registry) Add(reg *Registration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[reg.ID]; ok {
		return fmt.Errorf("registration ID collision")
	}
	if reg.Type == "http" {
		if reg.Subdomain == "" {
			return fmt.Errorf("http registration has no subdomain")
		}
		if _, ok := r.bySub[reg.Subdomain]; ok {
			return fmt.Errorf("subdomain %q is already in use", reg.Subdomain)
		}
		r.bySub[reg.Subdomain] = reg
	} else {
		if reg.RemotePort == 0 {
			return fmt.Errorf("tcp registration has no port")
		}
		if _, ok := r.byPort[reg.RemotePort]; ok {
			return fmt.Errorf("port %d is already in use", reg.RemotePort)
		}
		r.byPort[reg.RemotePort] = reg
	}
	r.byID[reg.ID] = reg
	return nil
}

// Remove deletes a registration from all indexes.
func (r *Registry) Remove(reg *Registration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, reg.ID)
	if reg.Type == "http" {
		delete(r.bySub, reg.Subdomain)
	} else {
		delete(r.byPort, reg.RemotePort)
	}
}

// BySubdomain resolves an HTTP route.
func (r *Registry) BySubdomain(sub string) *Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bySub[sub]
}

// ByPort resolves a TCP route.
func (r *Registry) ByPort(port int) *Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byPort[port]
}

// ByID looks up a registration by ID.
func (r *Registry) ByID(id string) *Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// List returns a snapshot sorted by creation time.
func (r *Registry) List() []*Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Registration, 0, len(r.byID))
	for _, reg := range r.byID {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// CountByOwner counts active tunnels owned by a principal.
func (r *Registry) CountByOwner(ownerID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, reg := range r.byID {
		if reg.OwnerID == ownerID {
			n++
		}
	}
	return n
}

// CountByPort counts registrations owned by one client session.
func (r *Registry) CountByClient(sessionID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, reg := range r.byID {
		if reg.Session != nil && reg.Session.ID == sessionID {
			n++
		}
	}
	return n
}
