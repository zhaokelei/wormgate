// Package alerts keeps a small in-memory stream of operational alerts.
package alerts

import (
	"sync"
	"time"

	"github.com/wormgate/wormgate/internal/idgen"
)

// Alert is an actionable operational event.
type Alert struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Ack     bool      `json:"ack"`
}

// Store is a bounded, concurrency-safe alert ring.
type Store struct {
	mu    sync.Mutex
	items []Alert
	max   int
}

// New creates a store holding up to max alerts.
func New(max int) *Store {
	if max <= 0 {
		max = 200
	}
	return &Store{max: max}
}

// Add records an alert.
func (s *Store) Add(level, code, message string) Alert {
	if level == "" {
		level = "warning"
	}
	a := Alert{ID: idgen.Hex(8), Time: time.Now().UTC(), Level: level, Code: code, Message: message}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, a)
	if len(s.items) > s.max {
		s.items = s.items[len(s.items)-s.max:]
	}
	return a
}

// List returns newest alerts first. limit <= 0 returns all.
func (s *Store) List(limit int) []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.items) {
		limit = len(s.items)
	}
	out := make([]Alert, 0, limit)
	for i := len(s.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.items[i])
	}
	return out
}

// Ack marks an alert as acknowledged.
func (s *Store) Ack(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Ack = true
			return true
		}
	}
	return false
}
