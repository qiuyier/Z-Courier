# V11 Release Guide

This document defines the `v0.11.0` release scope, upgrade path, production
configuration, verification steps, rollback procedure, and known boundaries.
V11 is an internal project phase; the public SemVer version is `v0.11.0`, not
`v11.0.0`.

## Release Scope

`v0.11.0` makes the embedded admin control plane more durable and useful in
clustered deployments. It does not change the client wire protocol, SDK packet
format, upstream routing model, or downlink delivery semantics.

Included in scope:

- Optional PostgreSQL-backed admin audit storage that survives gateway
  restarts.
- Optional Redis-backed admin console sessions shared by gateway nodes.
- Cluster-wide route visibility in the console, including owning gateway,
  internal address, route age, and remaining TTL.
- Explicit cluster-peer test-push delivery with route preflight, structured
  failure codes, and origin/target gateway metadata.
- CSRF protection, origin checks, JSON content-type enforcement, and security
  headers for cookie-authenticated browser mutations.
- Stable cursor pagination for admin audit events and stored downlink messages.
- Diagnostics, active dependency checks, Prometheus metrics, Grafana panels,
  and alert rules for admin audit and session storage.
- Cluster E2E coverage for cross-node Redis admin sessions, PostgreSQL audit
  persistence, and related metrics.

## Not Included

`v0.11.0` does not include:

- A public multi-tenant dashboard or a browser identity provider.
- SSO, SAML, OAuth, OIDC, user invitations, or password management.
- Remote session disconnect. Session disconnect remains local to the gateway
  that owns the TCP connection.
- Editing or hot-reloading arbitrary gateway configuration from the console.
- Displaying stored business message bodies in the console.
- Automatic PostgreSQL admin audit retention or archival.
- A replacement for Prometheus, Grafana, Alertmanager, or a SIEM.
- A new packet version or incompatible SDK change.
- Exactly-once delivery. Applications still own durable `MessageID`
  de-duplication for important business operations.

## Compatibility

Existing `v0.10.x` deployments remain compatible:

- Packet version `1` is unchanged.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go and PHP SDKs, backend SDKs, internal token/HMAC authentication,
  upstream routes, Redis cluster routes, and PostgreSQL downlink storage remain
  compatible.
- Existing `memory` admin audit and session stores remain valid defaults for
  local and single-node deployments.
- No client, SDK, NSQ, Redis route-key, or downlink-message schema migration is
  required.

PostgreSQL audit storage introduces the table
`z_courier_admin_audit_events`. When `auto_migrate` is true, the gateway creates
the table and its indexes at startup. Operators who disable automatic migration
must apply an equivalent schema before enabling the PostgreSQL audit store.

## Recommended Upgrade From V10

1. Back up the production gateway configuration and PostgreSQL database.
2. Deploy `v0.11.0` in staging with existing V10 memory-backed admin storage.
3. Verify `/healthz`, `/readyz`, `/metrics`, AUTH/BIND, upstream forwarding,
   downlink push/ACK, retry delivery, and cluster peer push.
4. For a clustered console, configure Redis-backed admin sessions with the same
   Redis address, database, and key prefix on every gateway node.
5. Configure PostgreSQL-backed admin audit storage when audit history must
   survive gateway restarts.
6. Run gateway configuration validation before rollout.
7. Roll one gateway node at a time and verify admin diagnostics and active
   dependency checks after each node starts.
8. Log in through one gateway, confirm the same session is recognized by a
   second gateway, and verify the login event exists in PostgreSQL.
9. Verify read-only and operator permissions, CSRF-protected mutations,
   cluster route lookup, peer test push, and cursor pagination.
10. Watch admin storage failures, cluster peer failures, stale routes, retry
    backlog, readiness, and upstream/downlink signals during canary traffic.

## Production Admin Storage

For a multi-node deployment, use Redis for browser admin sessions and
PostgreSQL for durable audit history:

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  session:
    enabled: true
    ttl: 8h
    cookie_name: zcourier_admin_session
    cookie_secure: true
    cookie_same_site: lax
    role: operator
    store:
      type: redis
      redis:
        addr: redis:6379
        username: ""
        password: "${ZCOURIER_REDIS_PASSWORD}"
        db: 0
        key_prefix: zcourier:production:admin-session
        dial_timeout: 1s
        read_timeout: 1s
        write_timeout: 1s
        operation_timeout: 2s
  audit:
    type: postgres
    capacity: 1000
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
      auto_migrate: true
      max_open_conns: 10
      max_idle_conns: 5
      conn_max_lifetime: 30m
      operation_timeout: 2s
```

All gateway nodes that may receive the same browser traffic must use compatible
session settings. In particular, keep `cookie_name`, session `ttl`, role, Redis
database, and `key_prefix` aligned. Use a deployment-specific key prefix so
staging and production sessions cannot collide.

The memory stores remain useful when:

- the gateway is a single local process;
- losing console sessions on restart is acceptable; and
- audit history is only needed for short-lived development inspection.

The Redis and PostgreSQL stores are recommended when:

- browser requests may be balanced across gateway nodes;
- gateway processes or pods are replaced regularly; or
- incident review requires durable admin action history.

## Browser Security Boundary

The console remains an internal operations surface:

- Keep the internal HTTP listener and `/console/` private.
- Use VPN, bastion, private ingress, or an authenticating reverse proxy.
- Set `cookie_secure: true` whenever the browser uses HTTPS.
- Prefer `cookie_same_site: strict` when the deployment does not need
  cross-site navigation; otherwise use `lax`.
- Do not expose `/internal/*` directly to the public internet.
- Use the narrowest practical role. Prefer `readonly` for inspection and
  `operator` only where mutation operations are required.
- Keep internal tokens, HMAC keys, Redis credentials, and PostgreSQL DSNs in a
  secret manager or injected environment variables.

For cookie-authenticated mutations, the server requires the CSRF token returned
by login or session introspection. The embedded console sends it in
`X-ZCourier-CSRF-Token`. The server also checks the request content type and,
when present, browser `Origin` or `Referer` information. Token- and HMAC-based
machine clients remain compatible with their existing authentication flow.

## Cluster Operations

The console can inspect Redis-backed online routes from either gateway node.
A cluster route identifies the gateway that owns the live TCP connection; it
is not itself a TCP session on the querying node.

V11 allows downlink test pushes to follow the discovered route through the
existing authenticated peer HTTP channel. The preflight and result include the
delivery path and target gateway. Peer failures are returned as structured
codes such as authentication failure, timeout, missing peer configuration, or
target not found.

Session disconnect remains local-only. To disconnect a client, use the console
or admin API on the gateway that owns the TCP session. V11 does not silently
forward disconnect requests to another node.

## Pagination And Data Retention

The audit and stored-message APIs use opaque cursors and bounded limits. Pass
the returned `next_cursor` to fetch the next page; clients must not parse or
construct cursor values themselves.

Downlink message retention remains controlled by `downlink.retention`:

```yaml
downlink:
  retention:
    delivered_ttl: 24h
    failed_ttl: 168h
    discarded_ttl: 168h
    cleanup_interval: 1h
    cleanup_limit: 1000
```

PostgreSQL admin audit entries are durable but are not automatically deleted in
V11. Production operators should monitor table growth and apply an external,
reviewed retention or archival policy appropriate to their compliance needs.

## Diagnostics And Monitoring

Use the admin diagnostics and active-check endpoints to confirm configured
storage modes and connectivity:

```bash
go run ./cmd/admin diagnostics \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"

go run ./cmd/admin check \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

The relevant Prometheus signals include:

```text
z_courier_admin_audit_write_total
z_courier_admin_audit_write_duration_seconds
z_courier_admin_session_store_operation_total
z_courier_admin_session_store_operation_duration_seconds
z_courier_admin_csrf_rejected_total
z_courier_cluster_peer_push_total
z_courier_cluster_peer_push_duration_seconds
z_courier_cluster_stale_routes_total
```

The bundled Grafana dashboard and Prometheus alert rules expose admin audit
write failures, Redis session-store errors, cluster peer failures, and stale
routes. Prometheus and Grafana remain the source of truth for historical
metrics; the console is an operational view, not a monitoring database.

## Rollback To V10

The safest rollback order is:

1. Stop V11-only browser mutations and allow active operations to finish.
2. Disable the console or browser sessions if the incident is isolated to the
   control plane:

   ```yaml
   admin_console:
     enabled: false
     session:
       enabled: false
   ```

3. Roll gateway nodes back one at a time to the last known-good `v0.10.0`
   image.
4. Restore the V10-compatible admin storage configuration. V10 browser
   sessions are node-local and do not understand the Redis session store.
5. Keep Redis route data, PostgreSQL downlink messages, and
   `z_courier_admin_audit_events` in place. The V10 gateway ignores the V11
   audit table, so dropping it is unnecessary and makes later recovery harder.
6. Verify readiness, AUTH/BIND, upstream forwarding, downlink push/ACK, retry
   delivery, online sessions, and cluster peer push.

Rollback does not require a client protocol, SDK, NSQ, Redis route registry, or
downlink-message migration. Browser admin sessions created by V11 should be
treated as expired after rollback; operators can log in again through V10.

## Verification

Run checks from the repository root on the exact commit intended for the tag.

Fast release checks:

```bash
bash scripts/release_check.sh
```

If Composer is provided by a local Docker image:

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
bash scripts/release_check.sh
```

Full release checks, including Docker, smoke, E2E, browser, and kind/Helm
validation:

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

The cluster E2E specifically verifies:

- cross-node downlink through Redis route discovery and peer HTTP;
- Redis-backed admin session lookup across gateway nodes;
- PostgreSQL persistence of the admin login audit event;
- admin audit and session-store Prometheus samples; and
- reconnect, queued retry, ACK, upstream NSQ, and stale-route behavior.

GitHub Actions must be green on the exact `main` commit before tagging. Run the
manual Kubernetes E2E workflow when kind validation cannot be repeated locally.
Manual load tests and baseline comparisons remain additional confidence signals
and are not release failure gates.

## Helm And Image Notes

Before tagging, update `deploy/helm/z-courier/Chart.yaml` to the chart version
chosen for the V11 release and set:

```yaml
appVersion: "v0.11.0"
```

The chart keeps the admin console opt-in. Cluster deployments that enable it
should explicitly configure Redis session storage and PostgreSQL audit storage.
Pin both chart and gateway image versions in production:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.11.0
```

## GitHub Release Notes

### Highlights

- Durable PostgreSQL admin audit history.
- Redis-backed browser sessions shared across gateway nodes.
- Cluster-wide route inspection from one console.
- Explicit, authenticated cluster-peer test-push delivery.
- CSRF and browser mutation hardening.
- Cursor pagination for audit events and stored messages.
- Admin storage diagnostics, metrics, dashboards, alerts, and cluster E2E.

### Upgrade Notes

No client protocol or SDK migration is required from `v0.10.x`. Existing
memory-backed admin stores remain compatible. Multi-node console deployments
should opt into Redis-backed admin sessions, and production deployments that
require durable operational history should use PostgreSQL-backed audit storage.

Enabling PostgreSQL audit with `auto_migrate: true` creates
`z_courier_admin_audit_events` and its indexes. V11 does not automatically
delete audit rows, so define an external retention or archival policy before
long-term production use.

### Known Boundaries

- The console remains private infrastructure, not a public dashboard.
- Remote test push is supported; remote session disconnect is not.
- Redis admin sessions provide cluster continuity, not user federation.
- PostgreSQL audit is an operational trail, not a SIEM replacement.
- Stored business message bodies remain hidden from the console.
- Delivery remains at-least-once and requires application-side durable
  `MessageID` de-duplication where business correctness depends on it.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm `CHANGELOG.md` has the final `v0.11.0` date and scope when updating
   the changelog for this milestone.
3. Update Helm chart `version` and `appVersion: "v0.11.0"` if publishing a chart
   package with this release.
4. Run the full release check on the intended release commit.
5. Confirm GitHub Actions and the manual Kubernetes E2E are green.
6. Review the GitHub release notes and known boundaries above.
7. Create and push the annotated tag:

```bash
git tag -a v0.11.0 -m "v0.11.0"
git push origin v0.11.0
```

8. Create a normal GitHub Release, not a pre-release.
9. Confirm Docker image publication, Helm release assets, and GHCR OCI chart
   publication when enabled for the tag.
