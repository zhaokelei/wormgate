package webui

import (
	"sync"
	"time"
)

// LoginLimiter is a small per-key sliding window used to slow down password
// guessing against local consoles.
type LoginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts map[string][]time.Time
}

// NewLoginLimiter allows max attempts per window for each key.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	if max <= 0 {
		max = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &LoginLimiter{max: max, window: window, attempts: map[string][]time.Time{}}
}

// Allow records an attempt and reports whether it is within the limit.
func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	list := l.attempts[key]
	kept := list[:0]
	for _, at := range list {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.max {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, time.Now())
	return true
}

// Reset clears the history for a key (e.g. after a successful login).
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}
