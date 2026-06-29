# V8 Release Guide

This document defines the `v0.8.0` release scope, upgrade notes, production
operations verification path, and known boundaries. V8 is an internal project
phase; the public SemVer version is `v0.8.0`, not `v8.0.0`.

## Release Scope

`v0.8.0` focuses on production operations governance. It does not change the
client wire protocol or delivery model; instead, it makes a running deployment
easier to validate, diagnose, alert on, and hand off during incidents.

Included in scope:

- Static gateway configuration validation through
  `cmd/gateway -check-config`.
- CI validation for representative local, integration, and cluster gateway
  configurations.
- Runtime admin diagnostics that report gateway identity, readiness and drain
  state, sanitized configuration summaries, dependency summaries, upstream
  route runtime state, cluster/session information, and capacity indicators.
- Active dependency checks through `cmd/admin check`.
- Safe diagnosis bundle collection through `cmd/admin diagnose`, including
  partial-section status and secret-safe output.
- Prometheus recording and alert rules for gateway, auth, upstream, downlink,
  retry, cluster, HMAC, and JWKS signals.
- Alertmanager local configuration plus webhook, email, and Slack examples.
- Grafana production-signal dashboard panels for alert-oriented operational
  views.
- Readiness drain diagnostics and `z_courier_gateway_readiness` metrics.
- HTTP upstream route health tracking with healthy, degraded, and unavailable
  states plus `z_courier_upstream_route_degraded`.
- Downlink retry jitter configuration to reduce synchronized retry bursts.
- More precise gateway error reason reporting for overload, rate-limit,
  forwarding, and delivery paths.
- PHP SDK receive-loop timeout hardening so reconnecting clients keep blocking
  correctly while waiting for downlink packets.

## Not Included

`v0.8.0` does not include:

- A new packet version or incompatible wire-format change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.
- A browser admin console.
- A Kubernetes operator.
- Built-in TLS, mTLS, ingress, Gateway API, or service-mesh resources.
- Installing PostgreSQL, Redis, NSQ, Prometheus, Grafana, Alertmanager, or
  other dependencies from the Helm chart.
- New SDK languages.
- A hard performance regression gate. Load-test baseline comparisons remain
  informational unless a later release process explicitly promotes them.

## Compatibility And Upgrade

Existing `v0.7.0` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go SDK, PHP SDK, backend SDK, admin CLI authentication modes,
  upstream routes, Redis cluster routes, PostgreSQL downlink storage, HMAC
  key rings, metrics, dashboards, Docker Compose references, and Helm chart
  values remain compatible.
- No gateway wire-protocol migration is required.

Recommended adoption path from `v0.7.0`:

1. Keep existing gateway configuration and client SDK usage unchanged.
2. Run static config validation before rollout:

   ```bash
   go run ./cmd/gateway -config configs/z-courier.yaml -check-config
   ```

3. Start in staging and verify `/healthz`, `/readyz`, `/metrics`,
   AUTH/BIND, upstream forwarding, downlink push, reconnect retry, and
   cross-node peer push.
4. Collect a pre-rollout diagnosis bundle:

   ```bash
   go run ./cmd/admin diagnose \
     -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
     -output reports/diagnose/staging-gateway.json
   ```

5. Import or refresh the bundled Prometheus rules, Alertmanager config, and
   Grafana dashboards.
6. Canary production traffic and watch readiness, online sessions and clients,
   downlink push and ACK, retry backlog, cluster registry, peer push, upstream
   forwarding, auth/HMAC/JWKS, dependency checks, and overload rejection
   signals.

## Static Configuration Validation

The gateway can validate static configuration without starting the TCP server
or connecting to external dependencies:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml -check-config
```

The validator checks YAML shape, durations, auth provider configuration,
internal HTTP auth, HMAC key-ring shape, cluster and downlink storage settings,
pipeline rate-limit settings, enabled upstream targets, upstream route overlaps,
reserved business MsgID conflicts, and operational warnings such as memory
cluster routing in deployment-like configurations.

The CI workflow runs validation against:

- `configs/z-courier.yaml`
- `configs/z-courier.integration.yaml`
- `configs/z-courier.cluster-a.yaml`
- `configs/z-courier.cluster-b.yaml`

Validation is static by design. It does not fetch JWKS documents, connect to
PostgreSQL or Redis, or publish to NSQ. Use `cmd/admin check` after startup for
active dependency checks.

## Runtime Diagnostics

`cmd/admin diagnostics` queries the authenticated internal admin API and returns
sanitized runtime state:

```bash
go run ./cmd/admin diagnostics \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

The response includes gateway identity, readiness/drain state, cluster summary,
local session counts, dependency summary, upstream route runtime states,
capacity indicators, and warnings. Secrets such as internal tokens, HMAC
secrets, DSNs, upstream route tokens, and message bodies are omitted or
redacted by the server-side admin APIs.

`cmd/admin check` actively probes dependencies that expose safe health checks:

```bash
go run ./cmd/admin check \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -probe-timeout 2s
```

The command exits non-zero when the gateway reports failed dependency status,
which makes it suitable for smoke scripts and deployment checks.

`cmd/admin diagnose` collects an issue-friendly bundle:

```bash
go run ./cmd/admin diagnose \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -output reports/diagnose/gateway.json
```

With a client/device pair, the bundle also includes route lookup and local
session inspection:

```bash
go run ./cmd/admin diagnose \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -client-id example-client \
  -device-id example-device \
  -output reports/diagnose/example-client.json
```

Each collected section records its HTTP status independently. Partial failures
remain visible instead of hiding the rest of the bundle.

## Monitoring And Alerting

The bundled monitoring files live under `deploy/monitoring`:

- Prometheus rules:
  `deploy/monitoring/prometheus/rules/z-courier-alerts.yml`
- Alertmanager default config:
  `deploy/monitoring/alertmanager/alertmanager.yml`
- Alertmanager webhook, email, and Slack examples:
  `deploy/monitoring/alertmanager/examples`
- Grafana dashboards:
  `deploy/monitoring/grafana/dashboards`

Validate Prometheus and Alertmanager files with:

```bash
bash scripts/promtool_check.sh
```

The default Alertmanager receiver is local and does not send external
notifications. Production users should copy one of the examples or wire the
alerts into their platform alerting system.

The bundled alert rules are example production defaults. Tune thresholds for
real traffic before using them for paging.

## Resilience Signals

V8 adds more visible degradation signals without changing delivery semantics:

- `/readyz` returns `503` after graceful shutdown drain begins.
- `cmd/admin diagnostics` includes readiness status, drain start time, and
  drain duration.
- Prometheus exports `z_courier_gateway_readiness{status="ready"}` and
  `z_courier_gateway_readiness{status="draining"}`.
- HTTP upstream routes track healthy, degraded, and unavailable states after
  consecutive classified failures.
- Prometheus exports `z_courier_upstream_route_degraded`.
- Downlink retry jitter spreads retries after failures so recovering
  dependencies are not hit by a synchronized retry burst.

These signals help operators distinguish local drain, dependency degradation,
rate limiting, overload, upstream forwarding failure, retry backlog growth, and
application-level delivery failures.

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
go run ./cmd/gateway -config configs/z-courier.yaml -check-config
go run ./cmd/gateway -config configs/z-courier.integration.yaml -check-config
go run ./cmd/gateway -config configs/z-courier.cluster-a.yaml -check-config
go run ./cmd/gateway -config configs/z-courier.cluster-b.yaml -check-config
bash scripts/promtool_check.sh
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
bash scripts/production_smoke.sh
bash scripts/production_cluster_smoke.sh
bash scripts/k8s_helm_smoke.sh
bash scripts/k8s_helm_e2e.sh
docker compose -f deploy/local/docker-compose.yml config
docker compose -f deploy/monitoring/docker-compose.yml config
docker compose --env-file deploy/production/.env.example -f deploy/production/docker-compose.yml config
docker compose --env-file deploy/production-cluster/.env.example -f deploy/production-cluster/docker-compose.yml config
helm lint deploy/helm/z-courier
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-kind-smoke.yaml
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-k8s-e2e.yaml
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
helm package deploy/helm/z-courier --destination /tmp
DOCKER_BUILDKIT=1 docker build --tag z-courier-gateway:release-check .
docker run --rm --entrypoint /bin/sh z-courier-gateway:release-check -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
git diff --check
```

GitHub Actions must be green for the exact `main` commit before tagging.
Run the manual **Kubernetes E2E** workflow before release if local kind
validation cannot be repeated on the release machine.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow in upstream and downlink modes.
- Review workflow summaries and `cmd/loadcompare` output.
- Treat baseline comparison as informational unless the release process
  explicitly promotes it to a hard gate.

## Helm And Image Notes

V8 itself does not require a chart template behavior change. If publishing a
new chart package with the `v0.8.0` release, update
`deploy/helm/z-courier/Chart.yaml` `appVersion` to `"v0.8.0"` and choose the
chart version according to [v6-helm-versioning.md](v6-helm-versioning.md).

For production image deployment, use immutable image tags:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.8.0
```

## GitHub Release Notes

### Highlights

- Static gateway config validation through `cmd/gateway -check-config`.
- Runtime diagnostics and safe `cmd/admin diagnose` bundles.
- Active dependency checks through `cmd/admin check`.
- Prometheus recording and alert rules plus Alertmanager examples.
- Grafana production-signal dashboard updates.
- Readiness drain diagnostics and gateway readiness metrics.
- HTTP upstream route degraded/unavailable state tracking.
- Downlink retry jitter to avoid synchronized retry bursts.
- More precise error reason reporting for operational diagnosis.
- PHP SDK receive-loop hardening for reconnect and blocking downlink receive.

### Upgrade Notes

No wire-format or SDK migration is required from `v0.7.0`. Existing gateway
configuration remains compatible.

Operators should add `cmd/gateway -check-config`, `cmd/admin check`, and
`cmd/admin diagnose` to their deployment and incident workflows. Alert rules
and dashboards are provided as production-oriented examples; tune thresholds
before using them for paging.

### Known Boundaries

- Diagnostics and admin bundles intentionally omit secrets and message bodies.
- Active dependency checks only cover dependencies that expose safe probe
  behavior.
- Static config validation does not connect to external dependencies.
- Alert thresholds are defaults and should be calibrated with real traffic.
- Delivery remains at-least-once; important business operations still need
  application-side durable `MessageID` de-duplication.
- TLS, mTLS, ingress, network policy enforcement, and service mesh policy
  remain deployment responsibilities.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm `CHANGELOG.md` has the final `v0.8.0` date and scope.
3. Confirm GitHub Actions is green on the exact commit.
4. Run or confirm the manual **Kubernetes E2E** workflow.
5. Confirm release notes match the final scope.
6. If publishing a chart package, confirm `Chart.yaml` `appVersion` and chart
   version are intentionally set.
7. Create and push the annotated tag:

```bash
git tag -a v0.8.0 -m "v0.8.0"
git push origin v0.8.0
```

8. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
9. Confirm Docker image, Helm release assets if applicable, and GHCR OCI chart
   publication if applicable succeed.
