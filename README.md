# Z-Courier

A high-performance message push gateway based on the `zinx` network framework.

## Features
- High concurrency based on zinx
- Lightweight and efficient message routing
- Fast JSON serialization based on Sonic
- Structured logging based on Zap
- Pluggable route targets such as HTTP, gRPC, NSQ, Kafka, and NATS
- Reliable downlink delivery with ACK, retry, and idempotency hooks
- Graceful shutdown with readiness drain and cluster route cleanup
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

Run the V2 two-node cluster verifier:

```bash
bash scripts/e2e_cluster.sh
```

It starts two local gateway processes sharing PostgreSQL and Redis, connects the
test client to `gateway-b`, sends `/internal/push` to `gateway-a`, and verifies
that peer push delivers the message across nodes.

Local service URLs and the manual workflow are documented in
[deploy/local/README.md](deploy/local/README.md).

## Development

Z-Courier targets Go 1.26.

Run tests:

```bash
go test ./...
```

Run a small upstream load test against a local gateway:

```bash
go run ./cmd/loadtest \
  -mode upstream \
  -port 9899 \
  -token e2e-token \
  -clients 100 \
  -messages 10 \
  -upstream-msg-id 2001
```

`cmd/loadtest` suppresses Zinx internal connection logs by default so the output
focuses on the summary. Pass `-zinx-log` when debugging low-level client
connections.

The summary includes ACK/HTTP latency percentiles and grouped failure reasons:

```text
latency upstream_ack count=1000 min=1.2ms avg=3.4ms p50=2.8ms p95=8.9ms p99=12.1ms max=20.4ms
failure reasons:
  upstream_overloaded=42
```

Run a downlink HTTP load test:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18082 \
  -clients 100 \
  -messages 10 \
  -http-concurrency 50
```

Run a sustained downlink load test for dashboard observation:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -duration 60s \
  -rate 500 \
  -clients 100 \
  -http-concurrency 100 \
  -report reports/loadtest-downlink.json
```

When `-duration` is set, `-rate` is required and means target messages per
second. Without `-duration`, `cmd/loadtest` keeps the original finite behavior
and sends `clients * messages` messages.

Pass `-report` to save the same summary as JSON. Missing report directories are
created automatically.

Run a local load-test smoke check:

```bash
bash scripts/loadtest_smoke.sh
```

The smoke script starts the local PostgreSQL and NSQ dependencies, starts one
integration gateway, then runs conservative upstream and downlink threshold
checks. Reports are written to `reports/loadtest-smoke/`.

Use threshold flags when you want the load test to behave like an acceptance
check. The command exits with code 1 when any check fails, but still writes the
JSON report first:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18182 \
  -duration 60s \
  -rate 500 \
  -clients 100 \
  -http-concurrency 100 \
  -min-qps 450 \
  -max-p95-ms 50 \
  -max-p99-ms 100 \
  -max-error-rate 0.01 \
  -report reports/loadtest-downlink.json
```

`-max-error-rate 0` means no failures are allowed. The error rate is reported as
a fraction, so `0.01` means 1%.

For real online downlink delivery, keep matching clients connected first. Without
online clients, the gateway still accepts the request and writes the offline
retry path when storage is enabled.

Run the local V1 integration verifier:

```bash
bash scripts/e2e.sh
```

It starts PostgreSQL, NSQ, Prometheus, Grafana, the gateway, and validates the
reliable downlink path with PostgreSQL storage. See
[deploy/local/README.md](deploy/local/README.md) for the manual workflow and
local URLs.

Run the local V2 cluster integration verifier:

```bash
bash scripts/e2e_cluster.sh
```

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

The gateway always registers `MsgID = 1000` for AUTH/BIND and `MsgID = 2` for
downlink delivery ACKs, then registers additional Zinx routes from
`route_msg_ids` and enabled upstream route ranges. The router decodes the
Z-Courier protocol packet from the Zinx request body, verifies the token, binds
the connection on AUTH/BIND, logs the metadata, forwards business packets when
an upstream route matches, and returns an ACK packet with `MsgID = 1`.

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
    ack_timeout: 30s
    retry_lease: 30s
    max_attempts: 5
    scan_limit: 100
    bind_flush_limit: 100
  retention:
    delivered_ttl: 24h
    failed_ttl: 168h
    discarded_ttl: 168h
    cleanup_interval: 1h
    cleanup_limit: 1000
```

When the target client is online, `/internal/push` returns `200` with
`delivery_state = sent`. When the message is stored but the client is offline,
it returns `202` with `delivery_state = queued`. The `memory` store is useful
for local development, but queued messages are lost on gateway restart.

Stored messages are retried in three ways:

- The retry worker scans due pending messages every `retry_interval`.
- When a client session is newly bound, the gateway immediately flushes pending
  messages for that `client_id` + `device_id`, up to `bind_flush_limit`.
- Sent messages that require client ACK are retried after `ack_timeout` if the
  ACK does not arrive.

Failed retry attempts update `attempts`, `last_error`, and `next_retry_at`.
After `max_attempts`, the message is marked `failed`.

The retention worker deletes expired terminal messages from the downlink store:
`delivered` after `delivered_ttl`, `failed` after `failed_ttl`, and
`discarded` after `discarded_ttl`. Pending and sent messages are not deleted by
retention cleanup.

Failed messages can be inspected and manually handled through the internal API:

```bash
go run ./cmd/devbackend list \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -status failed

go run ./cmd/devbackend requeue \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1

go run ./cmd/devbackend discard \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -reason "handled manually"
```

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

The client sends one `MsgID = 1000` AUTH/BIND packet with `dev-token` and
`device-1`, then prints ACK and downlink packets. With both gateway and
devclient running, the `curl` command above should make the client print a
`MsgID = 2001` packet whose body is `hello`. Because the request sets
`ack_required: true`, the development client also sends a `MsgID = 2` delivery
ACK back to the gateway.

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
bind the connection and ACK it locally. It will not forward the bind packet
upstream. Send a separate business packet in the route range, for example
`MsgID = 1001-1999`, to verify HTTP upstream forwarding:

```bash
go run ./cmd/devclient -upstream-msg-id 1001
```

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
