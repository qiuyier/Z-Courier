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
checks.

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
```

The ingress pipeline order is:

```text
auth -> allowlist/blocklist -> rate limit -> session bind -> access log
```

If an allowlist is empty, it does not restrict that dimension. Blocklists always
take priority.

The current rate limiter is a fixed-window per-client limiter.

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

- `url`: backend endpoint.
- `token`: optional bearer token sent to the backend.
- `timeout`: HTTP request timeout.
- `max_in_flight`: optional per-route upstream forwarding concurrency limit.
  Packets above this limit are rejected quickly instead of waiting behind a
  slow backend.

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
