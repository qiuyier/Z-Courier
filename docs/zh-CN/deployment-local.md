# 本地开发和验证

本地环境用于验证 Z-Courier 的核心路径：

- PostgreSQL 可靠下行存储。
- Redis 在线路由。
- NSQ upstream。
- Prometheus / Grafana 监控。
- 单节点和两节点 gateway。

## 一条命令跑单节点 E2E

从仓库根目录执行：

```bash
bash scripts/e2e.sh
```

这个脚本会：

1. 启动 `deploy/local/docker-compose.yml` 里的依赖。
2. 使用 `configs/z-courier.integration.yaml` 启动 gateway。
3. 运行 `cmd/e2e`。
4. 验证离线排队、bind 后补发、在线推送、客户端 ACK、NSQ upstream、metrics、
   Go SDK 和 PHP SDK。

## 两节点集群 E2E

```bash
bash scripts/e2e_cluster.sh
```

它会启动两个 gateway：

| 节点 | TCP | Internal HTTP |
| --- | ---: | ---: |
| `gateway-a` | `9901` | `18182` |
| `gateway-b` | `9902` | `18183` |

验证流程：

1. 客户端连接到 `gateway-b`。
2. 后端推送请求发到 `gateway-a`。
3. `gateway-a` 本地找不到连接。
4. `gateway-a` 查 Redis 在线路由，发现客户端在 `gateway-b`。
5. `gateway-a` 通过 peer push 把消息转给 `gateway-b`。
6. `gateway-b` 推送到客户端。

脚本还会验证断线后消息排队、重连后补发，以及 Redis route refresher 能让长时间安静的
连接继续可发现。

## 手动启动依赖

```bash
docker compose -f deploy/local/docker-compose.yml up -d
```

启动 integration gateway：

```bash
ZINX_CONFIG_FILE_PATH=conf/zinx.integration.json \
  go run ./cmd/gateway -config configs/z-courier.integration.yaml
```

运行验证：

```bash
go run ./cmd/e2e
```

## 手动启动两节点

终端 1：

```bash
ZINX_CONFIG_FILE_PATH=conf/zinx.cluster-a.json \
  go run ./cmd/gateway -config configs/z-courier.cluster-a.yaml
```

终端 2：

```bash
ZINX_CONFIG_FILE_PATH=conf/zinx.cluster-b.json \
  go run ./cmd/gateway -config configs/z-courier.cluster-b.yaml
```

两节点本地配置也开启了内置 console：

- `gateway-a`: `http://127.0.0.1:18182/console/`
- `gateway-b`: `http://127.0.0.1:18183/console/`

登录 token 是 `dev-internal-token`。两份配置使用了不同的
`admin_console.session.cookie_name`，避免你在同一个 `127.0.0.1` 域名下登录
A 节点后，又登录 B 节点把 A 节点的 console session cookie 覆盖掉。

浏览器级别的 admin console smoke：

```bash
bash scripts/console_smoke.sh
```

脚本会构建 console 资产，先用 `admin` 角色启动轻量 gateway 并跑 Playwright 检查，
再用 `readonly` 角色重复一次，确认受保护的变更操作在只读模式下不可用。如果本机还没
装 Playwright 浏览器，先执行一次：

```bash
npm --prefix web/admin exec -- playwright install chromium
```

终端 3 运行集群验证：

```bash
go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics \
  -online-push-delay 5s \
  -require-cluster-metrics \
  -expect-route-node gateway-b \
  -expect-route-internal-url http://127.0.0.1:18183 \
  -expect-session-url http://127.0.0.1:18183 \
  -expect-session-node gateway-b \
  -check-reconnect-retry
```

## 手动推送一条下行

先连接一个客户端到 `gateway-b`：

```bash
go run ./cmd/devclient \
  -port 9902 \
  -client-id e2e-client \
  -device-id e2e-device \
  -token e2e-token
```

再向 `gateway-a` 发内部推送：

```bash
go run ./cmd/devbackend push \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device \
  -msg-id 2001 \
  -body "hello from gateway-a"
```

如果集群路由正常，客户端虽然连在 `gateway-b`，仍然能收到来自 `gateway-a` 入口的
推送。

## route 和 sessions 的区别

查询路由：

```bash
go run ./cmd/devbackend route \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device
```

查询本机 sessions：

```bash
go run ./cmd/devbackend sessions \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client
```

`route` 回答“这台 gateway 会把某个 client/device 的下行发到哪里”，会先看本机
session，再看 Redis cluster route。

`sessions` 只列出你查询的这个 gateway 进程本机连接。如果客户端连在 `gateway-b`，
你查 `gateway-a` 的 `sessions` 得到 0 是正常的。

## Load Test Smoke

```bash
bash scripts/loadtest_smoke.sh
```

它会启动 PostgreSQL 和 NSQ，启动一个 integration gateway，然后跑保守的 upstream
和 downlink 压测。报告写到：

```text
reports/loadtest-smoke/
```

手动压测：

```bash
LOADTEST_MODE=upstream \
LOADTEST_DURATION=30s \
LOADTEST_RATE=200 \
LOADTEST_CLIENTS=100 \
LOADTEST_MIN_QPS=1 \
  bash scripts/loadtest_manual.sh
```

生成 Markdown 汇总：

```bash
go run ./cmd/loadreport \
  -output reports/loadtest-manual/summary.md \
  reports/loadtest-manual/*.json
```

## 常见问题

### 为什么 Redis 里没有在线路由？

客户端必须先完成 `AUTH/BIND`，网关才会写在线路由。两节点集群里 route 还会有 TTL，
gateway 会周期刷新。如果进程退出或 client 断开，路由会被清理或自然过期。

### 为什么发到 gateway-a，sessions 查不到？

因为 sessions 是本机连接列表。客户端连在 `gateway-b` 时，`gateway-a` 的 sessions
为空是正常的。用 route 查询才能看到 Redis 里的远端路由。

### NSQ admin 里只看到 depth，没有消费？

上行 NSQ route 的第一步是 producer 把消息写入 NSQ topic。是否消费取决于你有没有
启动 consumer。只验证发送成功时，看 topic message/depth 增加即可。
