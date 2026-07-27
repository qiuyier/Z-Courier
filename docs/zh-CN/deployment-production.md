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
| `ZCOURIER_ADMIN_CONSOLE_ENABLED` | 基础栈 Console 开关；edge override 会设为 true |
| `ZCOURIER_ADMIN_SESSION_ENABLED` | 基础栈浏览器 session 开关；edge override 会设为 true |
| `ZCOURIER_INTERNAL_HMAC_SECRET` | 后端调用 `/internal/*` 的 HMAC secret |
| `ZCOURIER_PEER_HMAC_SECRET` | gateway peer push 的 HMAC secret |
| `ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET` | 可选的终态 HTTP webhook 出站签名密钥 |
| `ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR` | 单节点可选 webhook TLS 宿主机目录 |
| `ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR_A/B` | 集群两个节点各自的可选 TLS 目录 |
| `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` | HTTP upstream 可选 token |
| `ZCOURIER_EDGE_SERVER_NAME` | edge 证书 DNS 名称 |
| `ZCOURIER_EDGE_TLS_DIR` | edge 服务端证书只读目录 |
| `ZCOURIER_EDGE_CLIENT_TLS_PORT` | Nginx 客户端 TLS 宿主机端口 |
| `ZCOURIER_EDGE_CONSOLE_HTTPS_PORT` | Console HTTPS 宿主机端口 |

backend HMAC、peer HMAC 和终态 webhook HMAC 应该使用不同密钥。

## 生产配置重点

生产参考配置使用：

- HTTP auth provider：业务后端拥有 token 语义。
- HMAC internal HTTP：保护 `/internal/*`。
- PostgreSQL downlink store：保存离线消息和重试状态。
- Redis cluster registry：保存在线路由。
- 静态服务发现 HTTP upstream：`MsgID 1001-1999`。
- NSQ upstream：`MsgID 2000-2999`。
- admin console 默认关闭。

如果没有 `auth-backend` 服务，客户端 AUTH/BIND 会失败，这是预期行为。生产 token
验证应该由你的业务后端或身份服务负责。

HTTP upstream 默认列出 `business-backend-a:8080` 和
`business-backend-b:8080`。它们是 round-robin 的对等活跃端点，不是主备关系。
请把两个服务加入同一个 `zcourier-private` 网络，或替换为真实私网地址。当前
failover 最多尝试两个端点，并且只会在收到响应头之前发生传输失败时切换；已经收到
的 `5xx` 不会自动重放。

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

终态事件可以发到 NSQ，也可以发到带签名的 HTTPS webhook。HTTP 模式需要设置
`ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET`，把参考配置中的 publisher 改为 `http`，
并启用注释里的 `http` 配置块。生产环境不要打开 `allow_insecure_http`。接收端必须
验证 `ZCOURIER-HMAC-SHA256`，并按稳定的 `event_id` 做持久化幂等。终态事件不包含
业务消息体。建议先在预发布环境制造一次受控策略耗尽，确认首次失败会重试且 outbox
最终变为 `published`，再在生产启用。

私有 CA 或 mTLS 模式需要在目录中准备 `ca.crt`、`tls.crt` 和 `tls.key`，开启生产
配置中注释的 `http.tls` 字段，并叠加专用 Compose override。单节点使用：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.terminal-webhook-tls.yml \
  up -d --build
```

集群改用 `deploy/production-cluster` 下的对应文件，并分别设置 A/B 的证书目录。
目录会以只读方式挂载到 `/run/secrets/terminal-webhook`。只使用私有 CA 时可以省略
客户端证书和私钥；使用 mTLS 时二者必须成对存在。`secrets/` 已被 Git 忽略，仍应
限制 `tls.key` 的宿主机文件权限。

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

## TLS 边缘代理

V14 不把证书管理塞进每一个 gateway listener，而是提供显式启用的 Nginx/Caddy
overlay。Nginx 同时负责客户端 TCP TLS 和 Console HTTPS，并移除 gateway 的明文
宿主机端口：

```bash
bash scripts/generate_edge_test_certs.sh deploy/production/secrets/edge

docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx.yml \
  up -d --build
```

两节点把路径换成 `deploy/production-cluster`，并使用该目录下的
`docker-compose.edge-nginx.yml`。生成的证书只能本地测试，生产必须替换为真实 PKI
签发的证书。

标准 Caddy 参考负责自动 Console HTTPS，不负责任意原始 TCP。选择 Caddy 时，客户端
TCP TLS 仍需云四层负载均衡、Nginx stream、HAProxy 或 Envoy。公网 Console listener
只放行前端实际使用的精确路径，backend push、peer push、metrics、health 和 readiness
都会直接返回 `404`。

edge override 会启用 Console 和 admin session，但生产配置仍是 internal HMAC。首次
浏览器登录必须由部署侧身份服务验证 operator 后签名登录请求；Nginx/Caddy 不持有
HMAC secret。完整命令、Caddy 本地证书模式、独立机器 mTLS listener、SDK 连接和
安全边界见 [TLS 边缘代理部署](edge-proxy.md)。

## 验证

渲染 Compose 配置：

```bash
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  config
```

同时验证 Compose 静态发现与 Helm 静态/DNS 示例：

```bash
bash scripts/discovery_deployment_check.sh
```

脚本会执行 Helm schema 校验和渲染，抽取生成的 gateway 配置，验证错误组合会被
拒绝，并用真实 gateway 配置加载器检查静态与 DNS 两种模式。

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
