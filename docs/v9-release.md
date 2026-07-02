# V9 Release Guide

This document defines the `v0.9.0` release scope, upgrade notes, Web admin
console deployment guidance, verification path, and known boundaries. V9 is an
internal project phase; the public SemVer version is `v0.9.0`, not `v9.0.0`.

## Release Scope

`v0.9.0` adds a browser-based operations console on top of the existing
internal admin APIs. It does not change the client wire protocol, SDK packet
format, delivery semantics, or upstream routing model.

Included in scope:

- Optional embedded Web admin console served from the gateway internal HTTP
  listener.
- React/Vite admin frontend build under `web/admin`.
- Docker image packaging for built console assets.
- Helm and YAML configuration for enabling or disabling the console path.
- Overview screen for gateway identity, readiness, sessions, routes, downlink
  storage, dependency summary, and configured monitoring links.
- Routes screen for MsgID ranges, target type, sanitized targets, route
  capacity, and HTTP route runtime state.
- Sessions screen for local session search and cluster route lookup by
  `ClientID` and `DeviceID`.
- Messages screen for bounded downlink message listing, single-message lookup,
  and guarded requeue/discard actions.
- Checks screen for active dependency probes.
- Diagnostics screen for sanitized runtime diagnostics and downloadable
  diagnosis bundles.
- Prometheus and Grafana context links plus PromQL snippets for important
  operational signals.
- Static console response hardening with CSP, no-referrer, nosniff,
  frame-deny, permission-deny, and cache-control headers.
- Production and Helm documentation that keeps `/console/` and `/internal/*`
  inside the internal admin boundary.

## Not Included

`v0.9.0` does not include:

- A public multi-tenant SaaS dashboard.
- Browser user/password account management inside Z-Courier.
- Direct browser-side HMAC signing as the recommended production path.
- Editing full gateway configuration from the browser.
- Displaying raw message bodies in the console.
- Replacing Prometheus, Grafana, or Alertmanager.
- A new packet version or incompatible wire-format change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.
- Built-in TLS, mTLS, ingress, Gateway API, service mesh, or public identity
  provider integration.
- Installing PostgreSQL, Redis, NSQ, Prometheus, Grafana, Alertmanager, or
  other dependencies from the Helm chart.

## Compatibility And Upgrade

Existing `v0.8.1` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go SDK, PHP SDK, backend SDK, admin CLI authentication modes,
  upstream routes, Redis cluster routes, PostgreSQL downlink storage, HMAC
  key rings, metrics, dashboards, Docker Compose references, and Helm chart
  values remain compatible.
- No gateway wire-protocol migration is required.

Recommended adoption path from `v0.8.1`:

1. Keep current gateway/client SDK configuration unchanged.
2. Deploy `v0.9.0` in staging with the console disabled first.
3. Verify `/healthz`, `/readyz`, `/metrics`, AUTH/BIND, upstream forwarding,
   downlink push, reconnect retry, and cross-node peer push.
4. Enable the console only on an internal network, VPN, bastion, private
   ingress, or authenticating reverse proxy.
5. Confirm the console overview, routes, sessions, messages, checks, and
   diagnostics screens match existing CLI/API output.
6. Canary production traffic and watch readiness, dependency checks, online
   sessions/clients, upstream forwarding, downlink push/ACK, retry backlog,
   cluster registry, peer push, auth/HMAC/JWKS, and overload rejection signals.

## Admin Console Configuration

The gateway serves the console only when `admin_console.enabled` is true:

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  monitoring:
    prometheus_url: http://prometheus.local:9090
    grafana_url: http://grafana.local:3000
    dashboard_url: http://grafana.local:3000/d/z-courier
```

The production Compose references and Helm chart defaults keep the console
disabled. That is intentional: the console is an operations UI for the
internal admin plane, not a public endpoint.

When enabling the console in production:

- Keep the internal HTTP listener private.
- Do not publish `/console/` or `/internal/*` directly to the public internet.
- Prefer VPN, bastion, private ingress, or an authenticating reverse proxy.
- In HMAC internal-auth deployments, use an operator-controlled proxy or the
  `cmd/admin` CLI for machine-to-machine access. The browser console is safest
  when a trusted private layer terminates operator authentication and forwards
  only authorized internal requests.
- Keep `admin_console.monitoring` URLs pointed at operator-accessible
  Prometheus and Grafana endpoints.

Console static responses set defensive headers and cache behavior:

- `Content-Security-Policy` restricts scripts, styles, images, fonts, form
  actions, objects, and frames to the console origin.
- `Referrer-Policy: no-referrer`
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Permissions-Policy` disables camera, microphone, geolocation, and payment.
- `Cache-Control: no-store` for the HTML shell and SPA fallback.
- `Cache-Control: public, max-age=31536000, immutable` for built asset files.

## Runtime Use

Local development with the default token-auth config:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

Open:

```text
http://127.0.0.1:18080/console/
```

Use the internal token configured in `configs/z-courier.yaml`.

Production operators should continue to keep CLI workflows available:

```bash
go run ./cmd/admin overview \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"

go run ./cmd/admin check \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -probe-timeout 2s

go run ./cmd/admin diagnose \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -output reports/diagnose/gateway.json
```

The console is a convenience layer over the same internal operational surface,
not a replacement for automated checks, Grafana dashboards, alerting, or
incident bundles.

## Rollback

`v0.9.0` does not require a packet, SDK, Redis, or PostgreSQL data migration, so
rollback is intentionally simple:

1. Disable the console first if the issue is UI-only:

   ```yaml
   admin_console:
     enabled: false
   ```

2. Roll the gateway image back to the last known-good tag, for example
   `v0.8.1`.
3. If using Helm, either roll back the Helm release or explicitly set:

   ```yaml
   image:
     tag: v0.8.1
   adminConsole:
     enabled: false
   ```

4. Keep PostgreSQL, Redis, NSQ, Prometheus, Grafana, and Alertmanager data in
   place. V9 does not introduce a schema or queue-format migration.
5. After rollback, verify `/readyz`, `/metrics`, AUTH/BIND, upstream
   forwarding, downlink retry/ACK, online sessions/clients, and cluster peer
   push.

## Verification

Run from the repository root on the exact commit intended for the tag.

Fast local release checks:

```bash
bash scripts/release_check.sh
```

Full local release checks, including Docker-backed validation and long-running
smoke/E2E paths:

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

The fast script covers:

- `actionlint` when installed.
- Admin console `npm ci` and production build.
- Go tests, selected race tests, and `go vet`.
- PHP SDK tests, syntax checks, dependency install, and PHPStan analysis.
- Gateway static config validation.
- Shell script syntax validation.
- `git diff --check`.

The Docker-enabled path additionally validates Docker Compose configs,
Prometheus and Alertmanager configs, Helm chart lint/template/package output,
and a local gateway image build that verifies the built console assets are
present in the image.

The slow path additionally runs local E2E, cluster E2E, load-test smoke,
production Compose smoke, production cluster smoke, and optional kind/Helm
smoke plus E2E validation.

GitHub Actions must be green for the exact `main` commit before tagging. Run
the manual **Kubernetes E2E** workflow before release if local kind validation
cannot be repeated on the release machine.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow in upstream and downlink modes.
- Review workflow summaries and `cmd/loadcompare` output.
- Treat baseline comparison as informational unless a later release process
  explicitly promotes it to a hard gate.

## Helm And Image Notes

For the `v0.9.0` release, update the Helm chart metadata intentionally before
tagging:

```yaml
version: 0.4.0
appVersion: "v0.9.0"
```

Chart `0.4.0` is recommended because the gateway image now includes the
embedded admin console assets. The chart still defaults
`adminConsole.enabled=false`, so installing the chart does not expose the UI
unless the operator opts in.

For production image deployment, use immutable image tags:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.9.0
```

Production users should pin both the chart version and gateway image tag.

## GitHub Release Notes

### Highlights

- Embedded Web admin console served by the gateway internal HTTP listener.
- Overview, routes, sessions, messages, checks, and diagnostics pages.
- Browser access to sanitized diagnosis bundles and active dependency checks.
- Guarded downlink requeue/discard actions from the console.
- Configurable Prometheus and Grafana links plus PromQL context snippets.
- Docker image packaging for built console assets.
- Helm values and documentation for opt-in console deployment.
- Static console security headers and cache-control hardening.
- Production docs clarifying that `/console/` and `/internal/*` are private
  admin-plane endpoints.

### Upgrade Notes

No wire-format or SDK migration is required from `v0.8.1`. Existing gateway
configuration remains compatible.

The console is optional. Production operators should enable it only through a
private access path such as VPN, bastion, private ingress, or an authenticating
reverse proxy. HMAC-protected internal APIs remain the preferred
machine-to-machine mode.

### Known Boundaries

- The console intentionally omits secrets, HMAC keys, DSNs, internal tokens,
  Authorization headers, and message bodies.
- The console is not a public dashboard and does not include Z-Courier-native
  browser account management.
- HMAC-authenticated browser access should be mediated by deployment-side
  infrastructure instead of exposing raw internal credentials to browsers.
- Prometheus and Grafana remain the source of truth for historical metrics and
  alerting.
- Delivery remains at-least-once; important business operations still need
  application-side durable `MessageID` de-duplication.
- TLS, mTLS, ingress, network policy enforcement, and service mesh policy
  remain deployment responsibilities.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm `CHANGELOG.md` has the final `v0.9.0` date and scope.
3. Update `deploy/helm/z-courier/Chart.yaml` to the intended chart version and
   `appVersion: "v0.9.0"` if publishing a chart package.
4. Confirm GitHub Actions is green on the exact commit.
5. Run or confirm the manual **Kubernetes E2E** workflow.
6. Confirm release notes match the final scope.
7. Create and push the annotated tag:

```bash
git tag -a v0.9.0 -m "v0.9.0"
git push origin v0.9.0
```

8. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
9. Confirm Docker image, Helm release assets if applicable, and GHCR OCI chart
   publication if applicable succeed.
