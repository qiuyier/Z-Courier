# 管理和运维

Z-Courier 的运维面包括：

- internal HTTP admin API。
- `cmd/admin` 命令行工具。
- Web admin console。
- Prometheus metrics。
- Grafana dashboard。
- 诊断 bundle。

所有 `/internal/*` admin API 都走 internal HTTP 的认证模式：

- token 模式：`X-ZCourier-Internal-Token`
- HMAC 模式：Z-Courier HMAC headers

`/healthz`、`/readyz`、`/metrics` 通常不需要认证，方便探针和 Prometheus 抓取。

## 常用 CLI

查看概览：

```bash
go run ./cmd/admin overview \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token
```

运行主动依赖检查：

```bash
go run ./cmd/admin check \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -probe-timeout 2s
```

导出诊断 bundle：

```bash
go run ./cmd/admin diagnose \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -output reports/diagnose/gateway.json
```

带 client/device 一起诊断：

```bash
go run ./cmd/admin diagnose \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -client-id client-1 \
  -device-id device-1 \
  -output reports/diagnose/client-1.json
```

## Web Admin Console

本地默认地址：

```text
http://127.0.0.1:18080/console/
```

控制台提供：

- Overview：节点状态、readiness、session、downlink、dependency summary。
- Routes：upstream route、MsgID 范围、目标类型、运行状态。
- Sessions：本机 session、Redis cluster route 查询，以及受权限保护的本机
  session 断开操作。
- Messages：下行测试推送、消息列表、状态查询、requeue、discard。
- Checks：主动依赖检查。
- Diagnostics：诊断快照和 diagnosis bundle 下载。

生产环境不要公开暴露 console。它是 internal admin plane 的 UI。

如果启用了 `admin_console.session.enabled`，console 可以使用短期 HTTP-only cookie
访问内部 admin/debug/message API。login 仍然需要有效 internal token，或者通过 HMAC
验签的内部请求。当前 session 是 gateway 单节点内存态，重启后需要重新登录。

## Admin API

### `GET /internal/admin/overview`

回答“这个节点现在大概健康吗”。

包含：

- gateway node。
- readiness / draining。
- 本机在线 session 数。
- unique client 数。
- cluster 配置摘要。
- internal HTTP 配置摘要。
- downlink delivery 配置摘要。
- upstream route 数。
- dependency 配置状态。
- admin console monitoring links。

### `GET /internal/admin/diagnostics`

回答“这个节点的运行时状态是什么”。

包含：

- runtime started / uptime。
- readiness。
- sessions。
- auth provider。
- upstream route runtime state。
- capacity 限制。
- downlink 配置。
- warnings。

它不会主动连接外部依赖，只报告进程内已知状态。

### `GET /internal/admin/check`

主动探测依赖：

- PostgreSQL：`PingContext`。
- Redis：`PING`。
- JWKS：刷新公钥。
- HTTP auth provider：`HEAD`。
- HTTP upstream：`HEAD`。
- NSQ upstream：TCP connect。

这是排障时很有用的命令，但它会真的发起网络探测。

### `GET /internal/admin/routes`

列出 upstream route：

- route name。
- `msg_id_min` / `msg_id_max`。
- target type。
- sanitized HTTP URL。
- NSQ addresses/topic。
- max in-flight。

敏感信息不会返回：token、DSN、Redis 密码、NSQ secret、URL query/user-info。

## 消息修复

列出消息：

```bash
go run ./cmd/admin messages \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -status failed \
  -limit 20
```

重新入队：

```bash
go run ./cmd/admin requeue \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -confirm
```

丢弃消息：

```bash
go run ./cmd/admin discard \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -reason "operator decision" \
  -confirm
```

`requeue` 和 `discard` 都需要显式 `-confirm`。`discard` 还需要原因。这样是为了避免误操作。

手动触发一轮 retry scan：

```bash
go run ./cmd/admin retry-scan \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -confirm
```

对应接口是 `POST /internal/messages/retry/scan`。它复用后台 retry worker 的
同一套规则：retry lease、ACK timeout、max attempts、cluster peer push 都不会被
绕过。请求体可以带 `{"limit":100}`；不传或传 `0` 时使用配置里的
`downlink.delivery.scan_limit`。响应会返回 `scanned`、`sent`、`queued`、
`failed` 计数。

这个接口需要 `message:retry_scan` 权限，readonly console session 无法调用。
它会记录 `z_courier_admin_retry_scan_total` 指标，并输出
`admin_retry_scan` 审计日志。

## 会话和路由查询

查询本机 sessions：

```bash
go run ./cmd/admin sessions \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -client-id client-1 \
  -device-id device-1
```

`sessions` 只查询当前 gateway 节点的本机连接。可以按 `session_id` 精确查询，
也可以按 `client_id` 或 `client_id + device_id` 缩小结果范围。

查询 client/device 路由：

```bash
go run ./cmd/admin route \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -client-id client-1 \
  -device-id device-1
```

重点区别：

- `sessions` 只看当前 gateway 本机连接。
- `route` 会看本机 session，也会看 Redis cluster route。

## 安全和脱敏

admin API 和诊断 bundle 不应该泄漏：

- internal token。
- HMAC secret。
- PostgreSQL DSN。
- Redis password。
- upstream token。
- NSQ auth secret。
- Authorization header。
- message body。

如果要把 diagnosis bundle 发到 issue，请仍然先快速扫一眼，确认没有部署侧额外注入的敏感字段。
