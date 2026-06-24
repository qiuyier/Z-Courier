# V5 Admin Operations

V5 introduces a small read-only operations surface for answering common gateway
questions without querying Redis, PostgreSQL, or raw logs by hand.

This is not a browser admin console. The first milestone is a stable internal
HTTP contract plus a safe `cmd/admin` CLI.

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
GET /internal/debug/sessions?client_id=...&limit=...
```

`route` answers where this gateway would push a client/device. It includes a
local session if one exists and a cluster route if cluster routing is enabled.

`sessions` answers which sessions are local to the gateway node you queried.
It does not claim to list all sessions across all nodes.

## CLI

`cmd/admin` wraps the read-only admin and debug APIs.

Token mode:

```bash
go run ./cmd/admin overview \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

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
  -client-id e2e-client
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

## Boundary With `cmd/devbackend`

`cmd/devbackend` remains a development helper and can still call push, batch,
message status, list, requeue, discard, route, and sessions endpoints.

`cmd/admin` is the safer operator entrypoint. Its first milestone is read-only:

- overview
- routes
- route lookup
- local session listing

Message repair commands such as requeue and discard should move into `cmd/admin`
only after their response contracts, audit logging, and operational guardrails
are tightened.
