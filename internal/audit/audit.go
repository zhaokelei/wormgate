// Package audit keeps a bounded, append-only audit trail.
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wormgate/wormgate/internal/idgen"
)

// Event is one auditable action.
type Event struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Actor    string    `json:"actor"`
	ActorID  string    `json:"actor_id,omitempty"`
	Action   string    `json:"action"`
	Target   string    `json:"target,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	IP       string    `json:"ip,omitempty"`
	Severity string    `json:"severity"`
}

// Log persists events as JSON lines and keeps the newest in memory.
type Log struct {
	mu     sync.Mutex
	path   string
	events []Event
	max    int
}

// Open loads an existing audit log. Malformed trailing lines are skipped so a
// crashed append cannot prevent startup.
func Open(path string, max int) (*Log, error) {
	if max <= 0 {
		max = 2000
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	l := &Log{path: path, max: max}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		l.events = append(l.events, e)
		if len(l.events) > max {
			l.events = l.events[len(l.events)-max:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	return l, nil
}

// Add appends an event. Persistence failures do not block the caller but are
// returned so the server can surface them in its own log.
func (l *Log) Add(actor, actorID, action, target, detail, ip, severity string) (Event, error) {
	if severity == "" {
		severity = "info"
	}
	e := Event{
		ID:       idgen.Hex(8),
		Time:     time.Now().UTC(),
		Actor:    actor,
		ActorID:  actorID,
		Action:   action,
		Target:   target,
		Detail:   detail,
		IP:       ip,
		Severity: severity,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	if len(l.events) > l.max {
		l.events = l.events[len(l.events)-l.max:]
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return e, fmt.Errorf("encode audit event: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return e, fmt.Errorf("open audit log for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return e, fmt.Errorf("append audit log: %w", err)
	}
	return e, nil
}

// List returns up to limit newest events.
func (l *Log) List(limit int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > len(l.events) {
		limit = len(l.events)
	}
	out := make([]Event, limit)
	copy(out, l.events[len(l.events)-limit:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
