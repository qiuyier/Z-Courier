# Z-Courier

A high-performance message push gateway based on the `zinx` network framework.

## Features
- High concurrency based on zinx
- Lightweight and efficient message routing
- Fast JSON serialization based on Sonic
- Structured logging based on Zap
- Pluggable route targets such as HTTP, gRPC, NSQ, Kafka, and NATS
- Reliable downlink delivery with ACK, retry, and idempotency hooks
- MIT Licensed

## Architecture

See [docs/architecture.md](docs/architecture.md) for the initial open-source
middleware architecture.

V2 cluster design is tracked in
[docs/v2-cluster-architecture.md](docs/v2-cluster-architecture.md).

Release history is tracked in [CHANGELOG.md](CHANGELOG.md).

## Quick Start

Run the V1 local integration verifier from the repository root:

```bash
bash scripts/e2e.sh
```

The script starts PostgreSQL, NSQ, Prometheus, Grafana, and the gateway, then
validates:

- offline downlink queueing with PostgreSQL
- client bind and offline message flush
- online push and client delivery ACK
- upstream forwarding to NSQ
- Prometheus metrics exposure

Local service URLs and the manual workflow are documented in
[deploy/local/README.md](deploy/local/README.md).

## Development

Z-Courier targets Go 1.26.

Run tests:

```bash
go test ./...
```

Run the local V1 integration verifier:

```bash
bash scripts/e2e.sh
```

It starts PostgreSQL, NSQ, Prometheus, Grafana, the gateway, and validates the
reliable downlink path with PostgreSQL storage. See
[deploy/local/README.md](deploy/local/README.md) for the manual workflow and
local URLs.

Start the gateway:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

Zinx loads its framework config from `conf/zinx.json` by default. To use another
file, set `ZINX_CONFIG_FILE_PATH` before starting the gateway.

Z-Courier loads its gateway config from `configs/z-courier.yaml` by default. You
can override it with `-config` or the `ZCOURIER_CONFIG` environment variable.
See [docs/configuration.md](docs/configuration.md) for all current V1 config
fields.

The current binary packet format is documented in
[docs/protocol.md](docs/protocol.md).

The gateway registers Zinx routes from `route_msg_ids` and enabled upstream
route ranges. The router decodes the Z-Courier protocol packet from the Zinx
request body, verifies the token, binds the connection to a session, logs the
metadata, forwards the packet when an upstream route matches, and returns an ACK
packet with `MsgID = 1`.

The default development token is:

```text
dev-token -> client_id: dev-client
```

`DeviceID` must be present in the protocol packet. `ClientID` from the packet is
treated as a claimed identity only; the gateway binds the session using the
identity returned by token verification.

The internal downlink API listens on `127.0.0.1:18080` by default:

```bash
curl -X POST http://127.0.0.1:18080/internal/push \
  -H 'Content-Type: application/json' \
  -H 'X-ZCourier-Internal-Token: dev-internal-token' \
  -d '{
    "client_id": "dev-client",
    "device_id": "device-1",
    "msg_id": 2001,
    "message_id": "message-1",
    "trace_id": "trace-1",
    "ack_required": true,
    "body": "aGVsbG8="
  }'
```

`body` is base64-encoded in the HTTP JSON request because the gateway treats it
as opaque bytes.

Downlink push requests are accepted into the configured downlink store before
the gateway tries to deliver them to an online client. The default development
store is in-memory:

```yaml
downlink:
  storage:
    type: memory
```

Use PostgreSQL for durable downlink messages:

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
  delivery:
    retry_interval: 5s
    retry_delay: 30s
    max_attempts: 5
    scan_limit: 100
    bind_flush_limit: 100
```

When the target client is online, `/internal/push` returns `200` with
`delivery_state = sent`. When the message is stored but the client is offline,
it returns `202` with `delivery_state = queued`. The `memory` store is useful
for local development, but queued messages are lost on gateway restart.

Queued messages are retried in two ways:

- The retry worker scans due pending messages every `retry_interval`.
- When a client session is newly bound, the gateway immediately flushes pending
  messages for that `client_id` + `device_id`, up to `bind_flush_limit`.

Failed retry attempts update `attempts`, `last_error`, and `next_retry_at`.
After `max_attempts`, the message is marked `failed`.

Clients confirm downlink delivery by sending a Z-Courier protocol packet with
`MsgID = 2`. The ACK packet is authenticated like other client packets and is
not forwarded upstream. Its JSON body is:

```json
{
  "message_id": "message-1",
  "code": "delivered"
}
```

Run the development client in another terminal:

```bash
go run ./cmd/devclient
```

The client sends one upstream bind packet with `dev-token` and `device-1`, then
prints ACK and downlink packets. With both gateway and devclient running, the
`curl` command above should make the client print a `MsgID = 2001` packet whose
body is `hello`. Because the request sets `ack_required: true`, the development
client also sends a `MsgID = 2` delivery ACK back to the gateway.

To test upstream forwarding, start the development backend:

```bash
go run ./cmd/devbackend
```

Then set the `dev-http-upstream` route in `configs/z-courier.yaml` to
`enabled: true` and start the gateway:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

When `devclient` sends its bind packet with `MsgID = 1000`, the gateway will
forward it to the development backend because the development route matches
`MsgID = 1000-1999`.

To publish upstream packets into NSQ, enable or add an NSQ route:

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
          - 127.0.0.1:4250
        topic: message_events
        write_timeout: 1s
        publish_mode: round_robin
        retry_attempts: 1
```

The NSQ message body is the same JSON envelope used by the HTTP upstream
adapter. Its `body` field is base64-encoded by JSON because the gateway treats
payload bytes as opaque data.

`addr` is still supported for a single `nsqd` node. Use `nsqd_addrs` for
multi-node producer publishing; `retry_attempts` makes the adapter try the next
configured `nsqd` when the first publish attempt fails.

The upstream gateway pipeline can be configured with client/MsgID allowlists,
blocklists, and a fixed-window per-client rate limit:

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

Prometheus metrics are exposed from the internal HTTP server:

```bash
curl http://127.0.0.1:18080/metrics
```

The first metrics include ingress packet totals, rejected ingress packets,
upstream forwarding totals and latency, online sessions, downlink push totals,
downlink ACK totals and latency, and rate-limit rejects.

Start a local Prometheus + Grafana monitoring stack:

```bash
docker compose -f deploy/monitoring/docker-compose.yml up -d
```

Prometheus is available at `http://127.0.0.1:9090`, and Grafana is available at
`http://127.0.0.1:3000` with the default local credentials `admin` / `admin`.
See [deploy/monitoring/README.md](deploy/monitoring/README.md) for dashboard and
scrape-target details.

## Project Structure
- `cmd/gateway`: Gateway entry point
- `cmd/devclient`: Development client for manual end-to-end testing
- `cmd/devbackend`: Development backend for upstream forwarding tests
- `configs`: Z-Courier gateway configuration
- `conf`: Zinx runtime configuration
- `deploy`: Local deployment examples such as monitoring
- `docs`: Design and operation documents
- `internal/adapter`: Upstream target adapters
- `internal/auth`: Token verification interfaces and development verifier
- `internal/config`: Z-Courier config loading and conversion
- `internal/downlink`: Internal push API and online delivery service
- `internal/pipeline`: Ingress gateway middleware chain
- `internal/protocol`: Packet codec and protocol types
- `internal/router`: MsgID route engine
- `internal/server`: Zinx server bootstrap
- `internal/session`: Connection binding and online state
