# Z-Courier Helm Chart

This chart deploys the Z-Courier gateway on Kubernetes. It is the first
Kubernetes-oriented deployment path for `v0.6.0` development and intentionally
focuses on the gateway process rather than installing the full data plane.

The chart expects PostgreSQL, Redis, NSQ or another upstream target, the auth
verifier, and the business backend to already exist in the cluster or be
reachable through private networking.

## Why StatefulSet

Z-Courier keeps TCP client connections inside the gateway process. In a
multi-replica Kubernetes deployment, downlink peer push must reach the exact pod
that owns the client session. A normal Deployment plus ClusterIP service would
load-balance peer push randomly, so this chart uses:

- `StatefulSet` for stable pod names.
- Headless Service for stable per-pod DNS.
- `POD_NAME` and `POD_NAMESPACE` env vars injected through the downward API.
- `cluster.internal_addr` rendered as a per-pod URL and resolved by Z-Courier
  runtime env expansion.

The generated peer address shape is:

```text
http://${POD_NAME}.<release>-z-courier-headless.${POD_NAMESPACE}.svc.cluster.local:18080
```

## Install

Create a namespace and a secret first:

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

Install with the published gateway image and private dependency addresses:

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  --set image.repository=ghcr.io/qiuyier/z-courier-gateway \
  --set image.tag=v0.9.0 \
  --set secret.name=z-courier-secret \
  --set cluster.registry.redis.addr=redis-master.z-courier.svc.cluster.local:6379 \
  --set auth.http.url=http://auth-backend.z-courier.svc.cluster.local:8080/gateway/auth/verify \
  --set upstream.routes[0].target.url=http://business-backend.z-courier.svc.cluster.local:8080/gateway/upstream \
  --set upstream.routes[1].target.nsqdAddrs[0]=nsqd.z-courier.svc.cluster.local:4150
```

For production, prefer a values file over a long command line:

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  -f deploy/helm/z-courier/examples/values-production.yaml
```

After a GitHub Release publishes the chart to GHCR, Kubernetes users can install
the packaged chart from the OCI registry instead of cloning this repository:

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.4.0 \
  --namespace z-courier \
  -f values-production.yaml
```

The OCI chart version is the Helm `Chart.yaml` `version`, not the Git release
tag. Bump the chart version whenever the published chart changes. See
[../../../docs/v6-helm-versioning.md](../../../docs/v6-helm-versioning.md) for the
chart/app compatibility matrix and release checklist. The gateway image release
workflow is described in
[../../../docs/v7-docker-image-release.md](../../../docs/v7-docker-image-release.md).

## Services

The chart creates three services:

| Service | Purpose |
| --- | --- |
| `<release>-z-courier-client` | TCP client ingress to gateway pods |
| `<release>-z-courier-internal` | Cluster-internal HTTP for admin, backend downlink, health, and metrics |
| `<release>-z-courier-headless` | Stable per-pod DNS for gateway peer push |

Keep the internal service private. If clients connect from outside the cluster,
set `clientService.type` to `LoadBalancer` or front it with an ingress/load
balancer that supports long-lived TCP connections.

## Network Policy

The chart does not install a default `NetworkPolicy`, because namespace labels,
ingress controllers, service meshes, and dependency placement vary by cluster.
Use [examples/networkpolicy.yaml](examples/networkpolicy.yaml) as a production
starting point.

The example restricts gateway pods to:

- Client TCP ingress on `zinx.tcpPort`.
- Internal HTTP ingress from backend, admin, monitoring, and peer gateway pods.
- Egress to DNS, peer gateway pods, auth service, business backend, PostgreSQL,
  Redis, and NSQ.

Adjust namespace labels and dependency ports before applying it.
If `clientService` is exposed directly through a cloud `LoadBalancer`, adjust
the client ingress rule for your CNI and load balancer source behavior.

## Prometheus Alerts

The chart can render a `ServiceMonitor` when `serviceMonitor.enabled=true`, but
it does not install Prometheus, Grafana, Alertmanager, or PrometheusRule
resources by default.

Prometheus Operator users can start from:

```bash
kubectl apply -f deploy/helm/z-courier/examples/prometheusrule.yaml
```

The example requires Prometheus Operator CRDs. Tune thresholds, labels, and
notification routing before using it for production paging. The Compose
monitoring stack uses the equivalent Prometheus rule file at
`deploy/monitoring/prometheus/rules/z-courier-alerts.yml`.

The embedded admin console can link operators to Prometheus, Grafana, and a
preferred dashboard when `adminConsole.monitoring` values are set:

```yaml
adminConsole:
  enabled: true
  monitoring:
    prometheusURL: https://prometheus.example.internal
    grafanaURL: https://grafana.example.internal
    dashboardURL: https://grafana.example.internal/d/z-courier-overview/z-courier-overview
```

Keep the internal service private. These links do not install monitoring
components; they only make existing monitoring surfaces easier to reach from
the console.

The chart defaults `adminConsole.enabled=false`. When enabling it, keep the
internal service on private networking and prefer VPN, bastion, private ingress,
or an authenticating reverse proxy for operator access. In production HMAC mode,
browser JavaScript cannot call `/internal/*` APIs unless a deployment-side proxy
signs those requests; direct HMAC operations remain available through
`cmd/admin`.

## Required Secrets

By default, the chart references an existing secret. The default key names are:

| Key | Env var |
| --- | --- |
| `auth-provider-shared-token` | `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` |
| `internal-hmac-secret` | `ZCOURIER_INTERNAL_HMAC_SECRET` |
| `peer-hmac-secret` | `ZCOURIER_PEER_HMAC_SECRET` |
| `postgres-password` | `ZCOURIER_POSTGRES_PASSWORD` |
| `redis-password` | `ZCOURIER_REDIS_PASSWORD` |
| `upstream-internal-token` | `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` |

For a private sandbox only, `secret.create=true` can render a Secret from
`secret.values`. Do not store real production secret values in Git.

## Validate

When Helm is installed locally:

```bash
helm lint deploy/helm/z-courier
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
helm package deploy/helm/z-courier --destination /tmp
```

`helm lint` validates the chart values against `values.schema.json`, including
the default values and any `-f` override file passed to Helm.

When a GitHub Release is published, CI packages this chart and uploads the
`.tgz` archive plus `SHA256SUMS` as release assets.
Another release workflow also publishes the chart to
`oci://ghcr.io/qiuyier/charts/z-courier`.

After install:

```bash
kubectl -n z-courier rollout status statefulset/z-courier
kubectl -n z-courier get pods -l app.kubernetes.io/instance=z-courier
kubectl -n z-courier port-forward svc/z-courier-internal 18080:18080
curl http://127.0.0.1:18080/readyz
curl http://127.0.0.1:18080/metrics
```

For a local kind smoke test:

```bash
bash scripts/k8s_helm_smoke.sh
```

The smoke test requires `docker`, `kind`, `kubectl`, and `curl`. It builds the
gateway image, creates a temporary kind cluster, loads the image into kind,
renders this chart with
`examples/values-kind-smoke.yaml`, waits for the StatefulSet to become ready,
and checks `/readyz` and `/metrics`.

For a local kind E2E test with real dependencies:

```bash
bash scripts/k8s_helm_e2e.sh
```

The E2E path installs PostgreSQL, Redis, and NSQ into the kind cluster, renders
this chart with `examples/values-k8s-e2e.yaml`, pins the client connection to
one gateway pod, sends internal downlink requests to another gateway pod, and
verifies AUTH/BIND, durable downlink storage, reconnect retry, Redis online
routing, cross-pod peer push, NSQ upstream forwarding, and metrics.

## Boundaries

This first chart does not install PostgreSQL, Redis, NSQ, Prometheus, Grafana,
cert-manager, an ingress controller, or a service mesh. Those should be managed
by your platform or by dedicated upstream charts.
