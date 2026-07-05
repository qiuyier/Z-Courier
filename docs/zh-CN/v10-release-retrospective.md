# v0.10.0 发布复盘和使用说明

本文记录 `v0.10.0` 的发布结果、验收情况、主要变化和推荐使用方式。它偏实操，适合在发布后回看，也适合作为部署 `v0.10.0` 的快速参考。

## 发布结论

`v0.10.0` 已正式发布。

| 项目 | 结果 |
| --- | --- |
| GitHub Release | `v0.10.0`，正式 release，非 prerelease |
| Tag | `v0.10.0` |
| 发布提交 | `c403fa0` |
| 发布时间 | 2026-07-05 |
| Gateway 镜像 | `ghcr.io/qiuyier/z-courier-gateway:v0.10.0` |
| Helm chart | `ghcr.io/qiuyier/charts/z-courier:0.5.0` |
| Release 附件 | `z-courier-0.5.0.tgz`、`SHA256SUMS` |

发布后验收通过：

- Release workflow 全部成功：Docker Image、Helm Chart、Helm OCI。
- Tag push CI 全部成功：Validate、Docker Image、Load Test Smoke、E2E、Production Smoke。
- Docker 镜像 manifest 可查询，包含 `linux/amd64` 和 `linux/arm64`。
- Helm OCI chart `0.5.0` 可以从 GHCR 正常拉取。

## 这一版解决了什么

`v0.10.0` 的核心目标是把 Web 管理控制台从“只读观察面”推进到“受控操作面”。

主要变化：

- Web 控制台支持登录、会话恢复、登出。
- 控制台会话使用 HTTP-only cookie，不再把内部 token 长期放在浏览器存储里。
- 引入 `readonly`、`operator`、`admin` 三类控制台角色。
- 后端对消息修复、retry scan、session disconnect、test push 等敏感操作做服务端权限校验。
- 控制台会根据权限展示禁用态和权限提示。
- 支持 session 查询、选中 session 详情、集群 route lookup、本地 session 断开。
- 支持下行 debug test push。
- 支持 retry/offline queue 查看、消息查询、requeue、discard、manual retry scan。
- 增加内存级 admin audit trail，记录登录、登出、权限拒绝、消息修复、test push、retry scan、session disconnect 等操作。
- 增加浏览器 smoke 测试，覆盖登录、核心页面、确认弹窗、retry scan、test push、readonly 禁用态等路径。

## 没有改变什么

这版没有改变客户端协议和可靠投递模型。

- Packet version 仍然是 `1`。
- 保留 MsgID 不变：`1` 是网关 ACK，`2` 是下行 delivery ACK，`1000` 是 AUTH/BIND。
- Go SDK、PHP SDK、后端 SDK 的协议格式不需要迁移。
- PostgreSQL 下行消息表不需要迁移。
- Redis 在线路由模型不变。
- HTTP/NSQ upstream route 模型不变。
- Z-Courier 仍然是 at-least-once 投递，业务侧仍应使用 `MessageID` 做持久化幂等。

## 发布产物怎么用

### Docker 镜像

拉取固定版本镜像：

```bash
docker pull ghcr.io/qiuyier/z-courier-gateway:v0.10.0
```

查看镜像平台：

```bash
docker buildx imagetools inspect ghcr.io/qiuyier/z-courier-gateway:v0.10.0
```

生产环境建议固定 `v0.10.0`，不要依赖浮动标签。

### Helm chart

拉取 OCI chart：

```bash
helm pull oci://ghcr.io/qiuyier/charts/z-courier --version 0.5.0
```

安装或升级：

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.5.0 \
  --namespace z-courier \
  -f values-production.yaml \
  --set image.tag=v0.10.0
```

注意：

- `--version 0.5.0` 是 Helm chart 版本。
- `image.tag=v0.10.0` 是 gateway 镜像版本。
- 生产环境应同时固定 chart version 和 image tag。

### 生产 Compose 参考

如果使用仓库里的生产 Compose 参考，推荐先切到 tag：

```bash
git checkout v0.10.0
cp deploy/production/.env.example deploy/production/.env
```

修改 `deploy/production/.env` 里的 secret 后启动：

```bash
docker compose --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml up -d --build
```

两节点生产参考：

```bash
cp deploy/production-cluster/.env.example deploy/production-cluster/.env

docker compose --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml up -d --build
```

Compose 参考默认会从当前代码构建本地镜像。若部署方希望直接使用 GHCR 发布镜像，可以在自己的 Compose 文件中把 gateway image 固定为：

```yaml
image: ghcr.io/qiuyier/z-courier-gateway:v0.10.0
```

## 如何启用 Web 控制台

控制台仍然是内部运维面，不能直接暴露公网。推荐放在 VPN、堡垒机、私有 ingress、内网反向代理或带认证的网关后面。

配置示例：

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  session:
    enabled: true
    ttl: 8h
    cookie_name: zcourier_admin_session
    cookie_secure: true
    cookie_same_site: lax
    role: operator
```

本地 HTTP 调试时可以临时使用：

```yaml
admin_console:
  session:
    cookie_secure: false
```

生产 HTTPS 环境应使用：

```yaml
admin_console:
  session:
    cookie_secure: true
```

Helm values 中对应写法：

```yaml
adminConsole:
  enabled: true
  path: /console/
  session:
    enabled: true
    ttl: 8h
    cookieName: zcourier_admin_session
    cookieSecure: true
    cookieSameSite: lax
    role: operator
```

访问地址：

```text
http://<internal-http-host>:18080/console/
```

登录时使用内部 token。登录成功后，浏览器通过 HTTP-only session cookie 调用控制台 API。

## 角色怎么选

| 角色 | 推荐用途 | 能力边界 |
| --- | --- | --- |
| `readonly` | 日常观察、排障查看、演示环境 | 只能查看，不能触发修复、推送、断开连接等变更操作 |
| `operator` | 值班和日常运维 | 可执行受保护的常规操作，例如 test push、retry scan、消息 requeue/discard 等 |
| `admin` | 高权限管理员 | 可执行完整控制台管理操作，包括更高风险的 session 操作 |

第一轮生产启用建议从 `readonly` 开始。确认访问边界、审计记录和页面行为正常后，再切到 `operator` 或 `admin`。

## 升级建议

从 `v0.9.x` 升级到 `v0.10.0` 时建议这样走：

1. 先保持网关、客户端 SDK、upstream route、Redis、PostgreSQL 配置不变。
2. 部署 `v0.10.0`，但先不要把控制台暴露给更多人。
3. 验证 `/healthz`、`/readyz`、`/metrics`。
4. 验证 AUTH/BIND、上行转发、下行推送、下行 ACK、离线重试、重连补发。
5. 集群部署时验证 Redis route lookup 和 peer push。
6. 打开 `admin_console.enabled=true`，先使用 `readonly`。
7. 验证登录、刷新、登出、session 过期。
8. 切换到 `operator` 或 `admin`，验证受保护操作和 audit trail。
9. 观察 Prometheus/Grafana 指标，确认 upstream、downlink、retry、cluster、auth、capacity、admin permission reject 都符合预期。

## 发布前后验证命令

本地快速检查：

```bash
bash scripts/release_check.sh
```

如果本地 Composer 是 Docker 封装的，可以指定 Composer 镜像：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=<composer-image-with-composer> \
bash scripts/release_check.sh
```

Docker-backed 检查：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=<composer-image-with-composer> \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
bash scripts/release_check.sh
```

完整慢速检查：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=<composer-image-with-composer> \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
bash scripts/release_check.sh
```

如果本机是 arm64，并且需要显式指定本地 Docker 构建平台：

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=<composer-image-with-composer> \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_DOCKER_BUILD_PLATFORM=linux/arm64 \
bash scripts/release_check.sh
```

发布后检查：

```bash
gh release view v0.10.0 --json tagName,isDraft,isPrerelease,name,publishedAt,assets,url

docker buildx imagetools inspect ghcr.io/qiuyier/z-courier-gateway:v0.10.0

helm pull oci://ghcr.io/qiuyier/charts/z-courier --version 0.5.0
```

## 回滚建议

`v0.10.0` 没有引入协议和数据库 schema 迁移，所以回滚相对直接。

如果只是控制台异常：

```yaml
admin_console:
  enabled: false
  session:
    enabled: false
```

如果要回滚 gateway：

```yaml
image:
  tag: v0.9.1
```

Helm 回滚：

```bash
helm rollback z-courier <revision> --namespace z-courier
```

回滚后重点验证：

- `/healthz`
- `/readyz`
- `/metrics`
- AUTH/BIND
- upstream forwarding
- downlink push 和 ACK
- retry/offline queue
- Redis route lookup
- peer push

## 这次发布的经验

这次发布比较顺的一点是，发布前已经把 release check、Docker-backed 检查、CI、Helm chart、生产 smoke、E2E 和浏览器 smoke 串起来了。真正发布时，问题主要集中在“确认产物是否已经被 GHCR 消费者可见”，所以发布后增加了 Docker manifest 和 Helm OCI pull 的消费者视角验证。

后续版本可以继续保留这条收口路径：

1. 功能完成后先收文档和 changelog。
2. 本地跑 fast release check。
3. 本地跑 Docker-backed release check。
4. CI 全绿后打 tag。
5. 发布 GitHub Release。
6. 验证 release workflows。
7. 从 GHCR 拉镜像和 Helm chart 做发布后验收。

这样可以避免“代码是绿的，但发布物不可用”的盲区。
