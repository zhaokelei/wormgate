# Wormgate

Wormgate 是一个用 Go 写的反向隧道工具：机器没有公网 IP、防火墙开不了洞，也能把内网的 HTTP 服务和 TCP 端口发布到公网入口上。它和 frp 解决同一个问题——`fps` 对应 `frps`，`fpc` 对应 `frpc`——区别在起步体验：服务端裸跑就能用，首次启动直接在日志里打印一条可以复制粘贴的客户端命令。

| 二进制 | 角色 | 一句话启动 |
|---|---|---|
| **`fps`** | 服务端：控制面 + 公网入口 + 管理台 + metrics | `fps` |
| **`fpc`** | 客户端：发布本地服务 + 本地图形化配置台 | `fpc --server x:7000 --token wg_... --tunnel web=http://127.0.0.1:8080` |

## 特性

**发布服务**

- HTTP 子域名路由：一个域名做泛解析，每条隧道分到 `<sub>.<domain>`；Host 不匹配一律 404 拒绝，不做静默转发。伪造的 `X-Forwarded-*` 会在代理前被剥离并重写为可信值
- TCP 端口映射：服务端自动分配端口，配置里也可以指定
- 可选的隧道流加密：TLS 之外还能再套一层逐流加密；服务端不配密钥则该功能整体不可用，配了也按隧道逐条 opt-in（`#enc`），密钥不匹配直接拒绝、不降级
- 多用户与配额：token 按 user 归属，公共入口按 owner 计量、限速、限额

**少折腾**

- 零配置起步：`fps` 不带任何参数就能跑；`fpc` 一行命令连上，不需要先写配置文件
- 一份 YAML 两端共用：`server:` / `client:` 各读各的段落；改动 300ms 内自动热重载，已建立的隧道不断线
- 图形控制台内置：客户端台 `:7200`、管理台 `:7100`，都是编进二进制的界面，中文/英文可切换，装好即有、无需另外部署

**好运维**

- 单二进制静态交付：前端经 `go:embed` 编入，`CGO_ENABLED=0` 交叉编译，Linux / macOS / Windows × amd64 / arm64 共 10 个产物，目标机器不需要 Node 或任何运行时
- 控制面与数据面分离：5 个独立监听（控制 / 入口 / 管理 / metrics / 客户端台），故障域互不牵连
- Prometheus metrics：`:9090/metrics`，连接数、流量、隧道状态全有指标
- 凭据只存哈希：管理员密码 Argon2id、token SHA-256，明文只在首次启动的日志里出现一次

依赖面很小：Go 1.26+，8 个直接依赖（websocket / yamux / zap / jwt / yaml / prometheus / fsnotify / x-crypto）；两个 Vue 控制台零 UI 组件库，自写设计系统。

## 和 frp 比有什么不同

frp 的功能面更宽（UDP、STCP/XTCP、客户端插件都有），Wormgate 目前覆盖最常用的 HTTP + TCP 场景，把力气花在了起步和日常使用上。逐项对照与路线图见 [docs/frp-parity.md](docs/frp-parity.md)。

| | frp | Wormgate |
|---|---|---|
| 首次连接 | 两端各写一份配置才能通 | 服务端裸跑，日志给出可直接粘贴的客户端命令 |
| 配置文件 | `frps.ini` + `frpc.ini` 两份 | 可以零配置；要写时一份 YAML 两端共用 |
| 图形界面 | 服务端 dashboard 需在配置里开启 | 两端各有，默认开启，无需任何配置 |
| 认证 | 全局单一 token | 多用户 token，按 user 配额与计量 |
| 加密 | 传输 TLS | TLS 之外再加逐隧道可选流加密 |
| 代理类型 | HTTP / HTTPS / TCP / UDP / STCP / XTCP / 插件 | HTTP / TCP（其余在路线图） |

---

## 1. 架构

五个独立的 HTTP 监听，控制面与数据面完全分离：

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

**HTTP 隧道数据流**

```
browser -> public ingress -> matchHost(host, domain) -> registry.BySubdomain
        -> [no match] http.NotFound 404
        -> ReverseProxy -> acquireSlot(quota) -> ClientSession.OpenStream()
        -> 12B StreamHeader{tunnel,conn_id,source,kind} -> WS /data (binary)
        -> yamux -> client acceptStreams() -> net.Dial(local)
        -> netpipe.Pipe(local, meteredStream) -> response back along the same path
```

**握手**：客户端先拨 `wss://host:7000/control`，带 `Authorization: Bearer <token>`，**必须在 10 秒内发出首帧 `hello`**（否则服务端关闭连接）；服务端回 `hello_ok`（含 `session_id`、心跳周期）；客户端再用 `X-Wormgate-Session` 头拨 `wss://host:7000/data`，`websocket.NetConn(..., MessageBinary)` 承载 yamux；随后 `register` / `tunnel_result` 完成注册。心跳默认 15s，会话超时 45s。

**路由默认是拒绝的**：`matchHost` 要求 Host 严格为 `<sub>.<domain>`、sub 是单层合法 DNS label、且 registry 中存在该 sub，否则一律 404。伪造的 `X-Forwarded-*`、`X-Real-IP`、`X-Wormgate-Tunnel` 会在代理前被剥离并重新注入可信值。

---

## 2. 快速开始

```bash
# 1) 编译两个二进制（先前端后 Go，顺序不能反，见「构建」）
cd web && npm ci && npm run build && cd ..
go build -o bin/fps ./cmd/fps
go build -o bin/fpc ./cmd/fpc
# Windows 下文件名加 .exe；跨平台一键出全部产物用 bash scripts/build.sh

# 2) 起一个本地源站（任意 HTTP 服务即可）
#    例：python -m http.server 8090

# 3) 全新数据目录启动 fps —— 首次启动打印一次性管理员密码、demo token，
#    以及一条可以直接复制粘贴到客户端的 fpc 命令
./bin/fps --data-dir ./data-server --http-addr 127.0.0.1:8088
#   warn  generated initial admin password - store it now {"username":"admin","password":"...","note":"..."}
#   info  demo client token (user: demo) {"token":"wg_...","reprinted":false,...}
#   info  quick start - run this on the client machine {"command":"fpc --server ... --token ... --tunnel ..."}

# 4) fpc 一行命令连上，不需要任何配置文件
./bin/fpc --server 127.0.0.1:7000 --token wg_... --insecure \
    --tunnel web=http://127.0.0.1:8090 \
    --tunnel api=http://127.0.0.1:8090
#   info  client console is ready {"url":"http://127.0.0.1:7200/","token":"wx_...","note":"..."}
#   info  tunnel is live {"tunnel":"web","public":"http://web.wormgate.local:8088"}
```

也可以 `./bin/fpc init --role client --path fpc.yaml --token wg_... --local 127.0.0.1:8090` 先生成配置文件，再 `./bin/fpc --config fpc.yaml`。**`fps` 和 `fpc` 可以共用同一份 YAML**：各自只读 `server:` / `client:` 一段。

**验证**（本机没有 DNS 时用 `Host` 头，不必改 hosts 文件）：

```bash
curl -i -H "Host: web.wormgate.local" http://127.0.0.1:8088/
curl -i -H "Host: api.wormgate.local" http://127.0.0.1:8088/
curl -i -H "Host: nope.wormgate.local" http://127.0.0.1:8088/   # 404，默认拒绝
```

控制台：客户端台 `http://127.0.0.1:7200/`（用日志里的 `wx_...` 登录），管理台 `http://<服务器IP>:7100/admin/`（默认 `0.0.0.0:7100`，公网可达；用一次性 `admin` 密码登录，**登录后请立即改密**）。

> 首次启动生成的凭据只在日志里打印一次，服务端只存 Argon2id 哈希 / SHA-256 token 哈希。换了 `--data-dir` 会重新生成；重启同一目录时 demo token 会以 `reprinted: true` 重印，但管理员密码不会再打印。**注意**：如果你通过 fpc 控制台的表单填写并保存了令牌，它会按你的意愿写入配置文件（`config show` 里的 `config_path`），请妥善保护该文件的权限。

---

## 3. 构建

`web/` 是**一个 npm 包、两个 Vite 工程**，产物直接写进 Go 的 embed 目录；`web/next/` 是独立的第三个前端（React，自带依赖）：

| 工程 | 技术栈 | 配置 | base | 输出目录 |
|---|---|---|---|---|
| 客户端台 | Vue 3 + 自绘 CSS 设计系统 | `vite.client.config.js` | `/` | `internal/webui/clientui/dist` |
| 管理台 | Vue 3 + 自绘 CSS 设计系统 | `vite.admin.config.js` | `/admin/` | `internal/webui/adminui/dist` |
| 管理台（灰） | React 19 + Ant Design 6 | `next/vite.config.ts` | `/next/` | `internal/webui/adminnext/dist` |

```bash
cd web
npm ci                       # 或 npm install
npm run build                # = build:client && build:admin && build:next
cd ..
go build -o bin/fps ./cmd/fps
go build -o bin/fpc ./cmd/fpc
go vet ./... && go test ./...
```

`web/next` 有自己的 `package.json` / `node_modules`（React 19、antd 6、uplot、TanStack Query 与 Vue 控制台的依赖互不干扰），首次构建前先 `npm --prefix next install`。

`//go:embed all:dist` 在**编译期**读取 dist，所以只改前端却不重跑 Vite、或只重跑 Vite 却不重编 Go，线上都不会生效。开发时可用 `npm run dev:client` / `dev:admin`（Vite 已配置 `/api` 代理到 7200 / 7100）。国内网络若 `proxy.golang.org` 不可达，设 `GOPROXY=https://goproxy.cn,direct`。

`cmd/fps` 与 `cmd/fpc` 是两个独立的 `main`，共享 `internal/cli` 里的 flag 定义、配置引导和工具命令。`cmd/tunnelx` 是保留给旧脚本的薄兼容层（`wormgate server|client ...`），发布构建**不再产出它**。

### 3.1 跨平台构建

```bash
bash scripts/build.sh              # 前端 + 全部平台
SKIP_WEB=1 bash scripts/build.sh   # 复用已构建的 web 产物
OUT=release bash scripts/build.sh  # 自定义输出目录
```

脚本用 `CGO_ENABLED=0` 交叉编译，产出 10 个静态二进制到 `dist/`：

| 平台 | GOARCH | 产物 | 格式 |
|---|---|---|---|
| Linux x86_64 | amd64 | `fps-linux-amd64` / `fpc-linux-amd64` | ELF |
| Linux ARM64 | arm64 | `fps-linux-arm64` / `fpc-linux-arm64` | ELF |
| macOS Intel | amd64 | `fps-darwin-amd64` / `fpc-darwin-amd64` | Mach-O |
| macOS Apple Silicon | arm64 | `fps-darwin-arm64` / `fpc-darwin-arm64` | Mach-O |
| Windows x86_64 | amd64 | `fps-windows-amd64.exe` / `fpc-windows-amd64.exe` | PE |

脚本末尾会读取每个产物的 4 字节魔数校验 ELF / Mach-O / PE 格式。给 macOS / Linux 产物加执行权限后即可直接运行：`chmod +x dist/fps-linux-amd64 && ./dist/fps-linux-amd64`。所有平台共用同一份代码与同一套 embed 前端，不需要目标机器安装 Node 或任何运行时。

---

## 4. 配置

### 4.1 三层优先级

```
CLI flags  >  环境变量  >  profile 覆盖层  >  项目配置文件  >  用户级全局配置  >  内置默认值
```

- 项目文件：`--config` → `WORMGATE_CONFIG` → 当前目录的 `wormgate.yaml` / `wormgate.yml`
- 全局配置：`WORMGATE_GLOBAL_CONFIG`，否则 `os.UserConfigDir()/wormgate/config.yaml（旧目录 tunnelx/ 自动回退读取）`；`WORMGATE_NO_GLOBAL=true` 可跳过
- profile：`--profile` → `WORMGATE_PROFILE` → 文件内 `profile` 键，从 `profiles` map 取同名覆盖层
- 合并是**逐 key 深合并**，YAML 只需写差异字段；`KnownFields(true)` 严格解码，未知字段会报错并给出带行号的诊断
- 配置文件变更后 300ms 防抖自动热重载，重载失败保留旧配置；已建立的隧道不会断开。但监听地址、TLS、data-dir、管理员凭据的变更需要重启

两个二进制都内置同一组工具命令：`fps|fpc config show|validate` 查看生效配置 / 只校验不启动，`fps|fpc password-hash 'your-password'` 生成 Argon2id 哈希，`fps|fpc init` 生成配置文件，`fps|fpc version` 打印版本与构建平台。**不带子命令即启动对应角色**，help 用 `fps --help` / `fpc --help`。

### 4.2 YAML（完整字段参考）

```yaml
# 顶层合法键：profile / log / server / client / profiles
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
    client_ca_file: ""     # 配置后启用 mTLS（RequireAndVerifyClientCert）
    auto_self_signed: true # 与 cert_file/key_file 二选一
  http:
    addr: "0.0.0.0:8080"
    domain: wormgate.local  # 小写 DNS 名，不能是 IP
    scheme: http           # http|https（公网入口自身的 scheme）
    public_port: 0         # 0 = 自动推导
    read_header_timeout: 10s
    idle_timeout: 90s
  tcp:
    bind_addr: "0.0.0.0"
    advertise_host: ""
    port_range: "20000-30000"
  # 可选的隧道流加密密钥。留空 = 关闭；填了之后，请求 encryption 的隧道
  # 才会加密，其余隧道照常（不强制）。必须与客户端配置一致。
  stream_secret: ""
  webui:
    enabled: true
    addr: "0.0.0.0:7100"
    admin_user: admin
    admin_password: ""          # 仅引导用；优先 admin_password_hash
    admin_password_hash: ""     # 必须 $argon2id$ 开头
    session_ttl: 12h
    secure_cookie: false        # 走 HTTPS 时置 true
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
    tokens: []                 # 额外静态客户端 token，逗号分隔
  quota:
    max_tunnels_per_user: 20
    max_connections_per_tunnel: 200
    daily_traffic_bytes: 0     # 0 = 不限
  data_dir: data

client:
  server: "127.0.0.1:7000"     # host:port，不带 scheme；TLS 始终开启
  token: ""                    # client 角色必填
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
      subdomain: ""            # 留空由服务端自动分配
      remote_port: 0           # tcp 隧道必填，1-65535
      encryption: false        # true = 该隧道启用流加密（需 stream_secret）
      enabled: true            # 省略即 true
  # 必须与服务端 server.stream_secret 完全一致；只被 encryption 隧道使用
  stream_secret: ""
  webui:
    enabled: true
    addr: "127.0.0.1:7200"    # 保持 loopback
    token: ""
    password_hash: ""
    data_dir: data
    session_ttl: 12h

profiles:                      # 覆盖层，不是 Config 的 struct 字段
  dev:
    log: { level: debug }
  prod:
    log: { level: info }
```

### 4.3 环境变量

任何字段都能用 `WORMGATE_<PATH>` 覆盖：按 YAML tag 逐级拼大写下划线，例如 `server.http.domain` → `WORMGATE_SERVER_HTTP_DOMAIN`。另保留四个专用变量。

```bash
export WORMGATE_CONFIG=/etc/wormgate/wormgate.yaml   # 项目配置文件
export WORMGATE_PROFILE=prod                       # profile 覆盖层
export WORMGATE_GLOBAL_CONFIG=/etc/wormgate/global.yaml
export WORMGATE_NO_GLOBAL=true                     # 忽略用户级全局配置

export WORMGATE_SERVER_HTTP_DOMAIN=example.com
export WORMGATE_SERVER_WEBUI_SECURE_COOKIE=true
export WORMGATE_CLIENT_TOKEN=wg_xxxxxxxxxxxxxxxx
export WORMGATE_CLIENT_TLS_INSECURE_SKIP_VERIFY=false
```

未匹配到任何字段的 `WORMGATE_*` 变量会打印 warning，避免拼写错误被静默忽略。

### 4.4 CLI

`fps` 与 `fpc` 默认就运行各自的角色，**不需要子命令**。两者共享 `config show|validate`、`init`、`password-hash`、`version` 工具命令（放在首位即可，如 `fpc config validate --config fpc.yaml`）。为兼容旧脚本，`fps server ...` / `fpc client ...` 的写法也能识别。

```bash
# fps：服务端常用 flag
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

# fpc：--tunnel 可重复，格式 name=type://local[@subdomain|@port][#enc]
./bin/fpc \
  --server example.com:7000 --token wg_xxx --name laptop-web \
  --tunnel web=http://127.0.0.1:8080@web \
  --tunnel ssh=tcp://127.0.0.1:22@2222#enc \
  --stream-secret 'a-long-random-shared-value' \
  --ca-file /etc/wormgate/ca.pem \
  --webui-addr 127.0.0.1:7200 --webui-token wx_xxx

# 生成配置文件（0o600，目录 0o700）
./bin/fpc init --role both --path ./wormgate.yaml --yes
```

注意：`--tunnel` 会**整体替换** YAML 里的隧道列表；只有被显式设置的 flag 才覆盖配置，未设置的沿用 YAML/env/默认值。完整的 flag 清单随时可用 `fps --help` / `fpc --help` 查看。

### 4.5 可选的隧道流加密

隧道数据本来就跑在 TLS 1.3 之上，但如果客户端用 `--insecure`（或在自签证书下跳过校验），TLS 只能保证「没人偷看」，保证不了「对端是谁」——中间人可以拿自己的证书冒充服务端并解密流量。流加密补的就是这个缺口：它在 TLS 之内再套一层 AES-256-GCM，密钥只由你配置的共享口令派生，**不依赖任何证书**。

**默认关闭，按隧道开启**，不影响任何现有隧道。两个 WebUI 都能配，不必碰 YAML：

- **服务端**：「安全 → 隧道加密」——显示启用状态、口令指纹（SHA-256 前 16 位）、当前口令（可显示/复制）、已加密/明文隧道条数；按钮为「生成并启用」「保存口令」「关闭加密」「重新生成」。写入是**行级编辑**，`wormgate.yaml` 里的注释与空行原样保留。
- **客户端**：「配置 → 隧道加密（可选）」——口令输入框（显示/隐藏/复制/生成随机口令）+ 与服务端一致的指纹，便于肉眼核对两端是否匹配；「隧道」页的新建/编辑弹窗底部有「隧道内层加密 · AES-256-GCM」开关，列表的「类型」列对已加密隧道显示绿色锁形徽标。

命令行等价写法：

```bash
# 服务端：填一个长随机串（不填则加密功能不可用，但其余一切照旧）
#   server.stream_secret: "a-long-random-shared-value"

# 客户端：同样的值，并在需要加密的隧道后加 #enc
fpc --server example.com:7001 --token wg_xxx \
    --stream-secret 'a-long-random-shared-value' \
    --tunnel 'ssh=tcp://127.0.0.1:22@2222#enc' \
    --tunnel 'web=http://127.0.0.1:8080'      # 这条不加密，照常走 TLS
```

行为约定：

| 情况 | 结果 |
|---|---|
| 两端 `stream_secret` 一致 + 隧道开了加密 | 数据层加密，密钥按 `session + 隧道名` 派生，各隧道互不复用 |
| 两端密钥不一致 | 数据面认证失败，请求返回 502，**不会退化成明文** |
| 服务端没配 `stream_secret` | 注册直接被拒：`stream encryption requested but this server has no stream_secret configured` |
| 客户端没配 `stream_secret` 却勾了加密 | 保存被拒，控制台直接提示去「配置 → 隧道加密」设置；后端另有独立校验 |
| 隧道没开加密 | 完全不变，只受 TLS 保护（默认行为） |

改口令是**立即生效**的：服务端下一次握手就用新值；客户端检测到 `stream_secret` 变化会自动重连（密钥由 `session + 口令` 派生，换口令必须换会话），所以两端都更新后连接自行恢复。只改一边的窗口期内，加密隧道的数据流认证失败——这是刻意的「失败关闭」。

实现落在 `internal/streamcipher`：流头部（隧道名、conn id）保持明文用于路由，其后的载荷按帧加密，每帧 4 字节长度前缀 + 密文，方向子密钥分离，底层半关闭（`CloseWrite`）原样透传。测试覆盖往返、跨帧读取、错误密钥、篡改标签与密钥隔离。服务端口令写入由 `config.Manager.SetServerScalar` 负责，只改动 `server:` 段下的单个标量键，其余字节不动（见 `internal/config/manager_scalar_test.go`）。

> **部署注意**：systemd 单元用了 `ProtectSystem=strict`，服务进程看到的是**只读视图**，只有 `ReadWritePaths` 里的路径可写。若从管理台保存口令时报
> `write /opt/wormgate/etc/wormgate.yaml: open ...: read-only file system`，不是权限或磁盘问题，而是该目录不在白名单里。加一个 drop-in 即可（无需改主单元）：
>
> ```ini
> # /etc/systemd/system/wormgate-fps.service.d/20-etc-rw.conf
> [Service]
> ReadWritePaths=/opt/wormgate/etc
> ```
>
> 然后 `systemctl daemon-reload && systemctl restart wormgate-fps`。仓库里的 `.run/deploy_fps.py` 已把该路径写进单元模板，并会在重写配置时**保留已有的 `stream_secret`**，避免重新部署把口令清空。

---

## 5. 控制台与 API

两个控制台都基于 Vue 3 + 自写 CSS 设计系统（深/浅双主题、首屏无闪白），带 CSRF 双提交、401 统一跳转、WS/SSE 断线重连（带抖动）。静态资源走 Go 侧 gzip 中间件（图表包 481 KB → 190 KB）；gzip 只包静态资源，**不包 API mux**，以免缓冲 SSE。

### 5.0 灰度的 React 管理台 `/next/`

`/admin/`（Vue）之外还挂了一份**同 API、同 cookie** 的 React 管理台，用于逐页验收后再决定是否整体切换：

| | `/admin/` | `/next/` |
|---|---|---|
| 技术栈 | Vue 3 + 自绘 CSS 设计系统（`web/common/theme.css`） | React 19 + Ant Design 6 + uplot + TanStack Query |
| 页面 | 总览/隧道/客户端/节点/告警/审计/用户与租户/安全 | 同上，已全部完成 |
| 登录态 | 共用 `wg_admin` HttpOnly cookie + `wg_admin_csrf` 双提交 | 同左（互不影响，切页面不用重新登录） |
| 构建产物 | `internal/webui/adminui/dist` | `internal/webui/adminnext/dist` |

两侧都通过 `webui.Compress(webui.SPAHandler(...))` 提供，行为一致：`/assets/*` 一年 immutable、`index.html` no-cache、未知路径回落到 SPA、`/api/*` 一律 404 以免掩盖接口拼错。切换只需改 `internal/server/admin.go` 里的挂载点与 `/` 重定向；下线旧台时删掉 `adminui` 的挂载和 `web/admin` 即可。

React 侧的取舍：图表用 uplot（约 40 KB，2 秒一帧的两条曲线不需要 ECharts 的绘制开销），服务端数据走 TanStack Query 的 `refetchInterval`（5 秒；总览 2 秒），表单用 antd Form 而不是 react-hook-form，避免为了三个弹窗多引一个依赖。

#### 视觉与动效规范（`/next/`）

一套缓动曲线（`cubic-bezier(0.4, 0, 0.2, 1)`，同时在 `styles.css` 的 `--ease` 与 `motion.ts` 的 `EASE` 里定义）贯穿所有自定义动效，antd 组件的过渡也通过 `motionEaseInOut` token 对齐：

| 位置 | 做法 | 落地文件 |
|---|---|---|
| 侧栏选中态 | 单个绝对定位块用 `translateY` 滑动，含 3px 品牌色指示条与光晕；antd 自带的选中底色置为透明，避免双重高亮 | `layout/Shell.tsx`、`styles.css` |
| 页面切换 | `PageTransition` 以 pathname 作 key 重挂载，子元素 380ms 上浮淡入、60ms 递进 | `components/PageTransition.tsx` |
| 状态点 | 绿点 2.4s 呼吸；标签页隐藏时转为静态灰点 | `components/LiveDot.tsx` |
| 数字 | KPI 从当前值缓动到新值（不是每次从 0 重播） | `motion.ts`、`components/AnimatedNumber.tsx` |
| 空状态 | 浅渐变的面积剪影 + 单点慢脉冲（早期版本是线框图 + 三圈涟漪 + 粒子，卡片尺寸下太吵，已收敛）；零流量也走空状态而不是贴着底部的直线 | `components/EmptyTraffic.tsx` |
| 表格 | 行悬停极浅底色 + 上浮 1px + 阴影；操作图标悬停放大 1.1 倍并转品牌色 | `styles.css` |
| 进度条 | 宽度 600ms 过渡；<70% 蓝、<88% 橙、≥88% 红。Go 堆是例外：alloc/sys 在 GC 后天然接近 100%，红橙阈值会造成误报，固定用紫色 | `usage.ts` |
| 标签 | 10% 透明度底色 + 同色系文字，深浅两套配色 | `components/SoftTag.tsx` |
| 深色质感 | 画布 `#0a0a0a`，卡片 `rgba(255,255,255,.03)` + 1px 半透明描边 + `backdrop-filter` | `theme.tsx`、`styles.css` |
| 主题切换 | `<html>` 上加 0.5s 类名过渡，覆盖 portal 层（弹窗/下拉） | `theme.tsx` |
| 无障碍 | `prefers-reduced-motion` 下动画与计数全部退化为瞬时 | `motion.ts`、`styles.css` |

`web/scripts/verify-motion.mjs` 把这些逐条读回 `getComputedStyle` 断言（21 项），避免改了 CSS 却被 antd 的优先级盖掉而无人察觉 —— 它就是这样发现 antd 6 把进度条填充的类名从 `.ant-progress-bg` 改成了 `.ant-progress-track`。



**fpc 的本地控制台就是图形化配置中心**：「配置」页的「连接设置」标签用表单配置服务端地址、令牌、TLS、心跳与重连策略，保存后严格校验 + 事务写入 + 自动重连；「隧道」页用表单增删改启停 HTTP / TCP 隧道（按类型显示子域名 / 远程端口字段）；「YAML 高级」标签保留给启动期参数（监听地址、日志格式等，需重启）。这是与 frp「先写 frpc.ini 再启动」最大的体验差异。

### 5.1 控制面 `:7000`（TLS）

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET (WS) | `/control` | `Bearer <token>` | 控制通道：`hello` / `register` / `unregister` / `heartbeat` / `ping` / `pong` |
| GET (WS) | `/data` | `Bearer` + `X-Wormgate-Session` | 数据通道，`websocket.NetConn` 承载 yamux，校验 session 归属 |
| GET | `/healthz` | 无 | `{"status":"ok","version":"0.1.0"}` |

### 5.2 公共入口 `:8080`

| 方法 | 路径 | 说明 |
|---|---|---|
| * | `/*` | 按 Host 子域反代到已注册隧道；未匹配一律 404（默认拒绝） |

### 5.3 管理台 `:7100`（`/api/*` 均受 `adminAuth` 保护，除非标注）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/meta` | 版本/服务器时间/oauth_enabled/roles（无认证） |
| POST | `/api/login` | 登录，签发 JWT + CSRF cookie（无认证，有同源校验与限流） |
| GET | `/api/oauth/start`、`/api/oauth/callback` | OAuth2 授权码 + PKCE（未启用时 404） |
| POST | `/api/logout` | 注销 |
| GET | `/api/me` | 当前用户 + csrf |
| GET | `/api/overview` | 概览 KPI、Top 隧道、节点、告警 |
| GET | `/api/stats` | 全局与逐隧道速率序列 |
| GET | `/api/tunnels` | 隧道列表 |
| DELETE | `/api/tunnels/{id}` | 撤销隧道（viewer 被拒，非 admin 只能撤自己的） |
| GET | `/api/clients` | 在线客户端会话 |
| POST | `/api/clients/{id}/kick` | 踢下线 |
| GET | `/api/nodes` | 节点运行时信息 |
| GET | `/api/alerts` | 告警列表 |
| POST | `/api/alerts/{id}/ack` | 确认告警（admin） |
| GET | `/api/audit?limit=100` | 审计事件，最大 1000（admin/viewer） |
| GET | `/api/encryption` | 隧道加密状态：是否启用、口令原文、指纹、已加密/明文条数（admin） |
| PUT | `/api/encryption` | `{"generate":true}` 生成并启用 · `{"secret":"..."}` 自定义 · `{"disable":true}` 关闭（admin，写审计） |
| GET (WS) | `/api/ws` | 实时快照，**每 2 秒**推送；内部校验 JWT cookie |
| GET/POST | `/api/users` | 用户列表 / 创建用户（返回一次性 token，admin） |
| PATCH/DELETE | `/api/users/{id}` | 改显示名/角色/配额/禁用、删除（admin） |
| POST | `/api/users/{id}/rotate-token`、`/password` | 轮换 token、设置控制台密码（admin） |
| GET | `/admin/` | SPA；`/` 与 `/admin` 302 跳转 |

### 5.4 客户端控制台 `:7200`（`/api/*` 除标注外均需 session）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/login` | 用 console token 或 Argon2 密码登录（无认证，有限流） |
| GET | `/api/meta` | 版本/客户端名/状态/csrf cookie 名/config_path（无认证） |
| GET | `/api/session` | 当前会话 csrf + expires_at |
| POST | `/api/logout` | 注销 |
| GET | `/api/status` | 运行时状态快照 |
| GET/POST | `/api/tunnels` | 隧道列表 / 新增（写回 YAML，`encryption: true` 即开启内层加密） |
| PUT/DELETE | `/api/tunnels/{name}` | 修改 / 删除 |
| POST | `/api/tunnels/{name}/start`、`/stop` | 启停 |
| POST | `/api/reconnect` | 触发重连 |
| GET | `/api/connections` | 活跃连接 |
| GET | `/api/stats` | 速率序列 |
| GET/PUT | `/api/config` | 读取 / 保存整份生效 YAML（事务式） |
| GET/PUT | `/api/settings` | **结构化连接设置**：服务端地址 / 令牌 / 共享口令（`stream_secret`）/ 客户端名 / TLS / 心跳 / 重连，深合并写回，仅改表单拥有的字段；改动口令会自动重连 |
| GET (SSE) | `/api/logs?level=` | 日志流：先回放 200 条、订阅缓冲 128、15s 心跳 |
| GET (WS) | `/api/ws` | 实时快照，每 2 秒推送 |
| GET | `/` | SPA |

### 5.5 metrics `:9090`

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/metrics` | 无 | Prometheus + Go runtime 指标，OpenMetrics 格式 |
| GET | `/healthz` | 无 | `{"status":"ok"}` |

主要指标：`wormgate_control_sessions`、`wormgate_tunnels`、`wormgate_client_sessions_total`、`wormgate_public_connections_total{owner}`、`wormgate_traffic_bytes_total{owner,direction}`、`wormgate_http_responses_total{code}`、`wormgate_registration_failures_total`。

---

## 6. 安全

### 已实现

- 控制面与数据面**强制 TLS 1.3**；可选 mTLS（`client_ca_file` 作为 token 之外的第二重认证）
- 数据通道校验 `Bearer` + session 存在 + `session.UserID == principal.UserID`
- 客户端 token 以 SHA-256 哈希存储；控制台密码用 **Argon2id**（m=65536,t=3,p=2）+ 常量时间比较；JWT HS256 强制校验 issuer / 过期 / 算法白名单
- CSRF 双提交 cookie + `SameSite=Strict` + 同源校验；登录按 IP 限流 10 次/分钟
- CSP（`default-src 'self'`）、`nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy`、`Permissions-Policy`、API `no-store`
- 各种体积上限：`controlReadLimit 64KB`、`dataReadLimit 8MB`、`maxStreamHeader 4096`、流头字段长度限制、请求体 `MaxBytesReader`（8KB ~ 1MB）
- 配置文件与凭据文件原子写、`0o600`

### 风险与部署注意

1. **默认自签证书**。`auto_self_signed: true` 仅供开发；生产必须配 `cert_file`/`key_file`，客户端用 `ca_file` 校验。
2. **不要在生产开 `insecure_skip_verify`**。校验器会为此打印 warning；它等于关掉了对控制面的身份验证。
3. **一次性凭据以明文进日志**：初始 admin 密码、demo token、客户端 console token。若日志被集中采集，等同于凭据泄露；生产应预置 `admin_password_hash`，并在首次登录后清理/限制日志访问。
4. **默认自动创建 demo 用户**，token 明文落在 `<data_dir>/demo-token`。生产用 `--admin-password` 之外还应在管理台禁用/删除 demo，改为按用户下发 token。
5. **`secure_cookie` 默认 false**。管理台默认以 HTTP 绑 `0.0.0.0:7100` 对外可达；若经 HTTPS 反代暴露，务必置 true，并给管理台配访问控制。
6. **公共入口本身无认证、默认是 HTTP**。TLS 终止、WAF、访问控制请在外部反代完成；命中已注册子域的请求会被直接转发到你的内网服务。
7. **`/metrics` 与 `/healthz` 无认证**，且默认绑 `0.0.0.0` 对外可达；生产请用云安全组 / 防火墙只放行采集端 IP，或在外层反代加认证。
8. **`sameOrigin` 对无 `Origin` 头的请求放行**，纵深防御依赖 CSRF 双提交 + `SameSite=Strict`。
9. **限流是单机内存滑动窗口**，重启即清空；多实例或置于代理之后需在外层补限流。
10. **Windows 上 POSIX 权限不生效**：`0o600` 是 best effort，NTFS 上报 0666，实际依赖目录 ACL，请手动收紧目录权限。
11. **OAuth 自动建号**默认角色 `viewer`，但 `default_role` 允许配成 `admin` —— 只在完全信任 IdP 时这么做。
12. 配置里的明文 `admin_password`、静态 `auth.tokens` 会被校验器警告，优先用哈希 / 每用户 token。

### 前端两个易踩的坑（都已修，改动时别再踩回去）

- **不要再用 ant-design-vue 重刷两个 Vue 控制台**：曾把 `/admin/` 与客户端台改成 antd 的扁平深色皮肤（`common/antd.js` 仍留在仓库里但已无人引用），但青绿/靛紫光晕 + 玻璃侧栏的旧观感更贴合运维场景，已整体回退（`theme.css`、`components.css`、`AppShell.vue`、`TopBar.vue`、`admin/src/views/DashboardView.vue`）。若日后再引入 antd：`Menu.items[].label` 不接受函数（只有 `icon` 会被当渲染函数调用），传 `label` 会被 `String()` 化成 `()=>h("span",…)` 印在侧栏里 —— 要带徽标就传 **VNode** 且每次渲染新建。
- **CSP 是 `script-src 'self'`**，`index.html` 里的内联主题脚本会被浏览器静默拦掉，表现为「先闪一帧深色再变浅色」外加一条 console error。两个 Vue 控制台的主题引导因此改成了 `public/theme-init.js` 外链（`<head>` 里同步加载，仍然在首次绘制前执行）；新增前端时同理，别往 `index.html` 塞内联脚本。

---

## 7. 性能与可观测性

| 项 | 值 | 影响 |
|---|---|---|
| 速率采样 | 1s 一次，环形窗口 **300 点（5 分钟）** | 超过 5 分钟的历史不会保留；控制台切到「5 分钟」才看得全 |
| WS 快照 | 2s 一次，只推最近 120 点 | 实时性与带宽的平衡 |
| SSE 日志 | 回放 200 条、订阅缓冲 128、15s 心跳 | 断线重连后能补最近日志 |
| 日志 / 告警 / 审计环形缓冲 | 2000 / 200 / 2000 条 | 均为内存，重启即清空 |
| yamux | KeepAlive 30s、写超时 15s、`MaxStreamWindowSize 4MB`、`AcceptBacklog 512` | 高并发长连接时关注流窗口与 backlog |
| `netpipe.Pipe` | `32KB` 缓冲池、`io.CopyBuffer`、半关闭后 5s 宽限 | 大文件传输的每连接内存约 64KB |
| 反向代理连接池 | `MaxIdleConns 256`、每 host 32、空闲 90s、`FlushInterval 100ms` | SSE/流式响应刷新的下限 |
| 配额 | 每用户 20 隧道 / 每隧道 200 并发 / 日流量默认不限 | 超限返回 429 |
| 重连 | 指数退避 1s → 30s，带抖动；会话存活 >1min 重置 | 服务端重启后自动恢复 |
| 请求上限 | 单条 `register` ≤ 32 隧道；自动子域最多试 12 次、端口最多 4096 次 | 注册风暴的保护 |
| gzip | 仅 ≥1024B 的 `.js/.css/.html/.json/.svg/.txt/.map`，`BestSpeed` + `sync.Pool` | **不压缩 API / SSE**，避免缓冲实时流 |

会话 GC 每 10 分钟清理一次；配置热重载防抖 300ms；控制面握手/拨号超时分别有 10s / 15s / 8s 等多层边界。

排障建议：`log.level=debug` 可看到 `no tunnel for host` 之类拒绝原因；`log.format=json` 便于采集；`wormgate_http_responses_total{code="404"}` 能直接观察未命中 Host 的量。

---

## 8. 测试

```bash
go test ./...
go vet ./...
gofmt -l .
```

当前仅 4 个测试文件、14 个用例：

- `internal/proto/proto_test.go`（5）：控制帧编解码往返、缺 type 拒绝、流头往返、错误 magic、超大 Source 拒绝
- `internal/auth/store_test.go`（4）：首次启动 demo token 即可认证（回归「token 打印出来却被 401」）、跨重启 token 不变、文件权限（Windows 跳过）、禁用用户 token 被拒
- `internal/server/disconnect_test.go`（1 组表驱动）：哪些断开算「正常」（含管理员踢下线），避免告警噪音
- `internal/webui/spa_test.go`（5）：`/next/` 的挂载契约 —— 裸前缀 307、index 指向 `/next/assets/`、客户端路由回落、`/next/api/*` 一律 404、前缀外 404

前端另有一个**行为级**冒烟脚本（不是像素比对，会断言）：

```bash
cd web
TX_PASS='<管理员密码>' node scripts/smoke-next.mjs   # 默认打 http://127.0.0.1:17200
```

它会真登录 `/next/`、走完 8 个页面、在 4 种视口宽度下检查横向溢出，再对一次性账号跑一遍「新建 → 改名 → 删除」，任何 console error / 4xx（`/api/me` 的预期 401 除外）都会让退出码非 0。截图落在 `.run/next-smoke/`。

动效与视觉规范另有独立断言脚本（21 项，含 reduced-motion 与主题过渡）：

```bash
cd web
TX_PASS='<管理员密码>' node scripts/verify-motion.mjs
TX_BASE=http://43.228.85.180:7100 TX_PASS='...' node scripts/verify-motion.mjs   # 也可打生产
```

**尚未覆盖**：config 加载/校验/env 映射、server 全套路由与代理、client 握手与重连、WebUI 的 CSRF/SSE/WS、TLS、netpipe。没有集成测试、基准测试与 CI 配置。改动这些模块时建议手工跑一遍第 2 节的端到端验证。

---

## 9. 目录结构

```
cmd/fps/               服务端独立入口（main -> cli.RunServerMain）
cmd/fpc/               客户端独立入口（main -> cli.RunClientMain）
cmd/tunnelx/           旧单二进制兼容 shim，发布构建不产出
internal/
  cli/                 两个二进制共享的 flag 定义、配置引导、工具命令、help
  proto/               控制帧 JSON 信封 + 12B 流头二进制协议
  config/              类型、加载优先级、校验、热重载（fsnotify）、结构化设置写入
  server/              控制面、注册/registry、公共反代、计量配额、admin API、metrics
  client/              Agent、会话、隧道 CRUD、本地控制台 API（含 /api/settings）、SSE/WS
  webui/               session、CSRF/限流、SPA 与 gzip 中间件；clientui/ adminui/ adminnext/ 为 embed 产物
  auth/                用户存储、Argon2id 密码、JWT、token 哈希
  tlsutil/             TLS 1.3 / 自签证书 / mTLS
  netpipe/             双向拷贝与半关闭
  logx/ audit/ alerts/ idgen/ version/
  tools/e2eorigin/     本地 E2E 用的静态源站（仅测试，不进主流程）
web/                   一个 npm 包、两个 Vite 工程 + common/ 共享设计系统
  next/                React 19 + Ant Design 6 灰度管理台（独立 package.json，挂 /next/）
  scripts/shoot.mjs    用 playwright-core 驱动本机 Edge/Chrome 拍控制台截图
scripts/build.sh       跨平台交叉编译 + 产物格式校验
dist/                  10 个平台产物（运行 build.sh 生成）
docs/
  frp-parity.md        与 frp 的功能差距表、差异化设计与分阶段路线图
  screenshots/         双控制台深/浅主题截图
```

`internal/tools/e2eorigin` 是刻意保留的端到端辅助程序；`.run/`、`bin/`、`data/`、`dist/` 是本地运行/构建产物，不参与源码编译。

---

## 端口与地址速查

**客户端不需要公网 IP，也不需要路由器端口映射**：fpc 主动外连 fps 的控制面，数据走同一条 TLS 连接里的 yamux 多路复用。只有服务端所在机器需要放行入站端口。

| 位置 | 默认监听 | 作用 | 要不要对公网开放 |
|---|---|---|---|
| fps `--control-addr` | `0.0.0.0:7000` | fpc 握手 + 数据通道（TLS 1.3） | **必须放行**，这是唯一需要开洞的端口 |
| fps `--http-addr` | `0.0.0.0:8080` | HTTP 隧道的公网入口（按 Host 匹配） | **必须放行**，生产通常改 `:80` / `:443` |
| fps `--tcp-port-range` | `20000-30000` | TCP 隧道分配的远程端口 | 用 TCP 隧道才需要放行该段 |
| fps `--webui-addr` | `0.0.0.0:7100` | 管理台 | 默认对外可达；**必须首启后立即改密**，建议限制来源 IP 或套 HTTPS 反代 |
| fps `--metrics-addr` | `0.0.0.0:9090` | Prometheus | 默认对外可达；**无认证**，建议只放行采集端 IP |
| fpc `--webui-addr` | `127.0.0.1:7200` | 客户端本地控制台 | **不要**对外开放（fpc 跑在内网机器上，仍保持 loopback） |
| `--tunnel ...=http://<host:port>` | 例如 `127.0.0.1:8080` | 被暴露的**内网服务**，填 fpc 所在机器能访问到的地址 | 完全不用动防火墙，也不需要公网可达 |

填 `--tunnel` 时的 `host:port` 是「fpc 能连到的本地服务」：本机服务写 `127.0.0.1:8080`，局域网内另一台机器写 `192.168.1.20:8080`，容器写服务名。它**不是**公网地址。

HTTP 隧道按 `Host` 头路由：子域名 = 隧道名（或 `--tunnel name=http://...@子域名`），基域由 fps 的 `--http-domain` 决定（默认 `wormgate.local`）。生产要把 `--http-domain example.com` 改成自己的域名，并把子域名解析到 fps 的**公网 IP**；有多条隧道时可以图省事用 `*.example.com` 泛解析，也可以按下面的方式逐条加 A 记录。不匹配或含有多段点的 Host 一律 404（默认拒绝）。本地验证可用 `curl -H "Host: web.wormgate.local" http://127.0.0.1:8080/`。

### 只用一条 A 记录，不做泛解析

泛解析不是必须的。匹配规则是 `Host = <隧道名>.<基域>`，所以让**隧道名等于你已解析的子域名标签**即可。假设只有 `fd.5t.pw -> <fps 公网 IP>` 这一条记录：

1. DNS：`fd.5t.pw` 的 A 记录指向 fps 公网 IP。基域 `5t.pw` 本身不必指向同一台机器，也不需要 `*.5t.pw`。
2. fps：`server.http.domain: 5t.pw`（或 `--http-domain 5t.pw`）。
3. fpc：隧道命名为 `fd`，例如 `--tunnel fd=http://127.0.0.1:8091`。
4. 访问 `http://fd.5t.pw:8080/`；再多开一条隧道就再加一条 A 记录并让隧道名与子域标签一致，如 `api.5t.pw` → 隧道 `api`。

DNS 只负责把域名指到 IP，**不负责端口**。`nip.io` / `sslip.io` 这类泛解析域名在部分网络会被明文 HTTP 拦截，导致浏览器打不开但服务器本机 `curl` 正常；换真实域名即可（本次部署实测：`nip.io` 0/10，真实子域名 8/8）。

### 用现有 Caddy 提供 HTTPS

fps 的 HTTP 入口只说**明文 HTTP**，TLS 必须由前置反代终止。若 80/443 已被 Caddy 占用，直接追加一个站点块（不要改动已有站点）：

```caddyfile
fd.5t.pw {
    encode zstd gzip
    reverse_proxy 172.17.0.1:8080 {
        flush_interval -1
    }
}
```

改前备份 `Caddyfile`，改后 `caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`，再 `caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile` 热重载（不中断现有业务）。Caddy 会自动申请并续期 Let's Encrypt 证书，前提是 80/443 公网可达且域名已解析到本机。

⚠️ **容器环境的坑**：Caddy 跑在 Docker 里时，反代目标不能写 `127.0.0.1:8080`（那是容器自己），要写宿主机在 docker 网桥上的地址，通常是 `172.17.0.1`（用 `docker network inspect bridge` 确认）。Caddy 的 `reverse_proxy` 默认保留原始 `Host`，所以 fps 的子域名匹配仍然生效。

最后把 fps 的 `server.http.scheme: https`、`server.http.public_port: 443` 配上（只影响管理台和 fpc 日志里显示的公网地址，监听端口仍是 8080），注册结果就会变成 `https://fd.5t.pw`。若不想动现有 Caddy，也可以用 Cloudflare 等边缘代理终止 TLS 后回源到 `8080`。

## 三类凭据，不要混用

| 凭据 | 用在哪 | 从哪来 |
|---|---|---|
| **本地登录密码** | 打开 fpc 控制台 `http://127.0.0.1:7200/` | fpc 首次启动打印一次，存 `data/client-console-password`（0600）；可用 `--webui-password` / `WORMGATE_CLIENT_WEBUI_PASSWORD` 自定义；可在「安全」页修改 |
| **服务端令牌 `wg_...`** | 在客户端台「配置」页填，用于 fpc 连 fps | fps 首次启动打印，或管理台按用户签发 / 轮换 |
| **管理员密码** | 打开 fps 管理台 `http://<服务器IP>:7100/admin/` | fps 新数据目录首次启动打印一次；可在「安全」页修改，或在「用户与租户」页重置他人密码 |

fpc **允许在没有 `wg_` 令牌时启动**：本地控制台照常打开，Agent 显示"待配置"（不会反复用 401 去撞控制面），在「配置」页填入令牌保存后会立即热加载并自动连接。若当前目录与用户目录都没有配置文件，首次保存会自动在工作目录生成 `wormgate.yaml`（含明文令牌，权限 0600，注意别提交到仓库）。不要再拿 `wg_` 去登录本地控制台——登录页会明确提示这一点。

### 在界面里改密码

两个控制台的侧栏都有「**安全**」页，改的是**只用于登录网页 UI 的密码**，和 `wg_` 令牌完全独立：改密码不会轮换令牌，轮换令牌也不会改密码。

| | 客户端控制台 `:7200` | 管理台 `:7100/admin` |
|---|---|---|
| 入口 | 安全 → 修改登录密码 | 安全 → 修改登录密码（任意角色改自己的） |
| 校验 | 当前密码 + 新密码（≥8 位）+ 确认 | 当前密码 + 新密码（≥8 位）+ 确认 |
| 接口 | `POST /api/password` | `POST /api/me/password` |
| 存储 | 配置文件的 `client.webui.password_hash`（argon2id，文件 0600） | `<data_dir>/users.json` 里的 `password_hash`（argon2id） |
| 重启后 | 仍然有效 | 仍然有效 |
| 其他会话 | 除当前浏览器外全部登出 | 所有旧 JWT 失效，当前浏览器自动换发新会话 |

客户端台改密后，**配置文件里的 `password_hash` 成为唯一凭据**：启动参数 `--webui-password`、`data/client-console-password` 里的旧值都会停止生效（后者会尽量同步成新值，便于人工查看）。这样即使启动脚本写死了 `--webui-password`，也不会在重启时把旧密码带回来。想恢复用启动参数指定密码，删除配置文件里的 `client.webui.password_hash` 即可。启动时日志不再打印旧密码，只提示"密码由安全页管理"。

管理员还可以在「**用户与租户**」页点盾牌图标**重置任意账号的登录密码**（`POST /api/users/{id}/password`，仅管理员，不需要对方的旧密码）；重置会让该账号所有已登录会话立即失效。OAuth 专用账号没有本地密码，需要先由管理员这样设置一次，才能使用「安全」页自助修改。

两类改密都会写入审计日志（管理台）或进程日志（客户端台），密码本身不会被打印或回传。

## Windows 双击启动

根目录两个启动器可直接双击，日志窗口会保留、失败时不会一闪而过，并在控制台就绪后自动打开浏览器：

- `start-fps.bat` — 零配置起服务端
- `start-fpc.bat` — 顶部三行可编辑（`FPC_SERVER` / `FPC_TOKEN` / `FPC_TUNNEL`），双击即连；令牌留空也能启动，之后在配置页填

直接双击 `fps.exe` / `fpc.exe` 也支持：程序会识别这种启动方式，失败时等待按键、成功时自动打开对应控制台。Linux / macOS 用 systemd / launchd / 终端运行，不会触发该行为。设置 `WORMGATE_OPEN_BROWSER=1` 可在脚本里强制自动打开浏览器。

## 界面语言

三个控制台（Vue 客户端台、Vue 管理台、React 管理台 `/next/`）都是中英双语，**默认中文**。顶栏（登录页底部）的 `中 / EN` 按钮切换，选择写入 `localStorage` 的 `wormgate.lang`（三处共用同一个键；改名前写入的 `tunnelx.lang` 会自动迁移读取），不会跟随浏览器语言自动切回中文。切换后静态文案、表单提示、toast、API 错误与 SSE 实时日志统一跟随当前语言；字典在 `web/common/locales.js`，新增文案按「中文原串 → English」补一条即可。

翻译在 **DOM 层**做（`web/common/i18n.js` 给 Vue，`web/next/src/i18n.tsx` 给 React），不要求每个模板都写 `t()`。这有两个必须遵守的规矩：

- **一句话要是一个文本节点**。`节点运行 {x} · {n} 次累计连接` 会被 React 拆成 4 个文本节点，句子级规则永远匹配不到，只能逐词替换成 `Nodes19s · 0 Total Connections`。写成 ``{`节点运行 ${x} · ${n} 次累计连接`}`` 才走得到 `ZH_PATTERNS`。
- **不想被翻译的文本要标 `data-i18n-skip`**（含它自己的文本子节点，语言按钮上的「中」就靠这个）。英文模式下未命中的中文片段会被清掉，半中半英比原样保留更难看。
- **中英文之间的空格要按语言给**。`需要旧版界面？<a>切回 Vue 控制台</a>` 中文不需要空格，翻成英文却是 `Need the legacy UI?Open the Vue console`。这类粘连只能写成 `{lang === 'en' ? ' ' : null}`，字典救不了；`i18n-check.mjs` 里有 `/[?!][A-Za-z][a-z]{2,}/` 断言专门抓它。

`web/scripts/i18n-check.mjs` 把这几条变成断言：登录页与顶栏都有切换入口、英文模式下全站无残留中文、英文句子无粘连、顶栏只出现一次账号名。改前端后跑一次：

```bash
TX_BASE=http://127.0.0.1:17200 TX_PASS='<管理员密码>' node web/scripts/i18n-check.mjs
```

登录页那两个断言不需要账号，生产上可以直接验（SSE 长连接会让 `networkidle` 永不触发，脚本用 `domcontentloaded`）：

```bash
node web/scripts/prod-login-check.mjs                                  # 默认打 43.228.85.180:7100
TX_BASE=http://127.0.0.1:17200 node web/scripts/prod-login-check.mjs
```
