# V14 发布验收指南

本文档是 V14 安全与边缘交付工作的发布验收依据。V14 不修改客户端包格式、PostgreSQL
schema、Redis 路由 key 或 NSQ topic；它新增的是可选 TLS/mTLS 部署参考、HMAC 重叠支持
和运维验收能力。

## 兼容性与回滚

原有私网明文部署和普通 HTTPS 部署继续兼容。TLS 在选择的边缘代理或平台 listener
终止，边缘后的 gateway 包协议不变。

HMAC 与证书回滚只涉及配置：

- 恢复上一个 active HMAC signer，并在整个回滚滚动期间继续接受旧、新 key ID；
- 每次只恢复一个 edge 实例的旧证书和旧 trust bundle；
- 不要为解决签名或 TLS 事故而删除 downlink 行、Redis route、NSQ 消息或数据库对象。

具体顺序见[密钥与证书轮换操作手册](rotation-runbook.md)。

## 发布验收矩阵

所有检查都应在准备打 tag 的同一个 commit 上执行。

| 范围 | 必需证据 | 命令或工作流 |
| --- | --- | --- |
| 源码 | 工作区干净、commit 正确、无被追踪的运行时证书/私钥 | `git status --short`、`git log -1 --oneline`、`bash scripts/secret_boundary_check.sh` |
| 快速验证 | Go、race、vet、PHP SDK、admin 构建、配置、shell 语法 | `bash scripts/release_check.sh` |
| HMAC 重叠 | Helm 渲染 active 与 previous 验签 key，ConfigMap 不含 secret 字节 | `bash scripts/helm_hmac_rotation_check.sh` |
| Terminal webhook | 私有 CA、mTLS client cert、HMAC 签名与重试 | `bash scripts/e2e.sh`、`bash scripts/compose_terminal_webhook_tls_check.sh` |
| 集群轮换 | old/new terminal webhook key、peer HMAC、共享存储与路由投递 | `bash scripts/e2e_cluster.sh` |
| 边缘策略 | Nginx/Caddy、Secret mount、Console 白名单、私有 mTLS 路径 | `bash scripts/edge_proxy_check.sh` |
| 边缘运行态 | Console HTTPS、Go SDK TCP TLS、Caddy HTTPS、私有 mTLS listener | `bash scripts/edge_proxy_smoke.sh` |
| 证书轮换 | old/new server 与 client CA 重叠、旧信任退役、Nginx reload 回滚 | `bash scripts/certificate_rotation_smoke.sh` |
| Compose 与生产参考 | Compose 渲染，单节点与集群生产 smoke | `docker compose ... config`、`bash scripts/production_smoke.sh`、`bash scripts/production_cluster_smoke.sh` |
| Helm 与 Kubernetes | lint、package、HMAC/terminal TLS 渲染、可选 kind smoke/E2E | `bash scripts/k8s_helm_smoke.sh`、`bash scripts/k8s_helm_e2e.sh` |
| CI | Validate、E2E、image、Helm/package、release 工作流在 tag commit 上全绿 | GitHub Actions 摘要 |

完整本地验收命令：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

只有在明确延期 kind/Helm 验收并在发布证据中记录原因时，才省略
`ZCOURIER_RELEASE_RUN_K8S=1`。

## 安全证据

发布记录只附非敏感证据：

- gateway image、Helm chart、commit SHA 与 workflow URL；
- Secret 或证书管理器版本标识、active key ID、证书序列号；
- readiness、rollout 时间；
- 签名结果、peer 投递、terminal 发布、TLS/edge 错误和连接健康指标快照；
- 轮换开始、退役和回滚决策的时间。

不能附 secret 值、私钥、完整签名请求头、客户端 token 或 PEM 内容。

## 发布决策

只有在验收矩阵完成、CI 在精确 tag commit 上全绿、正常流量和安全指标经过约定观察窗口
保持稳定，并且上一版 signer/证书材料仍可通过批准的 secret 或证书管理器恢复时，才发布
V14。
