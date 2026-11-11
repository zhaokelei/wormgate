// Package logx builds zap loggers that also feed an in-memory ring buffer for
// the console's live log stream.
package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/wormgate/wormgate/internal/config"
)

// Entry is one structured log record exposed to the WebUI.
type Entry struct {
	Seq     uint64         `json:"seq"`
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// Buffer is a bounded in-memory log ring with fan-out for SSE subscribers.
// Slow subscribers drop records instead of blocking the logger.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	next    uint64
	subs    map[int]chan Entry
	subID   int
}

// NewBuffer creates a ring holding at most capacity records (minimum 1).
func NewBuffer(capacity int) *Buffer {
	if capacity < 1 {
		capacity = 1
	}
	return &Buffer{entries: make([]Entry, 0, capacity), subs: map[int]chan Entry{}}
}

// Add appends a record and fans it out.
func (b *Buffer) Add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	e.Seq = b.next
	if len(b.entries) == cap(b.entries) {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = e
	} else {
		b.entries = append(b.entries, e)
	}
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Snapshot returns up to limit newest records in chronological order.
func (b *Buffer) Snapshot(limit int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || limit > len(b.entries) {
		limit = len(b.entries)
	}
	out := make([]Entry, limit)
	copy(out, b.entries[len(b.entries)-limit:])
	return out
}

// Subscribe returns a stream of new records and a cancel function.
func (b *Buffer) Subscribe(chanSize int) (<-chan Entry, func()) {
	if chanSize <= 0 {
		chanSize = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.subID
	b.subID++
	ch := make(chan Entry, chanSize)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
	}
}

// Logger bundles a zap logger with its mutable level and ring buffer.
type Logger struct {
	*zap.Logger
	Level  zap.AtomicLevel
	Buffer *Buffer
}

// New builds a logger from configuration.
func New(cfg config.LogConfig) (*Logger, error) {
	level, err := zapcore.ParseLevel(strings.ToLower(cfg.Level))
	if err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", cfg.Level, err)
	}
	atom := zap.NewAtomicLevelAt(level)
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encCfg.EncodeDuration = zapcore.StringDurationEncoder

	var encoder zapcore.Encoder
	switch cfg.Format {
	case "json":
		encoder = zapcore.NewJSONEncoder(encCfg)
	case "console", "":
		encoder = zapcore.NewConsoleEncoder(encCfg)
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Format)
	}

	buf := NewBuffer(2000)
	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), atom)
	buffered := &ringCore{Core: core, buf: buf}
	opts := []zap.Option{zap.AddStacktrace(zapcore.ErrorLevel)}
	if cfg.AddCaller {
		opts = append(opts, zap.AddCaller())
	}
	return &Logger{Logger: zap.New(buffered, opts...), Level: atom, Buffer: buf}, nil
}

// SetLevel applies a new level at runtime. Invalid levels are rejected.
func (l *Logger) SetLevel(level string) error {
	parsed, err := zapcore.ParseLevel(strings.ToLower(level))
	if err != nil {
		return fmt.Errorf("parse log level %q: %w", level, err)
	}
	l.Level.SetLevel(parsed)
	return nil
}

// ringCore mirrors records into the ring buffer before delegating.
type ringCore struct {
	zapcore.Core
	buf    *Buffer
	fields []zapcore.Field
}

func (c *ringCore) With(fields []zapcore.Field) zapcore.Core {
	cloned := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	cloned = append(cloned, c.fields...)
	cloned = append(cloned, fields...)
	return &ringCore{Core: c.Core.With(fields), buf: c.buf, fields: cloned}
}

func (c *ringCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *ringCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	c.buf.Add(Entry{
		Time:    ent.Time,
		Level:   ent.Level.String(),
		Message: ent.Message,
		Fields:  enc.Fields,
	})
	return c.Core.Write(ent, fields)
}
