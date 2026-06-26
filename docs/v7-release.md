# V7 Release Guide

This document defines the `v0.7.0` release scope, upgrade notes, Docker image
publishing checklist, verification path, and known boundaries. V7 is an
internal project phase; the public SemVer version is `v0.7.0`, not `v7.0.0`.

## Release Scope

`v0.7.0` completes the production deployment artifact loop by publishing the
gateway Docker image itself to GitHub Container Registry. This builds on the
`v0.6.0` Helm chart and lets operators deploy Z-Courier without building the
gateway image locally.

Included in scope:

- `Release Docker Image` GitHub Actions workflow.
- Release-triggered gateway image publishing to GHCR.
- Manual backfill support for existing tags through `workflow_dispatch`.
- Immutable release image tags such as
  `ghcr.io/qiuyier/z-courier-gateway:v0.7.0`.
- Optional `latest` tag publishing for stable GitHub Releases.
- No `latest` publishing for pre-release tags such as `v0.7.0-rc.1`.
- Multi-architecture Docker manifests for `linux/amd64` and `linux/arm64`.
- Container smoke checks before pushing the published image.
- Post-push manifest inspection that fails the workflow if either target
  platform is missing.
- Helm default image repository changed to
  `ghcr.io/qiuyier/z-courier-gateway`.
- Helm chart version bumped to `0.2.0` for the image repository default change.
- Production Helm values example updated to use the official gateway image.
- `v0.6.0` image backfilled and verified as a multi-architecture GHCR image.

## Not Included

`v0.7.0` does not include:

- Publishing PostgreSQL, Redis, NSQ, Prometheus, Grafana, auth backend, or
  business backend images.
- A new gateway wire protocol version.
- A new SDK release or client protocol change.
- A new Kubernetes operator.
- Chart-managed database, Redis, or NSQ dependencies.
- Built-in TLS, mTLS, ingress, or Gateway API resources.
- A browser admin console.

Those remain valid future work, but they are not required for the Docker image
publishing milestone.

## Compatibility And Upgrade

Existing `v0.6.0` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go SDK, PHP SDK, backend SDK, admin CLI, authentication providers,
  upstream routes, Redis cluster routes, PostgreSQL downlink storage, HMAC
  modes, metrics, dashboards, Docker Compose references, and Helm chart values
  remain compatible.
- No gateway wire-protocol migration is required.

Recommended adoption path from `v0.6.0`:

1. Keep current gateway configuration and client SDK versions unchanged.
2. Use the official image repository:

   ```yaml
   image:
     repository: ghcr.io/qiuyier/z-courier-gateway
     tag: v0.7.0
   ```

3. Keep using immutable image tags in production. Avoid relying on `latest` for
   rollbacks or reproducible deployments.
4. If using Helm, update to the chart version published with `v0.7.0` and pin
   both chart version and image tag.
5. Run staging smoke checks against `/readyz`, `/metrics`, AUTH/BIND, upstream
   forwarding, downlink push, reconnect retry, and cross-pod peer push.
6. Canary production traffic and watch online sessions, clients, downlink push,
   ACK, retry, cluster registry, peer push, upstream forwarding, HMAC
   signature, and dependency metrics.

## Docker Image Publishing

The release workflow publishes:

```text
ghcr.io/qiuyier/z-courier-gateway:<release-tag>
```

For a normal stable GitHub Release, it also publishes:

```text
ghcr.io/qiuyier/z-courier-gateway:latest
```

For pre-releases and any tag containing `-`, `latest` is not published.

Published release images are multi-architecture manifests:

```text
linux/amd64
linux/arm64
```

The workflow runs `docker buildx imagetools inspect` after pushing and fails if
either platform is missing. The manifest details are written to the GitHub
Actions summary.

The `v0.6.0` backfill was verified with:

```text
ghcr.io/qiuyier/z-courier-gateway:v0.6.0
sha256:f655c5366e69e3d38d6f694ebdb1842cb91e67fa9ba37fe016dc5dde2995d57d
linux/amd64
linux/arm64
```

BuildKit may include `unknown/unknown` attestation manifests next to the real
runtime platforms. Those entries are expected provenance metadata and are not
runtime images.

## Helm Versioning

The chart has its own version in
`deploy/helm/z-courier/Chart.yaml`.

The `v0.7.0` release should confirm:

```yaml
version: 0.2.0
appVersion: "v0.7.0"
```

Chart `0.2.0` exists because the chart now defaults to the official GHCR
gateway image repository:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: ""
```

When `image.tag` is empty, the chart uses `appVersion`.

The complete chart/app version policy and compatibility matrix are maintained
in [v6-helm-versioning.md](v6-helm-versioning.md).

## Installation Paths

Pull and smoke the gateway image directly:

```bash
docker pull ghcr.io/qiuyier/z-courier-gateway:v0.7.0
docker run --rm --entrypoint /bin/sh ghcr.io/qiuyier/z-courier-gateway:v0.7.0 -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
```

Inspect the multi-architecture manifest:

```bash
docker buildx imagetools inspect ghcr.io/qiuyier/z-courier-gateway:v0.7.0
```

Install with a local chart checkout:

```bash
helm upgrade --install z-courier ./deploy/helm/z-courier \
  --namespace z-courier \
  -f deploy/helm/z-courier/examples/values-production.yaml \
  --set image.tag=v0.7.0
```

Install from GHCR OCI:

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.2.0 \
  --namespace z-courier \
  -f values-production.yaml \
  --set image.tag=v0.7.0
```

Production users should pin both the chart version and gateway image tag.

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
DOCKER_BUILDKIT=1 docker build --tag z-courier-gateway:release-image-check .
docker run --rm --entrypoint /bin/sh z-courier-gateway:release-image-check -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
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

1. Confirm the `Release Docker Image` workflow succeeded.
2. Confirm the workflow summary includes:
   - `ghcr.io/qiuyier/z-courier-gateway:v0.7.0`
   - `linux/amd64`
   - `linux/arm64`
3. Pull and smoke the image:

   ```bash
   docker pull ghcr.io/qiuyier/z-courier-gateway:v0.7.0
   docker run --rm --entrypoint /bin/sh ghcr.io/qiuyier/z-courier-gateway:v0.7.0 -c \
     'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
   ```

4. Confirm the `Release Helm Chart` workflow succeeded.
5. Confirm the GitHub Release contains:
   - `z-courier-0.2.0.tgz`
   - `SHA256SUMS`
6. Confirm the `Release Helm OCI` workflow succeeded.
7. Pull the OCI chart:

   ```bash
   helm pull oci://ghcr.io/qiuyier/charts/z-courier --version 0.2.0
   ```

8. Optionally install the pulled chart into a staging cluster.

## GitHub Release Notes

### Highlights

- Official gateway Docker image published to GHCR.
- Multi-architecture image manifests for `linux/amd64` and `linux/arm64`.
- Release-triggered and manually backfillable Docker image publishing workflow.
- Container smoke checks before image push.
- Post-push manifest inspection with GitHub Actions summary output.
- Stable releases can publish `latest`; pre-release tags do not publish
  `latest`.
- Helm chart defaults to `ghcr.io/qiuyier/z-courier-gateway`.
- Helm chart version `0.2.0` for the official image repository default.

### Upgrade Notes

No wire-format or SDK migration is required from `v0.6.0`. Existing Docker
Compose references and gateway configurations remain valid.

Operators can keep building the image locally, but production deployments can
now use the official GHCR image directly. Pin immutable version tags for
production rollouts.

### Known Boundaries

- The release publishes the gateway image only; it does not publish PostgreSQL,
  Redis, NSQ, Prometheus, Grafana, auth backend, or business backend images.
- Delivery remains at-least-once; applications must de-duplicate important
  operations by `MessageID`.
- TLS, mTLS, and external load balancer configuration remain deployment
  responsibilities.
- Helm still expects PostgreSQL, Redis, NSQ, auth, business backend, and
  observability dependencies to be provided by the platform.
- BuildKit attestation manifests may show as `unknown/unknown` in manifest
  inspection output. They are expected metadata, not missing runtime platforms.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm `deploy/helm/z-courier/Chart.yaml` `appVersion` is `"v0.7.0"`.
3. Confirm `deploy/helm/z-courier/Chart.yaml` `version` is the intended chart
   version.
4. Confirm `CHANGELOG.md` has the final `v0.7.0` date and scope.
5. Confirm GitHub Actions is green on the exact commit.
6. Run or confirm the manual **Kubernetes E2E** workflow.
7. Confirm release notes match the final scope.
8. Create and push the annotated tag:

```bash
git tag -a v0.7.0 -m "v0.7.0"
git push origin v0.7.0
```

9. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
10. Confirm Docker image, Helm release assets, and GHCR OCI chart publication
    succeed.
