# V12 发布准备与升级回滚指南

本文是目标版本 `v0.12.0` 的发布准备依据。V12 是项目内部阶段名，公开的
SemVer 版本是 `v0.12.0`，不是 `v12.0.0`。

## 本版范围

V12 在不改变客户端协议版本、不解析业务消息体的前提下，完善可靠下行生命周期：

- 后端重复提交可以区分兼容重放与不可变身份冲突；
- 按 MsgID 选择具名投递策略，并把策略快照固化到每条消息；
- 重试次数、存活时间、退避、抖动和 ACK 超时都有明确上限；
- 终态原因持久化，并可通过 PostgreSQL outbox 可靠发布不含消息体的终态事件；
- 支持全局及单设备待投递容量限制；
- 大积压下按客户端和设备公平选择重试任务；
- 控制台、状态 API、审计、指标、告警、诊断和集群 E2E 覆盖新的生命周期。

投递语义仍然是 at-least-once。支付、订单等关键业务仍需在客户端或业务服务中，
基于 `MessageID` 做持久化去重。

## 兼容性

从 `v0.11.0` 升级不需要修改客户端二进制协议：

- 协议版本仍为 `1`；
- 保留 MsgID 仍是网关 ACK `1`、下行投递 ACK `2`、AUTH/BIND `1000`；
- 现有 Go/PHP 协议和客户端 SDK 保持兼容；
- 后端 SDK 新增的返回字段都是兼容性扩展；
- Redis 在线路由、管理会话 key 和 NSQ 上行行为不变；
- memory 下行存储不需要迁移。

PostgreSQL 下行表会发生 schema 变化，但所有变化都是新增列、表和索引，并为
V11 写入路径保留了兼容默认值。

## 部署配置

生产 Compose 参考和 Helm chart 已暴露 V12 投递策略与终态发布配置，但默认不会
开启新行为：

- 生产 Compose 只提供一条禁用的 `production-critical` 策略示例；
- Helm 的 `downlink.policies` 默认为空列表；
- `downlink.terminal.publisher.type` 默认为 `none`；
- Helm chart `0.7.0` 与 gateway 镜像 `v0.12.0` 对齐。

启用策略前必须分配经过审核且互不重叠的 MsgID 区间。共享同一个 PostgreSQL 下行
存储的所有 gateway 节点必须使用相同的策略、容量和终态发布配置。需要导出终态
事件时，把 publisher 改为 `nsq`，配置 NSQD 地址与 topic，并先在预发布环境验证
一次受控策略耗尽。事件不包含业务消息体，consumer 仍必须幂等。

## PostgreSQL Schema 变化

唯一权威迁移文件是
[`internal/downlink/migrations/v0.12.0.sql`](../../internal/downlink/migrations/v0.12.0.sql)。
当 `downlink.storage.postgres.auto_migrate` 为 `true` 时，网关嵌入并执行的就是
这份 SQL，不存在文档 SQL 和代码 SQL 各写一份的问题。

消息表新增字段分为三组：

| 字段组 | 用途 | 老数据行为 |
| --- | --- | --- |
| `identity_fingerprint` | 判断同一 `MessageID` 的不可变身份是否冲突 | 空指纹会在第一次兼容 V12 重放时计算并写回 |
| `policy_*` | 保存消息整个生命周期使用的投递策略 | 空/零快照按兼容旧行为的默认策略处理 |
| `terminal_*` | 保存终态原因和终态事件发布状态 | 老记录没有终态原因，发布状态默认为 `disabled` |

迁移还会创建 `z_courier_downlink_terminal_events` 及其唯一索引、到期扫描索引。
这是事务型 outbox，只保存路由和投递元数据，不保存业务消息体。

网关自动迁移会在同一个 PostgreSQL 事务中执行 SQL，并获取事务级 advisory
lock，避免多个网关节点同时启动时竞争系统表。

## 迁移由谁执行

本地开发和小规模部署可以继续使用自动迁移：

```yaml
downlink:
  storage:
    type: postgres
    postgres:
      auto_migrate: true
```

成熟生产环境建议在发布前由独立迁移任务执行审核过的 SQL，随后以
`auto_migrate: false` 启动网关：

```bash
psql "$ZCOURIER_POSTGRES_DSN" \
  --set ON_ERROR_STOP=1 \
  --single-transaction \
  --file internal/downlink/migrations/v0.12.0.sql
```

必须使用准备打 tag 的同一份源码执行。外部迁移任务不要并发运行；网关中的
advisory lock 只保护由网关自己发起的迁移。SQL 本身可以重复执行，但生产上仍应
明确只有一个迁移负责人，便于控制顺序和保留审计证据。

迁移后可检查关键对象：

```sql
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'z_courier_downlink_messages'
  AND (column_name = 'identity_fingerprint'
    OR column_name LIKE 'policy_%'
    OR column_name LIKE 'terminal_%')
ORDER BY column_name;

SELECT to_regclass(
  current_schema() || '.z_courier_downlink_terminal_events'
) AS terminal_events_table;
```

## 从 V11 升级的推荐顺序

1. 固定并记录当前 V11 镜像、配置、Helm revision 和数据库恢复点。
2. 备份 PostgreSQL，并在测试环境确认备份能够恢复。
3. 在非生产数据库执行 V11 到 V12 迁移兼容测试。
4. 在替换网关二进制之前先应用 V12 PostgreSQL 迁移。
5. 先部署一个 V12 canary，投递策略、终态发布和容量限制暂时保持兼容默认值或关闭。
6. 验证 readiness、AUTH/BIND、上行转发、下行推送/ACK、断线重试、兼容重放、
   冲突拒绝和消息状态查询。
7. 逐个替换其余节点，尽量缩短 V11/V12 混跑时间。
8. 全部节点升级完成后，再依次启用投递策略、终态事件发布、容量限制和公平重试。
9. 制造一次受控的策略耗尽，核对消息终态、outbox 发布、Prometheus 指标和操作审计。
10. 在 canary 流量期间观察容量拒绝、公平选择、终态发布失败、PostgreSQL 延迟和积压。

### 混跑边界

新增 schema 允许 V11 和 V12 同时连接数据库，但两者的可靠性能力并不相同：

- 由 V11 处理的请求不会执行 V12 的不可变身份冲突拒绝；
- 在 V11 中进入终态的消息不会创建 V12 outbox 事件；
- V11 不执行 V12 的容量限制，也不会保存策略快照。

因此，在所有负责写入的网关都升级到 V12 前，不要依赖这些新保证。如果发布窗口
内必须保证这些语义，应暂停后端下行提交，或只把提交请求路由到 V12 canary。

## 回滚到 V11

推荐只回滚二进制和配置，不做破坏性 schema 回滚：

1. 停止开启新的 V12 策略和批量修复操作。
2. 暂停终态事件 publisher，并尽量等待正在执行的发布请求结束。
3. 将网关节点回滚到已固定的 `v0.11.0` 镜像。
4. 恢复 V11 投递配置，保留 V12 新增的 PostgreSQL 列、索引和终态事件表。
5. 验证 readiness、AUTH/BIND、上行、下行推送/ACK、断线重试和集群 peer push。
6. 保留尚未发布的终态事件，等待后续重新升级后恢复处理。

V12 新增列都有兼容默认值，所以 V11 的旧 INSERT 列表仍可写入；V11 也会忽略
终态事件表。事故期间删除新增列或表会额外引入锁表与数据丢失风险，因此项目不提供
也不推荐 destructive downgrade SQL。

回滚期间 V12 语义会停用。V11 新写入的消息没有策略快照，指纹要等后续 V12
读取/重放时才会补齐；在 V11 中进入终态的消息不会自动补发历史 outbox 事件。
如果终态事件完整性很重要，需要记录回滚时间窗口。

## 如何验证迁移

本地 PostgreSQL 容器就绪后执行：

```bash
docker compose -f deploy/local/docker-compose.yml up -d postgres

ZCOURIER_TEST_POSTGRES_DSN='postgres://zcourier:zcourier@127.0.0.1:15432/zcourier?sslmode=disable' \
go test ./internal/downlink \
  -run '^TestPostgresStoreV11SchemaUpgradeAndRollbackCompatibilityIntegration$' \
  -count=1 -v
```

测试会创建隔离的 V11 schema，写入一条升级前消息，重复执行两次 V12 迁移，检查
新增对象、老数据和延迟补指纹逻辑，最后模拟回滚后的 V11 INSERT。双节点
`scripts/e2e_cluster.sh` 启动网关前也会执行同一项验证。

## 发布验收矩阵

所有必选项都要在准备打 tag 的同一个 commit 上执行：

| 范围 | 必须保留的证据 | 命令或 workflow |
| --- | --- | --- |
| 源码 | 工作区干净、`HEAD` 正确 | `git status --short`、`git log -1 --oneline` |
| 快速检查 | Go、race、vet、PHP、前端构建、配置和 shell 语法通过 | `bash scripts/release_check.sh` |
| PostgreSQL 升级 | V11 升级、重复迁移、老数据保留、回滚写兼容 | 上述迁移测试，集群 E2E 也会执行 |
| 单节点生命周期 | 离线入队、重试、ACK、策略耗尽、终态事件 | `bash scripts/e2e.sh` |
| 双节点生命周期 | 共享存储、幂等、容量、公平性、终态发布、修复审计 | `bash scripts/e2e_cluster.sh` |
| 浏览器运维 | 角色权限、受保护修复、readonly 边界 | `bash scripts/console_smoke.sh` |
| 生产参考 | 单节点/集群 Compose 启动与健康 | `bash scripts/production_smoke.sh`、`bash scripts/production_cluster_smoke.sh` |
| Kubernetes | Helm lint/template/package，以及 kind 策略选择、耗尽和终态事件消费 | `bash scripts/k8s_helm_smoke.sh`、`bash scripts/k8s_helm_e2e.sh` |
| 性能 | 审阅压测报告，baseline 比较保持提示性质 | `bash scripts/loadtest_smoke.sh` 和手动压测 workflow |
| GitHub | tag commit 上 CI 与 Kubernetes workflow 全绿 | GitHub Actions 汇总页 |
| 产物 | Docker、Helm 包/OCI chart、校验和及 release notes 正确 | tag 发布 workflow |

完整本地发布验收命令：

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

如果 Composer 只存在于本地 Docker 镜像，再添加：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82
```

上述矩阵未完成、目标 commit 的 CI 未全绿之前，不应创建 `v0.12.0` tag。压测
baseline 比较继续只生成 summary/warning，不作为发布失败门禁。

## 已知边界

- V12 保证网关接收下行请求时的幂等，不保证业务处理 exactly-once。
- 终态事件与外部 NSQ consumer 不是同一事务，可靠性由持久化 outbox 重试提供。
- outbox 只包含元数据，不包含任意业务消息体。
- 全局容量判断会串行化一段 PostgreSQL 决策路径，必须在实际负载压测后再启用。
- 项目刻意不提供破坏性降级 SQL；回滚时应保留 additive schema。
