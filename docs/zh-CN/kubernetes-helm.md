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
  --from-literal=upstream-internal-token='<replace-me>'
```

生产环境不要把 secret 写进 Git。

## 使用本地 chart 安装

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  -f deploy/helm/z-courier/examples/values-production.yaml \
  --set image.repository=ghcr.io/qiuyier/z-courier-gateway \
  --set image.tag=v0.9.1 \
  --set secret.name=z-courier-secret
```

更推荐把依赖地址写进自己的 values 文件，而不是在命令行里写一长串 `--set`。

## 使用 OCI chart 安装

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.4.1 \
  --namespace z-courier \
  -f values-production.yaml \
  --set image.tag=v0.9.1
```

注意：

- `--version 0.4.1` 是 Helm chart 版本。
- `image.tag=v0.9.1` 是 gateway 镜像版本。
- 两者相关，但不是同一个版本号体系。

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
  monitoring:
    prometheusURL: https://prometheus.example.internal
    grafanaURL: https://grafana.example.internal
    dashboardURL: https://grafana.example.internal/d/z-courier-overview/z-courier-overview
```

console 只应该通过私有网络、VPN、堡垒机、私有 ingress 或带认证的反向代理访问。
生产 HMAC 模式下，浏览器 JavaScript 不适合直接持有 HMAC secret；可以让反向代理
完成 operator 鉴权和内部签名。`adminConsole.session.enabled=true` 时，登录成功后
浏览器拿到短期 HTTP-only cookie；当前 session 是单 Pod 内存态，Pod 重启后需要重新
登录。如果 ingress 或代理会把 console 请求负载均衡到多个副本，需要使用粘性路由、
按 Pod 直连，或者接受请求落到另一个 Pod 时重新登录。角色建议按最小权限选择：
`readonly` 用于查看，`operator` 用于受保护的修复操作，`admin` 表示当前完整
console 权限集。

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
Redis 在线路由、跨 Pod peer push、NSQ upstream 和 metrics。
