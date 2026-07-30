# V16 发布验收指南

本文是 `v0.16.0` 的发布验收依据。V16 用可选的命名 token bucket 流量策略替代
“所有请求一套参数”的入站限流，同时保持业务消息体不透明的边界。

## 本版范围

V16 增加：

- 根据鉴权后的 ClientID、协议 MsgID 和已启用 upstream route 做确定性策略选择；
- 并发安全、Key 数有上限、空闲会回收的进程内 token bucket；
- 可选 Redis Store，让多个 gateway 原子共享一份额度，无法判断时 fail closed；
- 稳定的 `rate_limited`、`overloaded`、`admission_unavailable` 结果；
- 低基数指标、脱敏 diagnostics、只读 Console、Grafana、recording rules 和告警；
- 真实 TCP 单节点与双节点 Redis E2E；
- 带严格 schema 和最终配置加载校验的 Compose/Helm local、Redis 部署示例。

流量策略只读取可信协议元数据，不解析业务 Body。ClientID、DeviceID、token、Redis
真实 Key、业务 Body 和原始错误都不会进入指标 label、diagnosis bundle 或告警。

## 兼容性与升级

V16 不修改客户端包格式、保留 MsgID、SDK wire contract、PostgreSQL schema、可靠下行
状态、NSQ topic、Redis 在线路由 Key 或 internal HTTP API，不需要数据迁移。

现有 `pipeline.rate_limit` 会继续使用原来的进程内固定窗口行为。Helm 默认也继续启用
旧限流并关闭 `pipeline.trafficPolicies`。旧限流和命名策略不能同时启用。

滚动升级按以下顺序执行：

1. 保持旧 `rate_limit` 配置，先升级 V16 binary/image。
2. 验证 readiness、AUTH/BIND、正常上行/下行和原有限流基线。
3. 在所有节点加入已审核的流量策略，关闭 `rate_limit`，再次滚动 gateway。
4. Redis 模式下，所有节点必须使用相同 database、Key 前缀、策略、选择器、优先级
   和 bucket；第一个启用策略的 Pod 启动前先确认 Redis 可达。
5. 只引入少量流量，观察策略判断、upstream 成功率/延迟、本地 Key 使用率和 Redis
   可用性，再逐步放量。

不要把包含 `traffic_policies` 的配置直接交给 V16 之前的 binary。严格 YAML 解码会把
它当作未知字段拒绝。正确顺序是先升级 binary，再迁移限流配置。

## 准入语义

优先级数字越大越先匹配；同优先级且可能重叠的策略会导致启动失败。非空 selector
之间是 AND 关系。`default_policy` 留空时，未命中的包直接放行且不创建 bucket。

local 模式通过 `max_keys` 限制进程内存。Key 满时不会淘汰活跃 bucket，否则会重置
额度；清理空闲 bucket 后仍满，则新身份返回 `overloaded`。

Redis 模式使用服务端时间，并在一段 Lua 中原子完成补充与准入。ClientID 在 Key 中
使用 SHA-256 摘要，每个 Key 都有有界 idle TTL。Redis 只支持 `fail_closed`：
Store 无法安全判断时，在 upstream 转发前返回 `admission_unavailable`，不会退化为
各节点独立额度。

流量策略保护的是 gateway 准入路径。backend 仍需自己的并发保护、业务鉴权，以及基于
MessageID 的持久化幂等。

## 部署参考

官方参考分别展示两种模式：

- `deploy/production/config/z-courier.yaml`：单 gateway 的有界 local 策略；
- `deploy/production-cluster/config/` 下两个文件：相同 Redis namespace 和共享策略；
- `values-traffic-policy-local.yaml`、`values-traffic-policy-redis.yaml`：Helm 专用示例；
- `values-production.yaml`：多副本部署，因此使用 Redis。

示例容量和补充速率只是可审核起点，不是万能生产默认值。应根据真实入口突发、持续
速率和下游容量调整。

## 回滚

回滚不需要删除 Redis quota Key，也不需要迁移协议、PostgreSQL、NSQ 或在线路由：

1. 如果拒绝或依赖行为不安全，先停止 canary 放量。
2. 所有节点同时关闭 `traffic_policies` 并恢复旧 `rate_limit`，或者恢复完整的
   last-known-good 配置。
3. 逐步回滚 Pod/container，并验证 readiness、AUTH/BIND、上行 ACK、下行推送/ACK
   和 peer push。
4. Redis quota Key 保留，让 idle TTL 自然清理。
5. 确认流量策略告警恢复，旧限流基线重新稳定。

Redis 故障时不要临时启用 local fallback。它会把一份集群额度放大为每节点独立额度，
在最危险的时候改变准入语义。

## 发布验收矩阵

所有检查必须在准备打 tag 的同一个 commit 上执行。

| 范围 | 必需证据 | 命令或工作流 |
| --- | --- | --- |
| 源码 | 工作区干净、commit 正确、无 tracked secret | `git status --short`、`git log -1 --oneline`、`bash scripts/secret_boundary_check.sh` |
| 单元与 race | selector/config、local 并发与容量、Redis 原子性/TTL、gateway 集成 | `bash scripts/release_check.sh` |
| Local 真实链路 | 突发、补充、优先级、未命中放行、Key 上限、空闲回收、拒绝后不转发 | `bash scripts/e2e_traffic_policy.sh` |
| Redis 真实链路 | 两节点一份额度、正 PTTL 与真实过期、故障关闭、无需重启恢复 | `bash scripts/e2e_traffic_policy_redis.sh` |
| 部署 | Helm 默认/local/Redis schema 与渲染、非法组合、Compose、构建镜像配置加载 | `bash scripts/traffic_policy_deployment_check.sh` |
| 运维 | 脱敏 diagnostics/Console、Grafana、recording rules 与告警行为 | `bash scripts/promtool_check.sh`、`bash scripts/console_smoke.sh` |
| 回归 | HTTP/NSQ、discovery、可靠下行、cluster peer push、SDK、生产 Compose | CI E2E 与 Production Smoke |
| Kubernetes | V16 schema/ConfigMap 下 Helm smoke/E2E 继续通过 | `bash scripts/k8s_helm_smoke.sh`、`bash scripts/k8s_helm_e2e.sh` |
| CI | tag commit 上所有必需 job 全绿 | GitHub Actions summary |

Docker、Composer、Kind 都可用时执行完整验收：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

fast 路径会执行 local 有界 Key E2E；slow Docker 路径会执行双 gateway Redis E2E，
其中包括等待真实 quota Key 到期。若跳过 Redis 或 Kubernetes 路径，不能声称对应
验收已完成，应在发布记录中明确写出待补证据。

## 运维证据

发布记录只保存脱敏证据：

- commit SHA、image digest、chart version 和 workflow URL；
- 启用的策略名、mode、key scope、capacity 和 refill 参数；
- 聚合 `z_courier_traffic_policy_*` 指标与四个内置告警状态；
- local Key 使用率或 Redis Store 状态、canary 时间与回滚结论；
- E2E 和部署检查结果。

不要记录 ClientID、DeviceID、token、Redis 地址或真实 Key、业务 Body、原始内部错误。

## 打 Tag 与发布

验收矩阵完成、CI 在精确 commit 上全绿后：

```bash
git tag -a v0.16.0 -m "v0.16.0"
git push origin v0.16.0
```

从 `v0.16.0` 创建 GitHub Release，使用
[`CHANGELOG.md`](../../CHANGELOG.md) 中的 V16 条目作为 release notes。确认 image 与
Helm 发布 workflow 完成，并保存不可变产物 URL。

只有当 canary 观察窗口稳定，且团队能解释所有有意义的 `rate_limited`、
`overloaded` 和 `admission_unavailable` 事件时，才正式发布 V16。
