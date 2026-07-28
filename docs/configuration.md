# Z-Courier Configuration

Z-Courier uses two configuration files at runtime:

- `conf/zinx.json` configures the underlying Zinx TCP server.
- `configs/z-courier.yaml` configures gateway behavior.

The gateway config path defaults to `configs/z-courier.yaml`. Override it with
the `-config` flag or the `ZCOURIER_CONFIG` environment variable.

The Zinx config path defaults to `conf/zinx.json`. Override it with
`ZINX_CONFIG_FILE_PATH`.

## Environment Placeholders

`configs/z-courier.yaml` supports strict environment placeholders in the form
`${ENV_NAME}`. Z-Courier expands them before YAML parsing:

```yaml
downlink:
  storage:
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
```

Only braced placeholders are supported. Bare `$ENV_NAME` is treated as ordinary
text. If a referenced environment variable is not set, startup fails with a
configuration error; it is never silently replaced with an empty value.

This is intended for deployment-provided values such as passwords, HMAC keys,
internal tokens, and upstream secrets. Do not commit real secret values to Git.

## Zinx Runtime Config

Example:

```json
{
  "Name": "Z-Courier Gateway",
  "Host": "0.0.0.0",
  "TCPPort": 8999,
  "Mode": "tcp",
  "MaxConn": 12000,
  "MaxPacketSize": 8388608,
  "WorkerPoolSize": 10,
  "MaxWorkerTaskLen": 1024,
  "MaxMsgChanLen": 1024,
  "IOReadBuffSize": 8192,
  "HeartbeatMax": 30,
  "LogDir": "./log",
  "LogFile": "zinx.log",
  "LogCons": true,
  "LogIsolationLevel": 2
}
```

Important fields:

- `Host` and `TCPPort`: TCP listen address for client connections.
- `MaxConn`: maximum active Zinx connections.
- `MaxPacketSize`: maximum Zinx frame payload size.
- `WorkerPoolSize`: Zinx worker count for request handling.
- `IOReadBuffSize`: socket read buffer size.
- `HeartbeatMax`: Zinx heartbeat timeout in seconds.
- `LogIsolationLevel`: Zinx framework log level. `2` means Warn and above, which
  keeps startup output readable when many MsgID routes are registered.

## Gateway Identity

```yaml
gateway_node: local
route_msg_ids:
  - 1000
```

- `gateway_node`: logical node name recorded in session state.
- `route_msg_ids`: explicit MsgIDs registered on Zinx in addition to enabled
  upstream route ranges. `MsgID = 1000` AUTH/BIND and `MsgID = 2` downlink ACK
  are always registered by the gateway.

`MsgID = 1000` is a gateway control message. It authenticates and binds the TCP
connection to `client_id + device_id`, returns a gateway ACK, and is not
forwarded upstream.

## Static Validation

Validate a gateway config without starting the TCP server or connecting to
external dependencies:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml -check-config
```

The check validates the YAML schema, duration fields, auth provider shape,
internal HTTP auth settings, cluster and downlink storage structure, pipeline
rate-limit settings, enabled upstream targets, upstream route overlaps, and
reserved business MsgID conflicts. Warnings are printed for risky but still
valid local patterns, such as memory-backed cluster routing.

## Auth

```yaml
auth:
  type: static
  static_tokens:
    dev-token:
      client_id: dev-client
      token_id: dev-token
      scopes:
        - gateway:dev
```

`auth.type` selects the token verifier. Supported values are `static`, `http`,
and `jwt`; omitting `type` while providing `static_tokens` keeps existing
configurations compatible.

The static verifier is intended for local development and tests. Production
deployments can use the HTTP verifier so the backend application remains the
owner of token semantics:

```yaml
auth:
  type: http
  http:
    url: http://backend:8080/internal/auth/verify
    internal_token: replace-with-a-shared-secret
    timeout: 2s
    max_in_flight: 500
  cache:
    enabled: true
    max_entries: 10000
    positive_ttl: 30s
    negative_ttl: 3s
```

The gateway sends `POST` with `Authorization: Bearer <client-token>` and
`X-ZCourier-Internal-Token`. A successful backend response contains
`client_id`, optional `token_id`, `subject`, `scopes`, and `expires_at` fields.
HTTP `401` and `403` reject credentials; `429`, `5xx`, transport failures, and
invalid responses are treated as provider unavailability. Verification timeout
returns the retryable `auth_unavailable` ACK.

The HTTP verifier does not follow redirects and never includes raw tokens in
logs or metric labels. `max_in_flight` defaults to `500` and `timeout` defaults
to `2s`. When caching is enabled, defaults are 10,000 entries, 30 seconds for
successful verification, and 3 seconds for invalid tokens. Cache keys are
SHA-256 digests; timeout and provider-unavailable results are never cached.

For deployments whose identity provider issues signed JWTs, the gateway can
verify tokens locally without an authentication request per connection:

```yaml
auth:
  type: jwt
  jwt:
    issuer: https://identity.example.com
    audience: z-courier
    jwks_url: https://identity.example.com/.well-known/jwks.json
    algorithms: [RS256, ES256]
    client_id_claim: client_id
    token_id_claim: jti
    scopes_claim: scope
    clock_skew: 30s
    refresh_interval: 5m
    fetch_timeout: 2s
    max_response_body_size: 1048576
  cache:
    enabled: true
    max_entries: 10000
    positive_ttl: 30s
    negative_ttl: 3s
```

### JWKS Ownership And Deployment

Z-Courier does not generate signing keys or issue JWTs. The service that issues
the JWT owns the private key and must expose the corresponding public keys as a
JWKS document. For a monolithic backend, a conventional endpoint is:

```http
GET /.well-known/jwks.json
```

Example response:

```json
{
  "keys": [
    {
      "kty": "RSA",
      "kid": "key-2026-01",
      "use": "sig",
      "alg": "RS256",
      "n": "base64url-encoded-modulus",
      "e": "AQAB"
    }
  ]
}
```

Only public-key material belongs in this response. The private key remains in
the token-issuing service. When using an external identity provider, configure
the JWKS URL published by that provider instead of implementing this endpoint.

In Docker Compose, `jwks_url` is resolved from inside the gateway container.
Use the backend service name on the shared Docker network:

```yaml
jwks_url: http://backend:8080/.well-known/jwks.json
```

Do not use `127.0.0.1` unless the JWKS server runs in the same container as the
gateway; inside the gateway container, that address refers to the gateway
container itself. For a gateway process running directly on the host, a host
address such as `http://127.0.0.1:8080/.well-known/jwks.json` is valid.

The repository's default and integration configurations currently use
`static_tokens`, so they do not fetch a JWKS. Public-key loading starts only
when `auth.type` is set to `jwt`.

JWT mode requires `iss`, `aud`, `exp`, the configured client ID claim, a
matching `kid`, and a valid signature. Only explicitly allowed asymmetric
RSA, RSA-PSS, ECDSA, and Ed25519 algorithms are accepted; HMAC and `none` are
rejected. The gateway must load a valid JWKS before startup completes. It then
refreshes keys in the background and once on an unknown `kid`; refresh failures
preserve the last valid key set, while repeated unknown-key requests are
throttled to prevent a JWKS refresh storm.

The gateway does not trust `ClientID` from the packet until the token is
verified. Session binding uses the `client_id` returned by the verifier.

Authentication exports `z_courier_auth_verify_total`,
`z_courier_auth_verify_duration_seconds`, `z_courier_auth_inflight`, and
`z_courier_auth_cache_total`, `z_courier_auth_jwks_refresh_total`, and
`z_courier_auth_jwks_refresh_duration_seconds`, labeled only by provider or
bounded result values. Token and client identifiers are never metric labels.
The provisioned `Z-Courier Overview` Grafana dashboard displays auth request
rate, success rate, p95/p99 verification latency, in-flight verification count,
cache activity, and JWKS refresh health.

## Internal HTTP

```yaml
internal_http:
  enabled: true
  addr: 127.0.0.1:18080
  token: dev-internal-token
  auth:
    mode: token
  max_request_body_size: 10485760
  max_in_flight: 1000
```

- `enabled`: starts or disables internal HTTP endpoints.
- `addr`: listen address for `/internal/push`, `/metrics`, `/healthz`, and
  `/readyz`.
- `auth.mode`: `token` by default, or `hmac` for replay-resistant signed
  backend requests.
- `token`: value required in `X-ZCourier-Internal-Token` when mode is `token`.
- `max_request_body_size`: maximum JSON request size in bytes.
- `max_in_flight`: maximum concurrent `/internal/push` and
  `/internal/push/batch` requests. Requests above this limit return `429`.

`/healthz` returns `200` while the internal HTTP server is alive. `/readyz`
returns `200` while the gateway can receive traffic and `503` after graceful
shutdown begins. Use `/readyz` for load balancer or Kubernetes readiness
checks. Prometheus also exposes `z_courier_gateway_readiness{status="ready"}` and
`z_courier_gateway_readiness{status="draining"}` as one-hot readiness gauges.

For HMAC mode, remove `token` and configure one or more rotation-friendly key
IDs:

```yaml
internal_http:
  enabled: true
  addr: 127.0.0.1:18080
  auth:
    mode: hmac
    hmac:
      keys:
        backend-2026-01: ${ZCOURIER_INTERNAL_HMAC_SECRET}
      max_clock_skew: 30s
      nonce_ttl: 1m
      max_nonce_entries: 10000
  max_request_body_size: 10485760
  max_in_flight: 1000
```

`nonce_ttl` must be at least twice `max_clock_skew`, and every secret must be at
least 32 bytes. HMAC and token settings are mutually exclusive. Environment
placeholders such as `${ZCOURIER_INTERNAL_HMAC_SECRET}` are expanded by the
Z-Courier YAML loader before parsing. The exact cross-language signing contract
and TLS requirements are documented in
[internal-http-signing.md](internal-http-signing.md).

## Admin Console

```yaml
admin_console:
  enabled: true
  path: /console/
  assets_dir: web/admin/dist
  monitoring:
    prometheus_url: http://127.0.0.1:19090
    grafana_url: http://127.0.0.1:13000
    dashboard_url: http://127.0.0.1:13000/d/z-courier-overview/z-courier-overview
  session:
    enabled: true
    ttl: 8h
    cookie_name: zcourier_admin_session
    cookie_secure: false
    cookie_same_site: lax
    role: admin
    store:
      type: memory
      redis:
        addr: 127.0.0.1:16379
        username: ""
        password: ""
        db: 0
        key_prefix: zcourier:admin-session
        dial_timeout: 1s
        read_timeout: 1s
        write_timeout: 1s
        operation_timeout: 2s
  audit:
    type: memory
    capacity: 1000
    postgres:
      dsn: "postgres://zcourier:${ZCOURIER_POSTGRES_PASSWORD}@postgres:5432/zcourier?sslmode=disable"
      auto_migrate: true
      max_open_conns: 10
      max_idle_conns: 5
      conn_max_lifetime: 30m
      operation_timeout: 2s
```

- `enabled`: serves the embedded browser console from internal HTTP when true.
- `path`: base path for the single-page app. It must not overlap `/internal`,
  `/metrics`, `/healthz`, or `/readyz`.
- `assets_dir`: directory containing the built console assets.
- `monitoring.prometheus_url`: optional Prometheus UI URL used to build query
  shortcuts.
- `monitoring.grafana_url`: optional Grafana entrypoint.
- `monitoring.dashboard_url`: optional preferred Z-Courier dashboard link.
- `session.enabled`: enables short-lived admin console sessions. Existing
  internal token or HMAC authentication is still required to create a session.
- `session.ttl`: maximum lifetime of one browser console session.
- `session.cookie_name`: HTTP-only cookie name used by the browser.
- `session.cookie_secure`: set true when the console is served over HTTPS.
- `session.cookie_same_site`: one of `lax`, `strict`, or `none`. `none`
  requires `session.cookie_secure=true`.
- `session.role`: role assigned to newly created browser sessions. Supported
  values are `readonly`, `operator`, and `admin`. `readonly` can inspect
  console data; `operator` can also run guarded local session disconnect and
  downlink test push actions plus message repair actions such as requeue and
  discard; `admin` currently includes all operator permissions.
- `session.store.type`: admin browser session storage. `memory` stores sessions
  in the current gateway process; `redis` shares sessions across gateway nodes.
- `session.store.redis.addr`, `username`, `password`, `db`, and `key_prefix`:
  Redis connection and key namespace used when `session.store.type=redis`.
- `session.store.redis.dial_timeout`, `read_timeout`, `write_timeout`, and
  `operation_timeout`: Redis connection and operation timeouts.
- `audit.type`: admin operation audit storage. `memory` keeps the latest events
  in the current gateway process; `postgres` persists events in PostgreSQL so
  they survive restarts and can be queried across a longer retention window.
- `audit.capacity`: maximum in-memory audit entries retained when
  `audit.type=memory`.
- `audit.postgres.dsn`: PostgreSQL DSN used when `audit.type=postgres`.
- `audit.postgres.auto_migrate`: creates the audit table and indexes on
  startup when true.
- `audit.postgres.max_open_conns`, `audit.postgres.max_idle_conns`, and
  `audit.postgres.conn_max_lifetime`: optional PostgreSQL connection pool
  settings.
- `audit.postgres.operation_timeout`: timeout for inserting and listing audit
  events.

The console is an internal operations UI, not a public endpoint. Production
deployments should keep it on private networking and expose it only through a
VPN, bastion, private ingress, or an authenticating reverse proxy. The console
static responses include a restrictive Content Security Policy, no-referrer,
nosniff, frame-deny, and disabled browser permission headers. `index.html` is
served with `Cache-Control: no-store`; hashed assets under `assets/` are served
with long immutable caching.

When `admin_console.session.enabled=true`, the gateway exposes
`POST /internal/admin/session/login`, `GET /internal/admin/session/me`, and
`POST /internal/admin/session/logout`. The login endpoint exchanges a valid
internal token, or a successfully HMAC-authenticated request, for a short-lived
HTTP-only cookie. Use `session.store.type=memory` for single-node development.
Use `session.store.type=redis` when console traffic can move across gateway
nodes; logout deletes the shared Redis session, and Redis key TTL follows the
session expiry.

Browser admin sessions also use a per-session CSRF token. Login and `me`
responses include `session.csrf_token`; the embedded console keeps it in memory
and sends it as `X-ZCourier-CSRF-Token` on session-authenticated mutation
requests. Those mutation requests must use `Content-Type: application/json`,
and when `Origin` or `Referer` is present it must match the request origin.
Programmatic internal token or HMAC clients that do not send a browser admin
session cookie are not affected by this browser-only guard. Rejections are
audited as `admin_session_mutation_rejected`, and CSRF rejections increment
`z_courier_admin_csrf_rejected_total`.

Admin audit events are produced by console and internal admin APIs such as
login, permission denial, session disconnect, downlink test push, single or bulk
message requeue, discard, retry scans, and diagnostics actions. Use `audit.type=postgres`
in production if those events need to remain available after gateway restarts.

When `internal_http.auth.mode` is `hmac`, browser JavaScript cannot create a
session or call `/internal/*` APIs directly unless a deployment-side proxy
signs the login request. For production, prefer a private authenticated reverse
proxy or continue using the `cmd/admin` CLI for direct HMAC-signed operations.

For browser-level release verification, run:

```bash
bash scripts/console_smoke.sh
```

The smoke script builds the console assets, starts a lightweight gateway twice,
once as `admin` and once as `readonly`, then verifies login, navigation,
guarded operation confirmations, and read-only disabled states with Playwright.
Install the local Playwright browser once if needed:

```bash
npm --prefix web/admin exec -- playwright install chromium
```

## Cluster

```yaml
cluster:
  enabled: false
  internal_addr: http://127.0.0.1:18080
  route_refresh_interval: 10s
  registry:
    type: memory
    ttl: 30s
    redis:
      addr: 127.0.0.1:16379
      username: ""
      password: ""
      db: 0
      key_prefix: zcourier
      dial_timeout: 1s
      read_timeout: 1s
      write_timeout: 1s
  peer:
    token: dev-cluster-token
    timeout: 2s
    auth:
      mode: token
```

- `enabled`: enables V2 online route registration. `false` keeps local-only
  behavior.
- `internal_addr`: address other gateway nodes will use to call this node.
  It is required when cluster mode is enabled.
- `route_refresh_interval`: how often the gateway refreshes Redis or memory
  online routes for currently connected local sessions. If omitted, it defaults
  to one third of `registry.ttl`.
- `registry.type`: online route registry implementation. Supported values are
  `memory` and `redis`. Use `memory` for single-process development and `redis`
  when multiple gateway nodes must share online routes.
- `registry.ttl`: online route TTL. Each authenticated client packet refreshes
  the route by rebinding the current session.
- `registry.redis`: Redis registry connection settings. `addr` should point to
  the Redis TCP address reachable from the gateway process.
- `peer.auth.mode`: `token` by default, or `hmac` for signed, replay-resistant
  gateway-to-gateway requests.
- `peer.token`: token required by `POST /internal/cluster/push` when peer auth
  mode is `token`.
- `peer.timeout`: gateway-to-gateway HTTP push timeout used by the peer
  dispatcher.

For HMAC peer authentication, remove `peer.token`. Each node selects its
outbound signing key with `key_id` and accepts inbound signatures from every key
in `keys`:

```yaml
cluster:
  peer:
    timeout: 2s
    auth:
      mode: hmac
      hmac:
        key_id: gateway-a-2026-01
        keys:
          gateway-a-2026-01: ${GATEWAY_A_PEER_HMAC_SECRET}
          gateway-b-2026-01: ${GATEWAY_B_PEER_HMAC_SECRET}
        max_clock_skew: 30s
        nonce_ttl: 1m
        max_nonce_entries: 10000
```

Configure the same accepted key ring on each gateway, but select that node's
own `key_id`. Peer keys must be separate from backend internal-HTTP keys so a
compromised backend credential cannot impersonate a gateway node. The selected
`key_id` must exist in `keys`; secrets require at least 32 bytes, and
`nonce_ttl` must be at least twice `max_clock_skew`. The `${...}` values above
are strict environment placeholders expanded by the Z-Courier YAML loader.

When cluster mode is enabled, Z-Courier writes `client_id + device_id` online
routes after session binding and removes them on connection close only when the
stored `session_id` still matches. This prevents an old disconnected session
from deleting a newer route for the same client device. Cluster mode also
registers `POST /internal/cluster/push` for gateway-to-gateway local TCP
delivery. The public `/internal/push` downlink resolver first tries the local
session manager, then looks up the online registry and calls a remote gateway
peer when the client is connected elsewhere.

While a session remains connected, the gateway periodically refreshes its
online route. This keeps quiet clients discoverable even when they do not send
upstream packets before the registry TTL expires.

During graceful shutdown, the gateway marks readiness as unavailable, stops the
route refresher, removes all current node routes whose `session_id` still
matches, shuts down internal HTTP, then stops the Zinx TCP server and background
workers. Downlink messages still pending in PostgreSQL remain retryable by other
nodes.

The local cluster verifier uses `configs/z-courier.cluster-a.yaml` and
`configs/z-courier.cluster-b.yaml` with the same Redis key prefix and
PostgreSQL DSN. Those two integration configurations use HMAC peer
authentication, while the default single-node configuration keeps token mode:

```bash
bash scripts/e2e_cluster.sh
```

## Downlink Storage

```yaml
downlink:
  storage:
    type: postgres
    postgres:
      dsn: postgres://user:pass@postgres:5432/z_courier?sslmode=disable
      auto_migrate: true
      max_open_conns: 10
      max_idle_conns: 5
      conn_max_lifetime: 30m
```

Supported storage types:

- `memory`: useful for local development; queued messages are lost on restart.
- `postgres`: durable storage for offline and retryable downlink messages.

PostgreSQL fields:

- `dsn`: pgx-compatible PostgreSQL DSN.
- `auto_migrate`: creates the downlink table when the store starts.
- `max_open_conns`, `max_idle_conns`, `conn_max_lifetime`: database pool
  controls.

## Downlink Delivery

```yaml
downlink:
  delivery:
    retry_interval: 5s
    retry_delay: 30s
    retry_jitter: 5s
    ack_timeout: 30s
    retry_lease: 30s
    max_attempts: 5
    scan_limit: 100
    bind_flush_limit: 100
    retry_fairness:
      enabled: true
      candidate_multiplier: 4
```

- `retry_interval`: how often the retry worker scans due messages.
- `retry_delay`: delay before the next attempt after a failed send.
- `retry_jitter`: optional random delay window added after `retry_delay` to
  spread retry bursts. Use `0s` to disable it.
- `ack_timeout`: how long a sent ACK-required message may wait for client ACK
  before it is eligible for retry.
- `retry_lease`: how long one gateway node owns a claimed retry batch before
  another node may reclaim it.
- `max_attempts`: attempts before a message is marked `failed`.
- `scan_limit`: maximum due messages scanned per retry tick.
- `bind_flush_limit`: maximum pending messages flushed when a client binds.
- `retry_fairness.enabled`: select due messages round-robin by
  `client_id + device_id` so one hot offline device cannot monopolize a bounded
  scan. Disabled preserves FIFO selection.
- `retry_fairness.candidate_multiplier`: bounded oversampling window used
  before fair selection. `0` uses the default `4`; values above `16` are
  rejected. Increase it only when the oldest due window can contain a much
  larger single-device backlog than `scan_limit`.

When fairness is enabled, one scan first reads a bounded candidate window and
then selects up to `scan_limit` messages by device round. PostgreSQL keeps the
same claim lease and `FOR UPDATE SKIP LOCKED` behavior, so multiple gateway
nodes can share the store without claiming the same message. The metrics
`z_courier_downlink_retry_selected_devices` and
`z_courier_downlink_retry_max_per_device` expose the distribution selected by
each scan.

### Named Delivery Policies (V12.2)

The V12.2 policy resolver accepts named policies selected by inclusive MsgID
ranges:

```yaml
downlink:
  delivery:
    retry_delay: 30s
    retry_jitter: 5s
    ack_timeout: 30s
    max_attempts: 5
  policies:
    - name: critical
      enabled: true
      msg_id_min: 2100
      msg_id_max: 2199
      max_attempts: 10
      max_age: 1h
      ack_timeout: 10s
      retry_delay: 1s
      backoff_multiplier: 2
      max_retry_delay: 30s
      retry_jitter: 250ms
```

Configured ranges take precedence over the implicit `default` policy. MsgIDs
outside every configured range use `downlink.delivery`. Omitted policy fields
inherit the corresponding default value; `max_age` defaults to unlimited,
`backoff_multiplier` defaults to `1`, and `max_retry_delay` defaults to the
initial retry delay.

Enabled policy names must be unique and use lowercase letters, digits, `_`, or
`-`. Ranges are inclusive and cannot overlap. A multiplier greater than `1`
requires an explicit `max_retry_delay`. Invalid policy configuration prevents
the gateway from starting.

For every newly accepted reliable message, the gateway persists the resolved
policy name and all execution parameters with the message. Later configuration
changes therefore affect new messages only; existing messages continue under
their recorded policy. Rows created before V12.2.2 do not have a snapshot and
fall back to the currently resolved policy for their MsgID.

After failed attempt `N` (starting at `1`), the base retry delay is:

```text
min(retry_delay * backoff_multiplier^(N-1), max_retry_delay)
```

The gateway then adds a random duration from `0` through `retry_jitter`.
ACK-required messages persist their own ACK deadline from `ack_timeout`.
The retry worker marks a message `failed` with `max_attempts_exceeded` or
`max_age_exceeded` before another delivery can violate the selected policy.
The status/list APIs and admin console expose the persisted `policy_name`.

## Downlink Queue Capacity (V12.4)

Queue admission limits protect PostgreSQL and retry workers from an unbounded
offline backlog:

```yaml
downlink:
  capacity:
    max_pending_global: 1000000
    max_pending_per_device: 1000
```

- `max_pending_global`: maximum pending messages across the shared downlink
  store. `0` means unlimited.
- `max_pending_per_device`: maximum pending messages for one
  `client_id + device_id`. `0` means unlimited.

When both limits are enabled, the per-device limit cannot exceed the global
limit. Capacity requires reliable downlink storage. All gateway nodes sharing
one PostgreSQL database must use the same capacity configuration.

The store checks idempotency before capacity: replaying the same compatible
`message_id` still returns the existing message when the queue is full, while
a conflicting identity still returns HTTP `409`. A new message over capacity
returns HTTP `429` with `code = queue_capacity_exceeded`, plus
`capacity_scope`, `capacity_limit`, and `capacity_pending`. The rejected
message is not persisted and does not produce a terminal event. Manual requeue
uses the same admission rules.

PostgreSQL serializes each capacity decision and insert with transaction-level
advisory locks, so two gateway nodes cannot intentionally consume the last
slot together. The memory store provides the same semantics within one
process. These are admission limits, not eviction rules: the gateway never
deletes an older pending message to make room.

Reliable pushes are persisted before online delivery. Consequently, a new
online push also needs admission before the gateway can attempt the socket
write. Prefer a meaningful per-device limit to isolate one offline device, and
size the global limit from expected offline devices, message rate, offline
duration, average Body size, PostgreSQL headroom, and retry throughput.
Because a nonzero global limit coordinates every admission through one shared
database lock, benchmark the intended workload before enabling it. The
production examples therefore enable only the per-device limit by default.

## Downlink Terminal Events

Terminal events export bounded metadata when a reliable message becomes
`failed` or is manually `discarded`. Publication is disabled by default:

```yaml
downlink:
  terminal:
    publisher:
      type: nsq
      nsq:
        nsqd_addrs:
          - nsqd:4150
        topic: downlink_terminal_events
        dial_timeout: 1s
        read_timeout: 60s
        write_timeout: 1s
        publish_mode: round_robin
        retry_attempts: 1
    retry_interval: 5s
    retry_delay: 30s
    retry_jitter: 0s
    backoff_multiplier: 2
    max_retry_delay: 5m
    retry_lease: 30s
    scan_limit: 100
```

- `publisher.type`: `none`, `nsq`, or `http`. `none` preserves the pre-V12.3
  behavior.
- `publisher.nsq`: standard bounded NSQ producer settings. Producers publish
  directly to `nsqd`; `nsqlookupd` is not used for publication.
- `publisher.http`: a signed JSON `POST` target for the existing terminal-event
  envelope. It requires PostgreSQL storage so publication claims and retries
  survive restarts and coordinate across gateway nodes:

  ```yaml
  downlink:
    storage:
      type: postgres
    terminal:
      publisher:
        type: http
        http:
          url: https://terminal-events.example.internal/v1/z-courier
          timeout: 5s
          hmac:
            key_id: gateway-terminal-v1
            secret: ${ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET}
          tls:
            ca_file: /run/secrets/terminal-webhook/ca.crt
            client_cert_file: /run/secrets/terminal-webhook/tls.crt
            client_key_file: /run/secrets/terminal-webhook/tls.key
            server_name: terminal-events.example.internal
  ```

  The gateway only accepts an absolute `https` URL by default. Local-only
  `http` receivers require `allow_insecure_http: true` and should never be
  used across an untrusted network. The HMAC key ID and secret use the existing
  `ZCOURIER-HMAC-SHA256` request-signing protocol and require a secret of at
  least 32 bytes. See [internal HTTP signing](internal-http-signing.md) for
  the canonical request and receiver verification rules.
  The optional `tls` block supports private PKI and mTLS. `ca_file` adds PEM
  certificates to a root pool dedicated to this publisher while retaining
  system roots. `client_cert_file` and `client_key_file` must be configured
  together and contain a matching PEM certificate/key pair. `server_name`
  optionally overrides certificate-name verification and must not contain a
  scheme, port, or path. TLS uses a minimum version of 1.2; certificate
  verification cannot be disabled. Files are parsed during `-check-config` and
  again when the publisher starts, and TLS settings require an `https` URL.
- `retry_interval`: how often a gateway claims due terminal events.
- `retry_delay`, `retry_jitter`, `backoff_multiplier`, `max_retry_delay`:
  independent publication retry schedule. It never causes another client
  delivery attempt.
- `retry_lease`: claim lease shared by gateway nodes using the same store.
- `scan_limit`: maximum outbox events claimed by one worker scan.

The publisher requires downlink storage. PostgreSQL persists the message
terminal transition and its outbox event atomically; cluster nodes then use
independent claims so only one node publishes a due event at a time. Delivery
is at least once, so consumers must de-duplicate by stable `event_id` (or
`message_id` plus `terminal_status`). The event never contains the original
message Body or gateway credentials. HTTP publication sends the same versioned
metadata envelope as NSQ, follows no redirects, and treats only a `2xx`
response as success; timeout, transport, and non-`2xx` failures use the
existing independent publication retry schedule.

Rows that were already terminal before upgrading are not exported
retroactively. Retention keeps a terminal message while its current event is
`pending` or `failed`, so a publication retry is not deleted underneath the
worker.

## Downlink Retention

```yaml
downlink:
  retention:
    delivered_ttl: 24h
    failed_ttl: 168h
    discarded_ttl: 168h
    cleanup_interval: 1h
    cleanup_limit: 1000
```

- `delivered_ttl`: how long delivered messages stay in the downlink store.
- `failed_ttl`: how long failed messages stay available for inspection and
  manual requeue/discard decisions.
- `discarded_ttl`: how long manually discarded messages stay available for
  audit.
- `cleanup_interval`: how often the gateway scans for expired terminal
  messages.
- `cleanup_limit`: maximum expired messages deleted per status in one cleanup
  run.

Retention cleanup only deletes terminal messages: `delivered`, `failed`, and
`discarded`. It does not delete `pending` or `sent` messages.

## Pipeline

```yaml
pipeline:
  allowlist:
    client_ids: []
    msg_ids: []
  blocklist:
    client_ids: []
    msg_ids: []
  rate_limit:
    enabled: false
    max_requests: 100
    window: 1s
  traffic_policies:
    enabled: false
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: ""
    policies: []
```

The ingress pipeline order is:

```text
auth -> allowlist/blocklist -> legacy rate limit or traffic policy
-> session bind -> access log
```

If an allowlist is empty, it does not restrict that dimension. Blocklists always
take priority.

`rate_limit` is the legacy process-local fixed-window per-client limiter. It
retains its existing behavior for compatibility. It and `traffic_policies`
cannot both be enabled.

### Named Traffic Policies

Traffic policies select a bounded local token bucket from authenticated
`client_id`, packet MsgID, and the upstream route resolved from that MsgID. They
never inspect the opaque business body.

```yaml
pipeline:
  rate_limit:
    enabled: false
  traffic_policies:
    enabled: true
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: ""
    policies:
      - name: standard-upstream
        priority: 100
        match:
          msg_id_min: 1001
          msg_id_max: 2999
        key: client_id
        token_bucket:
          capacity: 100
          refill_tokens: 100
          refill_interval: 1s
      - name: orders
        priority: 200
        match:
          client_ids: [priority-client]
          routes: [orders-http]
        key: client_id
        token_bucket:
          capacity: 20
          refill_tokens: 20
          refill_interval: 1s
```

- `mode`: V16.1 supports `local`. `redis` is rejected until the atomic shared
  quota implementation is available; it is never accepted as a no-op.
- `max_keys`: maximum live `(policy, client_id)` buckets in this gateway
  process. Zero selects the default `100000`; negative values are invalid.
- `idle_ttl`: an idle bucket is removed after this duration. The default is
  `10m`.
- `default_policy`: optional policy name used when no selector matches. Leave
  it empty when unmatched packets should pass without consuming a bucket.
- `policies[].enabled`: defaults to `true`. A disabled policy is validated but
  excluded from selection.
- `priority`: larger values win. Two enabled policies at the same priority are
  rejected only when a packet could match both.
- `match`: non-empty dimensions are combined with AND. `client_ids` uses the
  authenticated identity, MsgID bounds are inclusive, and `routes` must name
  enabled upstream routes. Omitting both MsgID bounds means any MsgID; setting
  only `msg_id_min` selects that single MsgID.
- `key`: V16.1 supports only `client_id`. Device-based keys are intentionally
  unavailable before session binding establishes a trusted device identity.
- `token_bucket`: a new key starts with `capacity` tokens and refills
  continuously at `refill_tokens` per `refill_interval`.

When `max_keys` is full, a new key is rejected with `overloaded`; active buckets
are not evicted because that would reset their quota. A bucket without a token
is rejected with `rate_limited`. Existing idle buckets are removed before the
capacity decision.

`default_policy` is a true fallback. It can therefore apply to AUTH/BIND,
downlink ACK, and other protocol packets that do not match an upstream route.
To limit only business upstream traffic, omit `default_policy` and use MsgID or
route selectors. Disabled policies do not participate in selection, but all
declared policy fields are still validated at startup.

Local buckets are process-local. Multiple gateway nodes each enforce their own
quota; cluster-wide quotas are not available until Redis mode is implemented.

## Upstream Routes

```yaml
upstream:
  routes:
    - name: dev-http-upstream
      enabled: true
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        url: http://127.0.0.1:18081/gateway/upstream
        token: ""
        timeout: 5s
        max_in_flight: 1000
```

- `enabled`: disabled routes are ignored.
- `msg_id_min` and `msg_id_max`: inclusive MsgID range.
- `target.type`: currently supported values are `http` and `nsq`.

HTTP target fields:

- `url`: backend endpoint. It remains the operational single-endpoint mode and
  is mutually exclusive with `discovery`.
- `token`: optional bearer token sent to the backend.
- `timeout`: HTTP request timeout.
- `max_in_flight`: optional per-route upstream forwarding concurrency limit.
  Packets above this limit are rejected quickly instead of waiting behind a
  slow backend.

### HTTP Discovery Configuration (V15.1-V15.4.1)

V15.1 defines and validates the configuration contract for HTTP endpoint
discovery. V15.2.1 makes static discovery operational with immutable endpoint
snapshots, concurrent round-robin selection, and process-local cooldown.
V15.2.2 makes DNS A/AAAA discovery operational with periodic refresh and
last-known-good retention. Existing `url` routes remain fully operational.

Static discovery lists complete backend URLs:

```yaml
upstream:
  routes:
    - name: orders-http
      enabled: true
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        discovery:
          type: static
          endpoints:
            - http://orders-a:8080/gateway/upstream
            - http://orders-b:8080/gateway/upstream
        timeout: 2s
        failover:
          enabled: true
          max_attempts: 2
          unhealthy_cooldown: 15s
```

DNS discovery builds endpoint URLs from a scheme, resolved address, port, and
route path:

```yaml
target:
  type: http
  path: /gateway/upstream
  discovery:
    type: dns
    scheme: http
    hostname: orders.default.svc.cluster.local
    port: 8080
    refresh_interval: 10s
  timeout: 2s
  failover:
    enabled: true
    max_attempts: 2
    unhealthy_cooldown: 15s
```

Validation rules and defaults:

- `discovery.type` is `static` or `dns`; `url` and `discovery` cannot be set
  together.
- Static endpoints must be distinct absolute `http` or `https` URLs. Their
  paths are part of each URL. Requests select endpoints in round-robin order.
- DNS requires `scheme`, `hostname`, and a port from `1` through `65535`.
  `path` defaults to `/`; `refresh_interval` defaults to `30s` and accepts
  values from `1s` through `1h`.
- Each gateway process performs an immediate initial lookup in the background.
  The first message waits for that bounded lookup if it is still running. Until
  the first lookup succeeds, forwarding returns a clear no-available-endpoint
  error.
- A successful refresh atomically replaces the immutable endpoint snapshot.
  A failed or empty refresh retains the last-known-good snapshot; addresses
  removed by a later successful lookup are also removed from selector cooldown
  state.
- DNS results are used as connection addresses, while the configured logical
  hostname remains the HTTP `Host` header and HTTPS TLS SNI value. This keeps
  virtual hosting and certificate verification correct for both IPv4 and IPv6
  results.
- `failover` is available only with discovery. When enabled,
  `max_attempts` defaults to `2` and is bounded from `2` through `4`;
  `unhealthy_cooldown` defaults to `15s` and is bounded from `1s` through
  `10m`. For static discovery, `max_attempts` cannot exceed the number of
  configured endpoints.
- Without failover, each message has one selected endpoint and one HTTP
  attempt. With failover enabled, only a transport failure observed before
  response headers can try another unattempted endpoint. The failed endpoint
  remains in process-local cooldown and is skipped while another healthy
  endpoint exists.
- `timeout` applies to each endpoint attempt. Choose `timeout` and
  `max_attempts` together so their worst-case latency remains inside the
  gateway's request budget.
- A regular Kubernetes Service normally resolves to its virtual ClusterIP. A
  headless Service can return Pod addresses and therefore exposes multiple
  candidates to endpoint selection. Resolution and refresh state are
  process-local, so every gateway instance resolves independently.
- `refresh_interval` is the gateway refresh cadence, not an authoritative DNS
  TTL. Choose it according to expected endpoint churn and DNS load.
- A received HTTP response, including `5xx`, is not replayed automatically.
  Backends should use `MessageID` as an idempotency key where duplicate
  processing is unsafe.

V15.3.1 reports the final forwarding decision without exposing an internal URL,
response body, or network error through the client ACK:

- `failure_class` is one of `encoding`, `discovery`, `request`, `transport`,
  `timeout`, `canceled`, or `response`.
- `failover_decision` is `disabled`, `not_retryable`, `exhausted`, or
  `no_alternate`.
- Structured gateway logs include the route, target type, sanitized endpoint,
  `attempt_count`, `max_attempts`, and whether failover was attempted. Endpoint
  user information, query parameters, and fragments are removed.
- A rejected upstream packet receives the stable ACK reason
  `upstream_failed`. This does not prove that a backend never observed an
  earlier attempt. A client retry must reuse the same `MessageID`, and the
  backend remains responsible for business idempotency.

V15.4.1 exposes discovery and failover behavior through Prometheus:

- `z_courier_upstream_discovery_refresh_total` and
  `z_courier_upstream_discovery_refresh_duration_seconds` report DNS refresh
  outcomes (`success`, `error`, or `empty`) and latency.
- `z_courier_upstream_discovery_resolved_endpoints` reports the size of the
  active static or last-known-good DNS snapshot.
- `z_courier_upstream_endpoint_selection_total` reports `selected`,
  `resolver_error`, and `no_available` selection outcomes.
- `z_courier_upstream_endpoint_cooldown_skipped_total` and
  `z_courier_upstream_endpoint_unhealthy` show process-local cooldown activity
  and the current unhealthy count.
- `z_courier_upstream_endpoint_failure_total` groups failed endpoint attempts
  by the bounded `failure_class`.
- `z_courier_upstream_discovery_attempts` is a histogram of endpoint attempts
  used by each discovery-backed message, split by `success` or `failure`.
- `z_courier_upstream_failover_total` reports terminal decisions including
  `succeeded`, `disabled`, `not_retryable`, `exhausted`, and `no_alternate`.

Every metric is labeled only by route name, discovery type, and a bounded
result/class/decision where applicable. Resolved IP addresses, hostnames,
internal URLs, tokens, raw errors, and message identifiers are deliberately
not metric labels. Keep route names from a bounded configuration set to avoid
unnecessary Prometheus cardinality.

V15.4.2 also includes a nested `discovery` object for discovery-backed routes
in `GET /internal/admin/diagnostics` and diagnosis bundles. It reports current
resolved and unhealthy counts plus the most recent refresh, selection,
cooldown skip, classified endpoint failure, forwarding result, attempt count,
and failover decision. These values are process-local observations and the
endpoint performs no active DNS or backend probe. Endpoint addresses,
hostnames, URLs, tokens, and raw errors are never returned. Event fields and
timestamps are omitted until the corresponding event has occurred. A diagnosis
bundle embeds this same discovery snapshot; its separate route-configuration
section continues to follow the existing sanitized URL contract.

V15.4.4 carries the same contract into the deployment references:

- production Compose configs use two explicit static backend URLs;
- Helm includes `examples/values-static-discovery.yaml` and
  `examples/values-dns-discovery.yaml`;
- `examples/values-production.yaml` demonstrates Kubernetes headless-Service
  DNS; and
- `bash scripts/discovery_deployment_check.sh` validates the schema, rendered
  ConfigMaps, negative cases, and both generated gateway configs.

The Docker image CI reruns that verifier with the built image as the config
loader, so the examples are checked against the packaged gateway binary as
well as the source tree.

NSQ target example:

```yaml
upstream:
  routes:
    - name: dev-nsq-upstream
      enabled: true
      msg_id_min: 2000
      msg_id_max: 2999
      target:
        type: nsq
        nsqd_addrs:
          - 127.0.0.1:4150
        topic: message_events
        auth_secret: ""
        dial_timeout: 1s
        read_timeout: 60s
        write_timeout: 1s
        publish_mode: round_robin
        retry_attempts: 1
        max_in_flight: 1000
```

NSQ target fields:

- `nsqd_addrs`: TCP addresses of producer-facing nsqd nodes.
- `topic`: NSQ topic name.
- `auth_secret`: optional NSQ auth secret.
- `dial_timeout`, `read_timeout`, `write_timeout`: NSQ producer timeouts.
- `publish_mode`: currently supports `round_robin`.
- `retry_attempts`: retries across configured nsqd addresses after publish
  failure.
- `max_in_flight`: optional per-route NSQ publish concurrency limit.

`addr` is still accepted for a single nsqd node, but `nsqd_addrs` is preferred.
