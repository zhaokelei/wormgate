package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// ApplyFunc receives the previous and the candidate configuration. It must be
// fast and must not call Manager mutating methods (the manager holds its lock
// while notifying). Returning an error rolls the configuration back.
type ApplyFunc func(old, next *Config) error

// Manager owns the live configuration. The *Config returned by Current is
// immutable: it is replaced, never mutated, so readers may retain it.
type Manager struct {
	mu      sync.Mutex
	opts    Options
	cfg     *Config
	res     *LoadResult
	subs    []ApplyFunc
	onError func(error)
}

// NewManager loads the configuration once and returns a mutable manager.
func NewManager(opts Options) (*Manager, error) {
	res, err := Load(opts)
	if err != nil {
		return nil, err
	}
	return &Manager{opts: opts, cfg: res.Config, res: res}, nil
}

// Current returns the active configuration. Callers must not mutate it.
func (m *Manager) Current() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// ProjectPath returns the loaded project file, or "" when none was found.
func (m *Manager) ProjectPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.res.ProjectPath
}

// WritablePath is where edits are persisted: the project file when present,
// otherwise the global file.
func (m *Manager) WritablePath() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writablePathLocked()
}

// WritablePathOrEmpty is a convenience for display APIs.
func (m *Manager) WritablePathOrEmpty() string {
	p, err := m.WritablePath()
	if err != nil {
		return ""
	}
	return p
}

func (m *Manager) writablePathLocked() (string, error) {
	if m.res.ProjectPath != "" {
		return m.res.ProjectPath, nil
	}
	if !m.opts.NoGlobal && m.res.GlobalPath != "" {
		return m.res.GlobalPath, nil
	}
	// Zero-config launch (no project file and --no-global, or a brand new
	// machine): create a project file in the working directory instead of
	// refusing the edit. Without this the configuration page cannot save the
	// first server token, which is the entire point of a console-driven setup.
	return filepath.Join(".", "wormgate.yaml"), nil
}

// Files returns the configuration files currently in effect.
func (m *Manager) Files() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.res.Files...)
}

// Warnings returns non-fatal configuration diagnostics.
func (m *Manager) Warnings() []Issue {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Issue(nil), m.res.Warnings...)
}

// Subscribe registers a configuration-change callback.
func (m *Manager) Subscribe(fn ApplyFunc) {
	if fn == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, fn)
}

// SetErrorHandler registers a callback for background reload failures.
func (m *Manager) SetErrorHandler(fn func(error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onError = fn
}

// YAML renders the effective configuration. The result contains secrets.
func (m *Manager) YAML() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.res.EffectiveYAML()
}

// UpdateTunnels persists the client tunnel list into the project/global file,
// validates the merged result, then applies it. Other fields are preserved.
func (m *Manager) UpdateTunnels(tunnels []TunnelConfig) error {
	path, err := m.WritablePath()
	if err != nil {
		return err
	}
	m.mu.Lock()
	raw, readErr := os.ReadFile(path)
	m.mu.Unlock()
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read %s: %w", path, readErr)
	}
	root := map[string]any{}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	clientMap, _ := root["client"].(map[string]any)
	if clientMap == nil {
		clientMap = map[string]any{}
	}
	encodedTunnels, err := yaml.Marshal(map[string]any{"tunnels": tunnels})
	if err != nil {
		return fmt.Errorf("encode tunnels: %w", err)
	}
	var tunnelTree map[string]any
	if err := yaml.Unmarshal(encodedTunnels, &tunnelTree); err != nil {
		return fmt.Errorf("encode tunnels: %w", err)
	}
	clientMap["tunnels"] = tunnelTree["tunnels"]
	root["client"] = clientMap
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	return m.saveBytes(path, out)
}

// UpdateClientSettings merges a structured patch into the client section of
// the writable configuration file. The server section, profiles, log settings
// and the tunnel list are left untouched. Validation and transactional
// rollback come from saveBytes, so an invalid value changes nothing on disk.
func (m *Manager) UpdateClientSettings(patch map[string]any) error {
	path, err := m.WritablePath()
	if err != nil {
		return err
	}
	m.mu.Lock()
	raw, readErr := os.ReadFile(path)
	m.mu.Unlock()
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read %s: %w", path, readErr)
	}
	root := map[string]any{}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	clientMap, _ := root["client"].(map[string]any)
	if clientMap == nil {
		clientMap = map[string]any{}
	}
	root["client"] = deepMerge(clientMap, patch)
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	return m.saveBytes(path, out)
}

// deepMerge overlays src onto dst, recursing into nested maps so that editing
// `tls.ca_file` does not wipe out the other TLS keys.
func deepMerge(dst, src map[string]any) map[string]any {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				dst[key] = deepMerge(existing, child)
				continue
			}
		}
		dst[key] = value
	}
	return dst
}

// SetServerScalar sets (or, with an empty value, removes) one scalar key
// directly under the `server:` section of the writable file.
//
// Unlike UpdateClientSettings this is a surgical, line-based edit: the server
// configuration in production is hand-written and carries operator comments
// (why ports were chosen, why the console is loopback-only). Round-tripping it
// through map[string]any would silently delete all of that, so the rest of the
// document is left byte-for-byte intact.
func (m *Manager) SetServerScalar(key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("key must not be empty")
	}
	path, err := m.WritablePath()
	if err != nil {
		return err
	}
	m.mu.Lock()
	raw, readErr := os.ReadFile(path)
	m.mu.Unlock()
	if readErr != nil {
		return fmt.Errorf("read %s: %w", path, readErr)
	}
	doc, err := setNestedScalar(string(raw), "server", key, value)
	if err != nil {
		return err
	}
	return m.saveBytes(path, []byte(doc))
}

// setNestedScalar edits one `section: { key: value }` pair in a YAML document
// without re-encoding it. Comments, ordering and blank lines survive.
func setNestedScalar(document, section, key, value string) (string, error) {
	lines := strings.Split(document, "\n")
	// A trailing newline produces a final empty element; remember it so we can
	// restore the exact file shape at the end.
	hadTrailing := len(lines) > 0 && lines[len(lines)-1] == ""

	clean := func(s string) string { return strings.TrimSuffix(s, "\r") }
	headerAt := -1
	for i, line := range lines {
		c := clean(line)
		if c == section+":" || strings.HasPrefix(c, section+": ") && strings.TrimSpace(c[len(section)+1:]) == "" {
			headerAt = i
			break
		}
	}
	if headerAt < 0 {
		if value == "" {
			return document, nil
		}
		return "", fmt.Errorf("configuration has no %q section", section)
	}

	// The section body ends at the next column-0 token (another key, a comment
	// or a document marker).
	endAt := len(lines)
	for i := headerAt + 1; i < len(lines); i++ {
		c := clean(lines[i])
		if c == "" || strings.HasPrefix(c, " ") || strings.HasPrefix(c, "\t") || strings.HasPrefix(c, "#") {
			continue
		}
		endAt = i
		break
	}

	indent := "  "
	keyAt := -1
	for i := headerAt + 1; i < endAt; i++ {
		c := clean(lines[i])
		trimmed := strings.TrimLeft(c, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, key+":") {
			rest := strings.TrimSpace(trimmed[len(key)+1:])
			// Only a scalar leaf counts; `key:` followed by an indented block
			// means the caller is editing the wrong thing.
			if rest == "" || !strings.HasPrefix(rest, "#") {
				keyAt = i
				indent = c[:len(c)-len(trimmed)]
				break
			}
		}
	}

	encoded := encodeScalar(value)
	switch {
	case keyAt >= 0 && value == "":
		lines = append(lines[:keyAt], lines[keyAt+1:]...)
	case keyAt >= 0:
		lines[keyAt] = indent + key + ": " + encoded
	case value != "":
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:headerAt+1]...)
		out = append(out, "  "+key+": "+encoded)
		out = append(out, lines[headerAt+1:]...)
		lines = out
	default:
		return document, nil
	}

	if hadTrailing && (len(lines) == 0 || lines[len(lines)-1] != "") {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), nil
}

// encodeScalar quotes only when the value would otherwise be ambiguous YAML.
func encodeScalar(v string) string {
	safe := v != ""
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == '+', r == '=', r == '~':
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return v
	}
	return strconv.Quote(v)
}

// SaveYAML validates an edited configuration document and persists it. An
// invalid document changes nothing (transactional save).
func (m *Manager) SaveYAML(document []byte) error {
	path, err := m.WritablePath()
	if err != nil {
		return err
	}
	return m.saveBytes(path, document)
}

func (m *Manager) saveBytes(path string, document []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, err := loadWithProject(m.opts, path, document)
	if err != nil {
		return err
	}
	previous, prevErr := os.ReadFile(path)
	if err := writeFileAtomic(path, document); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	res.Files = append([]string{path}, res.Files...)
	if err := m.applyLocked(res); err != nil {
		// Transactional semantics: if a subscriber cannot apply the change,
		// restore the previous document so disk and memory agree.
		if prevErr == nil {
			_ = writeFileAtomic(path, previous)
		} else {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

// Reload re-reads all sources and applies the result. On error the previous
// configuration stays active.
func (m *Manager) Reload() error {
	res, err := Load(m.opts)
	if err != nil {
		m.reportError(err)
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applyLocked(res)
}

func (m *Manager) applyLocked(res *LoadResult) error {
	old := m.cfg
	if reflect.DeepEqual(old, res.Config) {
		m.res = res
		return nil
	}
	applied := make([]ApplyFunc, 0, len(m.subs))
	for _, fn := range m.subs {
		if err := fn(old, res.Config); err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				_ = applied[i](res.Config, old)
			}
			return fmt.Errorf("apply configuration: %w", err)
		}
		applied = append(applied, fn)
	}
	m.cfg = res.Config
	m.res = res
	return nil
}

func (m *Manager) reportError(err error) {
	m.mu.Lock()
	fn := m.onError
	m.mu.Unlock()
	if fn != nil {
		fn(err)
	}
}

// Watch starts hot reload. It watches the parent directories of the relevant
// files and debounces editor write bursts. The watcher stops when ctx ends.
func (m *Manager) Watch(ctx context.Context) error {
	m.mu.Lock()
	dirs := map[string]bool{}
	if p := m.res.ProjectPath; p != "" {
		dirs[filepath.Dir(p)] = true
	}
	if gp := m.res.GlobalPath; gp != "" {
		// The global directory may not exist yet on a zero-config run. Create
		// it so edits made from the console become hot-reloadable too.
		if err := os.MkdirAll(filepath.Dir(gp), 0o700); err == nil {
			dirs[filepath.Dir(gp)] = true
		}
	}
	names := map[string]bool{}
	if p := m.res.ProjectPath; p != "" {
		names[filepath.Base(p)] = true
	}
	if gp := m.res.GlobalPath; gp != "" {
		names[filepath.Base(gp)] = true
	}
	m.mu.Unlock()

	if len(dirs) == 0 {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create config watcher: %w", err)
	}
	for dir := range dirs {
		if err := w.Add(dir); err != nil {
			w.Close()
			return fmt.Errorf("watch %s: %w", dir, err)
		}
	}

	go func() {
		defer w.Close()
		var timer *time.Timer
		var debounce <-chan time.Time
		reset := func() {
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(300 * time.Millisecond)
			}
			debounce = timer.C
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
					continue
				}
				if !names[filepath.Base(ev.Name)] {
					continue
				}
				reset()
			case <-debounce:
				timer = nil
				debounce = nil
				if err := m.Reload(); err != nil {
					m.reportError(err)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				m.reportError(fmt.Errorf("config watcher: %w", err))
			}
		}
	}()
	return nil
}

// writeFileAtomic writes via a temp file and rename so readers never observe a
// partial document.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wormgate-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		// Windows does not implement POSIX modes; best effort only.
		_ = err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}
