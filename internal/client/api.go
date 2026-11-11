package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/wormgate/wormgate/internal/config"
)

type errorResponse struct {
	Error  string   `json:"error"`
	Issues []string `json:"issues,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func apiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func apiConfigError(w http.ResponseWriter, err error) {
	resp := errorResponse{Error: err.Error()}
	var list *config.ErrorList
	if errors.As(err, &list) {
		resp.Error = "configuration is invalid"
		for _, issue := range list.Issues {
			resp.Issues = append(resp.Issues, issue.String())
		}
	}
	writeJSON(w, http.StatusBadRequest, resp)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

type tunnelInput struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Local      string `json:"local"`
	Subdomain  string `json:"subdomain"`
	RemotePort int    `json:"remote_port"`
	Enabled    *bool  `json:"enabled"`
	// Encryption adds the optional AES-256-GCM layer inside the tunnel stream.
	// It is opt-in per tunnel and requires client.stream_secret to be set.
	Encryption bool `json:"encryption"`
}

func (in tunnelInput) toConfig(fallbackEnabled bool) config.TunnelConfig {
	enabled := fallbackEnabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	kind := strings.ToLower(strings.TrimSpace(in.Type))
	if kind == "" {
		kind = "http"
	}
	return config.TunnelConfig{
		Name:       strings.TrimSpace(in.Name),
		Type:       kind,
		Local:      strings.TrimSpace(in.Local),
		Subdomain:  strings.ToLower(strings.TrimSpace(in.Subdomain)),
		RemotePort: in.RemotePort,
		Enabled:    config.Bool(enabled),
		Encryption: in.Encryption,
	}
}

// requireStreamSecret rejects an encryption request the client cannot honour.
// Config validation would catch it too, but its message names the YAML path
// instead of telling the operator where the console setting lives.
func (a *Agent) requireStreamSecret(cfg config.TunnelConfig) error {
	if !cfg.Encryption {
		return nil
	}
	if strings.TrimSpace(a.cfg.Current().Client.StreamSecret) == "" {
		return fmt.Errorf("tunnel %q asked for encryption but client.stream_secret is empty; set the shared secret under 配置 → 隧道加密 first", cfg.Name)
	}
	return nil
}

func (a *Agent) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.Status())
}

func (a *Agent) handleListTunnels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnels":   a.Tunnels(),
		"timestamp": time.Now().UnixMilli(),
	})
}

func (a *Agent) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var in tunnelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tunnel := in.toConfig(true)
	if tunnel.Name == "" {
		apiError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := a.requireStreamSecret(tunnel); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := a.MutateTunnels(func(list []config.TunnelConfig) ([]config.TunnelConfig, error) {
		for _, existing := range list {
			if existing.Name == tunnel.Name {
				return nil, fmt.Errorf("tunnel %q already exists", tunnel.Name)
			}
		}
		return append(list, tunnel), nil
	})
	if err != nil {
		apiConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tunnel": tunnel, "tunnels": a.Tunnels()})
}

func (a *Agent) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var in tunnelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := a.requireStreamSecret(in.toConfig(true)); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := a.MutateTunnels(func(list []config.TunnelConfig) ([]config.TunnelConfig, error) {
		for i := range list {
			if list[i].Name != name {
				continue
			}
			enabled := list[i].IsEnabled()
			updated := in.toConfig(enabled)
			updated.Name = name
			if strings.TrimSpace(updated.Local) == "" {
				return nil, fmt.Errorf("local is required")
			}
			list[i] = updated
			return list, nil
		}
		return nil, fmt.Errorf("tunnel %q not found", name)
	})
	if err != nil {
		apiConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": a.Tunnels()})
}

func (a *Agent) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := a.MutateTunnels(func(list []config.TunnelConfig) ([]config.TunnelConfig, error) {
		out := list[:0]
		found := false
		for _, tunnel := range list {
			if tunnel.Name == name {
				found = true
				continue
			}
			out = append(out, tunnel)
		}
		if !found {
			return nil, fmt.Errorf("tunnel %q not found", name)
		}
		return out, nil
	})
	if err != nil {
		apiConfigError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) handleTunnelEnable(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		err := a.MutateTunnels(func(list []config.TunnelConfig) ([]config.TunnelConfig, error) {
			for i := range list {
				if list[i].Name == name {
					list[i].Enabled = config.Bool(enabled)
					return list, nil
				}
			}
			return nil, fmt.Errorf("tunnel %q not found", name)
		})
		if err != nil {
			apiConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tunnels": a.Tunnels()})
	}
}

func (a *Agent) handleReconnect(w http.ResponseWriter, _ *http.Request) {
	a.Reconnect()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (a *Agent) handleConnections(w http.ResponseWriter, _ *http.Request) {
	conns := a.Connections()
	if conns == nil {
		conns = []ConnView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connections": conns,
		"timestamp":   time.Now().UnixMilli(),
	})
}

func (a *Agent) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.Stats())
}

func (a *Agent) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.ConfigYAML()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	if path := a.cfg.WritablePathOrEmpty(); path != "" {
		w.Header().Set("X-Wormgate-Config-Path", path)
	}
	_, _ = w.Write(raw)
}

func (a *Agent) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		apiError(w, http.StatusBadRequest, "read configuration: "+err.Error())
		return
	}
	if err := a.cfg.SaveYAML(raw); err != nil {
		apiConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": a.cfg.Files()})
}

// ---------------------------------------------------------------------------
// Structured connection settings (the fpc console "连接设置" form).
//
// Unlike /api/config, which edits the whole YAML document, this endpoint only
// touches the keys the form owns. Fields that cannot be hot-reloaded
// (client.webui, log.format, log.add_caller) are returned read-only so the UI
// can show them without offering a save that would be rejected by
// Agent.onConfigChange.
// ---------------------------------------------------------------------------

type settingsTLS struct {
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	ServerName         string `json:"server_name"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

type settingsReconnect struct {
	MinBackoff string `json:"min_backoff"`
	MaxBackoff string `json:"max_backoff"`
}

type settingsPayload struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	// StreamSecret is the optional shared key for in-tunnel AES-256-GCM. The
	// form owns the value exactly like token does: GET returns it, PUT writes
	// it, and an empty string means "no secret / encryption unavailable".
	StreamSecret      string            `json:"stream_secret"`
	ClientName        string            `json:"client_name"`
	TLS               settingsTLS       `json:"tls"`
	HeartbeatInterval string            `json:"heartbeat_interval"`
	DialTimeout       string            `json:"dial_timeout"`
	Reconnect         settingsReconnect `json:"reconnect"`
}

func (a *Agent) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	c := a.cfg.Current().Client
	writeJSON(w, http.StatusOK, map[string]any{
		"server":      c.Server,
		"token":       c.Token,
		"client_name": c.ClientName,
		// Returned verbatim for the same reason client.token is: the operator
		// has to copy it to the server, and GET /api/config already exposes the
		// whole document to whoever is signed in to this console.
		"stream_secret":     c.StreamSecret,
		"stream_secret_set": strings.TrimSpace(c.StreamSecret) != "",
		"tls": map[string]any{
			"ca_file":              c.TLS.CAFile,
			"cert_file":            c.TLS.CertFile,
			"key_file":             c.TLS.KeyFile,
			"server_name":          c.TLS.ServerName,
			"insecure_skip_verify": c.TLS.InsecureSkipVerify,
		},
		"heartbeat_interval": c.HeartbeatInterval.String(),
		"dial_timeout":       c.DialTimeout.String(),
		"reconnect": map[string]any{
			"min_backoff": c.Reconnect.MinBackoff.String(),
			"max_backoff": c.Reconnect.MaxBackoff.String(),
		},
		"read_only": map[string]any{
			"webui_enabled":     c.WebUI.Enabled,
			"webui_addr":        c.WebUI.Addr,
			"webui_session_ttl": c.WebUI.SessionTTL.String(),
			"password_managed":  c.WebUI.PasswordHash != "",
		},
		"config_path":      a.cfg.WritablePathOrEmpty(),
		"restart_required": []string{"client.webui", "log.format", "log.add_caller"},
	})
}

func (a *Agent) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsPayload
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Server = strings.TrimSpace(in.Server)
	in.Token = strings.TrimSpace(in.Token)
	in.ClientName = strings.TrimSpace(in.ClientName)
	in.StreamSecret = strings.TrimSpace(in.StreamSecret)
	// The Go validator only warns below 16 characters; the console refuses
	// instead, because a short shared secret is the one way this feature goes
	// from "defence in depth" to "security theatre".
	if in.StreamSecret != "" && len(in.StreamSecret) < 16 {
		apiError(w, http.StatusBadRequest, "stream_secret must be at least 16 characters")
		return
	}

	// JSON is a subset of YAML and the json tags match the yaml tags, so this
	// round-trip yields plain nested maps suitable for UpdateClientSettings.
	raw, err := json.Marshal(in)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "encode settings: "+err.Error())
		return
	}
	var patch map[string]any
	if err := yaml.Unmarshal(raw, &patch); err != nil {
		apiError(w, http.StatusBadRequest, "invalid settings: "+err.Error())
		return
	}
	if err := a.cfg.UpdateClientSettings(patch); err != nil {
		apiConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": "修改服务端地址、令牌、TLS 或客户端名称时会自动重连；心跳与退避参数在下次重连后生效",
	})
}
