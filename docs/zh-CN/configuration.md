# 配置说明

Z-Courier 运行时主要有两个配置文件：

```text
conf/zinx.json              Zinx TCP 服务配置
configs/z-courier.yaml      Z-Courier 网关配置
```

启动 gateway：

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

配置路径也可以通过环境变量覆盖：

```bash
export ZCOURIER_CONFIG=configs/z-courier.yaml
export ZINX_CONFIG_FILE_PATH=conf/zinx.json
go run ./cmd/gateway
```

## 环境变量占位符

YAML 支持严格的 `${ENV_NAME}` 占位符：

```yaml
downlink:
  storage:
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
```

规则：

- 只支持 `${ENV_NAME}`，不支持裸 `$ENV_NAME`。
- 环境变量不存在时启动失败。
- 不会静默替换成空字符串。
- 适合密码、HMAC key、内部 token、upstream token。
- 不要把真实 secret 提交到 Git。

## Zinx 配置

`conf/zinx.json` 控制 TCP listener 和 Zinx worker：

```json
{
  "Name": "Z-Courier Gateway",
  "Host": "0.0.0.0",
  "TCPPort": 8999,
  "Mode": "tcp",
  "MaxConn": 12000,
  "MaxPacketSize": 8388608,
  "WorkerPoolSize": 10,
  "MaxWorkerTaskLen": 1024,
  "MaxMsgChanLen": 1024,
  "IOReadBuffSize": 8192,
  "HeartbeatMax": 30,
  "LogDir": "./log",
  "LogFile": "zinx.log",
  "LogCons": true,
  "LogIsolationLevel": 2
}
```

常看字段：

- `Host` / `TCPPort`：客户端 TCP 接入地址。
- `MaxConn`：最大连接数。
- `MaxPacketSize`：Zinx 外层包最大大小。
- `WorkerPoolSize`：Zinx worker 数。
- `HeartbeatMax`：心跳超时。
- `LogIsolationLevel`：Zinx 框架日志级别，`2` 表示 Warn 及以上。

## Gateway 基础配置

```yaml
gateway_node: local
route_msg_ids:
  - 1000
```

- `gateway_node`：当前网关节点名，会写入 session 和 cluster route。
- `route_msg_ids`：额外注册到 Zinx 的 MsgID。启用的 upstream route 范围会自动注册。

`MsgID = 1000` 是 `AUTH/BIND` 控制消息。`MsgID = 2` 是下行 ACK。它们由网关保留。

## 静态配置校验

不启动 TCP 服务，只检查配置：

```bash
go run ./cmd/gateway -config configs/z-courier.yaml -check-config
```

它会检查：

- YAML 格式和字段形状。
- duration 是否能解析。
- auth provider 配置。
- internal HTTP auth 配置。
- cluster / downlink storage 配置。
- upstream route 是否重叠。
- 是否占用了保留 MsgID。
- 一些高风险但仍合法的配置，例如生产环境使用 memory cluster registry。

## 客户端鉴权

### static

适合本地开发和测试：

```yaml
auth:
  type: static
  static_tokens:
    dev-token:
      client_id: dev-client
      token_id: dev-token
      scopes:
        - gateway:dev
```

客户端带 `Token = dev-token`，网关验证后绑定为 `client_id = dev-client`。

### http

适合后端单体应用或已有账号系统：

```yaml
auth:
  type: http
  http:
    url: http://backend:8080/internal/auth/verify
    internal_token: replace-with-a-shared-secret
    timeout: 2s
    max_in_flight: 500
  cache:
    enabled: true
    max_entries: 10000
    positive_ttl: 30s
    negative_ttl: 3s
```

网关会向后端发送 `POST`，带：

```text
Authorization: Bearer <client-token>
X-ZCourier-Internal-Token: <internal_token>
```

后端返回 `client_id`、`token_id`、`subject`、`scopes`、`expires_at` 等字段。

### jwt / jwks

适合已有 JWT 发行方：

```yaml
auth:
  type: jwt
  jwt:
    issuer: https://identity.example.com
    audience: z-courier
    jwks_url: https://identity.example.com/.well-known/jwks.json
    algorithms: [RS256, ES256]
    client_id_claim: client_id
    token_id_claim: jti
    scopes_claim: scope
    clock_skew: 30s
    refresh_interval: 5m
    fetch_timeout: 2s
```

Z-Courier 不签发 JWT，也不生成私钥。JWT 的私钥属于身份服务，Z-Courier 只通过
`jwks_url` 获取公钥来验签。

Docker Compose 中的 `jwks_url` 是从 gateway 容器内部访问的地址，不要误写成宿主机
的 `127.0.0.1`，除非 JWKS 服务就在同一个容器里。

## Internal HTTP

```yaml
internal_http:
  enabled: true
  addr: 127.0.0.1:18080
  token: dev-internal-token
  auth:
    mode: token
  max_request_body_size: 10485760
  max_in_flight: 1000
```

内部 HTTP 提供：

- `/internal/push`
- `/internal/push/batch`
- `/internal/messages`
- `/internal/message/requeue`
- `/internal/message/discard`
- `/internal/admin/*`
- `/internal/debug/*`
- `/metrics`
- `/healthz`
- `/readyz`
- `/console/`

开发环境可以用 `token` 模式。生产环境推荐 `hmac` 模式，防止请求被篡改或重放。

## Admin Console

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  monitoring:
    prometheus_url: http://127.0.0.1:9090
    grafana_url: http://127.0.0.1:3000
    dashboard_url: http://127.0.0.1:3000/d/z-courier
  session:
    enabled: true
    ttl: 8h
    cookie_name: zcourier_admin_session
    cookie_secure: false
    cookie_same_site: lax
    role: admin
  audit:
    type: memory
    capacity: 1000
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
      auto_migrate: true
      max_open_conns: 10
      max_idle_conns: 5
      conn_max_lifetime: 30m
      operation_timeout: 2s
```

注意：

- 生产 Compose 和 Helm 默认关闭 console。
- 不要把 `/console/` 或 `/internal/*` 暴露到公网。
- 推荐通过 VPN、堡垒机、私有 ingress 或带认证的反向代理访问。
- `session.enabled=true` 时，gateway 会开放
  `POST /internal/admin/session/login`、`GET /internal/admin/session/me` 和
  `POST /internal/admin/session/logout`。login 会把有效 internal token，或已经通过
  HMAC 验签的请求，换成一个短期 HTTP-only cookie。
- 当前 admin session 是单节点内存态，gateway 重启后会失效。
- `cookie_same_site` 可选 `lax`、`strict`、`none`；`none` 必须配合
  `cookie_secure=true`。
- `role` 是新建浏览器 session 的角色，可选 `readonly`、`operator`、`admin`。
  `readonly` 只能查看，`operator` 可以执行受保护的本机 session 断开、下行测试推送
  和消息修复操作，比如 requeue 和 discard，`admin` 目前包含 operator 的全部权限。
- `audit.type` 是管理员操作审计存储类型。`memory` 只保留当前 gateway 进程内最近
  的审计事件；`postgres` 会把事件持久化到 PostgreSQL，重启后仍然可以查询。
- `audit.capacity` 是 `audit.type=memory` 时最多保留的内存审计事件数。
- `audit.postgres.dsn` 是 `audit.type=postgres` 时使用的 PostgreSQL DSN。
- `audit.postgres.auto_migrate=true` 时，gateway 启动会自动创建审计表和索引。
- `audit.postgres.max_open_conns`、`audit.postgres.max_idle_conns`、
  `audit.postgres.conn_max_lifetime` 是可选连接池配置。
- `audit.postgres.operation_timeout` 是写入和查询审计事件的超时时间。
- 管理员审计会记录 login、权限拒绝、session 断开、下行测试推送、消息
  requeue/discard、retry scan、诊断操作等 console/internal admin API 行为。生产环境
  如果需要重启后仍能追踪这些事件，建议使用 `audit.type=postgres`。
- HMAC 模式下，浏览器直接持有 HMAC secret 并不理想，也无法自己完成安全签名链路。
  生产更适合由反向代理完成 operator 鉴权和内部签名，再转发到 gateway internal
  HTTP。

浏览器级别的 console 发布前验证可以执行：

```bash
bash scripts/console_smoke.sh
```

这个脚本会构建 console 资产，分别用 `admin` 和 `readonly` 两种角色启动轻量 gateway，
再通过 Playwright 验证登录、页面导航、受保护操作确认框，以及 readonly 下的禁用状态。
如果本机还没有安装 Playwright 浏览器，先执行一次：

```bash
npm --prefix web/admin exec -- playwright install chromium
```

## Cluster

```yaml
cluster:
  enabled: true
  route_refresh_interval: 10s
  registry:
    type: redis
    ttl: 30s
    redis:
      addr: redis:6379
      key_prefix: zcourier:cluster
  peer:
    timeout: 2s
    auth:
      mode: hmac
```

cluster registry 记录在线路由：

```text
client_id + device_id -> gateway_node + internal_addr + session_id
```

当下行请求打到错误节点时，网关可以查 Redis 并把消息 peer push 到真正持有连接的节点。

## Downlink

```yaml
downlink:
  storage:
    type: postgres
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
      auto_migrate: true
  delivery:
    retry_interval: 5s
    retry_delay: 30s
    retry_jitter: 5s
    ack_timeout: 30s
    retry_lease: 30s
    max_attempts: 10
    scan_limit: 500
    bind_flush_limit: 500
```

没有可靠存储时，离线消息无法长期保存。生产环境建议使用 PostgreSQL。

## Upstream Routes

```yaml
upstream:
  routes:
    - name: http-upstream
      enabled: true
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        url: http://backend:8080/gateway/upstream
        token: backend-shared-token
        timeout: 5s
        max_in_flight: 1000

    - name: nsq-upstream
      enabled: true
      msg_id_min: 2000
      msg_id_max: 2999
      target:
        type: nsq
        nsqd_addrs:
          - nsqd:4150
        topic: message_events
        publish_mode: round_robin
        retry_attempts: 2
        max_in_flight: 1000
```

同一个 `MsgID` 只能匹配一个启用的 route。配置校验会拦截 route 重叠和保留 MsgID。

## Pipeline

```yaml
pipeline:
  allowlist:
    client_ids: []
    msg_ids: []
  blocklist:
    client_ids: []
    msg_ids: []
  rate_limit:
    enabled: true
    max_requests: 1000
    window: 1s
```

pipeline 是通用网关能力所在的位置：黑白名单、限流、日志、指标、鉴权状态检查。
