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

## 5.1 服务发现无可用端点

如果出现 `ZCourierUpstreamDiscoveryEmpty` 或
`ZCourierUpstreamDiscoveryAllEndpointsUnavailable`，先打开 Console 的
Diagnostics 页面，或者执行：

```bash
go run ./cmd/admin diagnostics \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

相关 PromQL：

```promql
max by (instance, route, discovery_type) (z_courier_upstream_discovery_resolved_endpoints)
max by (instance, route, discovery_type) (z_courier_upstream_endpoint_unhealthy)
sum by (instance, route, discovery_type, result) (rate(z_courier_upstream_discovery_refresh_total[5m]))
sum by (instance, route, discovery_type, result) (rate(z_courier_upstream_endpoint_selection_total[5m]))
sum by (instance, route, discovery_type, failure_class) (rate(z_courier_upstream_endpoint_failure_total[5m]))
sum by (instance, route, discovery_type, decision) (rate(z_courier_upstream_failover_total[5m]))
```

判断方式：

- `resolved_endpoints == 0` 表示当前没有可用快照。DNS 模式检查配置域名、DNS
  可达性以及 A/AAAA 结果；static 模式检查实际运行配置中的 endpoint 列表。
- `unhealthy_endpoints >= resolved_endpoints`，同时持续出现
  `selection{result="no_available"}`，表示当前流量无法选出不在 cooldown 的端点。
  先按 `failure_class` 区分 transport、timeout、response 或 request 问题。
- DNS refresh 为 `error`，但 `resolved_endpoints > 0`，表示 last-known-good
  快照仍在工作。这种情况保留为 dashboard 信号；只有活动快照真的为空才触发
  empty-discovery 告警。
- discovery 和 cooldown 都是进程内状态。集群排障时按 Prometheus `instance`
  分节点比较，并分别查看每个 gateway 的 Diagnostics。

确认 backend 网络和业务幂等之前，不要直接增加 failover attempts。

## 5.2 流量策略准入

命名流量策略会把入口准入结果稳定分成四类：

- `allowed`：命中的策略允许该数据包继续处理。
- `rate_limited`：当前 bucket 没有 token；少量出现可能只是正常的流量整形。
- `overloaded`：local 模式的有界 Key 容量无法接纳新的 Key。
- `admission_unavailable`：Redis Store 无法安全判断，fail-closed 拒绝了该包。

先看 Console Diagnostics 的 `Traffic Policies`，再对照以下 PromQL：

```promql
sum by (instance, mode, policy, result) (rate(z_courier_traffic_policy_selection_total[5m]))
sum by (instance, mode, policy, key_scope, result) (rate(z_courier_traffic_policy_quota_store_total[5m]))
histogram_quantile(0.99, sum by (instance, mode, policy, key_scope, result, le) (rate(z_courier_traffic_policy_quota_store_duration_seconds_bucket[5m])))
100 * max by (instance) (z_courier_traffic_policy_local_keys{mode="local"}) / clamp_min(max by (instance) (z_courier_traffic_policy_local_key_limit{mode="local"}), 1)
```

内置告警不会因为单次正常限流就触发：

| 告警 | 默认条件 | 第一判断 |
| --- | --- | --- |
| `ZCourierTrafficPolicyStoreUnavailable` | Redis `admission_unavailable` 持续 2 分钟 | 共享准入正在 fail-closed |
| `ZCourierTrafficPolicyLocalKeyCapacityHigh` | local Key 使用率 >=80% 持续 10 分钟 | `max_keys` 余量不足 |
| `ZCourierTrafficPolicyOverloaded` | `overloaded` 持续 5 分钟 | 新的 local Key 正在被拒绝 |
| `ZCourierTrafficPolicyRateLimitedRatioHigh` | 决策速率大于 1/s 时，限流比例超过 20% 并持续 10 分钟 | 策略需求长期超过配置速率 |

这些阈值是生产示例。若某个策略本来就用于严格整形，应根据基线调整比例阈值或
通知路由。

调参与灰度步骤：

1. 启用前记录入口速率、upstream 延迟与失败率，以及唯一活跃 ClientID 数量。
2. 先只匹配一条 route 或一段 MsgID。没有评估 AUTH/BIND 与 ACK 影响之前，
   不要直接启用范围很大的 `default_policy`。
3. refill rate 不要超过被保护 backend 或 MQ 的持续处理能力；capacity 只承担
   依赖能够吸收的突发量。
4. local 模式的 `max_keys` 应高于预期并发活跃 Key，同时保留明确内存上限。
   必须按每个 gateway `instance` 看使用率，不能只看集群总和。
5. 多节点需要共享配额时使用 Redis，并在灰度期间保持 database、Key 前缀、
   策略名与策略配置一致。
6. 先运行 `scripts/e2e_traffic_policy.sh` 或
   `scripts/e2e_traffic_policy_redis.sh`，再导入少量真实流量，对比策略结果、
   upstream 成功率和延迟。

Redis 故障处理：

1. 在 dashboard 确认 `admission_unavailable` 和 Store 延迟，并在 Diagnostics
   确认 `store_status=unavailable`。
2. 检查 Redis 网络、认证、延迟、连接上限和 gateway 操作超时。事故记录中
   不要粘贴凭据或真实配额 Key。
3. 不要只把部分节点临时切换为 local；这会把一个共享配额变成多个节点独立
   配额。
4. 恢复 Redis 后发送一个受控请求。后续安全决策会让 Diagnostics 回到
   `configured`；基于 rate 的告警会在回看窗口不再包含不可用结果后恢复。

回滚时，在全部 gateway 上恢复上一份已审核的完整 pipeline 配置。重新启用
旧版 `pipeline.rate_limit` 前，必须先禁用或移除 `traffic_policies`，两者不能
同时启用。最后确认退役策略的 `selection_total` 不再增长、拒绝原因回到基线，
且 upstream 负载仍处于安全范围。

## 5.3 路由热加载异常

先直接查询收到操作的 gateway 节点：

```bash
go run ./cmd/admin routes status \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"

go run ./cmd/admin diagnose \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -output reports/diagnose/route-reload.json
```

根据 `last_attempt.stage` 处理：

| Stage | 首要检查 |
| --- | --- |
| `source_read` | 检查挂载文件、权限、投影或软链接目标和原子替换；网关不会接受请求传入的路径。 |
| `parse` | 检查 YAML 语法、未知字段和只允许一个文档的约束。 |
| `validation` | 检查版本、环境变量、路由名、MsgID 重叠/保留值、reload 接纳范围、目标配置和 Traffic Policy 引用。 |
| `candidate_build` | 查看脱敏后的 gateway 日志和依赖配置；失败不会替换 active generation。 |
| `precondition` | 刷新 status；generation conflict 使用新代际重试，reload busy 等旧代退休后再试。 |
| `activation` 或 `operation` | 检查取消、关闭、超时和 gateway 日志，再确认当前 active generation。 |

旧代退休过慢时，对比 `retiring_for_ms` 与 `drain_timeout_ms`，并查看
`retiring.in_flight`。这通常表示仍有上游请求持有旧 generation lease，不要强制关闭；
应定位慢 HTTP 请求、DNS 路由转发或 NSQ publish，最后一个 lease 归还后旧资源会自动
关闭。

集群代际不一致时，先确认是否正处于预期 canary 窗口。逐节点查询 active fingerprint
和 route 数；如果长时间不收敛，停止继续发布，在每个节点对同一挂载文件执行 Dry Run，
然后统一向前收敛或重新激活上一份已评审文件。单个节点的 Console 不能代表整个集群。

```promql
max by (instance) (z_courier_route_generation)
sum by (instance, trigger, result) (rate(z_courier_route_reload_total[5m]))
z_courier:route_reload:p95_seconds
z_courier:route_retirement_age_seconds
max by (instance) (z_courier_route_retirement_timeout_seconds)
```

被删除路由的可变指标只会保留到旧 generation 完成退休，随后自动删除。如果旧 route
时序仍在，先检查退休状态和 in-flight lease；累计 Counter 历史本来就不会删除。

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
  -client-id client-1 \
  -device-id device-1
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
- route reload status。
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

服务发现：

```promql
max by (instance, route, discovery_type) (z_courier_upstream_discovery_resolved_endpoints)
max by (instance, route, discovery_type) (z_courier_upstream_endpoint_unhealthy)
sum by (instance, route, discovery_type, failure_class) (rate(z_courier_upstream_endpoint_failure_total[5m]))
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
