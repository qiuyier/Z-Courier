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
- `/internal/messages/requeue`
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
    store:
      type: memory
      redis:
        addr: 127.0.0.1:16379
        username: ""
        password: ""
        db: 0
        key_prefix: zcourier:admin-session
        dial_timeout: 1s
        read_timeout: 1s
        write_timeout: 1s
        operation_timeout: 2s
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
- `cookie_same_site` 可选 `lax`、`strict`、`none`；`none` 必须配合
  `cookie_secure=true`。
- `role` 是新建浏览器 session 的角色，可选 `readonly`、`operator`、`admin`。
  `readonly` 只能查看，`operator` 可以执行受保护的本机 session 断开、下行测试推送
  和消息修复操作，比如 requeue 和 discard，`admin` 目前包含 operator 的全部权限。
- `session.store.type` 是浏览器 admin session 的存储类型。`memory` 只存当前
  gateway 进程；`redis` 会把 session 共享到 Redis，适合集群和负载均衡场景。
- `session.store.redis.addr`、`username`、`password`、`db`、`key_prefix` 是
  Redis 连接和 key 命名空间配置。
- `session.store.redis.dial_timeout`、`read_timeout`、`write_timeout`、
  `operation_timeout` 是 Redis 连接和操作超时。
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
- 单节点开发可以使用 `session.store.type=memory`；如果 console 请求可能在
  gateway-a/gateway-b 之间切换，建议使用 `session.store.type=redis`。logout 会删除
  Redis 中的共享 session，Redis key TTL 和 session 过期时间保持一致。
- 浏览器 admin session 会额外使用一个每个 session 独立的 CSRF token。login 和
  `me` 响应会返回 `session.csrf_token`；内置 console 会把它暂存在内存里，并在
  基于浏览器 session 的写操作请求中自动带上 `X-ZCourier-CSRF-Token`。这些写操作
  还必须使用 `Content-Type: application/json`；如果请求带了 `Origin` 或
  `Referer`，来源必须和当前请求同源。不携带浏览器 admin session cookie 的
  internal token/HMAC 机器调用不受这个浏览器保护逻辑影响。拒绝事件会写入
  `admin_session_mutation_rejected` 审计，CSRF 拒绝会计入
  `z_courier_admin_csrf_rejected_total`。
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
    retry_fairness:
      enabled: true
      candidate_multiplier: 4
  policies: []
```

没有可靠存储时，离线消息无法长期保存。生产环境建议使用 PostgreSQL。

- `retry_fairness.enabled`：按 `client_id + device_id` 轮转选择到期消息，避免
  一个热点离线设备独占有界扫描；关闭时保持 FIFO。
- `retry_fairness.candidate_multiplier`：公平选择前的有界候选窗口倍数。`0`
  使用默认值 `4`，大于 `16` 的配置会被拒绝。只有当最老的到期窗口经常被
  单设备积压占满时才需要提高。

开启后，每次扫描先读取一个有界候选窗口，再按设备轮次选出最多
`scan_limit` 条消息。PostgreSQL 仍使用 claim lease 和
`FOR UPDATE SKIP LOCKED`，共享存储的多个 gateway 不会领取同一条消息。
`z_courier_downlink_retry_selected_devices` 和
`z_courier_downlink_retry_max_per_device` 指标分别展示每次扫描覆盖的设备数和
单设备最多入选数。

### 命名下行策略（V12.2）

V12.2 策略解析器支持使用闭区间 MsgID 范围选择命名策略：

```yaml
downlink:
  delivery:
    retry_delay: 30s
    retry_jitter: 5s
    ack_timeout: 30s
    max_attempts: 5
  policies:
    - name: critical
      enabled: true
      msg_id_min: 2100
      msg_id_max: 2199
      max_attempts: 10
      max_age: 1h
      ack_timeout: 10s
      retry_delay: 1s
      backoff_multiplier: 2
      max_retry_delay: 30s
      retry_jitter: 250ms
```

命中范围的策略优先于隐式的 `default` 策略；没有命中任何范围的 MsgID
继续使用 `downlink.delivery`。策略中省略的字段继承默认值；`max_age`
默认不限制，`backoff_multiplier` 默认是 `1`，`max_retry_delay` 默认等于
初始重试延迟。

启用的策略名称必须唯一，只能使用小写字母、数字、`_` 和 `-`。MsgID
范围包含首尾值并且不能重叠。`backoff_multiplier` 大于 `1` 时必须显式
配置 `max_retry_delay`。配置不合法时网关会拒绝启动。

每一条新接收的可靠下行消息都会把命中的策略名称和全部执行参数一起持久化。
因此后续修改配置只影响新消息，存量消息仍按创建时的策略执行。V12.2.2 之前
创建、没有策略快照的旧数据，会按其 MsgID 使用当前配置解析出的策略。

第 `N` 次失败后（从 `1` 开始），基础重试延迟为：

```text
min(retry_delay * backoff_multiplier^(N-1), max_retry_delay)
```

之后再叠加 `0` 到 `retry_jitter` 的随机抖动。要求 ACK 的消息会根据自己的
`ack_timeout` 持久化 ACK 截止时间。下一次发送会违反次数或消息年龄上限时，
retry worker 会把消息标记为 `failed`，并写入 `max_attempts_exceeded` 或
`max_age_exceeded`。状态/列表接口和管理控制台都会显示持久化的 `policy_name`。

### 下行队列容量（V12.4）

容量准入可以防止长期离线积压无限占用 PostgreSQL 和 retry worker：

```yaml
downlink:
  capacity:
    max_pending_global: 1000000
    max_pending_per_device: 1000
```

- `max_pending_global`：共享下行存储允许的 pending 消息总数，`0` 表示不限。
- `max_pending_per_device`：单个 `client_id + device_id` 允许的 pending 消息数，
  `0` 表示不限。

两个限制同时开启时，单设备限制不能大于全局限制。启用容量保护必须配置可靠
下行存储；共享同一个 PostgreSQL 的所有 gateway 节点必须使用相同容量配置。

存储会先判断幂等，再检查容量。因此队列已满时，相同 `message_id` 的兼容重放仍
返回 existing；不可变身份冲突仍返回 HTTP `409`。真正的新消息超过容量时返回
HTTP `429`、`code = queue_capacity_exceeded`，并带上 `capacity_scope`、
`capacity_limit` 和 `capacity_pending`。被拒绝的消息不会写入存储，也不会产生
终态事件。人工 requeue 使用相同准入规则。

PostgreSQL 使用事务级 advisory lock 把容量计数与插入串成原子操作，因此两个
gateway 不能同时抢到最后一个名额。memory store 在单进程内保持相同语义。容量
限制不会淘汰旧消息，只会拒绝新的准入。

可靠下行必须先持久化再尝试在线 socket 投递，所以全局 pending 已满时，新在线
消息也需要先通过准入。生产上应优先设置合理的单设备限制隔离异常离线设备；全局
值应结合离线设备数、消息速率、离线时长、平均 Body 大小、PostgreSQL 余量和重试
吞吐量估算。非零全局限制会让所有准入请求竞争同一把数据库锁，启用前应使用预期
负载压测；因此生产示例默认只开启单设备限制。

### 下行终态事件

可靠下行消息进入 `failed`，或管理员把它标记为 `discarded` 时，网关可以异步
发布一条只包含元数据的终态事件。默认不开启：

```yaml
downlink:
  terminal:
    publisher:
      type: nsq
      nsq:
        nsqd_addrs:
          - nsqd:4150
        topic: downlink_terminal_events
        dial_timeout: 1s
        read_timeout: 60s
        write_timeout: 1s
        publish_mode: round_robin
        retry_attempts: 1
    retry_interval: 5s
    retry_delay: 30s
    retry_jitter: 0s
    backoff_multiplier: 2
    max_retry_delay: 5m
    retry_lease: 30s
    scan_limit: 100
```

- `publisher.type` 支持 `none`、`nsq` 和 `http`；`none` 保持 V12.3 之前的
  行为。
- `publisher.nsq` 是有界 NSQ producer 配置，发布端直连 `nsqd`，不通过
  `nsqlookupd`。
- `publisher.http` 把既有的终态事件 envelope 以签名 JSON `POST` 发给接收端。
  它必须使用 PostgreSQL 存储，才能在重启后保留发布重试，并在多网关节点之间
  协调 claim：

  ```yaml
  downlink:
    storage:
      type: postgres
    terminal:
      publisher:
        type: http
        http:
          url: https://terminal-events.example.internal/v1/z-courier
          timeout: 5s
          hmac:
            key_id: gateway-terminal-v1
            secret: ${ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET}
  ```

  默认只接受绝对 `https` URL。本地调试时如确实需要明文 `http` 接收端，必须
  显式设置 `allow_insecure_http: true`，且不能跨越不可信网络。HMAC 的 key ID
  和 secret 使用已有的 `ZCOURIER-HMAC-SHA256` 请求签名协议，secret 至少需要
  32 字节。请求规范和接收端验签方式见[内部 HTTP 签名](internal-http-signing.md)。
- `retry_interval` 是扫描待发布终态事件的周期。
- `retry_delay`、`retry_jitter`、`backoff_multiplier`、`max_retry_delay`
  共同控制独立的发布重试，不会重新触发客户端投递。
- `retry_lease` 是多网关共享存储时的事件认领租约。
- `scan_limit` 限制一次扫描认领的 outbox 事件数量。

启用 publisher 必须同时启用下行存储。PostgreSQL 会在同一个事务里写入消息终态
和 outbox 事件；集群节点通过独立 claim 竞争发布任务。事件投递语义是 at-least-once，
消费者应使用稳定的 `event_id`（或 `message_id + terminal_status`）去重。事件不包含
原始消息 Body、内部 token、HMAC 密钥或 DSN。HTTP publisher 与 NSQ 使用同一个版本化
元数据 envelope，不跟随重定向，只有 `2xx` 响应才算发布成功；超时、网络错误和非
`2xx` 响应都会进入既有的独立发布重试调度。

升级前已经处于终态的旧数据不会补发事件。当前终态事件仍为 `pending` 或 `failed`
时，retention 不会提前删除对应消息。

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
