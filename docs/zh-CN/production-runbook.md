# 生产排障手册

这份文档是值班时的快速路径。先判断是 gateway 进程、连接、鉴权、upstream、downlink、
cluster route、依赖，还是部署网络问题。

## 1. 先看健康状态

```bash
curl http://<gateway-internal>:18080/healthz
curl http://<gateway-internal>:18080/readyz
```

含义：

- `/healthz` 正常：进程活着。
- `/readyz` 正常：节点可以接流量。
- `/readyz` 失败且状态为 draining：节点正在优雅关闭，不应继续导入新流量。

## 2. 看概览

```bash
go run ./cmd/admin overview \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

重点看：

- `gateway_node` 是否是你想查的节点。
- `readiness.status`。
- `sessions.online` 和 `sessions.unique_clients`。
- `cluster.enabled`、`registry_type`。
- `downlink.store_configured`。
- `upstream.routes`。
- dependency summary。

## 3. 主动检查依赖

```bash
go run ./cmd/admin check \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -probe-timeout 2s
```

如果失败：

- PostgreSQL 失败：下行可靠存储、retry、ACK 状态会受影响。
- Redis 失败：集群在线路由和跨节点推送会受影响。
- HTTP auth provider 失败：新连接 AUTH/BIND 可能返回 `auth_unavailable`。
- JWKS 失败：JWT 模式可能无法加载新 key。
- HTTP upstream 失败：对应 MsgID 上行转发会失败或 degraded。
- NSQ 失败：NSQ upstream route 会失败。

## 4. 客户端无法连接或绑定失败

检查顺序：

1. TCP 端口是否可达。
2. Zinx `MaxConn` 是否打满。
3. 客户端是否发送了 `MsgID = 1000` 的 AUTH/BIND。
4. token 是否有效。
5. `auth.type` 是 static、http 还是 jwt。
6. HTTP auth provider / JWKS 是否可用。
7. gateway ACK 的 `code` 和 `reason`。

常见 ACK：

- `unauthorized`：token 无效。
- `auth_unavailable`：鉴权服务超时或不可用。
- `decode_failed`：协议包编码错误。

## 5. 上行消息没有到后端

先查 route：

```bash
go run ./cmd/admin routes \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

确认：

- `MsgID` 是否落在启用的 route 范围里。
- route target type 是否正确。
- HTTP upstream URL 是否指向正确服务。
- NSQ topic/address 是否正确。
- route 是否 degraded/unavailable。

相关指标：

```text
z_courier_upstream_forward_total
z_courier_upstream_forward_duration_seconds
z_courier_upstream_route_degraded
z_courier_upstream_inflight
z_courier_upstream_overload_rejected_total
```

## 6. 下行消息没有到客户端

先确认后端 push 返回：

- `sent`：已写入在线连接。
- `queued`：已进入可靠存储，等待 retry 或 bind flush。
- `rejected`：请求有问题或目标不可投递。

查询消息：

```bash
go run ./cmd/admin messages \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -status failed \
  -limit 20
```

单条查询：

```bash
go run ./cmd/admin message \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -message-id message-1
```

重点看：

- `status`。
- `attempts`。
- `next_retry_at`。
- `last_error`。
- `session_id`。
- `ack_required`。

## 7. 客户端在线但推不到

如果是集群部署，先判断 client 在哪个节点：

```bash
go run ./cmd/admin route \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -client-id client-1 \
  -device-id device-1
```

情况：

- `local_session_found=true`：连接就在当前节点。
- `cluster_route_found=true`：连接在其他节点，应该走 peer push。
- 都是 false：客户端离线，或者 Redis 路由过期/未刷新。

再查目标节点 sessions：

```bash
go run ./cmd/admin sessions \
  -internal-url http://<target-gateway-internal>:18080 \
  -client-id client-1
```

## 8. 处理 failed 消息

重新投递：

```bash
go run ./cmd/admin requeue \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -message-id message-1 \
  -confirm
```

确认不再投递：

```bash
go run ./cmd/admin discard \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -message-id message-1 \
  -reason "invalid target after incident review" \
  -confirm
```

不要对业务未确认的消息随意 discard。

## 9. 生成诊断包

```bash
go run ./cmd/admin diagnose \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -client-id client-1 \
  -device-id device-1 \
  -output reports/diagnose/client-1.json
```

诊断包包括：

- overview。
- diagnostics。
- active check。
- routes。
- failed messages。
- sessions。
- route lookup。

设计上会脱敏，但发给别人前仍建议快速检查一遍。

## 10. 常用 PromQL

在线连接：

```promql
z_courier_sessions_online
z_courier_clients_online
```

上行请求速率：

```promql
sum by (route, target_type, result) (rate(z_courier_upstream_forward_total[5m]))
```

下行 push：

```promql
sum by (result) (rate(z_courier_downlink_push_total[5m]))
```

下行 ACK：

```promql
sum by (result) (rate(z_courier_downlink_ack_total[5m]))
```

retry：

```promql
sum by (result) (rate(z_courier_downlink_retry_messages_total[5m]))
```

HMAC：

```promql
sum by (result) (rate(z_courier_internal_http_signature_total[5m]))
sum by (result) (rate(z_courier_cluster_peer_signature_total[5m]))
```

## 11. 判断责任边界

Z-Courier 负责：

- 连接接入。
- 鉴权后的 session 绑定。
- 上行 route 和 forward。
- 下行 push、存储、retry、ACK。
- 集群在线路由和 peer push。

业务系统负责：

- token 发行和验证语义。
- body 格式。
- 业务数据库事务。
- `MessageID` 持久化幂等。
- 消费 MQ。
- 业务处理失败后的补偿。
