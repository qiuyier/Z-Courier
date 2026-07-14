# 生产部署参考

Z-Courier 提供两个 Compose 参考：

| 目录 | 用途 |
| --- | --- |
| `deploy/production/` | 单节点生产参考 |
| `deploy/production-cluster/` | 两节点生产集群参考 |

这些目录是生产参考，不是可以直接上线的完整平台。真实部署前必须替换 secret、后端地址、
资源限制、网络安全策略和监控告警策略。

## 镜像

发布镜像：

```text
ghcr.io/qiuyier/z-courier-gateway:<release-tag>
```

稳定 release 也会发布：

```text
ghcr.io/qiuyier/z-courier-gateway:latest
```

生产环境建议固定版本号，不要依赖 `latest`：

```text
ghcr.io/qiuyier/z-courier-gateway:v0.12.0
```

## 单节点参考

目录结构：

```text
deploy/production/
  docker-compose.yml
  config/z-courier.yaml
  conf/zinx.json
  prometheus/prometheus.yml
```

包含服务：

- `gateway`
- `postgres`
- `redis`
- `nsqlookupd`
- `nsqd`
- `prometheus`

默认只把 TCP client port 发布到宿主机。internal HTTP 在 Compose 网络内部，地址是：

```text
gateway:18080
```

启动：

```bash
cp deploy/production/.env.example deploy/production/.env
$EDITOR deploy/production/.env

docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  up -d --build
```

停止：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  down
```

删除数据卷：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  down -v
```

## 两节点集群参考

两节点参考会启动 `gateway-a` 和 `gateway-b`，共享：

- PostgreSQL 下行可靠存储。
- Redis 在线路由。
- NSQ upstream。
- Prometheus。

核心路径：

```text
client -> gateway-b
backend -> gateway-a /internal/push
gateway-a -> Redis route lookup
gateway-a -> gateway-b /internal/cluster/push
gateway-b -> client
```

这能验证生产集群里最重要的跨节点推送路径。

## 必填环境变量

复制 `.env.example` 后替换所有值：

| 变量 | 作用 |
| --- | --- |
| `ZCOURIER_POSTGRES_PASSWORD` | PostgreSQL 密码和 gateway DSN |
| `ZCOURIER_REDIS_PASSWORD` | Redis 密码 |
| `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` | gateway 调用 auth backend 的共享 token |
| `ZCOURIER_INTERNAL_HMAC_SECRET` | 后端调用 `/internal/*` 的 HMAC secret |
| `ZCOURIER_PEER_HMAC_SECRET` | gateway peer push 的 HMAC secret |
| `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` | HTTP upstream 可选 token |

backend HMAC 和 peer HMAC 应该使用不同密钥。

## 生产配置重点

生产参考配置使用：

- HTTP auth provider：业务后端拥有 token 语义。
- HMAC internal HTTP：保护 `/internal/*`。
- PostgreSQL downlink store：保存离线消息和重试状态。
- Redis cluster registry：保存在线路由。
- HTTP upstream：`MsgID 1001-1999`。
- NSQ upstream：`MsgID 2000-2999`。
- admin console 默认关闭。

如果没有 `auth-backend` 服务，客户端 AUTH/BIND 会失败，这是预期行为。生产 token
验证应该由你的业务后端或身份服务负责。

## 可靠下行配置

生产 Compose 配置已包含一条默认禁用的 `production-critical` 策略示例，以及默认
关闭的终态发布器：

```yaml
downlink:
  policies:
    - name: production-critical
      enabled: false
      msg_id_min: 3000
      msg_id_max: 3099
      max_attempts: 20
      max_age: 24h
  terminal:
    publisher:
      type: none
```

这不会改变现有投递行为。上线前先规划并审核 MsgID 区间，确认所有启用的策略互不
重叠，再将对应策略设为 `enabled: true`。集群节点共享 PostgreSQL 存储时，策略、
容量和终态发布配置必须保持一致。

需要把策略耗尽事件发布到 NSQ 时，把 `publisher.type` 改为 `nsq`，并检查配置中
预留的 `nsqd_addrs`、topic、超时和重试参数。终态事件不包含业务消息体，consumer
仍应按 `MessageID` 与终态做幂等处理。建议先在预发布环境制造一次受控策略耗尽，
确认 outbox 最终变为 `published`，再在生产启用。

## 端口和安全边界

| 端口 | 用途 |
| ---: | --- |
| `8999` | TCP 客户端接入 |
| `18080` | internal HTTP、metrics、health、admin API、console |

生产环境只应该把客户端入口暴露给外部用户。`18080` 属于内部管理面。

不要把这些路径直接暴露到公网：

```text
/internal/*
/console/
/metrics
```

推荐访问方式：

- 私有网络。
- VPN。
- 堡垒机。
- 私有 ingress。
- 带 operator 鉴权的反向代理。
- mTLS 或 service mesh。

## 验证

渲染 Compose 配置：

```bash
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  config
```

构建镜像：

```bash
docker build -t z-courier-gateway:production .
```

单节点 smoke：

```bash
bash scripts/production_smoke.sh
```

两节点 smoke：

```bash
bash scripts/production_cluster_smoke.sh
```

保留 smoke stack：

```bash
PRODUCTION_SMOKE_KEEP_STACK=1 bash scripts/production_smoke.sh
PRODUCTION_CLUSTER_SMOKE_KEEP_STACK=1 bash scripts/production_cluster_smoke.sh
```

## 生产上线建议

上线前至少确认：

- `cmd/gateway -check-config` 通过。
- `/readyz` 正常。
- `/metrics` 被 Prometheus 抓到。
- AUTH/BIND 成功。
- HTTP/NSQ upstream 可用。
- 下行在线推送成功。
- 客户端断线后消息可以入库。
- 客户端重连后 pending 消息可以补发。
- 启用自定义策略时，消息保存了预期的 `policy_name`。
- 启用终态发布时，受控策略耗尽会产生且只产生一份可幂等消费的终态事件。
- 两节点时 peer push 成功。
- Grafana dashboard 和 alert rules 已导入并按实际流量调过阈值。
