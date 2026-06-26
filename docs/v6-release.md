# V6 Release Guide

This document defines the `v0.6.0` release scope, upgrade notes, Kubernetes
verification path, Helm publishing checklist, and known boundaries. V6 is an
internal project phase; the public SemVer version is `v0.6.0`, not `v6.0.0`.

## Release Scope

`v0.6.0` makes Z-Courier deployable through Kubernetes and Helm while
preserving the gateway, SDK, authentication, cluster routing, and reliable
delivery model proven in earlier releases.

Included in scope:

- A Helm chart under `deploy/helm/z-courier`.
- Gateway deployment through a StatefulSet with stable pod identity.
- Headless Service for per-pod peer push DNS.
- Separate client TCP, internal HTTP, and headless peer services.
- ConfigMap-rendered `z-courier.yaml` and `zinx.json`.
- Existing Secret integration for auth, HMAC, PostgreSQL, Redis, and upstream
  route credentials.
- Optional chart-rendered Secret for private sandbox and kind testing.
- Optional ServiceMonitor support for Prometheus Operator users.
- Production values example without real secrets.
- `values.schema.json` validation for default, production, kind smoke, and
  kind E2E values.
- Self-contained kind smoke validation for chart startup, readiness, and
  metrics.
- Kind Helm E2E validation with PostgreSQL downlink storage, Redis online
  routing, cross-pod HMAC peer push, NSQ upstream forwarding, reconnect retry,
  and metrics exposure.
- Kubernetes NetworkPolicy example for production-style ingress and dependency
  egress boundaries.
- CI Helm chart packaging artifact.
- GitHub Release asset workflow for uploading the chart `.tgz` and
  `SHA256SUMS`.
- GHCR OCI Helm chart publishing workflow.
- Helm chart versioning guide with chart/app version policy, compatibility
  matrix, and release checklist.

## Not Included

`v0.6.0` does not include:

- Installing PostgreSQL, Redis, NSQ, Prometheus, Grafana, cert-manager, an
  ingress controller, or a service mesh as chart dependencies.
- A Kubernetes operator.
- Built-in TLS or mTLS listeners.
- Built-in ingress or Gateway API resources.
- Built-in NetworkPolicy templates. The release includes an example manifest
  only.
- Built-in database lifecycle ownership beyond the gateway's existing
  `auto_migrate` option.
- A browser admin console.
- A new packet version or incompatible V1 wire-format changes.
- Automatic gateway container image publishing to GHCR.

Those remain valid future work, but they are not required for the first
Kubernetes/Helm release.

## Compatibility And Upgrade

Existing `v0.5.0` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go SDK, PHP SDK, backend SDK, admin CLI, authentication providers,
  upstream routes, Redis cluster routes, PostgreSQL downlink storage, HMAC
  modes, metrics, dashboards, and Docker Compose references remain compatible.
- No gateway wire-protocol migration is required.
- Kubernetes and Helm are an additional deployment path, not a replacement for
  the Docker Compose references.

Recommended adoption path from `v0.5.0`:

1. Keep current gateway configuration and client SDK versions unchanged.
2. Review the Helm chart values and decide where PostgreSQL, Redis, NSQ, auth,
   business backend, Prometheus, and Grafana will be provided by the platform.
3. Create a real Kubernetes Secret for auth, HMAC, PostgreSQL, Redis, and
   upstream credentials.
4. Set `image.repository` and `image.tag` to the gateway image you intend to
   deploy.
5. Run `helm lint` with your production values file.
6. Install into a staging namespace and confirm StatefulSet rollout,
   `/readyz`, `/metrics`, and Prometheus scraping.
7. Exercise AUTH/BIND, upstream forwarding, downlink push, reconnect retry, and
   cross-pod peer push in staging.
8. Apply a cluster-appropriate NetworkPolicy or service-mesh policy before
   exposing production traffic.
9. Canary production traffic and watch online sessions, clients, downlink push,
   ACK, retry, cluster registry, peer push, upstream forwarding, HMAC
   signature, and dependency metrics.

## Helm Versioning

The chart has its own version in
`deploy/helm/z-courier/Chart.yaml`:

```yaml
version: 0.1.0
appVersion: "v0.5.0"
```

Before tagging `v0.6.0`, update `appVersion` to the intended gateway image tag:

```yaml
version: 0.1.0
appVersion: "v0.6.0"
```

The Helm chart `version` is what users pass to `helm --version`. It is separate
from the Git tag. The first chart release is expected to use chart version
`0.1.0`.

The complete chart/app version policy and compatibility matrix are maintained
in [v6-helm-versioning.md](v6-helm-versioning.md).

## Installation Paths

From a repository checkout:

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  -f deploy/helm/z-courier/examples/values-production.yaml
```

From a GitHub Release asset:

```bash
helm upgrade --install z-courier ./z-courier-0.1.0.tgz \
  --namespace z-courier \
  -f values-production.yaml
```

From GHCR OCI:

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.1.0 \
  --namespace z-courier \
  -f values-production.yaml
```

Production users should pin both the chart version and gateway image tag.

## Production Secret Checklist

Before installing outside a private test environment, create real Kubernetes
Secrets and do not commit secret values to Git.

Review at least:

- auth-provider shared token or real auth provider credentials
- backend internal HTTP HMAC secret
- gateway peer HMAC secret
- PostgreSQL password
- Redis password
- upstream internal token or NSQ auth secret
- real auth service, business backend, Redis, PostgreSQL, and NSQ addresses

Do not reuse the same HMAC key for backend-to-gateway internal HTTP and
gateway-to-gateway peer push.

## Runtime Notes

- The chart deploys only Z-Courier gateway pods. Platform dependencies remain
  external to the chart.
- StatefulSet pod names become gateway node identities.
- Peer push uses the headless service and per-pod DNS.
- Internal HTTP should stay private. Public clients should only reach the TCP
  gateway listener through a TCP-capable load balancer or gateway.
- `/healthz`, `/readyz`, and `/metrics` remain unauthenticated for probes and
  Prometheus. Protect them with network boundaries.
- Admin APIs and backend downlink APIs live under `/internal/*` and should use
  HMAC in production.
- NetworkPolicy is port-level only. It cannot distinguish `/metrics`,
  `/readyz`, admin paths, and `/internal/push` when they share the same
  internal HTTP port.
- Delivery remains at-least-once. Applications must de-duplicate important
  operations by `MessageID`.

## Verification

Run from the repository root on the exact commit intended for the tag:

```bash
actionlint
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=90s \
  ./pkg/sdk/protocol ./pkg/sdk/client ./pkg/sdk/backend ./pkg/sdk/signing \
  ./internal/auth ./internal/downlink \
  ./internal/server ./internal/config
go vet ./...
php -d error_reporting=E_ALL sdk/php/tests/run.php
find sdk/php -name '*.php' -print0 | xargs -0 -n1 php -l
composer --working-dir=sdk/php install --no-interaction --prefer-dist
composer --working-dir=sdk/php analyse
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
bash scripts/production_smoke.sh
bash scripts/production_cluster_smoke.sh
bash scripts/k8s_helm_smoke.sh
bash scripts/k8s_helm_e2e.sh
helm lint deploy/helm/z-courier
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-kind-smoke.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-k8s-e2e.yaml
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
helm package deploy/helm/z-courier --destination /tmp
git diff --check
```

GitHub Actions must be green for the exact `main` commit before tagging.
Run the manual **Kubernetes E2E** workflow before the release if local kind
validation cannot be repeated on the release machine.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow in upstream and downlink modes.
- Review workflow summaries and `cmd/loadcompare` output.
- Treat baseline comparison as informational unless the release process
  explicitly promotes it to a hard gate.

## Release Artifact Checks

After publishing the GitHub Release:

1. Confirm the `Release Helm Chart` workflow succeeded.
2. Confirm the GitHub Release contains:
   - `z-courier-0.1.0.tgz`
   - `SHA256SUMS`
3. Confirm the `Release Helm OCI` workflow succeeded.
4. Pull the OCI chart:

   ```bash
   helm pull oci://ghcr.io/qiuyier/charts/z-courier --version 0.1.0
   ```

5. Optionally install the pulled chart into a staging cluster.

## GitHub Release Notes

### Highlights

- Kubernetes Helm chart for deploying Z-Courier gateway pods.
- StatefulSet-based gateway identity and headless peer-push service.
- Separate client TCP and internal HTTP services.
- ConfigMap-rendered gateway and Zinx configuration.
- Secret integration for auth, HMAC, PostgreSQL, Redis, and upstream
  credentials.
- Helm values schema validation and production values example.
- Local kind smoke and kind E2E verification scripts.
- Manual Kubernetes E2E GitHub Actions workflow.
- NetworkPolicy example for production-style gateway boundaries.
- GitHub Release asset and GHCR OCI Helm chart publishing workflows.
- Helm chart versioning and compatibility guide.

### Upgrade Notes

No wire-format or SDK migration is required from `v0.5.0`. Existing Docker
Compose references and gateway configurations remain valid.

Kubernetes adopters should provide PostgreSQL, Redis, NSQ, auth, business
backend, and observability dependencies outside the chart, pin a tested gateway
image tag, replace all secret values, keep internal HTTP private, and prefer
HMAC for backend and peer internal traffic.

### Known Boundaries

- The chart deploys the gateway only; it does not install PostgreSQL, Redis,
  NSQ, Prometheus, Grafana, ingress, cert-manager, or a service mesh.
- Delivery remains at-least-once; applications must de-duplicate important
  operations by `MessageID`.
- TLS, mTLS, and external load balancer configuration remain deployment
  responsibilities.
- NetworkPolicy is provided as an example, not as a chart-managed template.
- Gateway container image publishing to GHCR is not automated by this release.
- The chart is the first Kubernetes release and should be canaried before broad
  production rollout.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Update `deploy/helm/z-courier/Chart.yaml` `appVersion` to `"v0.6.0"`.
3. Confirm `deploy/helm/z-courier/Chart.yaml` `version` is the intended chart
   version.
4. Update `CHANGELOG.md` with the final `v0.6.0` date and scope.
5. Confirm GitHub Actions is green on the exact commit.
6. Run or confirm the manual **Kubernetes E2E** workflow.
7. Confirm release notes match the final scope.
8. Create and push the annotated tag:

```bash
git tag -a v0.6.0 -m "v0.6.0"
git push origin v0.6.0
```

9. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
10. Confirm GitHub Release assets and GHCR OCI publication succeed.
