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

## Development

Z-Courier targets Go 1.26.

Run tests:

```bash
go test ./...
```

Start the gateway:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

Zinx loads its framework config from `conf/zinx.json` by default. To use another
file, set `ZINX_CONFIG_FILE_PATH` before starting the gateway.

Z-Courier loads its gateway config from `configs/z-courier.yaml` by default. You
can override it with `-config` or the `ZCOURIER_CONFIG` environment variable.

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
    "body": "aGVsbG8="
  }'
```

`body` is base64-encoded in the HTTP JSON request because the gateway treats it
as opaque bytes.

Run the development client in another terminal:

```bash
go run ./cmd/devclient
```

The client sends one upstream bind packet with `dev-token` and `device-1`, then
prints ACK and downlink packets. With both gateway and devclient running, the
`curl` command above should make the client print a `MsgID = 2001` packet whose
body is `hello`.

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
        addr: 127.0.0.1:4150
        topic: message_events
        write_timeout: 1s
```

The NSQ message body is the same JSON envelope used by the HTTP upstream
adapter. Its `body` field is base64-encoded by JSON because the gateway treats
payload bytes as opaque data.

## Project Structure
- `cmd/gateway`: Gateway entry point
- `cmd/devclient`: Development client for manual end-to-end testing
- `cmd/devbackend`: Development backend for upstream forwarding tests
- `configs`: Z-Courier gateway configuration
- `conf`: Zinx runtime configuration
- `docs`: Design and operation documents
- `internal/adapter`: Upstream target adapters
- `internal/auth`: Token verification interfaces and development verifier
- `internal/config`: Z-Courier config loading and conversion
- `internal/downlink`: Internal push API and online delivery service
- `internal/protocol`: Packet codec and protocol types
- `internal/router`: MsgID route engine
- `internal/server`: Zinx server bootstrap
- `internal/session`: Connection binding and online state
