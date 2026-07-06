# V5 Admin Operations

V5 introduces a small operations surface for answering common gateway questions
without querying Redis, PostgreSQL, or raw logs by hand.

This is not a browser admin console. The first milestone is a stable internal
HTTP contract plus a safe `cmd/admin` CLI. For incident-oriented command
sequences and Prometheus queries, see the
[V5 Production Runbook](v5-production-runbook.md).

## Internal APIs

All admin endpoints live under the existing internal HTTP server and use the
same authentication mode as other backend-facing internal APIs:

- token mode: `X-ZCourier-Internal-Token`
- HMAC mode: Z-Courier HMAC headers

`/healthz`, `/readyz`, and `/metrics` remain unauthenticated for probes and
Prometheus. Admin APIs are under `/internal/*`, so HMAC mode protects them.

### `GET /internal/admin/overview`

Returns gateway identity, readiness, local session counts, cluster summary,
internal HTTP mode, downlink delivery settings, upstream route count, and
configuration-level dependency status.

Example:

```json
{
  "code": "ok",
  "gateway_node": "gateway-a",
  "readiness": {
    "ready": true,
    "status": "ready"
  },
  "sessions": {
    "online": 2,
    "unique_clients": 1
  },
  "cluster": {
    "enabled": true,
    "internal_addr": "http://gateway-a:18080",
    "registry_type": "redis",
    "registry_ttl": "30s",
    "route_refresh_interval": "10s",
    "peer_auth_mode": "hmac"
  },
  "internal_http": {
    "enabled": true,
    "addr": "0.0.0.0:18080",
    "auth_mode": "hmac",
    "max_request_body_size": 10485760,
    "max_in_flight": 2000
  },
  "downlink": {
    "storage_type": "postgres",
    "store_configured": true,
    "retry_interval": "5s",
    "retry_delay": "30s",
    "retry_jitter": "5s",
    "ack_timeout": "30s",
    "retry_lease": "30s",
    "max_attempts": 10,
    "scan_limit": 500,
    "bind_flush_limit": 500
  },
  "upstream": {
    "routes": 2
  },
  "dependencies": [
    {
      "name": "downlink_store",
      "status": "configured"
    },
    {
      "name": "cluster_registry",
      "status": "configured"
    }
  ]
}
```

The dependency list is intentionally conservative. It reports whether a
dependency is configured and attached to the gateway process; it is not a
replacement for active PostgreSQL, Redis, or NSQ health probes.

### `GET /internal/admin/diagnostics`

Returns a runtime diagnosis snapshot for one gateway process. This endpoint is
read-only and does not actively connect to PostgreSQL, Redis, NSQ, JWKS, or
business backends; it reports the state and configuration already known by the
process.

Example:

```json
{
  "code": "ok",
  "gateway_node": "gateway-a",
  "runtime": {
    "started": true,
    "started_at": "2026-06-27T12:00:00Z",
    "uptime": "10m0s"
  },
  "readiness": {
    "ready": true,
    "status": "ready"
  },
  "sessions": {
    "online": 2,
    "unique_clients": 1
  },
  "auth": {
    "provider": "http",
    "verifier_loaded": true
  },
  "upstream": {
    "routes": 2,
    "http_routes": 1,
    "nsq_routes": 1,
    "routes_with_capacity_limit": 2,
    "http_route_states": [
      {
        "name": "production-http-upstream",
        "target_type": "http",
        "status": "healthy"
      }
    ]
  },
  "capacity": {
    "internal_http_max_in_flight": 2000,
    "upstream_limited_routes": 2,
    "rate_limit_enabled": true,
    "rate_limit_max_requests": 1000,
    "rate_limit_window": "1s"
  },
  "warnings": []
}
```

Diagnostics intentionally omit secrets. Internal tokens, HMAC secrets, upstream
tokens, NSQ auth secrets, PostgreSQL DSNs, Redis passwords, URL user-info,
queries, fragments, and message bodies must not be exposed through this
endpoint.

When graceful shutdown begins, readiness changes to:

```json
{
  "ready": false,
  "status": "draining",
  "draining_since": "2026-06-29T10:00:00Z",
  "drain_duration": "2m30s"
}
```

HTTP upstream route states are runtime signals. A route starts as `healthy`,
becomes `degraded` after repeated safe-to-classify forwarding failures, becomes
`unavailable` after a longer failure streak, and returns to `healthy` after the
next successful forward. The `last_reason` field uses sanitized values such as
`http_status_502`, `timeout`, `canceled`, or `request_failed`; upstream response
bodies are not exposed.

### `GET /internal/admin/check`

Actively checks configured runtime dependencies for one gateway process. This
endpoint is read-only but it does perform outbound probes with a short timeout:

- PostgreSQL downlink store: `PingContext`
- Redis or memory cluster registry: `PING` or in-process readiness check
- JWT/JWKS auth provider: JWKS refresh
- HTTP auth provider: `HEAD` reachability check
- HTTP upstream routes: `HEAD` reachability check
- NSQ upstream routes: TCP connect to configured `nsqd` addresses

Query parameters:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `timeout` | `2s` | Maximum duration for the dependency probe request, capped at `30s` |

Example:

```json
{
  "code": "ok",
  "gateway_node": "gateway-a",
  "status": "ok",
  "timeout": "2s",
  "checks": [
    {
      "name": "auth_verifier",
      "status": "skipped",
      "target": "static",
      "error": "dependency does not expose an active health probe"
    },
    {
      "name": "downlink_store",
      "status": "ok",
      "target": "postgres",
      "latency": "1.2ms"
    },
    {
      "name": "cluster_registry",
      "status": "ok",
      "target": "redis",
      "latency": "800us"
    }
  ]
}
```

`status` is `ok`, `degraded`, or `failed`. A check can also be `skipped` when a
dependency is intentionally disabled or does not expose a safe active probe.
Secrets are not returned. HTTP route targets are sanitized, and failure messages
avoid echoing URLs, tokens, DSNs, Redis passwords, NSQ auth secrets, or message
bodies.

### `GET /internal/admin/routes`

Returns enabled upstream route ranges and sanitized target metadata.

Sensitive values are not returned:

- HTTP upstream tokens are omitted.
- HTTP URL user info, query strings, and fragments are stripped.
- NSQ auth secrets are omitted.

Example:

```json
{
  "code": "ok",
  "gateway_node": "gateway-a",
  "total": 2,
  "routes": [
    {
      "name": "production-http-upstream",
      "msg_id_min": 1001,
      "msg_id_max": 1999,
      "target_type": "http",
      "max_in_flight": 2000,
      "http": {
        "url": "http://business-backend:8080/gateway/upstream",
        "timeout": "5s"
      }
    },
    {
      "name": "production-nsq-upstream",
      "msg_id_min": 2000,
      "msg_id_max": 2999,
      "target_type": "nsq",
      "max_in_flight": 2000,
      "nsq": {
        "addresses": ["nsqd:4150"],
        "topic": "message_events",
        "dial_timeout": "1s",
        "read_timeout": "1m0s",
        "write_timeout": "1s",
        "publish_mode": "round_robin",
        "retry_attempts": 2
      }
    }
  ]
}
```

### Existing Route And Session Inspection

The existing debug endpoints remain part of the operator workflow:

```text
GET /internal/debug/route?client_id=...&device_id=...
GET /internal/debug/sessions?session_id=...&client_id=...&device_id=...&limit=...
GET /internal/debug/cluster/routes?session_id=...&client_id=...&device_id=...&limit=...
POST /internal/debug/session/disconnect
```

`route` answers where this gateway would push a client/device. It includes a
local session if one exists and a cluster route if cluster routing is enabled.

`sessions` answers which sessions are local to the gateway node you queried.
It does not claim to list all sessions across all nodes. Operators can filter
by `session_id`, by `client_id`, or by `client_id` plus `device_id`. A
`session_id` lookup is exact; optional `client_id` and `device_id` values act
as mismatch guards.

`debug/cluster/routes` answers which online routes are currently present in the
cluster registry. It is bounded by `limit` and includes the owning
`gateway_node`, peer `internal_addr`, route TTL, and whether the queried gateway
also owns the local TCP session. This endpoint is a cluster-wide route view, not
a remote session-disconnect API.

Browser admin sessions add a CSRF guard to mutation endpoints. The login and
`me` responses return `session.csrf_token`, and the embedded console sends it
as `X-ZCourier-CSRF-Token` on JSON `POST` operations such as local session
disconnect, downlink test push, message requeue/discard, retry scan, and
logout. Direct internal token or HMAC callers that do not send the browser
session cookie continue to use the normal internal API contract.

`session/disconnect` is a guarded mutation for local sessions only. Browser
admin sessions need the `session:disconnect` permission, which is granted to
`operator` and `admin` roles. The request body requires `session_id` and may
include `client_id` and `device_id` as mismatch guards:

```json
{
  "session_id": "zs_...",
  "client_id": "client-1",
  "device_id": "device-1"
}
```

The gateway closes the matching local connection and removes the local session
binding. If the session is only visible through the cluster registry on another
gateway, this endpoint returns a local-not-found response instead of performing
a peer disconnect.

### Downlink Test Push

The browser console uses a dedicated debug endpoint for operator test pushes:

```text
POST /internal/debug/push
```

It accepts the same JSON envelope as `POST /internal/push`, including
`client_id`, `device_id`, `msg_id`, optional `message_id`, optional `trace_id`,
`ack_required`, and base64-encoded `body`. This endpoint is intended for
operator diagnostics and is guarded by the `downlink:test_push` permission,
which is granted to `operator` and `admin` roles.

The response is the normal downlink push response and can return `sent`,
`queued`, or a failure code. V11 remote-operation metadata makes cluster test
pushes explicit:

| Field | Meaning |
| --- | --- |
| `delivery_path` | `local` for a direct local TCP write, `cluster_peer` for a peer gateway push |
| `origin_gateway_node` | Gateway that accepted the operator request |
| `target_gateway_node` | Gateway that owns the target client route |
| `target_internal_addr` | Peer internal HTTP address used for the operation |
| `failure_stage` | `session_lookup`, `route_lookup`, or `peer_dispatch` |
| `failure_code` | More specific reason such as `route_not_found`, `peer_auth_failed`, `peer_timeout`, or `peer_target_not_found` |

Remote test push is the only cross-node console operation currently surfaced.
Local session disconnect remains local-only until a separate remote disconnect
safety model is implemented.

Before the console opens the test-push confirmation dialog, it performs a
best-effort route preflight using `GET /internal/debug/route`. The confirmation
shows whether the operation is expected to be local, cluster-peer, stale, or
offline, plus the target gateway and internal address when known. This is an
operator safety hint, not a lock: the client can reconnect or disconnect before
the final send request reaches the gateway.

Test pushes increment `z_courier_admin_downlink_test_push_total` in addition to
the regular downlink push metrics, and audit logs include the admin principal,
role, target client, target device, message id, trace id, route metadata, and
result.

### Existing Message Inspection

The existing message inspection endpoints also remain part of the read-only
operator workflow:

```text
GET /internal/message/status?message_id=...
GET /internal/messages?status=failed&limit=100
POST /internal/messages/retry/scan
```

`message/status` answers the persisted delivery state, attempts, last error,
claim owner, retry timestamps, and body size for one reliable downlink message.

`messages` lists stored messages by status. Supported statuses are `pending`,
`sent`, `delivered`, `failed`, and `discarded`. When status is omitted, the
gateway defaults to failed messages.

`messages/retry/scan` triggers one bounded reliable-downlink retry scan. It
uses the same retry lease, ACK-timeout, max-attempt, and cluster peer-push
rules as the background retry worker. It accepts an optional JSON body:

```json
{"limit":100}
```

When `limit` is omitted or zero, the gateway uses the configured
`downlink.delivery.scan_limit`. The response reports `scanned`, `sent`,
`queued`, and `failed` counts. The endpoint is guarded by the
`message:retry_scan` console permission, increments
`z_courier_admin_retry_scan_total`, and emits an `admin_retry_scan` audit log.

### Audit Trail

The console exposes a bounded in-memory audit view for recent admin actions on
the connected gateway node:

```text
GET /internal/admin/audit?limit=100
```

Audit lists use stable cursor pagination. Results are ordered by descending
audit event `id`. When a response has `has_more=true`, pass its `next_cursor`
value as `cursor` to load the next page:

```text
GET /internal/admin/audit?limit=100&cursor=42
```

The response includes `limit`, `cursor`, `next_cursor`, `has_more`, `total`,
and `events`. `total` is the count for the active filters before applying the
cursor, so operators can see how many events match while paging through a
bounded window.

Optional filters:

```text
action=admin_retry_scan
result=success
principal=internal-token
client_id=client-1
session_id=zs_...
message_id=message-1
```

Each page is capped at 1000 rows. Events include action, result, HTTP status,
principal, role, admin session id, target client/session, message id, trace id,
reason, and small structured details. The response does not include internal
tokens, HMAC secrets, message bodies, request bodies, or route secrets.

The first implementation is node-local and in-memory. For production incident
retention, keep shipping gateway logs and Prometheus metrics to your normal log
or SIEM system. The audit list is meant for quick console review of recent
browser/admin operations.

Audited actions currently include admin session login/logout, permission
denials, local session disconnect, downlink test push, retry scan, requeue, and
discard. All audited events also increment:

```text
z_courier_admin_action_total{action=...,result=...}
```

## CLI

`cmd/admin` wraps the admin and debug APIs. Overview, diagnostics, dependency
check, diagnosis bundle collection, route, session, message, and message-list
commands are read-only. Message repair commands are guarded mutations and
require explicit confirmation.

Token mode:

```bash
go run ./cmd/admin overview \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin diagnostics \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin check \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -probe-timeout 2s

go run ./cmd/admin diagnose \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -output reports/diagnose/gateway-a.json

go run ./cmd/admin diagnose \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device \
  -output reports/diagnose/e2e-client.json

go run ./cmd/admin routes \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin route \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device

go run ./cmd/admin sessions \
  -internal-url http://127.0.0.1:18183 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device

go run ./cmd/admin message \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id message-1

go run ./cmd/admin messages \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -status failed \
  -limit 100

go run ./cmd/admin requeue \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -confirm

go run ./cmd/admin discard \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -reason "handled manually" \
  -confirm

go run ./cmd/admin retry-scan \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -confirm
```

HMAC mode:

```bash
go run ./cmd/admin overview \
  -internal-url http://gateway-a:18080 \
  -auth hmac \
  -hmac-key-id backend-1 \
  -hmac-secret "$ZCOURIER_INTERNAL_HMAC_SECRET"
```

Useful environment variables:

| Variable | Purpose |
| --- | --- |
| `ZCOURIER_ADMIN_INTERNAL_URL` | Default internal HTTP base URL |
| `ZCOURIER_ADMIN_AUTH` | `token` or `hmac` |
| `ZCOURIER_ADMIN_INTERNAL_TOKEN` | Token-mode secret |
| `ZCOURIER_ADMIN_HMAC_KEY_ID` | HMAC key id |
| `ZCOURIER_ADMIN_HMAC_SECRET` | HMAC secret |

The CLI defaults to `http://127.0.0.1:18082` and `dev-internal-token` for local
development. Production scripts should pass explicit values or use environment
variables.

`cmd/admin diagnose` is a CLI-side collector rather than a new server endpoint.
It collects overview, diagnostics, active dependency check, routes, failed
message summary, and optionally client/device route plus local sessions. Each
section records its HTTP status and response body independently, so partial
collection failures stay visible in the output bundle. The command sanitizes the
target URL and does not write admin tokens, HMAC secrets, DSNs, route tokens, or
message bodies beyond what the existing safe admin APIs already return.

## Boundary With `cmd/devbackend`

`cmd/devbackend` remains a development helper and can still call push, batch,
message status, list, requeue, discard, route, and sessions endpoints.

`cmd/admin` is the safer operator entrypoint. It supports:

- overview
- diagnostics
- dependency check
- diagnosis bundle collection
- routes
- route lookup
- local session listing
- message status lookup
- message listing by status
- single-message requeue with `-confirm`
- single-message discard with `-reason` and `-confirm`

`requeue` and `discard` intentionally operate on one `message_id` at a time.
Batch repair is not provided by the operator CLI yet.

## Mutation Audit Logs

`requeue` and `discard` emit one structured audit log for every accepted or
rejected mutation attempt. The log message is:

```text
admin message action audit
```

Stable fields include:

| Field | Meaning |
| --- | --- |
| `audit_event` | Always `downlink_message_action` |
| `action` | `requeue` or `discard` |
| `result` | `success`, `unauthorized`, `bad_request`, `invalid_transition`, and related result codes |
| `http_status` | HTTP status returned to the operator |
| `gateway_node` | Gateway node handling the operation, when configured |
| `message_id` | Target reliable downlink message id, when available |
| `reason` | Operator discard reason, when supplied |
| `message_status` | Message status after a successful mutation |
| `auth_mode` | `token`, `hmac`, or `none` |
| `auth_key_id` | HMAC key id for HMAC-authenticated operations |
| `remote_addr` | Operator source address as seen by the gateway |

The audit log never records the internal token or HMAC secret.
