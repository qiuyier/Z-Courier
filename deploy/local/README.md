# Local Integration Stack

This stack is for validating the Z-Courier reliable downlink path with
PostgreSQL storage, Redis, NSQ, Prometheus, and Grafana.

## One Command

From the repository root:

```bash
bash scripts/e2e.sh
```

The script starts the local Docker Compose services, starts the gateway with
`configs/z-courier.integration.yaml`, then runs `cmd/e2e`.

The integration config is intentionally isolated from the default development
ports and tokens. See [../../docs/configuration.md](../../docs/configuration.md)
and [../../docs/protocol.md](../../docs/protocol.md) for the config and packet
contracts used by the verifier.

## Manual Run

Start local dependencies:

```bash
docker compose -f deploy/local/docker-compose.yml up -d
```

Start the gateway with PostgreSQL storage:

```bash
ZINX_CONFIG_FILE_PATH=conf/zinx.integration.json \
  go run ./cmd/gateway -config configs/z-courier.integration.yaml
```

Run the E2E verifier:

```bash
go run ./cmd/e2e
```

## What E2E Checks

- Offline downlink push returns `queued` and is stored in PostgreSQL.
- Client session bind flushes the queued message.
- Client sends `MsgID = 2` delivery ACK.
- PostgreSQL message status becomes `delivered`.
- Online downlink push is delivered and ACKed.
- A `MsgID = 2001` upstream packet is accepted by the NSQ route.
- Gateway metrics include downlink push, downlink ACK, and online session
  metrics.

## Local URLs

- Prometheus: `http://127.0.0.1:19090`
- Grafana: `http://127.0.0.1:13000` (`admin` / `admin`)
- NSQ Admin: `http://127.0.0.1:14171`
- Redis: `127.0.0.1:16379`
- Gateway metrics: `http://127.0.0.1:18082/metrics`

## Stop

Stop the Docker services:

```bash
docker compose -f deploy/local/docker-compose.yml down
```

Remove local volumes:

```bash
docker compose -f deploy/local/docker-compose.yml down -v
```
