# Z-Courier Configuration

Z-Courier uses two configuration files at runtime:

- `conf/zinx.json` configures the underlying Zinx TCP server.
- `configs/z-courier.yaml` configures gateway behavior.

The gateway config path defaults to `configs/z-courier.yaml`. Override it with
the `-config` flag or the `ZCOURIER_CONFIG` environment variable.

The Zinx config path defaults to `conf/zinx.json`. Override it with
`ZINX_CONFIG_FILE_PATH`.

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
  upstream route ranges. Keep command MsgIDs such as bind commands here.

Z-Courier also always registers `MsgID = 2` for downlink delivery ACK packets.

## Auth

```yaml
auth:
  static_tokens:
    dev-token:
      client_id: dev-client
      token_id: dev-token
      scopes:
        - gateway:dev
```

The static verifier is for local development and tests. In production, replace
it with a verifier that matches the backend application's token semantics.

The gateway does not trust `ClientID` from the packet until the token is
verified. Session binding uses the `client_id` returned by the verifier.

## Internal HTTP

```yaml
internal_http:
  enabled: true
  addr: 127.0.0.1:18080
  token: dev-internal-token
  max_request_body_size: 10485760
```

- `enabled`: starts or disables internal HTTP endpoints.
- `addr`: listen address for `/internal/push` and `/metrics`.
- `token`: value required in the `X-ZCourier-Internal-Token` header.
- `max_request_body_size`: maximum JSON request size in bytes.

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
```

- `enabled`: enables V2 online route registration. `false` keeps V1 local-only
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
- `peer.token`: token required by `POST /internal/cluster/push`.
- `peer.timeout`: gateway-to-gateway HTTP push timeout used by the peer
  dispatcher.

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

The local cluster verifier uses `configs/z-courier.cluster-a.yaml` and
`configs/z-courier.cluster-b.yaml` with the same Redis key prefix and
PostgreSQL DSN:

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
- `postgres`: durable V1 storage for offline and retryable downlink messages.

PostgreSQL fields:

- `dsn`: pgx-compatible PostgreSQL DSN.
- `auto_migrate`: creates the V1 downlink table when the store starts.
- `max_open_conns`, `max_idle_conns`, `conn_max_lifetime`: database pool
  controls.

## Downlink Delivery

```yaml
downlink:
  delivery:
    retry_interval: 5s
    retry_delay: 30s
    max_attempts: 5
    scan_limit: 100
    bind_flush_limit: 100
```

- `retry_interval`: how often the retry worker scans pending messages.
- `retry_delay`: delay before the next attempt after a failed send.
- `max_attempts`: attempts before a message is marked `failed`.
- `scan_limit`: maximum due messages scanned per retry tick.
- `bind_flush_limit`: maximum pending messages flushed when a client binds.

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

The V1 rate limiter is a fixed-window per-client limiter.

## Upstream Routes

```yaml
upstream:
  routes:
    - name: dev-http-upstream
      enabled: true
      msg_id_min: 1000
      msg_id_max: 1999
      target:
        type: http
        url: http://127.0.0.1:18081/gateway/upstream
        token: ""
        timeout: 5s
```

- `enabled`: disabled routes are ignored.
- `msg_id_min` and `msg_id_max`: inclusive MsgID range.
- `target.type`: supported V1 values are `http` and `nsq`.

HTTP target fields:

- `url`: backend endpoint.
- `token`: optional bearer token sent to the backend.
- `timeout`: HTTP request timeout.

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
```

NSQ target fields:

- `nsqd_addrs`: TCP addresses of producer-facing nsqd nodes.
- `topic`: NSQ topic name.
- `auth_secret`: optional NSQ auth secret.
- `dial_timeout`, `read_timeout`, `write_timeout`: NSQ producer timeouts.
- `publish_mode`: V1 supports `round_robin`.
- `retry_attempts`: retries across configured nsqd addresses after publish
  failure.

`addr` is still accepted for a single nsqd node, but `nsqd_addrs` is preferred.
