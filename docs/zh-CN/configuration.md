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
- `route_msg_ids`：额外注册到 Zinx 的 MsgID。启用的 upstream route 范围和 V17
  reload 接纳范围都会在启动时自动注册。

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
          tls:
            ca_file: /run/secrets/terminal-webhook/ca.crt
            client_cert_file: /run/secrets/terminal-webhook/tls.crt
            client_key_file: /run/secrets/terminal-webhook/tls.key
            server_name: terminal-events.example.internal
  ```

  默认只接受绝对 `https` URL。本地调试时如确实需要明文 `http` 接收端，必须
  显式设置 `allow_insecure_http: true`，且不能跨越不可信网络。HMAC 的 key ID
  和 secret 使用已有的 `ZCOURIER-HMAC-SHA256` 请求签名协议，secret 至少需要
  32 字节。请求规范和接收端验签方式见[内部 HTTP 签名](internal-http-signing.md)。
  可选的 `tls` 配置块用于私有 PKI 和 mTLS。`ca_file` 中的 PEM CA 会加入这个
  publisher 独享的根证书池，同时保留系统 CA；`client_cert_file` 和
  `client_key_file` 必须成对配置，并且证书与私钥必须匹配。`server_name` 可以覆盖
  证书名称校验目标，但不能包含 scheme、端口或路径。最低 TLS 版本固定为 1.2，
  不提供关闭证书校验的选项。网关会在 `-check-config` 和 publisher 启动时分别解析
  文件，并且只有 `https` URL 才能配置 TLS 选项。
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

### 独立路由文件与热加载（V17.1-V17.3）

V17.1 可以从一个独立且有大小上限的 YAML 文件加载整张 upstream route 表。它和
内联的 `upstream.routes` 互斥，不能同时配置。

主网关配置：

```yaml
upstream:
  routes_file:
    path: upstream-routes.v17.yaml
    max_size_bytes: 1048576
    reload:
      enabled: true
      drain_timeout: 30s
      accepted_msg_id_ranges:
        - min: 1001
          max: 1999
        - min: 2000
          max: 2999
```

相对路径以主网关配置文件所在目录为基准。路由文件是严格的版本化文档：

```yaml
version: 1
routes:
  - name: orders-http
    enabled: true
    msg_id_min: 1001
    msg_id_max: 1999
    target:
      type: http
      url: http://orders.internal:8080/gateway/upstream
      token: ${ZCOURIER_UPSTREAM_INTERNAL_TOKEN}
      timeout: 5s
      max_in_flight: 2000
```

独立文件继续复用内联路由已有的 HTTP、DNS、NSQ、范围重叠、保留 MsgID 和
Traffic Policy 校验，并额外保证：

- 只接受一个 `version: 1` 的 YAML 文档；
- 拒绝未知字段、重复或空路由名、超大文件和缺失的环境变量；
- `max_size_bytes` 默认 1 MiB，最大 16 MiB；
- 开启 reload 时，每条已启用路由必须完整落在某一个接纳范围内；
- 接纳范围最多 64 段，总计最多 20,000 个 MsgID。

这些接纳范围会在 Zinx TCP 服务启动前注册，后续不需要并发修改 Zinx 内部的 MsgID
路由 map，同时为未来的路由代际预留可用 MsgID。

V17.2 已在 `reload.enabled: true` 时启用原子 generation manager。每个普通上行
请求会在 route-aware Traffic Policy 选择之前取得一个不可变 generation 的 lease，
并一直持有到转发和 ACK 处理结束。旧 generation 只有在最后一个 lease 归还后才会
关闭；`drain_timeout` 只记录运维告警，不会强制中断请求。只要还有一个 generation
处于 retiring 状态，下一次切换就会被拒绝，从而限制保留资源的数量。

内联路由以及没有开启 reload 的路由文件仍走原来的 `router.Engine` 静态快路径，
不会为每个请求增加 generation lease。

V17.3 已接入显式热加载入口。只修改文件**不会**改变当前生效的路由表。可以先通过
运维 CLI 检查候选配置，再激活：

```bash
go run ./cmd/admin routes status \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin routes validate \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -expected-generation 1

go run ./cmd/admin routes reload \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -expected-generation 1 \
  -confirm
```

`validate` 会读取、校验并完整构建固定路径里的候选配置，随后关闭候选 generation，
不会切换流量。`reload` 会重新执行同一套完整流程，并原子激活候选 generation。
`-expected-generation` 可以省略，但建议始终携带；如果当前 generation 已经变化，
网关会在读取路由文件前拒绝这次过期操作。Console 的 Routes 页面向 `operator` 和
`admin` 提供同样的 Dry Run 与确认激活操作，`readonly` 只能查看状态。

内部 HTTP 契约是：

```text
GET  /internal/admin/routes/status
POST /internal/admin/routes/reload
```

POST Body 只能是 `{"dry_run":true,"expected_generation":1}` 或
`{"dry_run":false,"expected_generation":1}`，不能携带路由正文、文件路径或目标
覆盖。稳定错误码包括 `reload_disabled`、`reload_busy`、
`generation_conflict`、`invalid_candidate`、`candidate_build_failed` 和
`reload_failed`。

Dry Run 通过后，也可以直接向网关进程发送 `SIGHUP` 来激活固定文件：

```bash
kill -HUP <gateway-pid>
```

`SIGHUP` 不会影响 SIGINT/SIGTERM 的优雅关闭。API、CLI 和 signal 触发会统一串行，
每次成功、拒绝或失败都会写审计记录，且不会记录路由凭据、原始文件路径和完整端点
URL。激活只作用于当前 gateway 节点；集群发布需要逐节点 Dry Run、canary 和分批
reload。扩大 accepted MsgID 范围、修改 Traffic Policy 定义或任何非路由配置仍然
需要重启。

可运行的源码示例：

- `configs/z-courier.routes-file.yaml`
- `configs/upstream-routes.v17.yaml`

校验命令：

```bash
go run ./cmd/gateway \
  -config configs/z-courier.routes-file.yaml \
  -check-config
```

### HTTP 服务发现配置（V15.1-V15.4.1）

原有 `target.url` 仍是当前可运行的单端点模式。V15.1 先确定并严格校验 HTTP
服务发现的配置契约；V15.2.1 已让静态发现可以运行，包括不可变端点快照、并发安全
轮询和进程内 cooldown；V15.2.2 已支持可定时刷新的 DNS A/AAAA 发现，并在解析
短暂失败时保留最近一次成功快照。

静态发现直接填写完整的后端 URL：

```yaml
upstream:
  routes:
    - name: orders-http
      enabled: true
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        discovery:
          type: static
          endpoints:
            - http://orders-a:8080/gateway/upstream
            - http://orders-b:8080/gateway/upstream
        timeout: 2s
        failover:
          enabled: true
          max_attempts: 2
          unhealthy_cooldown: 15s
```

DNS 发现使用协议、解析出的地址、端口和 route 级路径拼出端点 URL：

```yaml
target:
  type: http
  path: /gateway/upstream
  discovery:
    type: dns
    scheme: http
    hostname: orders.default.svc.cluster.local
    port: 8080
    refresh_interval: 10s
  timeout: 2s
  failover:
    enabled: true
    max_attempts: 2
    unhealthy_cooldown: 15s
```

规则和默认值：

- `discovery.type` 只能是 `static` 或 `dns`，并且不能和 `url` 同时配置。
- 静态端点必须是互不重复的完整 `http` / `https` URL，路径直接写在各 URL 中；
  每次请求按 round-robin 顺序选择端点。
- DNS 模式必须提供 `scheme`、`hostname` 和 `1` 到 `65535` 的端口；`path`
  默认 `/`；`refresh_interval` 默认 `30s`，范围为 `1s` 到 `1h`。
- 每个 gateway 进程启动后都会立即在后台执行首次 DNS 查询；如果第一条消息到达时
  查询尚未完成，它会在查询时限内等待。首次成功前没有可用端点时，转发会返回明确的
  无可用端点错误。
- 每次成功刷新会原子替换不可变端点快照。查询失败或返回空结果时继续使用最近一次
  成功快照；后续成功查询中已消失的地址，也会从端点选择器的 cooldown 状态中清理。
- DNS 解析出的 IP 只作为实际连接地址；配置的逻辑域名仍作为 HTTP `Host` 请求头
  和 HTTPS TLS SNI，因此 IPv4、IPv6、虚拟主机和证书校验都能保持正确。
- `failover` 只能搭配服务发现使用。启用后 `max_attempts` 默认 `2`，范围为
  `2` 到 `4`；`unhealthy_cooldown` 默认 `15s`，范围为 `1s` 到 `10m`。
  静态发现的 `max_attempts` 不能超过已配置的端点数量。
- 未启用 failover 时，每条消息只选择一个端点并发送一次。启用后，只有在收到
  响应头之前发生的传输错误才能尝试另一个尚未尝试的端点；失败端点进入当前 gateway
  进程内的 cooldown，有其他健康端点时会被跳过。
- `timeout` 对每一次端点尝试分别生效，应结合 `max_attempts` 一起设置，确保最坏
  情况下的总延迟仍在网关请求预算内。
- Kubernetes 普通 Service 通常只解析出虚拟 ClusterIP；Headless Service
  可以返回多个 Pod 地址，端点选择器才会看到多个候选地址。解析结果和刷新状态属于
  当前进程，因此每个 gateway 节点都会独立解析。
- `refresh_interval` 是 gateway 主动查询的周期，不等同于 DNS 权威 TTL；应根据
  端点变化速度和 DNS 负载进行设置。
- 只要已经收到 HTTP 响应，包括 `5xx`，默认就不自动重放；重要业务仍应在
  backend 端用 `MessageID` 做幂等。

V15.3.1 会记录最终转发决策，但不会通过客户端 ACK 暴露内部 URL、响应内容或
底层网络错误：

- `failure_class` 包括 `encoding`、`discovery`、`request`、`transport`、
  `timeout`、`canceled` 和 `response`。
- `failover_decision` 包括 `disabled`、`not_retryable`、`exhausted` 和
  `no_alternate`。
- gateway 结构化日志会记录 route、target type、脱敏后的 endpoint、
  `attempt_count`、`max_attempts` 以及是否发生切换；endpoint 中的用户信息、
  查询参数和 fragment 会被移除。
- 上行转发被拒绝时，客户端只收到稳定的 `upstream_failed` 原因。它不代表
  backend 一定没有看到之前的请求；客户端重试必须复用同一个 `MessageID`，
  业务幂等仍由 backend 负责。

V15.4.1 通过 Prometheus 暴露服务发现和故障切换状态：

- `z_courier_upstream_discovery_refresh_total` 和
  `z_courier_upstream_discovery_refresh_duration_seconds` 记录 DNS 刷新的
  `success`、`error`、`empty` 结果及耗时。
- `z_courier_upstream_discovery_resolved_endpoints` 表示当前静态快照或 DNS
  最近一次成功快照中的端点数量。
- `z_courier_upstream_endpoint_selection_total` 记录 `selected`、
  `resolver_error`、`no_available` 三种端点选择结果。
- `z_courier_upstream_endpoint_cooldown_skipped_total` 和
  `z_courier_upstream_endpoint_unhealthy` 分别表示因进程内 cooldown 被跳过的
  端点次数，以及当前被标记为不健康的端点数量。
- `z_courier_upstream_endpoint_failure_total` 按有限枚举
  `failure_class` 汇总实际端点尝试失败。
- `z_courier_upstream_discovery_attempts` 是每条发现式上行消息实际使用端点
  尝试次数的直方图，并区分 `success` 与 `failure`。
- `z_courier_upstream_failover_total` 汇总最终切换决策，包括 `succeeded`、
  `disabled`、`not_retryable`、`exhausted` 和 `no_alternate`。

这些指标只使用 route 名、发现类型以及有限的结果、分类或决策作为 label。
解析出的 IP、域名、内部 URL、token、原始错误和消息标识都不会成为指标 label。
生产配置中的 route 名也应保持为有限集合，避免产生不必要的 Prometheus 高基数。

V15.4.2 还会在 `GET /internal/admin/diagnostics` 和 diagnosis bundle 的发现式
HTTP route 下返回嵌套的 `discovery` 对象。它包含当前已解析端点数、不健康端点数，
以及最近一次刷新、选择、cooldown 跳过、端点失败分类、转发结果、尝试次数和
failover 决策。这些都是当前 gateway 进程内的被动观测；读取 diagnostics 不会主动
执行 DNS 查询或 backend 探测，也不会返回端点 IP、域名、URL、token 或原始错误。
某类事件尚未发生时，对应字段和时间戳会被省略。diagnosis bundle 会嵌入同一份
discovery 快照；其中独立的 route 配置分区仍沿用既有的 URL 脱敏规则。

V15.4.4 把同一套配置契约接入部署参考：

- production Compose 配置使用两个明确的静态 backend URL；
- Helm 提供 `examples/values-static-discovery.yaml` 和
  `examples/values-dns-discovery.yaml`；
- `examples/values-production.yaml` 展示 Kubernetes Headless Service DNS；
- `bash scripts/discovery_deployment_check.sh` 会校验 schema、渲染后的
  ConfigMap、错误组合，以及两份生成配置能否被真实 gateway 加载。

Docker 镜像 CI 还会改用刚构建的镜像重新执行这项检查，因此示例既对源码中的配置
加载器负责，也对最终镜像中的 gateway 二进制负责。

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
    enabled: false
    max_requests: 100
    window: 1s
  traffic_policies:
    enabled: false
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: ""
    policies: []
```

pipeline 是通用网关能力所在的位置：黑白名单、限流、日志、指标、鉴权状态检查。

入站处理顺序如下：

```text
鉴权 -> 黑白名单 -> 旧版限流或流量策略 -> Session 绑定 -> 访问日志
```

`rate_limit` 是为兼容旧配置保留的进程内、按 ClientID 固定窗口限流器。
`rate_limit` 和 `traffic_policies` 不能同时启用。

### 命名流量策略

流量策略只根据鉴权后的 `client_id`、协议头 MsgID，以及由 MsgID 解析出的上游
路由选择命名 token bucket，不读取业务 `Body`。单节点可以选择有界进程内
Store；多个 gateway 需要共享同一份额度时可以选择 Redis。

```yaml
pipeline:
  rate_limit:
    enabled: false
  traffic_policies:
    enabled: true
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: ""
    policies:
      - name: standard-upstream
        priority: 100
        match:
          msg_id_min: 1001
          msg_id_max: 2999
        key: client_id
        token_bucket:
          capacity: 100
          refill_tokens: 100
          refill_interval: 1s
      - name: orders
        priority: 200
        match:
          client_ids: [priority-client]
          routes: [orders-http]
        key: client_id
        token_bucket:
          capacity: 20
          refill_tokens: 20
          refill_interval: 1s
```

- `mode`：`local` 使用进程内 Store；`redis` 让使用相同 Redis database 和
  `key_prefix` 的 gateway 共享一份原子配额。
- `max_keys`：当前 gateway 进程最多保留的 `(策略, client_id)` 桶数量。
  仅用于 `local` 模式；`0` 使用默认值 `100000`，负数非法。
- `idle_ttl`：桶持续空闲超过该时间后删除，默认 `10m`。
- `default_policy`：可选兜底策略名。留空时，没有命中任何选择器的包直接放行，
  不创建桶。
- `policies[].enabled`：默认 `true`。禁用策略仍接受校验，但不参与选择。
- `priority`：数字越大优先级越高。同优先级策略只有在确实可能同时命中时，
  才会因歧义而启动失败。
- `match`：非空维度之间是 AND 关系。`client_ids` 使用鉴权身份，MsgID
  范围包含两端，`routes` 必须引用已启用的上游路由。两个 MsgID 边界都省略
  表示任意 MsgID；只设置 `msg_id_min` 表示仅匹配该单个 MsgID。
- `key`：V16.1 只支持 `client_id`。Session 绑定前客户端声明的 DeviceID
  尚未成为可信身份，因此暂不开放设备维度，避免通过更换 DeviceID 绕过配额。
- `token_bucket`：新 Key 初始拥有 `capacity` 个令牌，并按照每
  `refill_interval` 补充 `refill_tokens` 的速率连续恢复。

在 local 模式下，当 `max_keys` 已满时，新 Key 返回 `overloaded`；系统不会
淘汰仍活跃的桶，否则会导致配额被重置。已存在的桶没有令牌时返回
`rate_limited`。容量判断前会先清理已经超过 `idle_ttl` 的桶。

Redis 模式沿用同一份策略和选择器：

```yaml
pipeline:
  traffic_policies:
    enabled: true
    mode: redis
    idle_ttl: 10m
    redis:
      addr: redis:6379
      username: ""
      password: ""
      db: 0
      key_prefix: zcourier:traffic-policy
      dial_timeout: 1s
      read_timeout: 500ms
      write_timeout: 500ms
      operation_timeout: 250ms
      failure_mode: fail_closed
    policies:
      - name: shared-upstream
        priority: 100
        match:
          msg_id_min: 1001
          msg_id_max: 2999
        key: client_id
        token_bucket:
          capacity: 100
          refill_tokens: 100
          refill_interval: 1s
```

`redis.addr` 必填，`redis.db` 不能为负数，Key 前缀不能是空白字符串。连接和
操作超时必须为正数，默认值就是上面展示的值；Redis 模式的 `idle_ttl`
不能小于 `1ms`。Redis 模式只支持 `failure_mode: fail_closed`，不接受本地
fallback，因为 fallback 会在 Redis 故障时把一份集群共享额度悄悄变成各节点
独立额度。

Redis Store 在一段 Lua 脚本内完成补充令牌和准入判断，并使用 Redis 服务端
时间，gateway 之间的时钟偏差不会制造额外令牌。Key 按前缀、策略和作用域隔离，
ClientID 部分使用 SHA-256 摘要，不会以明文进入 Key。每次判断都会刷新有界
TTL。Redis 操作有硬超时且客户端不做隐藏重试；故障时返回
`admission_unavailable`，Redis 恢复后的下一次请求可以继续共享限流，无需
重启 Store。

Gateway 构造时会创建 Redis Store，并在开放服务前执行有超时边界的 `PING`。
启动探活失败时 Gateway 不会开始服务；优雅关闭会先停止 TCP 入站，再关闭
Redis 客户端。`gateway -check-config` 只校验语法和配置约束，不会连接 Redis。

这些语义同时由内存 Redis 测试和可选的真实 Redis 集成测试覆盖：

```bash
ZCOURIER_TEST_REDIS_ADDR=127.0.0.1:16379 \
  go test -count=1 -v ./internal/pipeline \
  -run TestRedisQuotaStoreRealRedisIntegration
```

配额 Store 契约预留 `admission_unavailable`，表示已选中的 Store 无法安全
作出准入决定。合法的 `local` 配置不应产生该结果。Redis 模式会在运行时超时、
连接失败、共享状态非法或 Lua 脚本失败时返回该原因，被拒绝的包不会转发给
上游。

流量策略准入会暴露以下 Prometheus 指标：

```text
z_courier_traffic_policy_selection_total
z_courier_traffic_policy_quota_store_total
z_courier_traffic_policy_quota_store_duration_seconds
z_courier_traffic_policy_local_keys
z_courier_traffic_policy_local_key_limit
```

`selection_total` 用来区分命中某个已配置策略和 `no_match` 直接放行。
配额 Store 计数器和耗时直方图使用稳定的 `allowed`、`rate_limited`、
`overloaded`、`admission_unavailable` 结果。两个 local gauge 分别表示
当前存活桶数量和该进程配置的桶数量上限。原有
`z_courier_rate_limit_rejected_total` 会继续作为兼容的聚合拒绝指标保留。

这些指标的 label 受到严格约束：`mode`、`key_scope`、`result` 都是固定枚举，
`policy` 只来自启动时校验过的静态策略配置。ClientID、DeviceID、token、
Redis Key、消息正文、原始错误和端点地址都不会成为指标 label。

`GET /internal/admin/diagnostics` 的 `traffic_policy` 还会提供流量策略运行快照。
diagnosis bundle 会在 `sections.diagnostics.body.traffic_policy` 中包含同一份
脱敏数据。

- `store_status` 可能是 `disabled`、`not_configured`、`configured`、
  `degraded` 或 `unavailable`。local 模式的 Key 使用率达到 80% 时为
  `degraded`；Redis 最近一次准入返回 `admission_unavailable` 时为
  `unavailable`，后续请求重新得到安全结果后恢复为 `configured`。
- `no_match_total` 和 `decisions` 是当前进程启动以来的聚合计数。
  `last_result`、`last_state` 和有限的时间戳描述最近一次已发生的判断，不会
  为读取接口额外执行探测。
- `local` 展示存活 Key 数、上限和使用率；`policies` 展示静态 bucket 参数，
  以及每个已配置策略的有限聚合选择和判断状态。
- dependencies 会增加 `traffic_policy_store`。warnings 会指出集群中使用
  节点本地配额、runtime 未挂载、最近一次 Store 不可用，以及 local Key
  使用率达到 80% 等情况。

读取 diagnostics 不会向 Redis 发送命令，因此不能把它当成 Redis 当前连通性
探测。计数和时间戳会在 gateway 进程重启后清零。快照不会包含选择器中的
ClientID、Redis 地址、账号、密码、Key 前缀、真实配额 Key、消息 Body 或原始
错误。策略名属于运维可见的静态标识，不要在策略名中写入敏感信息。
Console Diagnostics 会把同一份只读快照展示为 Store 状态、聚合结果、本地容量
和逐策略 bucket 摘要，不提供修改策略的入口。

内置 Grafana dashboard 会展示对应的策略选择、准入结果、Store 延迟和本地
Key 容量指标。Prometheus 默认告警，以及调参、灰度、Redis 故障与回滚流程见
[流量策略准入](production-runbook.md#52-流量策略准入)。

`default_policy` 是真正的兜底，因此也可能限制 AUTH/BIND、下行 ACK 等没有
命中普通上行路由的协议包。只想限制业务上行时，不要设置 `default_policy`，
改用 MsgID 或 route 选择器。禁用的策略不会参与选择，但声明过的字段仍会在
启动时接受严格校验。

本地桶只在单个 gateway 进程内生效，多节点各自拥有一份配额。使用同一 Redis
database、Key 前缀、策略名、Key 作用域和鉴权 ClientID 的 gateway 会共享
同一个 Redis 桶。

可通过真实 gateway TCP 连接和公开 Go SDK 验证本地策略链路：

```bash
bash scripts/e2e_traffic_policy.sh
```

这个无需 Docker 的验证器会检查突发额度耗尽与持续补充、高优先级 route
策略胜出、未匹配流量不创建桶直接放行、有界 Key 容量过载、空闲桶回收、
稳定的拒绝 ACK reason、被拒绝的包不会到达 HTTP upstream，以及预期的
local 准入指标和脱敏 diagnostics。脚本使用 TCP `9941`、内部 HTTP `18201`
和测试 backend `18202`，运行前需保证三个端口空闲。

可通过两个真实 gateway TCP 监听验证 Redis 模式：

```bash
bash scripts/e2e_traffic_policy_redis.sh
```

脚本会启动一个专用、用完即删的 Redis 容器，验证同一 ClientID 在 gateway
A/B 之间共享容量、Redis 故障时返回 `admission_unavailable` 且不转发，以及
Redis 重启后无需重启任一 gateway 就能在下一次请求恢复准入。脚本使用 TCP
`9951`/`9952`、内部 HTTP `18211`/`18213`、backend `18212` 和 Redis
`16389`，同时检查两个 gateway 上预期的 Redis 模式准入指标、故障/恢复
diagnostics 和 Redis 连接信息脱敏。运行前需保证这些端口空闲。
