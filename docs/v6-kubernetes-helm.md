# V6 Kubernetes And Helm Plan

V6 is the planning track for the next public milestone after `v0.5.0`. Its
target SemVer version is `v0.6.0`, not `v6.0.0`.

The goal is to make Z-Courier deployable in Kubernetes without losing the
cluster semantics that were proven in the Docker Compose reference stack.

## Scope

The first `v0.6.0` Kubernetes milestone includes:

- A Helm chart under `deploy/helm/z-courier`.
- Gateway deployment through a StatefulSet.
- Headless Service for stable per-pod peer push addresses.
- Separate client TCP and internal HTTP services.
- ConfigMap-rendered `z-courier.yaml` and `zinx.json`.
- Existing Secret integration for auth, HMAC, PostgreSQL, Redis, and upstream
  route credentials.
- Optional chart-rendered Secret for private sandbox testing.
- Optional ServiceMonitor for Prometheus Operator users.
- Documentation for install, validation, and production boundaries.
- A production values example without real secrets.

## Non-Goals

The first chart does not include:

- Installing PostgreSQL, Redis, NSQ, Prometheus, Grafana, cert-manager, an
  ingress controller, or a service mesh.
- A Kubernetes operator.
- Automatic database migration ownership beyond the gateway's existing
  `auto_migrate` option.
- A browser admin console.
- Built-in TLS or mTLS listeners.

Those remain valid future work, but the first chart should stay small enough to
audit.

## StatefulSet Decision

Z-Courier clients hold long-lived TCP connections to one gateway process. A
backend may send downlink traffic to any gateway node. If the target session is
not local, the receiving gateway looks up the online route in Redis and peer
pushes to the owning gateway.

In Kubernetes, that means each gateway pod needs a stable, directly reachable
internal address. A Deployment behind a ClusterIP service would load-balance
peer pushes randomly and could hit the wrong pod. The chart therefore uses:

- StatefulSet pod names as gateway node identities.
- A headless service for per-pod DNS.
- Downward API env vars for `POD_NAME` and `POD_NAMESPACE`.
- Runtime env expansion in Z-Courier config for `gateway_node` and
  `cluster.internal_addr`.

The configured peer URL is rendered as:

```text
http://${POD_NAME}.<headless-service>.${POD_NAMESPACE}.svc.cluster.local:18080
```

At runtime each pod advertises its own stable address in Redis.

## Service Model

The chart separates network surfaces:

| Surface | Kubernetes object | Intended callers |
| --- | --- | --- |
| Client TCP | `<release>-z-courier-client` Service | Client SDKs or an external TCP load balancer |
| Internal HTTP | `<release>-z-courier-internal` Service | Backends, admin tooling, health probes, Prometheus |
| Peer push | `<release>-z-courier-headless` Service | Gateway pods only |

Internal HTTP should stay private. Public clients should only reach the TCP
listener through a TCP-capable load balancer or gateway.

## External Dependencies

The chart expects these to be provided by the platform:

- PostgreSQL for durable downlink storage.
- Redis for cluster online route registry.
- NSQ or another configured upstream route target.
- Auth verifier or JWT/JWKS provider reachable by the gateway.
- Business backend route targets.
- Prometheus and Grafana if metrics visualization is needed.

This keeps the Z-Courier chart focused on the gateway while allowing teams to
use their preferred database, Redis, and observability operators.

## Validation Path

Local validation:

```bash
helm lint deploy/helm/z-courier
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
```

Cluster validation:

```bash
kubectl -n z-courier rollout status statefulset/z-courier
kubectl -n z-courier port-forward svc/z-courier-internal 18080:18080
curl http://127.0.0.1:18080/readyz
curl http://127.0.0.1:18080/metrics
```

Runtime validation should then reuse the existing SDK clients against the
client TCP service and the existing backend SDK against the internal HTTP
service.

## Future Work

Good next increments:

- Add CI `helm lint` and `helm template` once Helm is available in the runner.
- Add a kind-based smoke test that installs external dependencies and the chart.
- Add NetworkPolicy examples for client, backend, peer, Redis, PostgreSQL, and
  NSQ traffic.
- Add TLS termination examples for public TCP traffic.
- Add mTLS or service-mesh examples for internal HTTP and peer push.
- Add a values compatibility matrix for chart version, gateway image version,
  and protocol version.
