# TLS 边缘代理部署

V14 的生产边界是让 Nginx、Caddy 或云负载均衡负责公网 TLS，gateway 继续只在私有
网络中监听原始 TCP 和 internal HTTP：

```text
Go/PHP SDK -- TLS --> Nginx stream -- 私网 TCP --> gateway:8999

浏览器 -- HTTPS --> Nginx/Caddy -- Console 白名单 --> gateway:18080

机器调用方 -- 可选 mTLS --> 独立 Nginx listener
           -- 原有 token/HMAC --> gateway:18080
```

TLS 只负责传输加密和证书身份，不替代 AUTH/BIND、admin session、CSRF、内部 HMAC、
peer HMAC 或终态 webhook HMAC。

## 公网路径白名单

Console HTTPS listener 只转发：

- `/console/*`。
- 三个 admin session 接口。
- Web Console 实际使用的 admin、route、session、message、diagnostics 和受保护写操作
  的精确路径。

其他路径直接由代理返回 `404`。下面这些机器接口不会经过公网 Console listener：

```text
/internal/push
/internal/push/batch
/internal/cluster/push
/metrics
/healthz
/readyz
```

代理还限制 HTTP method：读取只能 `GET`，写操作只能 `POST`，静态资源只能
`GET/HEAD`。gateway 内部仍会继续检查 method、角色权限、admin session、CSRF、
token 和 HMAC。

## 本地测试证书

生成 7 天有效的临时 CA、服务端证书和 mTLS 客户端证书：

```bash
bash scripts/generate_edge_test_certs.sh deploy/production/secrets/edge
```

目录结构：

```text
secrets/edge/
  issuer/  # 测试 CA 私钥，绝对不能挂载给代理
  server/  # tls.crt、tls.key、可选 client-ca.crt
  client/  # ca.crt、测试客户端证书和私钥
```

脚本不会覆盖非空目录，生成的证书只能用于本地验证。生产证书必须来自真实 PKI 或
证书管理平台。代理只应挂载 `server/`，不能接触 CA 私钥。

## Nginx：客户端 TCP TLS 与 Console HTTPS

在 `deploy/production/.env` 中设置：

```text
ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test
ZCOURIER_EDGE_TLS_DIR=./secrets/edge/server
ZCOURIER_EDGE_CLIENT_TLS_PORT=8999
ZCOURIER_EDGE_CONSOLE_HTTPS_PORT=8443
```

启动单节点参考：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx.yml \
  up -d --build
```

override 会启用 Console 和 admin session，移除 gateway 原来的明文宿主机端口，只
发布 Nginx 的 TLS 端口。Compose 私网内部仍由 Nginx 访问 `gateway:8999` 和
`gateway:18080`。

Go SDK 使用本地测试 CA 的示例：

```bash
ZCOURIER_CLIENT_TOKEN='<client-token>' go run ./examples/go-client \
  -address 127.0.0.1:8999 \
  -client-id '<client-id>' \
  -device-id '<device-id>' \
  -tls \
  -tls-ca-file deploy/production/secrets/edge/client/ca.crt \
  -tls-server-name edge-proxy.test
```

TLS 内部的 Z-Courier 二进制包格式不变。当前 gateway 不解析 PROXY protocol，因此
参考配置不会向 gateway 发送 PROXY protocol 头。

两节点集群使用：

```bash
bash scripts/generate_edge_test_certs.sh deploy/production-cluster/secrets/edge

docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.edge-nginx.yml \
  up -d --build
```

Nginx 会对长连接 TCP 使用 least-connections。Console 请求可以落到任意节点，因为
生产集群参考使用共享 Redis admin session 和 PostgreSQL audit store。

## Caddy：Console HTTPS

标准 Caddy 参考只负责 Console HTTPS，并自动管理公网证书：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-caddy.yml \
  up -d --build
```

生产环境要把 `ZCOURIER_EDGE_SERVER_NAME` 改成真实 DNS，并把
`ZCOURIER_EDGE_CONSOLE_HTTPS_PORT` 设为 `443`。本地使用刚生成的文件证书时，再加：

```bash
-f deploy/production/docker-compose.edge-caddy-local.yml
```

标准 Caddy 不支持任意原始 TCP 代理，所以客户端 TCP TLS 还需要 Nginx stream、
HAProxy、Envoy、云四层负载均衡，或单独审查过的 Caddy L4 构建。Caddy Compose
override 会把剩余明文 gateway 端口限制在 `127.0.0.1`，不能把它当公网入口。

## Console 登录与 HMAC

代理允许 session login 路径，不代表 TLS 已经完成管理员鉴权：

- token 模式的私有部署可以由浏览器把 internal token 换成短期 HTTP-only session。
- 生产参考仍使用 `internal_http.auth.mode: hmac`。浏览器不能安全持有 HMAC 密钥，
  也不能直接生成签名。需要在登录路径前增加部署侧身份代理：先验证管理员身份，再
  只对上游登录请求签名；或者继续使用 `cmd/admin` 完成 HMAC 运维操作。
- 不要把 `ZCOURIER_INTERNAL_HMAC_SECRET` 放进 Nginx、Caddy、前端 JavaScript、
  ConfigMap 或公开环境变量输出。

即使已经启用 HTTPS 和 admin session，Console 仍建议放在 VPN、私有 ingress 或
其他 operator 访问边界后面。

## 独立 mTLS 机器入口

可选 mTLS listener 是独立服务和独立路由策略，默认只绑定
`127.0.0.1:9443`：

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx-mtls.yml \
  up -d edge-nginx-mtls
```

它只允许 health、metrics、backend push、batch push 和 peer push 的精确路径。客户端
证书只证明传输层身份；backend 和 peer 请求仍必须通过 gateway 原有 token/HMAC。
只有在完成防火墙和调用方身份审查后，才应该修改默认 loopback 绑定。

## Kubernetes 和云负载均衡

Helm chart 不安装 Ingress Controller，也不签发公网证书。Kubernetes 中应保持这些
边界：

1. client Service 放在支持 TCP 的负载均衡后面，由它终止或透传已验证 TLS，再把
   原始 TCP 发到 `8999`。
2. internal Service 保持私有。Console ingress 必须复制精确白名单，不能粗暴放行
   `/internal` 前缀。
3. backend push、peer push、metrics、health、readiness 使用单独的私有路由或
   listener，不能和公网 Console 策略混用。
4. 边缘证书放 Kubernetes Secret 或平台证书管理器，不能把 PEM 内容写进 Helm
   values 或 ConfigMap。
5. Console 流量可能落到多个 Pod 时使用 Redis admin session。
6. 为长连接设置合适的 LB idle timeout 和 drain；gateway 明确支持前不要打开
   PROXY protocol。

不同 Ingress、Gateway API 和云 LB 的 TCP/TLS 注解差异很大，因此项目只给出边界，
不提交绑定某个 controller 的 manifest。

## 验证

静态配置、Compose 合并和 secret 边界：

```bash
bash scripts/edge_proxy_check.sh
```

完整 smoke 会验证 Nginx HTTPS 下的 Console 登录/读取/写操作、公共路径拒绝、经
Nginx TCP TLS 的 SDK AUTH/BIND、Caddy HTTPS 登录，以及独立 mTLS listener：

```bash
bash scripts/edge_proxy_smoke.sh
```

这两项已经接入 CI 和 release checker。证书与私钥只存在于临时或 Git 忽略目录。
