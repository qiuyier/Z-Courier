# Z-Courier 中文文档

这组文档面向第一次接触 Z-Courier 的开发者和运维同学。英文 release、
roadmap、历史版本文档仍保留在 `docs/` 根目录；这里主要放“怎么用、怎么接、
怎么部署、怎么排障”的中文说明。

## 推荐阅读路径

如果你是第一次看这个项目：

1. [架构说明](architecture.md)：先理解 Z-Courier 解决什么问题，不解决什么问题。
2. [协议说明](protocol.md)：理解客户端和网关之间的包结构、保留 MsgID、ACK。
3. [配置说明](configuration.md)：知道每段 YAML 是干什么的。
4. [本地开发和验证](deployment-local.md)：在本机把 gateway、NSQ、PostgreSQL 跑起来。
5. [Go SDK 使用](go-sdk.md)：用 SDK 写客户端或后端推送代码。

如果你准备上生产：

1. [生产部署参考](deployment-production.md)
2. [Kubernetes / Helm 部署](kubernetes-helm.md)
3. [TLS 边缘代理部署](edge-proxy.md)
4. [内部 HTTP HMAC 签名](internal-http-signing.md)
5. [管理和运维](admin-ops.md)
6. [生产排障手册](production-runbook.md)

## 文档地图

| 文档 | 适合谁读 | 内容 |
| --- | --- | --- |
| [架构说明](architecture.md) | 后端、架构、运维 | 网关职责、上行/下行流程、可靠投递、集群路由 |
| [配置说明](configuration.md) | 后端、运维 | Zinx 配置、gateway YAML、鉴权、内部 HTTP、集群、下行存储、upstream route |
| [协议说明](protocol.md) | 客户端、SDK 作者 | 二进制包格式、保留 MsgID、AUTH/BIND、ACK、下行推送 |
| [Go SDK 使用](go-sdk.md) | Go 开发者 | `protocol`、`client`、`backend` 包的常见用法 |
| [内部 HTTP HMAC 签名](internal-http-signing.md) | 后端、安全、运维 | HMAC 请求签名、重放保护、密钥轮换 |
| [本地开发和验证](deployment-local.md) | 开发者 | 本地 docker compose、e2e、devclient、devbackend |
| [生产部署参考](deployment-production.md) | 运维、SRE | 单节点/两节点生产 Compose、环境变量、端口、安全边界 |
| [Kubernetes / Helm 部署](kubernetes-helm.md) | K8s 用户 | Helm values、Service、StatefulSet、Secret、ServiceMonitor |
| [TLS 边缘代理部署](edge-proxy.md) | 运维、安全、SRE | Nginx TCP TLS、Console HTTPS、Caddy、mTLS、K8s 边界 |
| [管理和运维](admin-ops.md) | 运维、后端 | admin CLI、Web 控制台、消息修复、诊断 bundle |
| [生产排障手册](production-runbook.md) | 运维、值班同学 | 健康检查、路由排查、下行失败、依赖异常、监控指标 |
| [v0.10.0 发布复盘和使用说明](v10-release-retrospective.md) | 开发、运维、发布负责人 | 发布结果、产物、升级、控制台启用、验证、回滚 |
| [V12 发布准备与升级回滚](v12-release.md) | 运维、DBA、发布负责人 | PostgreSQL 迁移、混跑边界、回滚与发布验收矩阵 |

## 版本说明

通用中文版以 `v0.10.0` 时整理的文档集为基础，并持续补充后续版本的重要部署与
发布说明。若中文文档和代码行为冲突，以代码、配置示例和英文主文档为准；欢迎继续
把发现的差异补回中文文档。
