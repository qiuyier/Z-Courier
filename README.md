# Z-Courier

A high-performance message push gateway based on the `zinx` network framework.

## Features
- High concurrency based on zinx
- Lightweight and efficient message routing
- Pluggable route targets such as HTTP, gRPC, NSQ, Kafka, and NATS
- Reliable downlink delivery with ACK, retry, and idempotency hooks
- MIT Licensed

## Architecture

See [docs/architecture.md](docs/architecture.md) for the initial open-source
middleware architecture.

## Development

Run tests:

```bash
go test ./...
```

Start the gateway:

```bash
go run ./cmd/gateway
```

The first MVP registers a Zinx route for `MsgID = 1000`. The router decodes the
Z-Courier protocol packet from the Zinx request body, logs the metadata, and
returns an ACK packet with `MsgID = 1`.

## Project Structure
- `cmd/gateway`: Gateway entry point
- `configs`: Sample configuration files
- `internal/server`: Zinx server bootstrap
- `internal/protocol`: Packet codec and protocol types
- `internal/session`: Connection binding and online state
- `internal/pipeline`: Gateway middleware chain
- `internal/router`: Route matching and dispatch
- `internal/adapter`: Built-in forwarding adapters
- `internal/downlink`: Internal push APIs and delivery flow
- `docs`: Design and operation documents
