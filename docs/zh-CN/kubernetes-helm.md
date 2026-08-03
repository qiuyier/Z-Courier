# Kubernetes / Helm 部署

Helm chart 位于：

```text
deploy/helm/z-courier
```

它只部署 Z-Courier gateway，不安装 PostgreSQL、Redis、NSQ、Prometheus、
Grafana、Alertmanager、Ingress Controller 或 Service Mesh。这些依赖应由你的平台或
专门的 chart 管理。

## 为什么用 StatefulSet

Z-Courier 的 TCP 连接存在某个具体 gateway 进程里。下行 peer push 必须打到持有
连接的那个 Pod。

普通 Deployment + ClusterIP 会随机负载均衡 peer push，不适合这个场景。因此 chart
使用：

- `StatefulSet`：稳定 Pod 名。
- Headless Service：稳定 per-pod DNS。
- Downward API：注入 `POD_NAME`、`POD_NAMESPACE`。
- `cluster.internal_addr`：渲染成当前 Pod 的内部访问 URL。

peer 地址形状：

```text
http://${POD_NAME}.<release>-z-courier-headless.${POD_NAMESPACE}.svc.cluster.local:18080
```

## 创建 Secret

```bash
kubectl create namespace z-courier

kubectl -n z-courier create secret generic z-courier-secret \
  --from-literal=auth-provider-shared-token='<replace-me>' \
  --from-literal=internal-hmac-secret='<replace-me>' \
  --from-literal=peer-hmac-secret='<replace-me>' \
  --from-literal=postgres-password='<replace-me>' \
  --from-literal=redis-password='<replace-me>' \
  --from-literal=terminal-webhook-hmac-secret='<replace-with-separate-32-byte-secret>' \
  --from-literal=upstream-internal-token='<replace-me>'
```

生产环境不要把 secret 写进 Git。

## HMAC 轮换重叠窗口

`internalHttp.auth.hmac.additionalKeys` 和
`cluster.peer.auth.hmac.additionalKeys` 用于滚动轮换期间临时扩展验签 Key 集合。
Peer 的 `keyID` 仍然决定当前出站签名使用哪把 Key；`additionalKeys` 只接受入站
签名，不会自行成为签名 Key。

每个附加 Key 只填写 Key ID 和环境变量名，再通过 `extraEnv` 从 Kubernetes Secret
注入真正的 secret。不要把 secret 明文写入 ConfigMap 或 values。可参考
`deploy/helm/z-courier/examples/values-hmac-rotation.yaml`，它表示“新 Key 已用于签名，
旧 Key 暂时仍被接受”的重叠阶段。确认旧 Key 流量归零后，应删除附加 Key 及其环境
变量。

下面的命令会验证默认 Chart 不包含旧 Key、轮换配置能渲染双 Key、重复 Key ID 会被
拒绝、Secret 引用正确，并把生成的配置交给 gateway 真实加载：

```bash
bash scripts/helm_hmac_rotation_check.sh
```

## 使用本地 chart 安装

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  -f deploy/helm/z-courier/examples/values-production.yaml \
  --set image.repository=ghcr.io/qiuyier/z-courier-gateway \
  --set image.tag=v0.17.0 \
  --set secret.name=z-courier-secret
```

更推荐把依赖地址写进自己的 values 文件，而不是在命令行里写一长串 `--set`。

## HTTP Upstream 服务发现

chart 保留原有单 `target.url` 写法，同时支持静态端点列表和 DNS A/AAAA 发现，
三种写法互斥。

固定容器、VM 或服务地址可参考：

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-static-discovery.yaml
```

Kubernetes DNS 可参考：

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-dns-discovery.yaml
```

production values 使用
`business-backend-headless.z-courier.svc.cluster.local`。普通 Service 通常只返回
一个 ClusterIP；希望 gateway 直接得到多个 Pod 地址并进行端点选择和有限 failover
时，应使用 Headless Service。DNS 模式的 `path` 会附加到每一个解析出的地址；
静态模式则要求每个 endpoint 已包含完整路径。

修改这些示例后运行：

```bash
bash scripts/discovery_deployment_check.sh
```

它会校验并渲染两种模式、拒绝互相冲突的 values、抽取 ConfigMap 中的配置，并交给
真实 gateway 配置加载器验证。

## ConfigMap 路由文件热加载

默认仍使用 inline routes。要把 routes 渲染成独立 ConfigMap 文件，使用：

```yaml
upstream:
  routesFile:
    enabled: true
    maxSizeBytes: 1048576
    reload:
      enabled: true
      drainTimeout: 30s
      acceptedMsgIDRanges:
        - min: 1001
          max: 1999
        - min: 2000
          max: 2999
```

可以参考 `deploy/helm/z-courier/examples/values-route-file.yaml`。Chart 会单独创建
`<release>-z-courier-upstream-routes` ConfigMap，并把整个目录只读挂载到
`/app/routes`。这里故意不用 `subPath`，因为 `subPath` 文件不会收到 ConfigMap 后续
更新。只修改 `upstream.routes` 也不会改变 StatefulSet 的 `checksum/config`，所以
`helm upgrade` 不会为了路由变化重启长连接 Pod。修改启动接纳范围属于主配置变化，
仍然需要正常滚动重启。

ConfigMap 投影不是即时事务，延迟可能包含 kubelet sync period 和缓存传播时间。先等
所有 Pod 看到同一份文件：

```bash
for pod in $(kubectl -n z-courier get pods \
  -l app.kubernetes.io/instance=z-courier -o name); do
  kubectl -n z-courier exec "$pod" -- \
    sha256sum /app/routes/upstream-routes.yaml
done
```

文件更新不会自动激活。对每个 Pod 单独 port-forward internal HTTP，先查询当前
generation，再带着该 generation dry-run：

```bash
kubectl -n z-courier port-forward pod/z-courier-0 18080:18080

go run ./cmd/admin routes status \
  -internal-url http://127.0.0.1:18080 \
  -auth hmac \
  -hmac-key-id prod-internal-2026-01 \
  -hmac-secret "$ZCOURIER_INTERNAL_HMAC_SECRET"

go run ./cmd/admin routes validate \
  -internal-url http://127.0.0.1:18080 \
  -auth hmac \
  -hmac-key-id prod-internal-2026-01 \
  -hmac-secret "$ZCOURIER_INTERNAL_HMAC_SECRET" \
  -expected-generation 1

go run ./cmd/admin routes reload \
  -internal-url http://127.0.0.1:18080 \
  -auth hmac \
  -hmac-key-id prod-internal-2026-01 \
  -hmac-secret "$ZCOURIER_INTERNAL_HMAC_SECRET" \
  -expected-generation 1 \
  -confirm
```

先选择一个 Pod 作为 canary，确认转发、错误率和旧 generation retirement 正常，再
逐 Pod 激活其余节点。generation 是节点本地值，每次操作前都要重新查询。回滚时恢复
旧 routes values，等待投影收敛，再逐 Pod dry-run 和激活；generation 仍会继续递增。

Kubernetes 官方说明见
[ConfigMap 自动投影更新](https://kubernetes.io/docs/concepts/configuration/configmap/#mounted-configmaps-are-updated-automatically)
和 [`subPath` 更新限制](https://kubernetes.io/docs/concepts/storage/volumes/#configmap)。

部署契约静态验证：

```bash
bash scripts/route_reload_deployment_check.sh
```

## 使用 OCI chart 安装

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.9.0 \
  --namespace z-courier \
  -f values-production.yaml \
  --set image.tag=v0.17.0
```

注意：

- `--version 0.9.0` 是 Helm chart 版本。
- `image.tag=v0.17.0` 是 gateway 镜像版本。
- 两者相关，但不是同一个版本号体系。

## 命名流量策略

chart 默认保持旧版兼容：`pipeline.rateLimit.enabled=true`，
`pipeline.trafficPolicies.enabled=false`，两者不能同时启用。

单 gateway 使用有界进程内 token bucket：

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-traffic-policy-local.yaml
```

多个 gateway 需要共享一份额度时使用 Redis：

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-traffic-policy-redis.yaml
```

Redis 密码通过 `passwordEnv` 指向 Kubernetes Secret 注入的环境变量；生成的
ConfigMap 只会保留 `${ZCOURIER_REDIS_PASSWORD}` 占位符。共享额度的所有 Pod
必须保持 Redis database、Key 前缀、策略名、选择器、优先级和 bucket 参数一致。
配置中的 `capacity` 是整个共享配额，不是每个 Pod 各一份。

Redis 模式只支持 `fail_closed`。Store 不可用时，被策略选中的包返回
`admission_unavailable`，不会偷偷回退为各 Pod 独立 local 配额。production values
中的数值只是可审核起点，上线前应按真实入口突发、持续速率和 backend 容量调整。

修改后运行完整部署契约校验：

```bash
bash scripts/traffic_policy_deployment_check.sh
```

脚本会检查默认兼容、local/Redis 渲染、非法双开、Secret 占位符、生产集群两节点
配置一致性，并把生成的 YAML 交给真实 gateway 配置加载器。回滚不需要迁移协议或
数据库：恢复旧 values，或者关闭 `trafficPolicies`、重新启用 `rateLimit`，再正常
滚动重启。

## Services

chart 创建三个 Service：

| Service | 作用 |
| --- | --- |
| `<release>-z-courier-client` | TCP 客户端接入 |
| `<release>-z-courier-internal` | 内部 HTTP、admin、backend downlink、health、metrics |
| `<release>-z-courier-headless` | gateway pod 间 peer push |

internal service 必须保持私有。外部客户端如果要连接 TCP，可以把
`clientService.type` 设置为 `LoadBalancer`，或接入支持长连接 TCP 的网关/负载均衡。

## Admin Console

默认：

```yaml
adminConsole:
  enabled: false
```

启用：

```yaml
adminConsole:
  enabled: true
  session:
    enabled: true
    ttl: 8h
    cookieName: zcourier_admin_session
    cookieSecure: true
    cookieSameSite: lax
    role: admin
    store:
      type: redis
      redis:
        addr: redis-master.z-courier.svc.cluster.local:6379
        keyPrefix: zcourier:production-k8s:admin-session
  monitoring:
    prometheusURL: https://prometheus.example.internal
    grafanaURL: https://grafana.example.internal
    dashboardURL: https://grafana.example.internal/d/z-courier-overview/z-courier-overview
```

console 只应该通过私有网络、VPN、堡垒机、私有 ingress 或带认证的反向代理访问。
生产 HMAC 模式下，浏览器 JavaScript 不适合直接持有 HMAC secret；可以让反向代理
完成 operator 鉴权和内部签名。`adminConsole.session.enabled=true` 时，登录成功后
浏览器拿到短期 HTTP-only cookie。`adminConsole.session.store.type=redis` 可以让
console session 在多个 gateway Pod 之间共享；单节点开发也可以使用 `memory`。
角色建议按最小权限选择：
`readonly` 用于查看，`operator` 用于受保护的修复操作，`admin` 表示当前完整
console 权限集。

## TLS Edge、Ingress 与云负载均衡

chart 不直接安装 Ingress Controller、Gateway API 实现或证书签发器。平台侧接入应
保持以下边界：

- client Service 后面必须是支持长连接原始 TCP 的负载均衡。它可以终止 TLS，也可
  以 TCP 方式透传 TLS，但发往 gateway Pod 的最终协议必须仍是原始 Z-Courier 包流。
- internal Service 保持私有。Console ingress 只能放行项目 edge 参考中列出的精确
  路径，不能直接放行整个 `/internal` 前缀。
- `/internal/push`、`/internal/cluster/push`、`/metrics`、`/healthz`、`/readyz`
  必须走单独私有 route/listener，不能和公网 Console listener 共用策略。
- 边缘证书放 Kubernetes Secret、cert-manager 或云证书管理器，不能把 PEM 写进
  values 或 ConfigMap。
- Console 可能在 Pod 间负载均衡时，使用 Redis admin session。
- 为长连接配置足够的 idle timeout、连接 draining 和健康摘流。gateway 明确支持前
  不要启用 PROXY protocol。

不同 controller 和云厂商的 TCP/TLS 注解差异很大，所以项目暂不提交绑定某个实现的
Ingress manifest。可复制的 Nginx/Caddy 白名单与完整说明见
[TLS 边缘代理部署](edge-proxy.md)。

## 可靠下行策略与终态事件

V12 chart 已暴露 `downlink.policies` 和 `downlink.terminal`。默认仍兼容旧版本：
策略列表为空，终态发布器类型为 `none`，不会向外部系统发布终态事件。

```yaml
downlink:
  policies:
    - name: critical-notifications
      enabled: true
      msgIDMin: 3000
      msgIDMax: 3099
      maxAttempts: 20
      maxAge: 24h
      ackTimeout: 30s
      retryDelay: 5s
      backoffMultiplier: 2
      maxRetryDelay: 5m
      retryJitter: 5s
  terminal:
    publisher:
      type: nsq
      nsq:
        nsqdAddrs:
          - nsqd.z-courier.svc.cluster.local:4150
        topic: downlink_terminal_events
        authSecret: ""
        dialTimeout: 1s
        readTimeout: 60s
        writeTimeout: 1s
        publishMode: round_robin
        retryAttempts: 2
```

所有连接同一个 PostgreSQL 下行存储的 gateway Pod 必须使用相同的策略和终态发布
配置。启用前应确认 MsgID 区间没有重叠。终态事件只包含受限的投递元数据，不包含
业务消息体；consumer 应按 `MessageID` 和终态做幂等处理。

不使用 NSQ 时，可以把 publisher 换成签名 HTTP：

```yaml
downlink:
  terminal:
    publisher:
      type: http
      http:
        url: https://terminal-events.example.internal/v1/z-courier
        timeout: 5s
        allowInsecureHTTP: false
        hmac:
          keyID: gateway-terminal-v1
          secretEnv: ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET
        tls:
          serverName: terminal-events.example.internal
          secret:
            name: z-courier-terminal-webhook-tls
            mountPath: /run/secrets/terminal-webhook
            caKey: ca.crt
            clientCertKey: tls.crt
            clientKeyKey: tls.key
```

chart 只在 `type: http` 时从 `secret.keys.terminalWebhookHMACSecret` 引用
Kubernetes Secret，并把它注入 `secretEnv` 指定的环境变量；默认 `none` 与 `nsq`
不会依赖这个 Secret key。接收端必须验签并按稳定 `event_id` 幂等。可以运行
`bash scripts/helm_terminal_http_check.sh` 静态验证 ConfigMap、StatefulSet 和 Secret
的条件渲染。

TLS 证书使用独立、外部管理的 Kubernetes Secret，不写入 values 或 ConfigMap：

```bash
kubectl -n z-courier create secret generic z-courier-terminal-webhook-tls \
  --from-file=ca.crt=/secure/path/ca.crt \
  --from-file=tls.crt=/secure/path/client.crt \
  --from-file=tls.key=/secure/path/client.key
```

chart 会按 `secret.name` 把指定 key 只读挂载到 `mountPath`。只需要私有 CA 时，把
`clientCertKey` 和 `clientKeyKey` 都设为空字符串。默认 Pod 安全上下文使用
`fsGroup: 101`，TLS Secret 文件权限为 `0440`，非 root gateway 可以读取，但不会在
Pod 内对所有用户开放。证书文件只在 gateway 启动时读取，因此替换 Secret 后需要
执行滚动重启：

```bash
kubectl -n z-courier rollout restart statefulset/z-courier
kubectl -n z-courier rollout status statefulset/z-courier
```

## ServiceMonitor 和告警

Prometheus Operator 用户可以打开：

```yaml
serviceMonitor:
  enabled: true
```

告警规则示例：

```bash
kubectl apply -f deploy/helm/z-courier/examples/prometheusrule.yaml
```

示例规则需要 Prometheus Operator CRD。生产使用前请根据真实流量调整阈值和 label。

## NetworkPolicy

chart 不默认安装 NetworkPolicy，因为不同集群的 namespace label、CNI、Ingress、
Service Mesh 和依赖位置差异很大。

可以从这个示例开始：

```bash
kubectl apply -f deploy/helm/z-courier/examples/networkpolicy.yaml
```

应用前必须按你的集群改 namespace label、依赖服务名和端口。

## 校验 chart

```bash
helm lint deploy/helm/z-courier
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-static-discovery.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-dns-discovery.yaml
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
helm package deploy/helm/z-courier --destination /tmp
```

用 production values 校验：

```bash
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
```

安装后检查：

```bash
kubectl -n z-courier rollout status statefulset/z-courier
kubectl -n z-courier get pods -l app.kubernetes.io/instance=z-courier
kubectl -n z-courier port-forward svc/z-courier-internal 18080:18080
curl http://127.0.0.1:18080/readyz
curl http://127.0.0.1:18080/metrics
```

## kind 验证

轻量 smoke：

```bash
bash scripts/k8s_helm_smoke.sh
```

完整 E2E：

```bash
bash scripts/k8s_helm_e2e.sh
```

E2E 会在 kind 里安装 PostgreSQL、Redis、NSQ，验证 AUTH/BIND、可靠下行、重连补发、
Redis 在线路由、跨 Pod peer push、策略选择与耗尽、NSQ 终态事件发布和消费、
NSQ upstream 以及 metrics。
