# 与 frp 的功能对齐与差异化

> 目标：**功能对齐 frp（`frps` / `frpc`），但在配置与使用体验上明显更好**。
> 本文档是路线图，不是"已全部实现"的声明。Wormgate 现状一栏逐项对照了本仓库源码；
> frp 能力清单来自其官方 README 的特性目录（2026-11 核实）。

## 1. 命名与形态

| | frp | Wormgate |
|---|---|---|
| 服务端 | `frps` | **`fps`** |
| 客户端 | `frpc` | **`fpc`** |
| 配置 | 两份 INI/TOML/YAML：`frps.ini` + `frpc.ini` | 可零配置；需要时**一份 YAML 两端共用**，各读自己的 section |
| 首次连接 | 先写好两份配置文件 | `fps` 直接跑，日志打印一条可复制粘贴的 `fpc` 命令 |
| 交付 | 两个二进制 + 配置文件 | 两个静态二进制（前端已 embed），无外部依赖 |

## 2. 功能对齐差距表

图例：✅ 已具备 · ⚠️ 部分具备 / 语义不同 · ❌ 缺失

### 2.1 代理类型

| frp 能力 | Wormgate 现状 | 说明 | 优先级 |
|---|---|---|---|
| TCP 隧道 | ✅ | `type: tcp`，支持自动端口分配（`tcp.port_range`） | — |
| HTTP 子域名路由 | ✅ | `<sub>.<domain>` 单层子域；未匹配 Host **默认 404 拒绝** | — |
| UDP 隧道 | ❌ | `TunnelConfig.Type` 仅允许 `http` / `tcp` | **P1** |
| HTTPS / vhost TLS | ⚠️ | 公共入口是 HTTP，TLS 终止交给外部反代；无按域名的证书管理 | **P1** |
| `custom_domains` 自定义域名 | ⚠️ | 只支持 `<sub>.<base-domain>`，不支持任意完整域名 | **P1** |
| STCP / SUDP（密钥访问） | ❌ | 无 visitor / preshared-key 模型 | P2 |
| XTCP（P2P 打洞） | ❌ | 无 NAT 穿透与 UDP 打洞 | P2 |
| URL 路由 / location | ❌ | 无按路径分发 | P2 |
| 范围端口映射 | ⚠️ | 自动分配单个端口，无批量区间映射 | P2 |
| 客户端插件 | ❌ | 无 `https2http` / `static_file` / `socks5` / `unix_domain_socket` 等 | P2 |
| 服务端管理插件 | ❌ | 无 HTTP 插件回调 | P2 |
| SSH Tunnel Gateway | ❌ | — | P2 |

### 2.2 传输与可靠性

| frp 能力 | Wormgate 现状 | 说明 | 优先级 |
|---|---|---|---|
| TCP 流多路复用 | ✅ | 数据通道 WebSocket + yamux，控制面与数据面分离 | — |
| 连接池 | ✅ | `http.Transport` 256 空闲 / 每 host 32 / 90s idle | — |
| 加密 | ✅ | 控制面与数据面**强制 TLS 1.3**，可选 mTLS | — |
| 应用层压缩 | ❌ | 静态资源有 gzip，隧道载荷不压缩 | P2 |
| KCP / QUIC | ❌ | 仅 WebSocket over TCP | P2 |
| 服务健康检查 | ⚠️ | 只有控制面心跳（15s / 45s 超时），无对本地后端的主动探测 | **P1** |
| 负载均衡 / 分组 | ❌ | 一个隧道名只对应一个客户端 | P2 |
| 每代理带宽限制 | ⚠️ | 有用户级日流量配额与并发上限，无实时令牌桶限速 | **P1** |
| 端口复用 | ⚠️ | 控制/数据共用 `:7000` 的两个 WS 路径；TCP 隧道仍是独立端口 | 部分 |
| 服务端端口白名单 | ✅ | `server.tcp.port_range` | — |
| 通过 HTTP PROXY 连服务端 | ❌ | 客户端直连控制面 | P2 |

### 2.3 HTTP 与安全

| frp 能力 | Wormgate 现状 | 说明 | 优先级 |
|---|---|---|---|
| 真实 IP / X-Forwarded-For | ✅ | 剥离伪造的 `X-Forwarded-*` / `X-Real-IP` 后注入可信值 | — |
| 改写 Host / 自定义 Header | ⚠️ | 注入 `X-Wormgate-Tunnel`，无可配置的改写规则 | **P1** |
| Web 服务 HTTP Basic Auth | ❌ | 隧道本身不做访问认证 | P2 |
| Token 认证 | ✅ | 每用户 token（SHA-256 存储）+ 静态 token | — |
| OIDC / 第三方登录 | ⚠️ | 管理台支持 OAuth2 授权码 + PKCE；客户端认证无 OIDC | 部分 |
| 控制台认证 | ✅ | Argon2id 密码 / JWT / CSRF 双提交 / 登录限流 | — |
| Dashboard + Client Admin UI | ✅✅ | **双 WebUI**：管理台 8 页 + 客户端台 5 页，深/浅主题、实时图表 | 超越 |
| Prometheus | ✅ | `/metrics` + Go runtime 指标 | — |

### 2.4 配置与运维

| frp 能力 | Wormgate 现状 | 说明 | 优先级 |
|---|---|---|---|
| 配置文件 | ✅ | YAML（严格解码，未知字段报错并给行号） | 超越 |
| 环境变量 | ✅✅ | **原生 `WORMGATE_<PATH>` 覆盖任意字段**，无需在文件里写 `{{ .Envs.* }}` 模板 | 超越 |
| 配置拆分 `includes` | ❌ | 单文件 + profile 覆盖层 | P2 |
| 热重载 | ✅✅ | fsnotify 300ms 防抖、事务式校验、失败整体回滚、**不中断已建立隧道** | 超越 |
| 图形化配置 | ✅✅ | **fpc 控制台表单化配置**服务端地址/令牌/TLS/心跳/退避与隧道 CRUD | 超越 |
| CLI 工具 | ✅ | `init` / `config show\|validate` / `password-hash` / `version` 内置于两个二进制 | 超越 |
| 跨平台构建 | ✅ | `scripts/build.sh` 一次产出 Linux/macOS/Windows × amd64/arm64 共 10 个静态二进制 | — |

## 3. 已经比 frp 更好用的地方

1. **零配置启动**：`fps` 不带任何参数即可运行。首次启动打印一次性管理员密码、demo token，
   以及一条可以直接复制到客户端执行的 `fpc --server ... --token ... --tunnel ...` 命令。
   frp 需要先手写两份配置文件才能建立第一条隧道。
2. **一行命令发布服务**：`fpc --server x:7000 --token wg_xxx --tunnel web=http://127.0.0.1:8080`，
   `--tunnel` 可重复，不需要创建文件。
3. **一份配置两端共用**：同一份 YAML 里 `server:` 与 `client:` 各自独立，`fps` / `fpc` 各读一段。
4. **三层配置 + profile**：CLI > 环境变量 > profile 覆盖层 > 项目文件 > 全局文件 > 默认值，
   逐 key 深合并；任何字段都能用 `WORMGATE_<PATH>` 覆盖，配置文件一个字都不用改。
5. **图形化配置中心**：fpc 本地控制台用表单配置服务端连接、TLS、心跳、重连与隧道，
   保存走严格校验 + 事务写入；改了服务端地址/令牌/TLS 会**自动重连**。
   YAML 编辑器降级为「高级」标签，用于启动期参数。
6. **默认安全**：控制面 TLS 1.3 强制、未匹配 Host 一律 404、两个控制台默认只绑 loopback、
   CSP / CSRF / SameSite、Argon2id、一次性凭据不落盘明文。
7. **可观测性开箱即用**：双控制台实时图表、SSE 日志流、WS 2s 快照、Prometheus、
   审计 / 告警 / 配额，全部内置，不需要额外部署组件。
8. **单文件交付**：Vite 产物通过 `go:embed` 编进二进制，部署只需拷贝 `fps` / `fpc` 两个文件。

## 4. 分阶段路线图

### P0 — 已完成（本轮）
- [x] 拆分为 `fps` / `fpc` 两个独立二进制（共享 `internal/cli`，`cmd/tunnelx` 保留为兼容 shim）
- [x] `scripts/build.sh` 交叉编译 5 个平台 × 2 个二进制，ELF/Mach-O/PE 校验
- [x] `fps` 首次启动打印可复制的 `fpc` 命令
- [x] `fpc` 控制台表单化「服务端连接」配置（`/api/settings` 结构化读写 + 深合并）
- [x] 双主题控制台与文档

### P1 — 下一阶段（补齐高频能力，不改核心协议模型）
- [ ] **UDP 隧道**：扩展 `TunnelConfig.Type`、注册帧、服务端 UDP 监听与客户端转发
- [ ] **HTTPS / vhost TLS**：公共入口按域名加载证书，支持 `custom_domains`
- [ ] **Header 改写规则**：可配置的 Host 重写与请求/响应头注入
- [ ] **后端健康检查**：主动探测本地目标，异常时摘除并告警
- [ ] **实时带宽限速**：每隧道令牌桶，替代"仅日配额"
- [ ] **更多数据图表**：按隧道流量分布、连接数趋势、错误率、会话时长、地域/来源 IP
- [ ] **fps 管理台增强**：服务端配置可视化、一键生成 `fpc` 命令卡片、批量隧道操作

### P2 — 远期（需扩展协议栈）
- [ ] STCP / SUDP 密钥访问模型与 visitor
- [ ] XTCP P2P 打洞（UDP 打洞 + 回退路径）
- [ ] 客户端插件体系（https2http、static_file、socks5、unix socket）
- [ ] QUIC / KCP 传输
- [ ] 负载均衡与隧道分组
- [ ] 配置 `includes` 拆分、服务端管理插件、通过 HTTP PROXY 连控制面

## 5. 实施约束

- 新增协议类型时，`internal/proto/proto.go` 的 `TunnelSpec` / `StreamHeader` 与
  `internal/config/validate.go` 的类型白名单必须同步放宽，否则注册帧会被拒绝。
- 服务端 `matchHost()` 目前是「单层子域 + 严格匹配 + 默认拒绝」。放开 `custom_domains`
  时要保留默认拒绝语义，并防止 Host 头注入。
- 所有新配置项必须同时支持 YAML / `WORMGATE_<PATH>` / CLI 三层，并进入 `config show` 输出。
- 每新增一个协议，补齐 `internal/proto/proto_test.go` 的帧往返用例，避免协议漂移。
