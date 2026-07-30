# Z-Courier Helm Chart

This chart deploys the Z-Courier gateway on Kubernetes. Chart `0.7.0` aligns
with gateway `v0.12.0` and focuses on the gateway process rather than
installing the full data plane.

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
  --set image.tag=v0.12.0 \
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
  --version 0.7.0 \
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

The example includes traffic-policy alerts for Redis fail-closed decisions,
sustained local-key utilization at or above 80%, overload decisions, and a
high rate-limited ratio under meaningful traffic. Review
`docs/v5-production-runbook.md#traffic-policy-admission` before enabling paging;
normal quota shaping should not page solely because one request was
rate-limited.

The embedded admin console can link operators to Prometheus, Grafana, and a
preferred dashboard when `adminConsole.monitoring` values are set:

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
  audit:
    type: postgres
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgresql.z-courier.svc.cluster.local:5432/zcourier?sslmode=disable"
```

Keep the internal service private. These links do not install monitoring
components; they only make existing monitoring surfaces easier to reach from
the console.

`adminConsole.audit.type` defaults to `memory`, which keeps only the latest
audit events inside each gateway process. Use `postgres` when production
operators need admin audit history to survive pod restarts.

`adminConsole.session.store.type` can be `memory` or `redis`. Use `memory` for
single-node development. Use `redis` when console requests can land on
different gateway pods; logout deletes the shared session and the Redis key TTL
tracks the configured session TTL.

The chart defaults `adminConsole.enabled=false`. When enabling it, keep the
internal service on private networking and prefer VPN, bastion, private ingress,
or an authenticating reverse proxy for operator access. In production HMAC mode,
browser JavaScript cannot call `/internal/*` APIs unless a deployment-side proxy
signs those requests; direct HMAC operations remain available through
`cmd/admin`. When `adminConsole.session.enabled=true`, the browser receives a
short-lived HTTP-only session cookie after a valid internal token or
HMAC-authenticated login request. Choose the lowest role that fits the operator workflow:
`readonly` for inspection, `operator` for guarded repair actions, and `admin`
for the current full console permission set.

## HTTP Upstream Discovery

The chart keeps the legacy single `target.url` form for compatibility and can
also render static endpoint lists or DNS A/AAAA discovery. These forms are
mutually exclusive.

For fixed container, VM, or service addresses:

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-static-discovery.yaml
```

For Kubernetes DNS:

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-dns-discovery.yaml
```

The production values use
`business-backend-headless.z-courier.svc.cluster.local`. A normal Kubernetes
Service usually resolves to one ClusterIP; use a headless Service when the
gateway should receive multiple Pod addresses for client-side selection and
bounded failover. `path` is appended to every DNS-resolved endpoint, while
static endpoints already contain their complete path.

Run the deployment verifier after changing either example:

```bash
bash scripts/discovery_deployment_check.sh
```

It lints and renders both modes, rejects ambiguous values, extracts the
generated gateway configs, and validates them through the gateway's real
configuration loader.

## Named Traffic Policies

The chart defaults preserve the legacy fixed-window limiter:
`pipeline.rateLimit.enabled=true` and
`pipeline.trafficPolicies.enabled=false`. They cannot both be enabled.

Use the bounded local token bucket for a standalone gateway:

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-traffic-policy-local.yaml
```

Use Redis when every replica must consume one shared quota:

```bash
helm lint deploy/helm/z-courier \
  -f deploy/helm/z-courier/examples/values-traffic-policy-redis.yaml
```

Redis mode reads its password from `passwordEnv`; the generated ConfigMap
contains only `${ZCOURIER_REDIS_PASSWORD}`, while the StatefulSet injects that
environment variable from the chart Secret. Keep the Redis address, database,
key prefix, policy names, selectors, priorities, and bucket parameters
identical across all gateway replicas sharing a quota.

The production values enable a Redis-backed `production-shared-client`
starting point. Its `capacity` and refill rate are examples, not universal
production defaults. Size them from measured ingress traffic and backend
capacity, then canary the policy before a full rollout. Redis mode is
fail-closed: an unavailable quota store rejects selected packets instead of
silently creating independent per-Pod quotas.

Validate the complete deployment contract after changing these values:

```bash
bash scripts/traffic_policy_deployment_check.sh
```

The verifier checks default compatibility, local and Redis rendering, invalid
combinations, secret placeholders, identical production-cluster policy
configuration, and each generated config through the real gateway loader.
Rollback does not require a packet or database migration: restore the previous
values, or disable `trafficPolicies` and re-enable `rateLimit`, then perform a
normal rolling restart.

## Required Secrets

By default, the chart references an existing secret. The default key names are:

| Key | Env var |
| --- | --- |
| `auth-provider-shared-token` | `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` |
| `internal-hmac-secret` | `ZCOURIER_INTERNAL_HMAC_SECRET` |
| `peer-hmac-secret` | `ZCOURIER_PEER_HMAC_SECRET` |
| `postgres-password` | `ZCOURIER_POSTGRES_PASSWORD` |
| `redis-password` | `ZCOURIER_REDIS_PASSWORD` |
| `terminal-webhook-hmac-secret` | `ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET` when HTTP publication is enabled |
| `upstream-internal-token` | `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` |

For a private sandbox only, `secret.create=true` can render a Secret from
`secret.values`. Do not store real production secret values in Git.

## HMAC Rotation Overlap

`internalHttp.auth.hmac.additionalKeys` and
`cluster.peer.auth.hmac.additionalKeys` extend the accepted verification key
ring during a rolling rotation. The primary `keyID` remains the key used by the
peer signer; additional keys verify inbound requests but never become active
signers by themselves.

Each additional key names an environment variable. Inject that variable from a
Kubernetes Secret with `extraEnv`; do not put the secret value in the ConfigMap
or values file. See
`examples/values-hmac-rotation.yaml` for the overlap stage where new keys are
active and previous keys are still accepted. After all old-key traffic has
stopped, remove the additional keys and their environment variables.

Run `bash scripts/helm_hmac_rotation_check.sh` to verify default isolation,
multi-key rendering, duplicate-key rejection, Secret references, and gateway
config loading.

## Reliable Downlink Policies

V12 chart values expose named, inclusive MsgID-range policies and the terminal
event publisher. The defaults preserve previous behavior: `downlink.policies`
is empty and `downlink.terminal.publisher.type` is `none`.

Enable policy ranges only after checking that enabled ranges do not overlap.
Every gateway sharing one PostgreSQL downlink store should use the same policy
and terminal-publisher configuration.

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

Terminal events contain bounded delivery metadata and never include the
business message body. Consumers should process them idempotently by
`MessageID` and terminal state. See
[examples/values-production.yaml](examples/values-production.yaml) for a
disabled policy example and the safe `none` publisher default.

For a private-CA or mTLS HTTP receiver, create a separate externally managed
Secret containing only the certificate files:

```bash
kubectl -n z-courier create secret generic z-courier-terminal-webhook-tls \
  --from-file=ca.crt=/secure/path/ca.crt \
  --from-file=tls.crt=/secure/path/client.crt \
  --from-file=tls.key=/secure/path/client.key
```

Then configure the HTTP publisher using
[examples/values-terminal-http-mtls.yaml](examples/values-terminal-http-mtls.yaml).
The chart mounts the external Secret read-only and writes only file paths to
the ConfigMap. It never copies certificate or private-key bytes into chart
values or the generated ConfigMap. For custom CA without mTLS, leave
`clientCertKey` and `clientKeyKey` empty and keep only `caKey`. The default pod
security context uses `fsGroup: 101`, and the TLS Secret volume uses mode
`0440`, so the non-root gateway process can read it without making the private
key world-readable inside the pod.

The gateway loads these files at startup. After replacing Secret data, perform
a rolling restart so every pod loads the new trust and client identity:

```bash
kubectl -n z-courier rollout restart statefulset/z-courier
kubectl -n z-courier rollout status statefulset/z-courier
```

## Validate

When Helm is installed locally:

```bash
helm lint deploy/helm/z-courier
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-static-discovery.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-dns-discovery.yaml
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
routing, cross-pod peer push, policy selection, policy exhaustion, NSQ terminal
event publication and consumption, NSQ upstream forwarding, and metrics.

## Boundaries

This chart does not install PostgreSQL, Redis, NSQ, Prometheus, Grafana,
cert-manager, an ingress controller, or a service mesh. Those should be managed
by your platform or by dedicated upstream charts.
