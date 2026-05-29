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
go run ./cmd/gateway
```

Zinx loads its framework config from `conf/zinx.json` by default. To use another
file, set `ZINX_CONFIG_FILE_PATH` before starting the gateway.

The current MVP registers a Zinx route for `MsgID = 1000`. The router decodes
the Z-Courier protocol packet from the Zinx request body, verifies the token,
binds the connection to a session, logs the metadata, and returns an ACK packet
with `MsgID = 1`.

The default development token is:

```text
dev-token -> client_id: dev-client
```

`DeviceID` must be present in the protocol packet. `ClientID` from the packet is
treated as a claimed identity only; the gateway binds the session using the
identity returned by token verification.

## Project Structure
- `cmd/gateway`: Gateway entry point
- `conf`: Zinx runtime configuration
- `configs`: Sample configuration files
- `internal/server`: Zinx server bootstrap
- `internal/protocol`: Packet codec and protocol types
- `internal/session`: Connection binding and online state
- `internal/pipeline`: Gateway middleware chain
- `internal/router`: Route matching and dispatch
- `internal/adapter`: Built-in forwarding adapters
- `internal/downlink`: Internal push APIs and delivery flow
- `docs`: Design and operation documents
