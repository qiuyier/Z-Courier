# V15 发布验收指南

本文是 `v0.15.0` 的发布验收依据。V15 为 HTTP 上游路由增加健康感知的 endpoint
发现和有界故障切换，同时保持网关不解析业务消息体的原则。

## 本版范围

V15 为 HTTP upstream route 增加两种可选发现方式：

- `static`：显式完整 URL 列表，适合 Docker、VM，或已有外部服务注册中心的部署；
- `dns`：定期解析内部 hostname 的 A/AAAA 记录，适合 Kubernetes Service、Headless
  Service 和普通内网 DNS。

发现路由会维护不可变的 endpoint 快照，在健康 endpoint 间 round-robin，并在本进程内
记录短暂的失败冷却。只有显式开启 failover 后，响应头之前的连接失败才允许切换 endpoint，
且最多尝试 `max_attempts` 次。已经收到 HTTP 响应（包括 `5xx`）时，网关不会自动重放。

本版还包括发现诊断、低基数 Prometheus 指标、Grafana 面板、告警、Compose/Helm 示例、
无需 Docker 的双 upstream E2E，以及真实 Kind 环境中“Headless Service DNS + backend Pod
替换”的验证。

## 兼容性与升级

V15 不修改客户端包协议、保留 MsgID、PostgreSQL schema、Redis 路由 key、NSQ 上行行为，
也不改变原有单 URL HTTP route。只有把 `target.url` 显式替换为 `target.discovery` 后，
该路由才会启用发现与 failover。

本版不需要数据库迁移。正常升级 gateway 节点后，再逐条启用发现路由；启用前确认：

1. 对重复处理敏感的业务，backend 已按 `MessageID` 做幂等；
2. 所有 gateway 节点都能解析 static endpoint 或 DNS 名称；
3. 每个 endpoint 的 path、token、TLS server name、timeout 和 network policy 都正确；
4. `max_attempts`、`unhealthy_cooldown` 与 backend 可用性和幂等设计匹配。

在 Kubernetes 中，如果希望 gateway 直接选择多个 backend Pod，应使用 Headless Service。
普通 Service 往往只解析为一个 ClusterIP，负载均衡会交给 kube-proxy，而不是 gateway。

## 投递边界

发现提升可用性，但不会提供 exactly-once 上行投递。网络失败发生在 backend 可能已经收到
请求、但 gateway 尚未收到响应头的区间时，业务处理结果可能是不确定的。网关会按路由策略
做有界重试，因此 backend 必须按 `MessageID` 幂等。

收到 HTTP 响应后绝不自动重试。特别是 `500` 表示 backend 已经观察到一次请求，V15 会向
客户端返回上游失败，而不会把它重放给另一个 endpoint。

## 回滚

回滚只涉及配置与二进制：

1. 保留上一版镜像和最后一份已验证的路由配置；
2. 将 discovery route 恢复为原来的 `target.url`，紧急情况下只禁用受影响路由；
3. 逐步回滚 gateway Pod，并验证 readiness、AUTH/BIND、上行 ACK、下行 push/ACK 和
   cluster peer 投递；
4. 不要为了回滚发现能力而删除 PostgreSQL、Redis、NSQ 或 DNS 数据。

unhealthy cooldown 只存在于单个 gateway 进程中，重启即消失；DNS 和 backend load
balancer 才是跨节点的事实来源。

## 发布验收矩阵

所有检查应在准备打 tag 的同一个 commit 上执行。

| 范围 | 必需证据 | 命令或工作流 |
| --- | --- | --- |
| 源码 | 工作区干净、commit 正确、没有被追踪的 secret | `git status --short`、`git log -1 --oneline`、`bash scripts/secret_boundary_check.sh` |
| 快速验证 | Go/race/vet、PHP SDK、Console 构建、配置和 shell 校验 | `bash scripts/release_check.sh` |
| Static discovery | 选择、冷却、有界 failover、MessageID 保持、HTTP `500` 不重放 | `bash scripts/e2e_discovery.sh` |
| DNS discovery | A/AAAA 刷新、last-known-good、endpoint 退役和取消 | `./internal/adapter/httpforwarder` 与 `./internal/server` 的 Go 测试 |
| 运维能力 | 脱敏诊断、Prometheus、Grafana、告警规则和响应手册 | `bash scripts/promtool_check.sh`、Console smoke、面板检查 |
| 部署参考 | Compose static discovery、Helm static/Kubernetes DNS 渲染 | `bash scripts/discovery_deployment_check.sh` |
| Kubernetes | 两个 Pod 的 Headless-Service DNS、Pod 替换、DNS 刷新、无需重启继续转发 | `bash scripts/k8s_helm_e2e.sh` |
| CI | tag commit 上 Validate/E2E 全绿；发布证据要求时手动执行 Kubernetes E2E | GitHub Actions 摘要 |

Docker、Composer 和 Kind 都可用时，执行完整本地验收：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

设置 `ZCOURIER_RELEASE_RUN_K8S=1` 后，会执行
`scripts/k8s_helm_e2e.sh`，其中已包含 V15 Headless-Service DNS backend 替换检查。
若省略该项，不能宣称 Kubernetes 验收已完成，应在发布记录中明确说明缺失的证据。

## 运维证据

发布记录只能保存脱敏信息：

- commit SHA、gateway image digest、chart version 和 workflow URL；
- 启用的发现路由名和类型，不能包含 endpoint URL、原始 DNS 应答、token 或业务 body；
- `z_courier_upstream_discovery_*`、`z_courier_upstream_endpoint_*`、
  `z_courier_upstream_failover_*` 指标快照；
- readiness/rollout 时间、DNS backend 替换结果；
- discovery 告警状态、冷却/endpoint failure 趋势，以及 canary 的回滚结论。

## 打 Tag 与发布

验收矩阵完成、CI 在精确 commit 上全绿后：

```bash
git tag -a v0.15.0 -m "v0.15.0"
git push origin v0.15.0
```

从 `v0.15.0` 创建 GitHub Release，使用
[`CHANGELOG.md`](../../CHANGELOG.md) 中的 V15 条目作为 release notes。确认 image 与 Helm
发布工作流完成，并把产物 URL 保存到发布证据中。

只有当正常上游流量在约定观察窗口内稳定，并且团队能解释 canary 中出现的任何 endpoint
cooldown 或 failover 时，才正式发布 V15。
