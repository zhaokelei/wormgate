package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Options controls layered configuration loading.
type Options struct {
	// ConfigPath is an explicit project configuration file. When empty,
	// WORMGATE_CONFIG and then ./wormgate.yaml are consulted.
	ConfigPath string
	// Profile selects an overlay from the file's "profiles" map.
	Profile string
	// NoGlobal skips the per-user global configuration file.
	NoGlobal bool
	// Role enables role-specific validation: "server" or "client". Empty
	// validates both sections while treating a missing client token as a
	// warning (useful for `wormgate config validate`).
	Role string
	// EnvPrefix defaults to WORMGATE_.
	EnvPrefix string
	// Overrides applies CLI flags last. It must not fail; values are
	// validated together with the rest of the config.
	Overrides func(*Config)
}

// LoadResult is the outcome of a successful (or partially successful) load.
type LoadResult struct {
	Config      *Config
	ProjectPath string
	GlobalPath  string
	Files       []string
	Warnings    []Issue
}

// EffectiveYAML renders the resolved configuration, including secret values.
// Callers must treat the output as sensitive.
func (r *LoadResult) EffectiveYAML() ([]byte, error) {
	b, err := yaml.Marshal(r.Config)
	if err != nil {
		return nil, fmt.Errorf("render configuration: %w", err)
	}
	return b, nil
}

// GlobalPath returns the per-user configuration path. WORMGATE_GLOBAL_CONFIG
// overrides the default location.
func GlobalPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("WORMGATE_GLOBAL_CONFIG")); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	p := filepath.Join(dir, "wormgate", "config.yaml")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// Compatibility: installations created before the Wormgate rename stored
	// the global file under the old directory. Keep reading it so an upgrade
	// does not silently drop user settings; new writes always go to the new
	// location.
	if legacy := filepath.Join(dir, "tunnelx", "config.yaml"); func() bool { _, err := os.Stat(legacy); return err == nil }() {
		return legacy, nil
	}
	return p, nil
}

// FindProjectFile looks for the conventional project config in the current
// working directory.
func FindProjectFile() string {
	// New names first; the legacy names keep older checkouts working.
	for _, name := range []string{"wormgate.yaml", "wormgate.yml", "tunnelx.yaml", "tunnelx.yml"} {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(name); err == nil {
				return abs
			}
			return name
		}
	}
	return ""
}

// Load resolves the configuration using the documented precedence.
func Load(opts Options) (*LoadResult, error) {
	return load(opts, nil, false)
}

// loadWithProject validates candidate project YAML without writing it. The
// manager uses this for save-and-apply transactions.
func loadWithProject(opts Options, projectPath string, raw []byte) (*LoadResult, error) {
	opts.ConfigPath = projectPath
	return load(opts, raw, true)
}

func load(opts Options, projectOverride []byte, hasOverride bool) (*LoadResult, error) {
	if opts.EnvPrefix == "" {
		opts.EnvPrefix = "WORMGATE_"
	}
	cfg := Default()
	res := &LoadResult{Config: cfg}

	globalPath := ""
	if !opts.NoGlobal {
		gp, err := GlobalPath()
		if err != nil {
			res.Warnings = append(res.Warnings, Issue{Path: "config", Message: err.Error()})
		} else {
			globalPath = gp
		}
	}
	res.GlobalPath = globalPath

	projectPath := strings.TrimSpace(opts.ConfigPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(os.Getenv(opts.EnvPrefix + "CONFIG"))
	}
	projectExplicit := projectPath != ""
	if projectPath == "" {
		projectPath = FindProjectFile()
	}
	res.ProjectPath = projectPath

	var issues []Issue
	merged := map[string]any{}

	if globalPath != "" {
		if st, err := os.Stat(globalPath); err == nil && !st.IsDir() {
			m, fileIssues, err := readConfigFile(globalPath)
			if err != nil {
				return res, fmt.Errorf("read global config %s: %w", globalPath, err)
			}
			issues = append(issues, fileIssues...)
			merged = mergeMaps(merged, m)
			res.Files = append(res.Files, globalPath)
		}
	}

	var projectMap map[string]any
	switch {
	case hasOverride:
		m, fileIssues, err := parseConfigBytes(projectPath, projectOverride)
		if err != nil {
			return res, fmt.Errorf("parse configuration: %w", err)
		}
		issues = append(issues, fileIssues...)
		projectMap = m
	case projectPath != "":
		if st, err := os.Stat(projectPath); err != nil {
			if projectExplicit {
				return res, fmt.Errorf("config file %s: %w", projectPath, err)
			}
			projectPath = ""
			res.ProjectPath = ""
		} else if st.IsDir() {
			return res, fmt.Errorf("config path %s is a directory", projectPath)
		} else {
			m, fileIssues, err := readConfigFile(projectPath)
			if err != nil {
				return res, fmt.Errorf("read project config %s: %w", projectPath, err)
			}
			issues = append(issues, fileIssues...)
			projectMap = m
			res.Files = append(res.Files, projectPath)
		}
	}
	if projectMap != nil {
		merged = mergeMaps(merged, projectMap)
	}

	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv(opts.EnvPrefix + "PROFILE"))
	}
	if profile == "" {
		if raw, ok := merged["profile"]; ok {
			s, ok := raw.(string)
			if !ok {
				issues = append(issues, Issue{Path: "profile", Message: "must be a string"})
			} else {
				profile = strings.TrimSpace(s)
			}
		}
	}
	if profilesRaw, ok := merged["profiles"]; ok && profilesRaw != nil {
		profiles, ok := profilesRaw.(map[string]any)
		if !ok {
			issues = append(issues, Issue{Path: "profiles", Message: "must be a mapping of profile name to overrides"})
		} else if profile != "" {
			overlay, ok := profiles[profile].(map[string]any)
			if !ok {
				names := make([]string, 0, len(profiles))
				for name := range profiles {
					names = append(names, name)
				}
				issues = append(issues, Issue{
					Path:    "profiles." + profile,
					Message: fmt.Sprintf("profile %q not found", profile),
					Hint:    "available: " + strings.Join(names, ", "),
				})
			} else {
				merged = mergeMaps(merged, overlay)
			}
		}
	} else if profile != "" {
		issues = append(issues, Issue{Path: "profiles." + profile, Message: "profile not found; no profiles section is defined"})
	}
	delete(merged, "profiles")
	if profile != "" {
		merged["profile"] = profile
	}

	if len(issues) == 0 {
		encoded, err := yaml.Marshal(merged)
		if err != nil {
			return res, fmt.Errorf("prepare configuration: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(encoded))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return res, fmt.Errorf("decode configuration: %w", err)
		}
	}
	cfg.Profile = profile

	envIssues, envUsed := applyEnv(cfg, opts.EnvPrefix)
	issues = append(issues, envIssues...)
	issues = append(issues, unknownEnvWarnings(opts.EnvPrefix, envUsed)...)

	if opts.Overrides != nil {
		opts.Overrides(cfg)
	}

	vr := ValidateRole(cfg, opts.Role)
	issues = append(issues, vr.Errors...)
	res.Warnings = append(res.Warnings, vr.Warnings...)

	if len(issues) > 0 {
		return res, &ErrorList{Issues: issues}
	}
	return res, nil
}

func readConfigFile(path string) (map[string]any, []Issue, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return parseConfigBytes(path, raw)
}

func parseConfigBytes(displayPath string, raw []byte) (map[string]any, []Issue, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, nil, fmt.Errorf("%s: invalid YAML: %w", displayPath, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("%s: top level must be a mapping: %w", displayPath, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	var issues []Issue
	validateKnownKeys(&root, reflect.TypeOf(Config{}), "", displayPath, &issues)
	return m, issues, nil
}

func mergeMaps(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = mergeMaps(dm, sm)
				continue
			}
			dst[k] = mergeMaps(map[string]any{}, sm)
			continue
		}
		dst[k] = sv
	}
	return dst
}

var durationType = reflect.TypeOf(Duration(0))

// applyEnv maps WORMGATE_SERVER_HTTP_DOMAIN style variables onto the schema.
// It returns any parse failures and the set of variables it consumed.
func applyEnv(cfg *Config, prefix string) ([]Issue, map[string]bool) {
	used := map[string]bool{}
	var issues []Issue
	var walk func(v reflect.Value, path []string)
	walk = func(v reflect.Value, path []string) {
		t := v.Type()
		if t == durationType {
			if len(path) == 0 {
				return
			}
			name := envName(prefix, path)
			raw, ok := os.LookupEnv(name)
			if !ok {
				return
			}
			used[name] = true
			d, err := parseDurationEnv(raw)
			if err != nil {
				issues = append(issues, Issue{Path: strings.Join(path, "."), Message: fmt.Sprintf("%s: %v", name, err)})
				return
			}
			v.SetInt(int64(d))
			return
		}
		switch t.Kind() {
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
				if tag == "" {
					tag = strings.ToLower(f.Name)
				}
				if tag == "-" || (len(path) == 0 && tag == "profile") {
					continue
				}
				if v.Field(i).Kind() == reflect.Pointer || v.Field(i).Kind() == reflect.Interface {
					continue
				}
				walk(v.Field(i), append(path, tag))
			}
		case reflect.String:
			if len(path) == 0 {
				return
			}
			name := envName(prefix, path)
			if raw, ok := os.LookupEnv(name); ok {
				used[name] = true
				v.SetString(strings.TrimSpace(raw))
			}
		case reflect.Bool:
			name := envName(prefix, path)
			if raw, ok := os.LookupEnv(name); ok {
				used[name] = true
				b, err := strconv.ParseBool(strings.TrimSpace(raw))
				if err != nil {
					issues = append(issues, Issue{Path: strings.Join(path, "."), Message: fmt.Sprintf("%s: must be true or false", name)})
					return
				}
				v.SetBool(b)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			name := envName(prefix, path)
			if raw, ok := os.LookupEnv(name); ok {
				used[name] = true
				n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil {
					issues = append(issues, Issue{Path: strings.Join(path, "."), Message: fmt.Sprintf("%s: must be an integer", name)})
					return
				}
				v.SetInt(n)
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			name := envName(prefix, path)
			if raw, ok := os.LookupEnv(name); ok {
				used[name] = true
				n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
				if err != nil {
					issues = append(issues, Issue{Path: strings.Join(path, "."), Message: fmt.Sprintf("%s: must be a non-negative integer", name)})
					return
				}
				v.SetUint(n)
			}
		case reflect.Slice:
			if t.Elem().Kind() != reflect.String || len(path) == 0 {
				return
			}
			name := envName(prefix, path)
			raw, ok := os.LookupEnv(name)
			if !ok {
				return
			}
			used[name] = true
			parts := []string{}
			for _, p := range strings.Split(raw, ",") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, p)
				}
			}
			v.Set(reflect.ValueOf(parts))
		}
	}
	walk(reflect.ValueOf(cfg).Elem(), nil)
	return issues, used
}

func envName(prefix string, path []string) string {
	return prefix + strings.ToUpper(strings.Join(path, "_"))
}

func parseDurationEnv(raw string) (Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("must not be empty")
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("must not be negative")
		}
		return Duration(time.Duration(n) * time.Second), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("use forms like 500ms, 15s, 2m: %w", err)
	}
	if d < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return Duration(d), nil
}

func unknownEnvWarnings(prefix string, used map[string]bool) []Issue {
	reserved := map[string]bool{
		prefix + "CONFIG":        true,
		prefix + "PROFILE":       true,
		prefix + "GLOBAL_CONFIG": true,
		prefix + "NO_GLOBAL":     true,
	}
	var warnings []Issue
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		if !strings.HasPrefix(name, prefix) || used[name] || reserved[name] {
			continue
		}
		warnings = append(warnings, Issue{
			Path:    name,
			Message: "environment variable does not match any configuration field",
		})
	}
	return warnings
}
