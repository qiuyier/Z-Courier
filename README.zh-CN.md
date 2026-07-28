# Z-Courier

Z-Courier 是一个基于 `zinx` 的高性能消息推送网关。它的定位不是业务
消息系统本身，而是一个连接接入、消息路由、下行推送和运维治理中间件。

网关只理解协议头里的元数据，例如 `ClientID`、`DeviceID`、`MsgID`、
`MessageID`、`TraceID`、`Token`。真正的业务消息体对网关来说是 opaque
bytes，也就是一段不解析、不改写的字节。

[English README](README.md)

## 核心能力

- 基于 Zinx 的 TCP 长连接接入。
- 显式 `AUTH/BIND`，把连接绑定到经过鉴权确认的 `client_id + device_id`。
- 按 `MsgID` 路由上行消息到 HTTP 或 NSQ 等后端目标。
- 后端通过内部 HTTP API 做下行推送。
- 支持可靠下行：ACK、PostgreSQL 存储、重试、离线排队、重连补发。
- 支持两节点/多节点思路：Redis 在线路由 + gateway peer push。
- 支持静态 token、HTTP auth provider、JWT/JWKS 鉴权。
- 支持内部 HTTP token 或 HMAC 签名认证。
- 提供 Go SDK、PHP SDK、后端 SDK、admin CLI、Prometheus 指标、
  Grafana dashboard、Alertmanager 示例。
- 从 `v0.9.0` 开始提供可选的嵌入式 Web 管理控制台。

## 快速开始

运行本地集成验证：

```bash
bash scripts/e2e.sh
```

运行无需 Docker 的双 HTTP 上游服务发现验证：

```bash
bash scripts/e2e_discovery.sh
```

脚本会启动两个可控 HTTP backend 和一个真实 gateway 进程，通过公开 Go SDK
连接 TCP 入口，验证 round-robin、响应头返回前的有界故障切换、两次尝试复用同一
`MessageID` 和消息体、故障端点 cooldown 与恢复，以及收到 HTTP `500` 后不重放。
脚本使用 TCP `9931`、内部 HTTP `18191` 和 backend 端口 `18192`、`18193`，
运行前需要保证这些端口空闲。

运行两节点集群验证：

```bash
bash scripts/e2e_cluster.sh
```

本地启动 gateway：

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

默认本地端口：

```text
TCP client:      127.0.0.1:8999
Internal HTTP:   127.0.0.1:18080
Admin console:   http://127.0.0.1:18080/console/
Metrics:         http://127.0.0.1:18080/metrics
Health:          http://127.0.0.1:18080/healthz
Readiness:       http://127.0.0.1:18080/readyz
```

## 最小工作流

1. 客户端建立 TCP 连接。
2. 客户端发送 `MsgID = 1000` 的 `AUTH/BIND` 包。
3. 网关验证 token，并把连接绑定到真实 `client_id + device_id`。
4. 客户端发送业务上行包，例如 `MsgID = 2001`。
5. 网关根据 `MsgID` 查找 upstream route，并转发到 HTTP 或 NSQ。
6. 后端需要推送消息时，调用 `/internal/push`。
7. 网关查找目标客户端连接，在线就直接推送，离线就进入可靠存储等待重试。
8. 客户端收到需要 ACK 的下行消息后，发送 `MsgID = 2` 的下行 ACK。

## 中文文档

建议按这个顺序阅读：

- [中文文档索引](docs/zh-CN/README.md)
- [架构说明](docs/zh-CN/architecture.md)
- [配置说明](docs/zh-CN/configuration.md)
- [协议说明](docs/zh-CN/protocol.md)
- [Go SDK 使用](docs/zh-CN/go-sdk.md)
- [内部 HTTP HMAC 签名](docs/zh-CN/internal-http-signing.md)
- [本地开发和验证](docs/zh-CN/deployment-local.md)
- [生产部署参考](docs/zh-CN/deployment-production.md)
- [Kubernetes / Helm 部署](docs/zh-CN/kubernetes-helm.md)
- [管理和运维](docs/zh-CN/admin-ops.md)
- [生产排障手册](docs/zh-CN/production-runbook.md)
- [v0.10.0 发布复盘和使用说明](docs/zh-CN/v10-release-retrospective.md)

## 生产部署

Docker 镜像发布在 GHCR：

```text
ghcr.io/qiuyier/z-courier-gateway:<release-tag>
```

稳定版本也会发布 `latest`，生产环境仍然建议固定版本号：

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.10.0
```

Helm chart 发布到 GHCR OCI：

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.5.0 \
  --namespace z-courier \
  -f values-production.yaml \
  --set image.tag=v0.10.0
```

生产环境不要把 `/console/` 或 `/internal/*` 直接暴露到公网。Web 管理控制台
是内部运维界面，应该放在 VPN、堡垒机、私有 ingress 或带认证的反向代理后面。

## 开发检查

快速发布检查：

```bash
bash scripts/release_check.sh
```

完整发布检查：

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

## License

MIT
