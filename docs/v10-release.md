# V10 Release Guide

This document defines the `v0.10.0` release scope, upgrade notes, admin
console operations model, verification path, and known boundaries. V10 is an
internal project phase; the public SemVer version is `v0.10.0`, not
`v10.0.0`.

## Release Scope

`v0.10.0` turns the embedded Web admin console from an inspection-only surface
into a guarded operations surface. It does not change the client wire protocol,
SDK packet format, upstream routing model, or delivery semantics.

Included in scope:

- Short-lived admin console sessions backed by HTTP-only cookies.
- Console login, session introspection, and logout endpoints.
- Read-only, operator, and admin roles for browser-initiated admin operations.
- Server-side permission checks for message repair, retry scan, session
  disconnect, and downlink test push.
- Console-visible permission metadata so disabled actions are clear instead of
  looking broken.
- Session search, selected-session details, cluster route lookup, route reuse
  for test push, and local session disconnect.
- Downlink debug playground for bounded internal test pushes.
- Retry/offline queue visibility, single-message lookup, guarded requeue,
  guarded discard, and manual retry scan.
- Bounded in-memory admin audit trail for browser admin actions, permission
  denials, session login/logout, debug pushes, retry scans, session
  disconnects, and message repair.
- Console UX hardening: copy buttons for operational IDs, permission notices,
  consistent mutation confirmation dialogs, clearer success/failure states,
  and browser smoke coverage.
- Admin console browser smoke tests with Playwright for login, core
  navigation, operator confirmations, retry scan completion, test push
  confirmation, and read-only disabled states.

## Not Included

`v0.10.0` does not include:

- A public multi-tenant SaaS dashboard.
- Full browser user management, password reset, invitations, or organization
  administration.
- Editing full gateway configuration from the browser.
- Hot-reloading upstream route configuration from the console.
- Displaying arbitrary business message bodies from stored downlink messages.
- A distributed admin session store. First-pass admin sessions are node-local.
- Replacing Prometheus, Grafana, Alertmanager, or a SIEM.
- A new packet version or incompatible wire-format change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.
- Built-in TLS, mTLS, ingress, Gateway API, service mesh, or public identity
  provider integration.

## Compatibility And Upgrade

Existing `v0.9.x` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go SDK, PHP SDK, backend SDK, admin CLI authentication modes,
  upstream routes, Redis cluster routes, PostgreSQL downlink storage, HMAC
  key rings, metrics, dashboards, Docker Compose references, and Helm chart
  values remain compatible.
- No gateway wire-protocol migration is required.
- No PostgreSQL message schema migration is required for the V10 console
  changes.

Recommended adoption path from `v0.9.x`:

1. Keep current gateway/client SDK configuration unchanged.
2. Deploy `v0.10.0` in staging with the console still behind the same private
   internal access boundary used for `v0.9.x`.
3. Confirm `/healthz`, `/readyz`, `/metrics`, AUTH/BIND, upstream forwarding,
   downlink push, reconnect retry, and cross-node peer push.
4. Enable `admin_console.session.enabled=true` and choose the intended role for
   the deployment: `readonly`, `operator`, or `admin`.
5. Verify console login, refresh, logout, and session expiry behavior.
6. Verify read-only sessions cannot call mutation endpoints.
7. Verify operator/admin sessions can run the intended guarded operations and
   that audit entries are recorded.
8. Canary production traffic and watch readiness, dependency checks, online
   sessions/clients, upstream forwarding, downlink push/ACK, retry backlog,
   cluster registry, peer push, auth/HMAC/JWKS, admin permission rejects, and
   overload rejection signals.

## Admin Console Sessions

The gateway serves the console only when `admin_console.enabled` is true. V10
adds optional browser-friendly sessions under the same internal HTTP boundary:

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  session:
    enabled: true
    ttl: 8h
    cookie_name: zcourier_admin_session
    cookie_secure: false
    cookie_same_site: lax
    role: operator
```

Session fields:

- `enabled`: enables login, session introspection, logout, and cookie-backed
  console API calls.
- `ttl`: maximum lifetime for one browser admin session.
- `cookie_name`: cookie name used by this gateway. Use distinct names when
  running multiple local gateway nodes on the same host.
- `cookie_secure`: set true when the console is served over HTTPS.
- `cookie_same_site`: `lax`, `strict`, or `none`; `none` requires
  `cookie_secure=true`.
- `role`: `readonly`, `operator`, or `admin`.

Role model:

- `readonly`: inspect overview, routes, sessions, messages, audit, checks,
  diagnostics, diagnosis bundles, and monitoring links.
- `operator`: all read-only permissions plus local session disconnect,
  downlink test push, retry scan, and guarded message repair.
- `admin`: currently equivalent to operator for implemented console
  operations; reserved for future higher-impact admin actions.

The browser login exchanges the configured internal credential for a
short-lived session. The internal token is not stored in browser local storage.
Console APIs continue to enforce permissions on the server, so disabled
buttons are a usability cue rather than a security boundary.

## Production Access Boundary

The console remains an internal operations UI:

- Keep the internal HTTP listener private.
- Do not publish `/console/` or `/internal/*` directly to the public internet.
- Prefer VPN, bastion, private ingress, or an authenticating reverse proxy.
- In HMAC internal-auth deployments, use an operator-controlled proxy or the
  `cmd/admin` CLI for machine-to-machine access.
- Keep `admin_console.monitoring` URLs pointed at operator-accessible
  Prometheus and Grafana endpoints.
- Do not treat the node-local admin session cookie as an identity provider or
  tenant boundary.

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

Local two-node cluster console URLs:

```text
http://127.0.0.1:18182/console/
http://127.0.0.1:18183/console/
```

The local cluster configs use separate cookie names so gateway A and gateway B
can be logged in independently on `127.0.0.1`.

Production operators should continue to keep CLI workflows available:

```bash
go run ./cmd/admin overview \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"

go run ./cmd/admin sessions \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -client-id "$CLIENT_ID"

go run ./cmd/admin audit \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL" \
  -limit 100
```

The console is a convenience layer over the same internal operational surface,
not a replacement for automated checks, Grafana dashboards, alerting, or
incident bundles.

## Browser Smoke

V10 adds a dedicated browser smoke path:

```bash
bash scripts/console_smoke.sh
```

The script:

1. Builds the admin console.
2. Builds a temporary gateway binary.
3. Starts a lightweight admin-console gateway with role `admin`.
4. Runs Playwright smoke tests against `/console/`.
5. Starts a second lightweight gateway with role `readonly`.
6. Runs the same smoke suite and verifies read-only mutation buttons are
   disabled.

The smoke test covers:

- Login shell before authentication.
- Console login and core page navigation.
- Retry Scan confirmation and completion state.
- Test Push confirmation dialog.
- Read-only permission notices and disabled mutation buttons.

First local run after installing dependencies may need:

```bash
npm --prefix web/admin exec -- playwright install chromium
```

GitHub Actions installs Chromium automatically before running the smoke script.

## Rollback

`v0.10.0` does not require a packet, SDK, Redis, or PostgreSQL data migration,
so rollback is intentionally simple:

1. If the issue is console-only, disable browser sessions or the console first:

   ```yaml
   admin_console:
     enabled: false
     session:
       enabled: false
   ```

2. Roll the gateway image back to the last known-good tag, for example
   `v0.9.1`.
3. If using Helm, either roll back the Helm release or explicitly set:

   ```yaml
   image:
     tag: v0.9.1
   adminConsole:
     enabled: false
   ```

4. Keep PostgreSQL, Redis, NSQ, Prometheus, Grafana, and Alertmanager data in
   place. V10 does not introduce a schema or queue-format migration.
5. After rollback, verify `/readyz`, `/metrics`, AUTH/BIND, upstream
   forwarding, downlink retry/ACK, online sessions/clients, and cluster peer
   push.

## Verification

Run from the repository root on the exact commit intended for the tag.

Fast local release checks:

```bash
bash scripts/release_check.sh
```

If Composer is provided by a Docker-based local PHP toolchain instead of a
real `composer` executable on `PATH`, point the release script at an image that
contains Composer:

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
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
- Gateway static config validation, including the console smoke config.
- Shell script syntax validation.
- `git diff --check`.

The Docker-enabled path additionally validates Docker Compose configs,
Prometheus and Alertmanager configs, Helm chart lint/template/package output,
and a local gateway image build that verifies the built console assets are
present in the image.

The slow path additionally runs local E2E, cluster E2E, admin console browser
smoke, load-test smoke, production Compose smoke, production cluster smoke,
and optional kind/Helm smoke plus E2E validation.

GitHub Actions must be green for the exact `main` commit before tagging. Run
the manual **Kubernetes E2E** workflow before release if local kind validation
cannot be repeated on the release machine.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow in upstream and downlink modes.
- Review workflow summaries and `cmd/loadcompare` output.
- Treat baseline comparison as informational unless a later release process
  explicitly promotes it to a hard gate.

## Helm And Image Notes

For the `v0.10.0` release, update the Helm chart metadata intentionally before
tagging if the chart is published with this gateway milestone:

```yaml
version: 0.5.0
appVersion: "v0.10.0"
```

Chart `0.5.0` is recommended because the gateway image now includes the V10
admin console operations surface and browser session settings. The chart
should still keep the console disabled unless the operator explicitly opts in.

For production image deployment, use immutable image tags:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.10.0
```

Production users should pin both the chart version and gateway image tag.

## GitHub Release Notes

### Highlights

- Admin console sessions with HTTP-only cookies.
- Read-only/operator/admin role model for browser console operations.
- Server-side permission checks for guarded mutation endpoints.
- Session search, selected-session detail, cluster route lookup, and local
  session disconnect.
- Downlink debug playground for safe test pushes.
- Retry/offline queue inspection with guarded requeue, discard, and retry
  scan operations.
- Browser-initiated admin audit trail for key console actions.
- Unified confirmation dialogs and copy buttons for operational IDs.
- Playwright browser smoke coverage in CI for admin and read-only flows.

### Upgrade Notes

No wire-format, SDK, Redis, or PostgreSQL migration is required from `v0.9.x`.
Existing gateway configuration remains compatible.

Admin console sessions are optional but recommended for browser use. Keep the
console behind private network controls, and choose the narrowest practical
role. Use `readonly` for inspection-only environments and `operator` for
incident-response consoles that need disconnect, test push, retry scan, and
message repair.

### Known Boundaries

- Admin console sessions are node-local in this release.
- The console intentionally omits secrets, HMAC keys, DSNs, internal tokens,
  Authorization headers, and stored business message bodies.
- The console is not a public dashboard and does not include Z-Courier-native
  browser account management.
- Prometheus and Grafana remain the source of truth for historical metrics and
  alerting.
- Delivery remains at-least-once; important business operations still need
  application-side durable `MessageID` de-duplication.
- TLS, mTLS, ingress, network policy enforcement, and service mesh policy
  remain deployment responsibilities.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm `CHANGELOG.md` has the final `v0.10.0` date and scope if the
   changelog is being updated for this milestone.
3. Update `deploy/helm/z-courier/Chart.yaml` to the intended chart version and
   `appVersion: "v0.10.0"` if publishing a chart package.
4. Confirm GitHub Actions is green on the exact commit.
5. Run or confirm the manual **Kubernetes E2E** workflow.
6. Confirm release notes match the final scope.
7. Create and push the annotated tag:

```bash
git tag -a v0.10.0 -m "v0.10.0"
git push origin v0.10.0
```

8. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
9. Confirm Docker image, Helm release assets if applicable, and GHCR OCI chart
   publication if applicable succeed.
