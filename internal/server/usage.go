package server

import (
	"sync"
	"time"
)

// Usage tracks per-owner traffic for daily quota enforcement. Counters reset
// at UTC midnight.
type Usage struct {
	mu     sync.Mutex
	day    string
	owners map[string]int64
}

func newUsage() *Usage {
	return &Usage{day: todayKey(), owners: map[string]int64{}}
}

// Add records bytes for an owner and returns the owner's total today.
func (u *Usage) Add(owner string, n int64) int64 {
	if n <= 0 {
		return u.Today(owner)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked()
	u.owners[owner] += n
	return u.owners[owner]
}

// Today returns the owner's traffic since UTC midnight.
func (u *Usage) Today(owner string) int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked()
	return u.owners[owner]
}

// Snapshot returns a copy of today's usage by owner.
func (u *Usage) Snapshot() map[string]int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked()
	out := make(map[string]int64, len(u.owners))
	for k, v := range u.owners {
		out[k] = v
	}
	return out
}

func (u *Usage) rollLocked() {
	key := todayKey()
	if key != u.day {
		u.day = key
		u.owners = map[string]int64{}
	}
}

func todayKey() string { return time.Now().UTC().Format("2006-01-02") }
