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

Install with your gateway image and private dependency addresses:

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  --set image.repository=ghcr.io/your-org/z-courier-gateway \
  --set image.tag=v0.5.0 \
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

## Boundaries

This first chart does not install PostgreSQL, Redis, NSQ, Prometheus, Grafana,
cert-manager, an ingress controller, or a service mesh. Those should be managed
by your platform or by dedicated upstream charts.
