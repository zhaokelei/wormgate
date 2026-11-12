# Wormgate

[English](README.md) | [简体中文](README.zh-CN.md)

Wormgate is a reverse tunnel written in Go. Machines without a public IP — behind NAT, firewalls, or carrier-grade CGNAT — can publish local HTTP services and TCP ports through a public ingress. The client dials out to the server's control plane, so no inbound ports, port forwarding, or router configuration are needed on the client side.

Two static binaries:

| Binary | Role | Start with |
|---|---|---|
| **`fps`** | Server: control plane + public ingress + admin console + metrics | `fps` |
| **`fpc`** | Client: publishes local services + local web config console | `fpc --server x:7000 --token wg_... --tunnel web=http://127.0.0.1:8080` |

## Features

**Publishing services**

- HTTP subdomain routing: one wildcard DNS record serves every tunnel as `<sub>.<domain>`. Unmatched `Host` headers get a 404 — the ingress never forwards blindly. Spoofed `X-Forwarded-*` headers are stripped and re-injected with trusted values before proxying.
- TCP port mapping: the server assigns remote ports automatically, or you can pin them in the config.
- Optional per-tunnel stream encryption: an additional AES-256-GCM layer inside TLS. If the server has no key configured the feature is unavailable; once configured, each tunnel opts in individually (`#enc`). A key mismatch is a hard failure — never a silent fallback to plaintext.
- Multi-user tokens with quotas: every token belongs to a user; the public ingress meters, rate-limits, and enforces quotas per owner.

**Low operational overhead**

- Zero-config startup: `fps` runs bare and prints a ready-to-paste client command on first start. `fpc` connects with a single line — no config file required.
- One YAML shared by both ends: the server reads the `server:` block, the client reads `client:`. Edits hot-reload within 300 ms without dropping established tunnels.
- Web consoles built in: a client console on `:7200` and an admin console on `:7100`, both embedded in the binaries, available in Chinese and English. Nothing to deploy separately.

**Operability**

- Single static binaries: the frontend is compiled in via `go:embed`, built with `CGO_ENABLED=0` — ten artifacts covering Linux / macOS / Windows × amd64 / arm64. Target machines need no Node.js or any other runtime.
- Control plane and data plane are separated: five independent listeners (control, ingress, admin, metrics, client console) with isolated failure domains.
- Prometheus metrics on `:9090/metrics`: connection counts, traffic, tunnel state.
- Credentials are stored hashed only: admin passwords use Argon2id, tokens use SHA-256. Plaintext appears exactly once, in the first-start log.

Dependencies are minimal: Go 1.26+, eight direct modules (websocket / yamux / zap / jwt / yaml / prometheus / fsnotify / x-crypto). Both Vue consoles use no UI component library — the design system is hand-rolled.

---

## 1. Architecture

Five independent HTTP listeners; control plane and data plane are fully separated:

```
                       PUBLIC REQUEST
                  Host: web.wormgate.local
                             |
                             v
  +--------------------------------------------------------------+
  | [1] public ingress   :8080        (server.http.addr)          |
  |     matchHost() -> registry; unmatched Host -> 404 (deny)     |
  |     ReverseProxy + per-owner metering / quota                 |
  +-----------------------------+--------------------------------+
                                |
                                v  OpenStream() + 12B StreamHeader
  +--------------------------------------------------------------+
  | Wormgate SERVER                                               |
  |                                                              |
  |  [2] control :7000 (TLS 1.3)      [3] admin :7100/admin/     |
  |      GET /control  WS control          Vue 3 SPA + /api/*    |
  |      GET /data     WS + yamux                                |
  |      GET /healthz                 [4] metrics :9090/metrics  |
  +------------^------------------------^------------------------+
               |                        |
     wss:// + Bearer token     wss:// + X-Wormgate-Session
     hello -> hello_ok          yamux multiplexed streams
               |                        |
  +------------+------------------------+-------------------------+
  | Wormgate CLIENT                                               |
  |   acceptStreams() -> StreamHeader -> net.Dial(local)          |
  |   [5] local console :7200   (SSE /api/logs + WS /api/ws)      |
  +-----------------------------+--------------------------------+
                                |
                                v
                     127.0.0.1:8090  (your app)
```

**HTTP tunnel data flow**

```
browser -> public ingress -> matchHost(host, domain) -> registry.BySubdomain
        -> [no match] http.NotFound 404
        -> ReverseProxy -> acquireSlot(quota) -> ClientSession.OpenStream()
        -> 12B StreamHeader{tunnel,conn_id,source,kind} -> WS /data (binary)
        -> yamux -> client acceptStreams() -> net.Dial(local)
        -> netpipe.Pipe(local, meteredStream) -> response back along the same path
```

**Handshake.** The client dials `wss://host:7000/control` with `Authorization: Bearer <token>` and must send its first `hello` frame within 10 seconds or the server drops the connection. The server replies `hello_ok` (containing `session_id` and heartbeat intervals). The client then dials `wss://host:7000/data` with the `X-Wormgate-Session` header; `websocket.NetConn(..., MessageBinary)` carries yamux. `register` / `tunnel_result` complete registration. Heartbeat defaults to 15 s, session timeout to 45 s.

**Routing is deny by default.** `matchHost` requires the `Host` to be exactly `<sub>.<domain>`, with `sub` a single valid DNS label that exists in the registry; anything else gets a 404. Spoofed `X-Forwarded-*`, `X-Real-IP`, and `X-Wormgate-Tunnel` headers are stripped before proxying and replaced with trusted values. The only exception is a connection from a network listed in `server.trusted_proxies`: there the rightmost untrusted address in `X-Forwarded-For` becomes the reported client, which is how a load balancer in front of Wormgate still yields real visitor addresses.

---

## 2. Quick start

```bash
# 1) Build both binaries (frontend first, then Go — order matters, see "Building")
cd web && npm ci && npm run build && cd ..
go build -o bin/fps ./cmd/fps
go build -o bin/fpc ./cmd/fpc
# Append .exe on Windows; bash scripts/build.sh produces every platform in one shot

# 2) Start any local HTTP service as the origin
#    e.g. python -m http.server 8090

# 3) Start fps with a fresh data dir — the first start prints a one-time admin
#    password, a demo token, and a ready-to-paste fpc command
./bin/fps --data-dir ./data-server --http-addr 127.0.0.1:8088
#   warn  generated initial admin password - store it now {"username":"admin","password":"...","note":"..."}
#   info  demo client token (user: demo) {"token":"wg_...","reprinted":false,...}
#   info  quick start - run this on the client machine {"command":"fpc --server ... --token ... --tunnel ..."}

# 4) Connect the client with a single line — no config file needed
./bin/fpc --server 127.0.0.1:7000 --token wg_... --insecure \
    --tunnel web=http://127.0.0.1:8090 \
    --tunnel api=http://127.0.0.1:8090
#   info  client console is ready {"url":"http://127.0.0.1:7200/","token":"wx_...","note":"..."}
#   info  tunnel is live {"tunnel":"web","public":"http://web.wormgate.local:8088"}
```

You can also generate a config file first with `./bin/fpc init --role client --path fpc.yaml --token wg_... --local 127.0.0.1:8090` and run `./bin/fpc --config fpc.yaml`. **`fps` and `fpc` can share the same YAML**: each reads only its own `server:` / `client:` block.

**Verify** (use a `Host` header locally — no need to edit your hosts file):

```bash
curl -i -H "Host: web.wormgate.local" http://127.0.0.1:8088/
curl -i -H "Host: api.wormgate.local" http://127.0.0.1:8088/
curl -i -H "Host: nope.wormgate.local" http://127.0.0.1:8088/   # 404 — deny by default
```

Consoles: the client console at `http://127.0.0.1:7200/` (log in with the `wx_...` token from the log) and the admin console at `http://<server-ip>:7100/admin/` (binds `0.0.0.0:7100` by default and is reachable from the public internet; log in with the one-time `admin` password and **change it immediately**).

> First-start credentials are printed once. The server stores only Argon2id password hashes / SHA-256 token hashes. Changing `--data-dir` regenerates them; restarting with the same data dir reprints the demo token as `reprinted: true` but never reprints the admin password. **Note**: if you save a token through the fpc console forms, it is written to your config file at your request (see `config_path` in `config show`) — protect that file's permissions.

---

## 3. Building

`web/` is **one npm package with two Vite projects**; build output goes straight into the Go embed directories. `web/next/` is a third, independent frontend (React, with its own dependencies):

| Project | Stack | Config | Base | Output |
|---|---|---|---|---|
| Client console | Vue 3 + hand-rolled CSS design system | `vite.client.config.js` | `/` | `internal/webui/clientui/dist` |
| Admin console | Vue 3 + hand-rolled CSS design system | `vite.admin.config.js` | `/admin/` | `internal/webui/adminui/dist` |
| Admin console (canary) | React 19 + Ant Design 6 | `next/vite.config.ts` | `/next/` | `internal/webui/adminnext/dist` |

```bash
cd web
npm ci                       # or npm install
npm run build                # = build:client && build:admin && build:next
cd ..
go build -o bin/fps ./cmd/fps
go build -o bin/fpc ./cmd/fpc
go vet ./... && go test ./...
```

`web/next` has its own `package.json` / `node_modules` (React 19, antd 6, uplot, TanStack Query — kept separate from the Vue consoles' dependencies); run `npm --prefix next install` before the first build.

`//go:embed all:dist` reads the dist directories **at compile time**, so editing the frontend without re-running Vite — or re-running Vite without recompiling Go — changes nothing in production. During development use `npm run dev:client` / `dev:admin` (Vite proxies `/api` to 7200 / 7100). If `proxy.golang.org` is unreachable, set `GOPROXY=https://goproxy.cn,direct`.

`cmd/fps` and `cmd/fpc` are separate `main` packages sharing flag definitions, config bootstrapping, and utility commands from `internal/cli`. `cmd/tunnelx` is a thin compatibility shim kept for old scripts (`wormgate server|client ...`); release builds no longer produce it.

### 3.1 Cross-platform builds

```bash
bash scripts/build.sh              # frontend + all platforms
SKIP_WEB=1 bash scripts/build.sh   # reuse already-built web assets
OUT=release bash scripts/build.sh  # custom output directory
```

The script cross-compiles with `CGO_ENABLED=0` and produces ten static binaries in `dist/`:

| Platform | GOARCH | Artifacts | Format |
|---|---|---|---|
| Linux x86_64 | amd64 | `fps-linux-amd64` / `fpc-linux-amd64` | ELF |
| Linux ARM64 | arm64 | `fps-linux-arm64` / `fpc-linux-arm64` | ELF |
| macOS Intel | amd64 | `fps-darwin-amd64` / `fpc-darwin-amd64` | Mach-O |
| macOS Apple Silicon | arm64 | `fps-darwin-arm64` / `fpc-darwin-arm64` | Mach-O |
| Windows x86_64 | amd64 | `fps-windows-amd64.exe` / `fpc-windows-amd64.exe` | PE |

The script verifies each artifact's 4-byte magic number (ELF / Mach-O / PE). Add the executable bit and run: `chmod +x dist/fps-linux-amd64 && ./dist/fps-linux-amd64`. All platforms share the same code and the same embedded frontend; target machines need no Node and no runtime.

---

## 4. Configuration

### 4.1 Precedence

```
CLI flags  >  environment variables  >  profile overlay  >  project config file  >  user-level global config  >  built-in defaults
```

- Project file: `--config` → `WORMGATE_CONFIG` → `wormgate.yaml` / `wormgate.yml` in the current directory
- Global config: `WORMGATE_GLOBAL_CONFIG`, otherwise `os.UserConfigDir()/wormgate/config.yaml` (the legacy `tunnelx/` directory is still read as a fallback); set `WORMGATE_NO_GLOBAL=true` to skip it
- Profile: `--profile` → `WORMGATE_PROFILE` → the `profile` key inside the file, resolved against the `profiles` map
- Merging is a **deep, per-key merge** — the YAML only needs the fields that differ. Decoding is strict (`KnownFields(true)`); unknown fields fail with a line-numbered diagnostic
- Config changes hot-reload automatically after a 300 ms debounce; a failed reload keeps the old config. Established tunnels are never dropped. Listener addresses, TLS, data-dir, and admin credentials require a restart

Both binaries ship the same utility commands: `fps|fpc config show|validate` (effective config / validate only), `fps|fpc password-hash 'your-password'` (Argon2id hash), `fps|fpc init` (scaffold a config file), `fps|fpc version`. **No subcommand means "start in this role"**; see `fps --help` / `fpc --help`.

### 4.2 YAML (full field reference)

```yaml
# Top-level keys: profile / log / server / client / profiles
profile: dev

log:
  level: info              # debug|info|warn|error
  format: console          # console|json
  add_caller: false

server:
  control_addr: "0.0.0.0:7000"
  heartbeat_interval: 15s
  session_timeout: 45s
  tls:
    cert_file: ""
    key_file: ""
    client_ca_file: ""     # when set, enables mTLS (RequireAndVerifyClientCert)
    auto_self_signed: true # alternative to cert_file/key_file
  http:
    addr: "0.0.0.0:8080"
    domain: wormgate.local  # lowercase DNS name, not an IP
    scheme: http           # http|https (scheme of the public ingress itself)
    public_port: 0         # 0 = derive automatically
    read_header_timeout: 10s
    idle_timeout: 90s
  tcp:
    bind_addr: "0.0.0.0"
    advertise_host: ""
    port_range: "20000-30000"
  # Reverse proxies in front of the HTTP ingress whose X-Forwarded-For may be
  # believed. Leave empty when Wormgate accepts connections itself: the
  # forwarding headers are then always discarded, so a caller cannot claim an
  # address it does not own. With an entry here, the rightmost untrusted
  # address in the chain becomes X-Real-IP for the tunnel backend.
  # trusted_proxies: ["172.19.0.0/16", "127.0.0.1"]
  trusted_proxies: []
  # Optional per-tunnel stream-encryption key. Empty = feature disabled.
  # When set, only tunnels that request encryption are encrypted; all other
  # tunnels behave as before (opt-in, never forced). Must match the client.
  stream_secret: ""
  webui:
    enabled: true
    addr: "0.0.0.0:7100"
    admin_user: admin
    admin_password: ""          # bootstrap only; admin_password_hash preferred
    admin_password_hash: ""     # must start with $argon2id$
    session_ttl: 12h
    secure_cookie: false        # set true when serving over HTTPS
    oauth2:
      enabled: false
      client_id: ""
      client_secret: ""
      auth_url: ""
      token_url: ""
      userinfo_url: ""
      redirect_url: ""
      scopes: []
      username_field: ""
      default_role: ""          # admin|user|viewer
  metrics:
    enabled: true
    addr: "0.0.0.0:9090"
  auth:
    tokens: []                 # extra static client tokens, comma-separated
  quota:
    max_tunnels_per_user: 20
    max_connections_per_tunnel: 200
    daily_traffic_bytes: 0     # 0 = unlimited
  data_dir: data

client:
  server: "127.0.0.1:7000"     # host:port, no scheme; TLS is always on
  token: ""                    # required for the client role
  client_name: ""
  tls:
    ca_file: ""
    cert_file: ""
    key_file: ""
    insecure_skip_verify: false
    server_name: ""
  heartbeat_interval: 15s
  dial_timeout: 5s
  reconnect:
    min_backoff: 1s
    max_backoff: 30s
  tunnels:
    - name: web
      type: http               # http|tcp
      local: "127.0.0.1:8080"
      subdomain: ""            # empty = assigned by the server
      remote_port: 0           # required for tcp tunnels, 1-65535
      encryption: false        # true = enable stream encryption (needs stream_secret)
      enabled: true            # omitted = true
  # Must match server.stream_secret exactly; used only by encrypted tunnels
  stream_secret: ""
  webui:
    enabled: true
    addr: "127.0.0.1:7200"    # keep on loopback
    token: ""
    password_hash: ""
    data_dir: data
    session_ttl: 12h

profiles:                      # overlays, not a Config struct field
  dev:
    log: { level: debug }
  prod:
    log: { level: info }
```

### 4.3 Environment variables

Any field can be overridden with `WORMGATE_<PATH>`: upper-cased YAML tags joined by underscores, e.g. `server.http.domain` → `WORMGATE_SERVER_HTTP_DOMAIN`. Four dedicated variables are also reserved:

```bash
export WORMGATE_CONFIG=/etc/wormgate/wormgate.yaml   # project config file
export WORMGATE_PROFILE=prod                       # profile overlay
export WORMGATE_GLOBAL_CONFIG=/etc/wormgate/global.yaml
export WORMGATE_NO_GLOBAL=true                     # ignore user-level global config

export WORMGATE_SERVER_HTTP_DOMAIN=example.com
export WORMGATE_SERVER_WEBUI_SECURE_COOKIE=true
export WORMGATE_CLIENT_TOKEN=wg_xxxxxxxxxxxxxxxx
export WORMGATE_CLIENT_TLS_INSECURE_SKIP_VERIFY=false
```

`WORMGATE_*` variables that match no field print a warning, so typos are never silently ignored.

### 4.4 CLI

`fps` and `fpc` run their respective roles by default — **no subcommand needed**. Both share the `config show|validate`, `init`, `password-hash`, and `version` utility commands (place them first, e.g. `fpc config validate --config fpc.yaml`). For compatibility with old scripts, `fps server ...` / `fpc client ...` are also accepted.

```bash
# fps: common server flags
./bin/fps \
  --control-addr 0.0.0.0:7000 \
  --http-addr 0.0.0.0:8080 --http-domain example.com --http-public-port 80 \
  --tcp-bind 0.0.0.0 --tcp-port-range 20000-30000 \
  --webui-addr 0.0.0.0:7100 --metrics-addr 0.0.0.0:9090 \
  --data-dir /var/lib/wormgate \
  --tls-cert /etc/wormgate/cert.pem --tls-key /etc/wormgate/key.pem \
  --tls-client-ca /etc/wormgate/ca.pem \
  --token "wg_aaa, wg_bbb" --admin-password 'change-me-now' \
  --log-level info --log-format json

# fpc: --tunnel is repeatable; format name=type://local[@subdomain|@port][#enc]
./bin/fpc \
  --server example.com:7000 --token wg_xxx --name laptop-web \
  --tunnel web=http://127.0.0.1:8080@web \
  --tunnel ssh=tcp://127.0.0.1:22@2222#enc \
  --stream-secret 'a-long-random-shared-value' \
  --ca-file /etc/wormgate/ca.pem \
  --webui-addr 127.0.0.1:7200 --webui-token wx_xxx

# Scaffold a config file (0o600 file, 0o700 directory)
./bin/fpc init --role both --path ./wormgate.yaml --yes
```

Note: `--tunnel` **replaces** the tunnel list from YAML wholesale. Only flags set explicitly override the config; unset flags fall back to YAML / env / defaults. The full flag list is always available via `fps --help` / `fpc --help`.

### 4.5 Optional per-tunnel stream encryption

Tunnel traffic already runs over TLS 1.3 — but when the client uses `--insecure` (or skips verification against a self-signed certificate), TLS guarantees "nobody is eavesdropping", not "the peer is who it claims to be": a man in the middle can present its own certificate and decrypt everything. Stream encryption closes exactly that gap: an AES-256-GCM layer inside TLS, keyed from a shared passphrase you configure — **independent of any certificate**.

Disabled by default, enabled per tunnel, and it changes nothing for existing tunnels. Both web consoles can manage it without touching YAML:

- **Server** — "Security → Tunnel encryption": shows enabled state, the passphrase fingerprint (first 16 hex of SHA-256), the current passphrase (reveal/copy), and how many tunnels are encrypted vs. plaintext. Buttons: generate & enable, save passphrase, disable, regenerate. Writes are **line-level edits** — comments and blank lines in `wormgate.yaml` are preserved verbatim.
- **Client** — "Config → Tunnel encryption (optional)": a passphrase field (show/hide/copy/generate) plus the same fingerprint as the server, so you can verify both ends match at a glance. The tunnel create/edit dialogs have a "Stream encryption · AES-256-GCM" switch, and the tunnel list marks encrypted tunnels with a green badge.

CLI equivalent:

```bash
# Server: set a long random value (empty = feature unavailable; everything else unchanged)
#   server.stream_secret: "a-long-random-shared-value"

# Client: same value, plus #enc on the tunnels that should be encrypted
fpc --server example.com:7001 --token wg_xxx \
    --stream-secret 'a-long-random-shared-value' \
    --tunnel 'ssh=tcp://127.0.0.1:22@2222#enc' \
    --tunnel 'web=http://127.0.0.1:8080'      # this one stays TLS-only
```

Behavior:

| Situation | Result |
|---|---|
| Both ends share `stream_secret`, tunnel opts in | Data plane encrypted; keys derived per `session + tunnel name`, never reused across tunnels |
| Keys differ between ends | Data-plane authentication fails, the request returns 502 — **never a plaintext fallback** |
| Server has no `stream_secret` | Registration rejected: `stream encryption requested but this server has no stream_secret configured` |
| Client opts in without a `stream_secret` | Save rejected by the console with a hint to set it under "Config → Tunnel encryption"; the backend validates independently |
| Tunnel does not opt in | Unchanged, protected by TLS only (default behavior) |

Rotating the passphrase takes effect **immediately**: the server uses the new value on the next handshake, and clients detect the change and reconnect automatically (keys derive from `session + passphrase`, so changing the passphrase forces a new session). While only one side is updated, encrypted tunnels fail authentication — deliberate fail-closed behavior.

The implementation lives in `internal/streamcipher`: the stream header (tunnel name, conn id) stays plaintext for routing; everything after it is encrypted frame by frame (4-byte length prefix + ciphertext), with direction-specific subkeys and transparent half-close (`CloseWrite`). Tests cover round-trips, cross-frame reads, wrong keys, tampering detection, and key isolation. Server-side passphrase writes go through `config.Manager.SetServerScalar`, which rewrites a single scalar key under `server:` and leaves every other byte untouched (see `internal/config/manager_scalar_test.go`).

> **Deployment note**: the systemd unit uses `ProtectSystem=strict`, so the service sees a **read-only** filesystem except for paths in `ReadWritePaths`. If saving the passphrase from the admin console fails with
> `write /opt/wormgate/etc/wormgate.yaml: open ...: read-only file system`, it is not a permissions or disk problem — the directory is simply not whitelisted. Add a drop-in (no need to touch the main unit):
>
> ```ini
> # /etc/systemd/system/wormgate-fps.service.d/20-etc-rw.conf
> [Service]
> ReadWritePaths=/opt/wormgate/etc
> ```
>
> Then `systemctl daemon-reload && systemctl restart wormgate-fps`. The repository's `.run/deploy_fps.py` already writes that path into the unit template and **preserves any existing `stream_secret`** when rewriting the config, so redeploying never wipes the passphrase.

---

## 5. Consoles and API

Both consoles are Vue 3 with a hand-written CSS design system (dark/light themes, no first-paint flash), CSRF double-submit, unified 401 handling, and WS/SSE reconnect with jitter. Static assets pass through a Go-side gzip middleware (the chart bundle: 481 KB → 190 KB); gzip wraps static assets only, **not the API mux**, so SSE is never buffered.

### 5.0 The canary React admin console `/next/`

Besides `/admin/` (Vue), a React admin console with the **same API and the same cookies** is mounted for page-by-page evaluation before switching over:

| | `/admin/` | `/next/` |
|---|---|---|
| Stack | Vue 3 + hand-rolled CSS design system (`web/common/theme.css`) | React 19 + Ant Design 6 + uplot + TanStack Query |
| Pages | Overview / Tunnels / Clients / Nodes / Alerts / Audit / Users & tenants / Security | Same — all pages complete |
| Session | Shared `wg_admin` HttpOnly cookie + `wg_admin_csrf` double submit | Same (no re-login when switching between the two) |
| Build output | `internal/webui/adminui/dist` | `internal/webui/adminnext/dist` |

Both are served through `webui.Compress(webui.SPAHandler(...))` with identical behavior: `/assets/*` cached one year immutable, `index.html` no-cache, unknown paths fall back to the SPA, and `/api/*` under the prefix returns 404 so that typos in endpoint paths never hide. Switching is a matter of changing the mount points and `/` redirect in `internal/server/admin.go`; retiring the old console means removing the `adminui` mount and the `web/admin` directory.

React-side trade-offs: charts use uplot (~40 KB — two lines refreshed every 2 s don't need ECharts' rendering overhead); server data goes through TanStack Query's `refetchInterval` (5 s, 2 s on the overview); forms use antd Form rather than react-hook-form to avoid pulling another dependency for three dialogs.

#### Visual and motion conventions (`/next/`)

One easing curve (`cubic-bezier(0.4, 0, 0.2, 1)`, defined both as `--ease` in `styles.css` and `EASE` in `motion.ts`) drives every custom animation; antd transitions are aligned via the `motionEaseInOut` token:

| Where | How | Files |
|---|---|---|
| Sidebar selection | A single absolutely-positioned block slides with `translateY`, including a 3 px brand indicator and glow; antd's own selection background is set transparent to avoid double highlighting | `layout/Shell.tsx`, `styles.css` |
| Page transitions | `PageTransition` remounts keyed by pathname; children float in over 380 ms with a 60 ms stagger | `components/PageTransition.tsx` |
| Status dots | Green dot breathes over 2.4 s; becomes a static grey dot when the tab is hidden | `components/LiveDot.tsx` |
| Numbers | KPIs ease from the previous value (never a replay from 0) | `motion.ts`, `components/AnimatedNumber.tsx` |
| Empty state | Light gradient area silhouette + a single slow pulse (an earlier version with wireframes, three ripples and particles was too noisy at card size, and was toned down); zero traffic also renders the empty state rather than a flat line pinned to the bottom | `components/EmptyTraffic.tsx` |
| Tables | Very faint row hover + 1 px lift + shadow; action icons scale 1.1× and take the brand color on hover | `styles.css` |
| Progress bars | 600 ms width transition; <70% blue, <88% orange, ≥88% red. Go heap is an exception: alloc/sys sit near 100% after GC by nature, so the red/orange thresholds would misfire — fixed purple | `usage.ts` |
| Tags | 10%-opacity background + text in the same hue, both themes | `components/SoftTag.tsx` |
| Dark surfaces | Canvas `#0a0a0a`, cards `rgba(255,255,255,.03)` + 1 px translucent border + `backdrop-filter` | `theme.tsx`, `styles.css` |
| Theme switch | 0.5 s class transition on `<html>`, covering portal layers (modals, dropdowns) | `theme.tsx` |
| Accessibility | Under `prefers-reduced-motion` all animations and counters collapse to instant | `motion.ts`, `styles.css` |

`web/scripts/verify-motion.mjs` reads each of these back via `getComputedStyle` and asserts them (21 checks) — precisely so that a CSS change silently overridden by antd's specificity doesn't go unnoticed. That is how it caught antd 6 renaming the progress-bar fill class from `.ant-progress-bg` to `.ant-progress-track`.

**The fpc console doubles as a graphical configuration center**: the "Config" page's "Connection" tab configures server address, token, TLS, heartbeat, and reconnect policy through forms, with strict validation, transactional writes, and automatic reconnection on save. The "Tunnels" page adds, edits, starts, and stops HTTP / TCP tunnels (subdomain / remote-port fields shown per type). The "YAML (advanced)" tab is reserved for startup-time parameters (listen addresses, log format — restart required). Day-to-day changes never touch the config file.

### 5.1 Control plane `:7000` (TLS)

| Method | Path | Auth | Description |
|---|---|---|---|
| GET (WS) | `/control` | `Bearer <token>` | Control channel: `hello` / `register` / `unregister` / `heartbeat` / `ping` / `pong` |
| GET (WS) | `/data` | `Bearer` + `X-Wormgate-Session` | Data channel; `websocket.NetConn` carries yamux, session ownership verified |
| GET | `/healthz` | none | `{"status":"ok","version":"0.1.0"}` |

### 5.2 Public ingress `:8080`

| Method | Path | Description |
|---|---|---|
| * | `/*` | Reverse proxy by `Host` subdomain to registered tunnels; unmatched hosts always get 404 (deny by default) |

### 5.3 Admin console `:7100` (all `/api/*` protected by `adminAuth` unless noted)

| Method | Path | Description |
|---|---|---|
| GET | `/api/meta` | Version / server time / oauth_enabled / roles (no auth) |
| POST | `/api/login` | Login, issues JWT + CSRF cookie (no auth; same-origin check and rate limiting) |
| GET | `/api/oauth/start`, `/api/oauth/callback` | OAuth2 authorization code + PKCE (404 when disabled) |
| POST | `/api/logout` | Logout |
| GET | `/api/me` | Current user + csrf |
| GET | `/api/overview` | Overview KPIs, top tunnels, nodes, alerts |
| GET | `/api/stats` | Global and per-tunnel rate series |
| GET | `/api/tunnels` | Tunnel list |
| DELETE | `/api/tunnels/{id}` | Revoke tunnel (viewers denied; non-admins only their own) |
| GET | `/api/clients` | Online client sessions |
| POST | `/api/clients/{id}/kick` | Kick offline |
| GET | `/api/nodes` | Node runtime info |
| GET | `/api/alerts` | Alert list |
| POST | `/api/alerts/{id}/ack` | Acknowledge alert (admin) |
| GET | `/api/audit?limit=100` | Audit events, max 1000 (admin/viewer) |
| GET | `/api/encryption` | Tunnel encryption status: enabled, passphrase, fingerprint, encrypted/plaintext counts (admin) |
| PUT | `/api/encryption` | `{"generate":true}` generate & enable · `{"secret":"..."}` custom · `{"disable":true}` disable (admin, audited) |
| GET (WS) | `/api/ws` | Live snapshot, pushed **every 2 s**; JWT cookie verified internally |
| GET/POST | `/api/users` | User list / create user (returns a one-time token, admin) |
| PATCH/DELETE | `/api/users/{id}` | Change display name / role / quota / disabled; delete (admin) |
| POST | `/api/users/{id}/rotate-token`, `/password` | Rotate token, set console password (admin) |
| GET | `/admin/` | SPA; `/` and `/admin` 302 redirect |

### 5.4 Client console `:7200` (all `/api/*` require a session unless noted)

| Method | Path | Description |
|---|---|---|
| POST | `/api/login` | Log in with the console token or an Argon2 password (no auth; rate limited) |
| GET | `/api/meta` | Version / client name / status / csrf cookie name / config_path (no auth) |
| GET | `/api/session` | Current session csrf + expires_at |
| POST | `/api/logout` | Logout |
| GET | `/api/status` | Runtime status snapshot |
| GET/POST | `/api/tunnels` | Tunnel list / create (written back to YAML; `encryption: true` enables stream encryption) |
| PUT/DELETE | `/api/tunnels/{name}` | Update / delete |
| POST | `/api/tunnels/{name}/start`, `/stop` | Start / stop |
| POST | `/api/reconnect` | Trigger reconnect |
| GET | `/api/connections` | Active connections |
| GET | `/api/stats` | Rate series |
| GET/PUT | `/api/config` | Read / save the full effective YAML (transactional) |
| GET/PUT | `/api/settings` | **Structured connection settings**: server address / token / shared passphrase (`stream_secret`) / client name / TLS / heartbeat / reconnect — deep-merged write-back touching only form-owned fields; changing the passphrase triggers an automatic reconnect |
| GET (SSE) | `/api/logs?level=` | Log stream: 200-entry replay, 128 buffer, 15 s heartbeat |
| GET (WS) | `/api/ws` | Live snapshot, every 2 s |
| GET | `/` | SPA |

### 5.5 Metrics `:9090`

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/metrics` | none | Prometheus + Go runtime metrics, OpenMetrics format |
| GET | `/healthz` | none | `{"status":"ok"}` |

Key metrics: `wormgate_control_sessions`, `wormgate_tunnels`, `wormgate_client_sessions_total`, `wormgate_public_connections_total{owner}`, `wormgate_traffic_bytes_total{owner,direction}`, `wormgate_http_responses_total{code}`, `wormgate_registration_failures_total`.

---

## 6. Security

### Implemented

- Control and data planes **enforce TLS 1.3**; optional mTLS (`client_ca_file` as a second factor beyond tokens)
- The data channel verifies `Bearer` + session existence + `session.UserID == principal.UserID`
- Client tokens stored as SHA-256 hashes; console passwords use **Argon2id** (m=65536, t=3, p=2) with constant-time comparison; JWT HS256 with strict issuer / expiry / algorithm-allowlist checks
- CSRF double-submit cookie + `SameSite=Strict` + same-origin validation; login rate-limited to 10 attempts/min per IP
- CSP (`default-src 'self'`), `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, API `no-store`
- Size limits everywhere: `controlReadLimit 64KB`, `dataReadLimit 8MB`, `maxStreamHeader 4096`, stream-header field length caps, `MaxBytesReader` on request bodies (8KB–1MB)
- Config and credential files are written atomically with `0o600`

### Risks and deployment notes

1. **Self-signed certificates by default.** `auto_self_signed: true` is for development; production must set `cert_file`/`key_file`, and clients should verify via `ca_file`.
2. **Never enable `insecure_skip_verify` in production.** The validator prints a warning for it; it amounts to disabling control-plane authentication.
3. **One-time credentials are logged in plaintext**: the initial admin password, demo token, and client console tokens. If logs are shipped to a central store, that equals credential leakage. In production pre-set `admin_password_hash` and restrict log access after first login.
4. **A demo user is created automatically by default**, with the token plaintext at `<data_dir>/demo-token`. Beyond `--admin-password`, production should disable/delete the demo user in the admin console and issue per-user tokens instead.
5. **`secure_cookie` defaults to false.** The admin console binds `0.0.0.0:7100` over plain HTTP by default; if you expose it behind an HTTPS reverse proxy, set it to true and put access control in front of the console.
6. **The public ingress itself has no authentication and is plain HTTP by default.** TLS termination, WAF, and access control belong in an external reverse proxy; requests hitting a registered subdomain are forwarded straight to your internal service.
7. **`/metrics` and `/healthz` are unauthenticated** and bind `0.0.0.0` by default; restrict them with cloud security groups / firewalls to collector IPs, or add authentication at the reverse proxy.
8. **`sameOrigin` lets requests without an `Origin` header through**; defense in depth relies on CSRF double-submit + `SameSite=Strict`.
9. **Rate limiting is an in-memory sliding window on a single host** — cleared on restart. Behind a proxy or with multiple instances, enforce limits at the outer layer.
10. **POSIX permissions don't apply on Windows**: `0o600` is best effort (reported as 0666 on NTFS); tighten directory ACLs manually.
11. **OAuth auto-provisioning** defaults new accounts to the `viewer` role, but `default_role` can be set to `admin` — only do this with a fully trusted IdP.
12. Plaintext `admin_password` and static `auth.tokens` in the config trigger validator warnings; prefer hashes / per-user tokens.

### Two frontend pitfalls (both fixed — don't reintroduce them)

- **Do not restyle the two Vue consoles with ant-design-vue**: an attempt turned `/admin/` and the client console into a flat dark antd skin (`common/antd.js` is still in the repository but unreferenced), then was fully reverted because the original teal/indigo glow + glass sidebar fits operations work better (`theme.css`, `components.css`, `AppShell.vue`, `TopBar.vue`, `admin/src/views/DashboardView.vue`). If antd ever comes back: `Menu.items[].label` does not accept functions (only `icon` is treated as a render function) — a `label` gets `String()`-ed into `()=>h("span",…)` and printed into the sidebar; pass a **VNode** and create it fresh on every render if you need a badge.
- **CSP is `script-src 'self'`**, so an inline theme script in `index.html` is silently blocked — symptom: a dark frame flashes before the light theme applies, plus a console error. Both Vue consoles therefore bootstrap the theme from `public/theme-init.js` (loaded synchronously in `<head>`, still before first paint). Same rule for any new frontend: no inline scripts in `index.html`.

---

## 7. Performance and observability

| Item | Value | Notes |
|---|---|---|
| Rate sampling | Every 1 s, ring buffer of **300 points (5 min)** | History beyond 5 minutes is not kept; switch the console to "5 min" to see the full window |
| WS snapshots | Every 2 s, last 120 points only | Balance between liveness and bandwidth |
| SSE logs | 200-entry replay, 128 buffer, 15 s heartbeat | Recent logs survive a reconnect |
| Log / alert / audit ring buffers | 2000 / 200 / 2000 entries | In-memory, cleared on restart |
| yamux | KeepAlive 30s, write timeout 15s, `MaxStreamWindowSize 4MB`, `AcceptBacklog 512` | Watch the stream window and backlog under high-concurrency long connections |
| `netpipe.Pipe` | 32KB buffer pool, `io.CopyBuffer`, 5 s grace after half-close | ~64KB memory per connection for large transfers |
| Reverse-proxy connection pool | `MaxIdleConns 256`, 32 per host, idle 90s, `FlushInterval 100ms` | Lower bound for SSE / streaming response flushing |
| Quotas | 20 tunnels per user / 200 connections per tunnel / daily traffic unlimited by default | Over limit returns 429 |
| Reconnect | Exponential backoff 1s → 30s with jitter; reset when a session lived >1 min | Automatic recovery after a server restart |
| Request limits | A single `register` ≤ 32 tunnels; auto-subdomain up to 12 tries, ports up to 4096 | Protection against registration storms |
| gzip | Only `.js/.css/.html/.json/.svg/.txt/.map` ≥1024B, `BestSpeed` + `sync.Pool` | **API / SSE are never compressed**, keeping live streams unbuffered |

Session GC runs every 10 minutes; config hot-reload debounces 300 ms; the control plane enforces layered timeouts (10s / 15s / 8s) on handshakes and dials.

Troubleshooting: `log.level=debug` shows rejection reasons such as `no tunnel for host`; `log.format=json` is easier to collect; `wormgate_http_responses_total{code="404"}` directly shows unmatched-Host volume.

---

## 8. Testing

```bash
go test ./...
go vet ./...
gofmt -l .
```

Currently 4 test files, 14 cases:

- `internal/proto/proto_test.go` (5): control-frame encode/decode round-trips, missing-type rejection, stream-header round-trip, bad magic, oversized Source rejection
- `internal/auth/store_test.go` (4): the first-start demo token authenticates (regression for "token printed but 401"), tokens survive restarts, file permissions (skipped on Windows), disabled-user tokens rejected
- `internal/server/disconnect_test.go` (1 table-driven set): which disconnects count as "normal" (admin kicks included), keeping alert noise down
- `internal/webui/spa_test.go` (5): the `/next/` mount contract — bare prefix 307, index points at `/next/assets/`, client routes fall back, `/next/api/*` always 404, outside-prefix 404

The frontend has a **behavioral** smoke script (assertions, not pixel comparison):

```bash
cd web
TX_PASS='<admin password>' node scripts/smoke-next.mjs   # targets http://127.0.0.1:17200 by default
```

It logs into `/next/` for real, walks all 8 pages, checks horizontal overflow at 4 viewport widths, then runs create → rename → delete on a disposable account. Any console error or 4xx (except the expected 401 from `/api/me`) fails the exit code. Screenshots land in `.run/next-smoke/`.

Motion and visual conventions have a separate assertion script (21 checks, including reduced-motion and theme transitions):

```bash
cd web
TX_PASS='<admin password>' node scripts/verify-motion.mjs
TX_BASE=http://your-server:7100 TX_PASS='...' node scripts/verify-motion.mjs   # can target production
```

**Not yet covered**: config loading/validation/env mapping, server routing and proxying, client handshake and reconnect, WebUI CSRF/SSE/WS, TLS, netpipe. There are no integration tests, benchmarks, or CI configuration yet. When touching those modules, walk through the end-to-end verification in section 2 manually.

---

## 9. Repository layout

```
cmd/fps/               server entry point (main -> cli.RunServerMain)
cmd/fpc/               client entry point (main -> cli.RunClientMain)
cmd/tunnelx/           legacy single-binary shim, not produced by release builds
internal/
  cli/                 shared flag definitions, config bootstrap, utility commands, help
  proto/               control-frame JSON envelope + 12B stream-header binary protocol
  config/              types, load precedence, validation, hot reload (fsnotify), structured settings writes
  server/              control plane, registry, public reverse proxy, metering/quota, admin API, metrics
  client/              agent, session, tunnel CRUD, local console API (incl. /api/settings), SSE/WS
  webui/               session, CSRF/rate limiting, SPA + gzip middleware; clientui/ adminui/ adminnext/ hold embed assets
  auth/                user store, Argon2id passwords, JWT, token hashing
  tlsutil/             TLS 1.3 / self-signed certs / mTLS
  netpipe/             bidirectional copy and half-close
  logx/ audit/ alerts/ idgen/ version/
  tools/e2eorigin/     static origin server for local E2E (test-only)
web/                   one npm package, two Vite projects + common/ shared design system
  next/                React 19 + Ant Design 6 canary admin (own package.json, mounted at /next/)
  scripts/shoot.mjs    console screenshots via playwright-core driving local Edge/Chrome
scripts/build.sh       cross-platform builds + artifact format verification
dist/                  ten platform artifacts (produced by build.sh)
docs/
  screenshots/         dark/light screenshots of both consoles
```

`internal/tools/e2eorigin` is a deliberately kept end-to-end helper; `.run/`, `bin/`, `data/`, and `dist/` are local runtime/build output and play no part in the source build.

---

## Ports and addresses at a glance

**The client machine needs no public IP and no router port forwarding**: fpc dials out to the fps control plane, and data flows back over yamux multiplexed inside the same TLS connection. Only the server machine needs inbound ports open.

| Listener | Default | Purpose | Public? |
|---|---|---|---|
| fps `--control-addr` | `0.0.0.0:7000` | fpc handshake + data channel (TLS 1.3) | **Must be open** — the only port that must be reachable |
| fps `--http-addr` | `0.0.0.0:8080` | Public ingress for HTTP tunnels (matched by Host) | **Must be open**; production usually moves to `:80` / `:443` |
| fps `--tcp-port-range` | `20000-30000` | Remote ports assigned to TCP tunnels | Open this range only if you use TCP tunnels |
| fps `--webui-addr` | `0.0.0.0:7100` | Admin console | Publicly reachable by default; **change the password right after first start**, and restrict source IPs or front it with an HTTPS proxy |
| fps `--metrics-addr` | `0.0.0.0:9090` | Prometheus | Publicly reachable by default; **unauthenticated** — allow only collector IPs |
| fpc `--webui-addr` | `127.0.0.1:7200` | Client's local console | **Never expose** (fpc runs on an intranet machine; keep loopback) |
| `--tunnel ...=http://<host:port>` | e.g. `127.0.0.1:8080` | The **internal service** being published — anything fpc can reach | No firewall changes at all; never needs to be publicly reachable |

The `host:port` in `--tunnel` means "a service fpc can connect to": `127.0.0.1:8080` for a local service, `192.168.1.20:8080` for another machine on the LAN, a service name inside a container network. It is **not** a public address.

HTTP tunnels route by the `Host` header: the subdomain equals the tunnel name (or `--tunnel name=http://...@subdomain`), and the base domain comes from fps's `--http-domain` (default `wormgate.local`). In production set `--http-domain example.com` to your domain and resolve subdomains to the fps server's **public IP**. With many tunnels a `*.example.com` wildcard is convenient, or add A records one by one as described below. Unmatched or multi-dot hosts always get 404 (deny by default). For local verification use `curl -H "Host: web.wormgate.local" http://127.0.0.1:8080/`.

### One A record, no wildcard

A wildcard record is not required. The match rule is `Host = <tunnel-name>.<base-domain>`, so make the **tunnel name equal the subdomain label you have already resolved**. Suppose your only record is `fd.5t.pw -> <fps public IP>`:

1. DNS: an A record `fd.5t.pw` pointing at the fps public IP. The base domain `5t.pw` itself need not point at the same machine, and `*.5t.pw` is unnecessary.
2. fps: `server.http.domain: 5t.pw` (or `--http-domain 5t.pw`).
3. fpc: name the tunnel `fd`, e.g. `--tunnel fd=http://127.0.0.1:8091`.
4. Visit `http://fd.5t.pw:8080/`. Each additional tunnel gets its own A record with the tunnel name matching the label, e.g. `api.5t.pw` → tunnel `api`.

DNS only maps names to IPs, **not ports**. Wildcard services like `nip.io` / `sslip.io` are intercepted as plain HTTP on some networks — the browser fails while `curl` on the server works; switch to a real domain (measured during deployment: `nip.io` 0/10, real subdomains 8/8).

### HTTPS with an existing Caddy

fps's HTTP ingress speaks **plain HTTP**; TLS must be terminated by a front reverse proxy. If 80/443 are already taken by Caddy, append a site block (don't touch existing ones):

```caddyfile
fd.5t.pw {
    encode zstd gzip
    reverse_proxy 172.17.0.1:8080 {
        flush_interval -1
    }
}
```

Back up the `Caddyfile` before editing; validate with `caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`, then hot-reload with `caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile` (no downtime). Caddy obtains and renews Let's Encrypt certificates automatically, provided 80/443 are publicly reachable and the domain resolves to the machine.

⚠️ **Container gotcha**: when Caddy runs in Docker, the proxy target cannot be `127.0.0.1:8080` (that is the container itself) — use the host's address on the Docker bridge, usually `172.17.0.1` (confirm with `docker network inspect bridge`). Caddy's `reverse_proxy` preserves the original `Host`, so fps's subdomain matching keeps working.

Finally set `server.http.scheme: https` and `server.http.public_port: 443` on fps (these only affect the public URLs shown in the consoles and fpc logs; the listener stays on 8080), and registrations will read `https://fd.5t.pw`. If you'd rather not touch the existing Caddy, a Cloudflare-style edge proxy terminating TLS and forwarding to `:8080` works too.

## Three kinds of credentials — don't mix them up

| Credential | Used for | Where it comes from |
|---|---|---|
| **Local console password** | Opening the fpc console at `http://127.0.0.1:7200/` | Printed once at fpc first start, stored at `data/client-console-password` (0600); customize with `--webui-password` / `WORMGATE_CLIENT_WEBUI_PASSWORD`; changeable on the Security page |
| **Server token `wg_...`** | Entered on the client console's Config page, used by fpc to connect to fps | Printed at fps first start, or issued / rotated per user in the admin console |
| **Admin password** | Opening the fps admin console at `http://<server-ip>:7100/admin/` | Printed once on the first start with a fresh data dir; changeable on the Security page; admins can reset other users' passwords on the Users page |

fpc **is allowed to start without a `wg_` token**: the local console still opens and the agent shows "awaiting configuration" (it will not hammer the control plane with 401s). Enter the token on the Config page, save, and it hot-loads and connects. If neither the current nor the user directory has a config file, the first save generates `wormgate.yaml` in the working directory (containing the plaintext token, permissions 0600 — keep it out of any repository). Do not use the `wg_` token to log into the local console — the login page says so explicitly.

### Changing passwords from the UI

Both consoles have a "**Security**" page. It changes **only the password for the web UI**, fully independent of the `wg_` token: changing the password never rotates the token, and rotating the token never changes the password.

| | Client console `:7200` | Admin console `:7100/admin` |
|---|---|---|
| Entry | Security → Change login password | Security → Change login password (any role, own account) |
| Validation | Current + new (≥8 chars) + confirm | Current + new (≥8 chars) + confirm |
| Endpoint | `POST /api/password` | `POST /api/me/password` |
| Storage | `client.webui.password_hash` in the config file (argon2id, file 0600) | `password_hash` in `<data_dir>/users.json` (argon2id) |
| After restart | Still valid | Still valid |
| Other sessions | All logged out except the current browser | All old JWTs invalidated; the current browser gets a fresh session |

After a client-console password change, **the config file's `password_hash` becomes the only credential**: the `--webui-password` startup flag and the old value in `data/client-console-password` stop taking effect (the latter is best-effort synced to the new value for human inspection). This prevents a hardcoded `--webui-password` in a startup script from resurrecting the old password on restart. To go back to startup-flag passwords, delete `client.webui.password_hash` from the config file. Startup logs no longer print the old password — only a note that the password is managed from the Security page.

Admins can additionally **reset any account's login password** from the "Users & tenants" page via the shield icon (`POST /api/users/{id}/password`, admin-only, no old password required); a reset immediately invalidates that account's active sessions. OAuth-only accounts have no local password until an admin sets one this way, after which the Security page works for self-service changes.

Both flows write to the audit log (admin console) or process log (client console); passwords are never printed or echoed back.

## Windows double-click launchers

The two launchers in the repository root can be double-clicked; the log window stays open on failure (no flash-and-vanish) and the browser opens automatically once the console is ready:

- `start-fps.bat` — starts the server with zero configuration
- `start-fpc.bat` — three editable lines at the top (`FPC_SERVER` / `FPC_TOKEN` / `FPC_TUNNEL`); double-click to connect; it also starts with an empty token, which you can fill in later on the Config page

Double-clicking `fps.exe` / `fpc.exe` directly also works: the program detects this launch mode, waits for a keypress on failure, and opens the matching console on success. On Linux / macOS (systemd / launchd / terminal) this behavior never triggers. Set `WORMGATE_OPEN_BROWSER=1` to force automatic browser opening from scripts.

## Interface language

All three consoles (Vue client console, Vue admin console, React admin console `/next/`) are bilingual, **defaulting to Chinese**. The `中 / EN` button in the top bar (bottom of the login page) switches the language; the choice is stored in `localStorage` under `wormgate.lang` (one shared key across all three; values written to the pre-rename `tunnelx.lang` key are migrated automatically) and is never reset to Chinese based on the browser locale. After switching, static text, form hints, toasts, API errors, and SSE log lines all follow the active language. The dictionary lives in `web/common/locales.js`; adding a string means one entry of "original Chinese → English".

Translation happens at the **DOM layer** (`web/common/i18n.js` for Vue, `web/next/src/i18n.tsx` for React) — no `t()` calls required in templates. That imposes two rules:

- **A sentence must be one text node.** `节点运行 {x} · {n} 次累计连接` gets split by React into 4 text nodes, which sentence-level rules can never match — you end up replacing word by word. Write `` {`节点运行 ${x} · ${n} 次累计连接`} `` to reach `ZH_PATTERNS`.
- **Mark untranslatable text with `data-i18n-skip`** (covers its own text subtree — the "中" on the language button depends on it). In English mode, unmatched Chinese fragments are removed; a half-Chinese half-English mix looks worse than leaving text alone.
- **Space between CJK and Latin depends on the language.** `需要旧版界面？<a>切回 Vue 控制台</a>` needs no space in Chinese but becomes `Need the legacy UI?Open the Vue console` in English. That glue can only be fixed with `{lang === 'en' ? ' ' : null}` — no dictionary can help; `i18n-check.mjs` asserts `/[?!][A-Za-z][a-z]{2,}/` precisely to catch it.

`web/scripts/i18n-check.mjs` turns these into assertions: both login page and top bar have a switcher, English mode leaves no Chinese residue, no glued sentences, the top bar shows the account name exactly once. Run it after any frontend change:

```bash
TX_BASE=http://127.0.0.1:17200 TX_PASS='<admin password>' node web/scripts/i18n-check.mjs
```

The two login-page assertions need no account and can be verified against production (SSE keeps `networkidle` from ever firing, so the script uses `domcontentloaded`):

```bash
node web/scripts/prod-login-check.mjs                                  # defaults to the deployed host; override with TX_BASE
TX_BASE=http://127.0.0.1:17200 node web/scripts/prod-login-check.mjs
```
