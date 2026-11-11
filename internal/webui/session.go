// Package webui holds shared helpers for the embedded consoles.
package webui

import (
	"context"
	"sync"
	"time"

	"github.com/wormgate/wormgate/internal/idgen"
)

// Session is a local console login session. The CSRF token is bound to the
// session and echoed by the browser on state-changing requests.
type Session struct {
	ID      string
	CSRF    string
	Expires time.Time
}

// SessionStore is an in-memory, expiring session store.
type SessionStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	sessions map[string]Session
}

// NewSessionStore creates a store with the given session lifetime.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionStore{ttl: ttl, sessions: map[string]Session{}}
}

// Create issues a new session.
func (s *SessionStore) Create() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := Session{
		ID:      idgen.Session(),
		CSRF:    idgen.Hex(24),
		Expires: time.Now().Add(s.ttl),
	}
	s.sessions[sess.ID] = sess
	return sess
}

// Get returns a session if it exists and has not expired.
func (s *SessionStore) Get(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(sess.Expires) {
		delete(s.sessions, id)
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// DeleteExcept removes all sessions but the one that initiated a credential
// change, so other browsers must sign in again with the new password.
func (s *SessionStore) DeleteExcept(id string) {
	s.mu.Lock()
	for key := range s.sessions {
		if key != id {
			delete(s.sessions, key)
		}
	}
	s.mu.Unlock()
}

// StartGC removes expired sessions periodically until ctx ends.
func (s *SessionStore) StartGC(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			for id, sess := range s.sessions {
				if now.After(sess.Expires) {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
