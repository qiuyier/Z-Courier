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

Run the two-node cluster verifier:

```bash
bash scripts/e2e_cluster.sh
```

It starts two gateway processes:

- `gateway-a`: TCP `9901`, internal HTTP `18182`
- `gateway-b`: TCP `9902`, internal HTTP `18183`

The verifier connects the client to `gateway-b`, sends `/internal/push` to
`gateway-a`, and checks that Redis route lookup plus peer push delivers the
message to the client on `gateway-b`. The cluster config uses a short Redis
route TTL and waits before the online push, so the test also verifies the
gateway-side route refresher keeps quiet clients discoverable.

Run the local load-test smoke verifier:

```bash
bash scripts/loadtest_smoke.sh
```

It starts PostgreSQL and NSQ, starts one integration gateway, then runs
conservative upstream and downlink `cmd/loadtest` checks with JSON reports in
`reports/loadtest-smoke/`. The downlink smoke targets `/internal/push`; it
checks internal HTTP acceptance and storage/queueing behavior rather than
real online client delivery.

Run the same manual load-test path used by GitHub Actions:

```bash
LOADTEST_MODE=upstream \
LOADTEST_DURATION=30s \
LOADTEST_RATE=200 \
LOADTEST_CLIENTS=100 \
LOADTEST_MIN_QPS=1 \
  bash scripts/loadtest_manual.sh
```

Reports are written to `reports/loadtest-manual/`, and the gateway log is
written to `log/loadtest-manual-gateway.log`. Convert JSON reports into a
Markdown summary with:

```bash
go run ./cmd/loadreport \
  -output reports/loadtest-manual/summary.md \
  reports/loadtest-manual/*.json
```

Save a stable report as a local baseline and compare future runs against it:

```bash
mkdir -p reports/baseline
cp reports/loadtest-manual/upstream.json reports/baseline/upstream.json

go run ./cmd/loadcompare \
  -base reports/baseline/upstream.json \
  -current reports/loadtest-manual/upstream.json \
  -output reports/loadtest-manual/compare.md
```

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

Run the same verifier against two local gateway nodes:

```bash
ZINX_CONFIG_FILE_PATH=conf/zinx.cluster-a.json \
  go run ./cmd/gateway -config configs/z-courier.cluster-a.yaml

ZINX_CONFIG_FILE_PATH=conf/zinx.cluster-b.json \
  go run ./cmd/gateway -config configs/z-courier.cluster-b.yaml

go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics
```

Manually push a downlink message through `gateway-a` to a client connected to
`gateway-b`:

```bash
go run ./cmd/devclient \
  -port 9902 \
  -client-id e2e-client \
  -device-id e2e-device \
  -token e2e-token

go run ./cmd/devbackend push \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device \
  -msg-id 2001 \
  -body "hello from gateway-a"
```

Push multiple downlink messages in one request:

```bash
go run ./cmd/devbackend batch \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message "e2e-client,e2e-device,2001,hello one" \
  -message "e2e-client,e2e-device,2001,hello two"
```

Query a stored downlink message status:

```bash
go run ./cmd/devbackend status \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id devbackend-push-...
```

Query where a client/device is currently routed:

```bash
go run ./cmd/devbackend route \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device
```

List local sessions on a gateway node:

```bash
go run ./cmd/devbackend sessions \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client
```

List failed messages and manually handle one:

```bash
go run ./cmd/devbackend list \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -status failed

go run ./cmd/devbackend requeue \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id devbackend-push-...

go run ./cmd/devbackend discard \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -message-id devbackend-push-... \
  -reason "handled manually"
```

## What E2E Checks

- Offline downlink push returns `queued` and is stored in PostgreSQL.
- Client session bind flushes the queued message.
- Client sends `MsgID = 2` delivery ACK.
- PostgreSQL message status becomes `delivered`.
- Online downlink push is delivered and ACKed.
- A `MsgID = 2001` upstream packet is accepted by the NSQ route.
- Gateway metrics include downlink push, downlink ACK, online session, and
  unique online client metrics.
- The cluster verifier additionally checks cross-node downlink dispatch through
  Redis online routes and `POST /internal/cluster/push`.

## Local URLs

- Prometheus: `http://127.0.0.1:19090`
- Grafana: `http://127.0.0.1:13000` (`admin` / `admin`)
- NSQ Admin: `http://127.0.0.1:14171`
- Redis: `127.0.0.1:16379`
- Gateway metrics: `http://127.0.0.1:18082/metrics`
- Gateway readiness: `http://127.0.0.1:18082/readyz`
- Cluster gateway A metrics: `http://127.0.0.1:18182/metrics`
- Cluster gateway B metrics: `http://127.0.0.1:18183/metrics`
- Cluster gateway A readiness: `http://127.0.0.1:18182/readyz`
- Cluster gateway B readiness: `http://127.0.0.1:18183/readyz`

## Stop

Stop the Docker services:

```bash
docker compose -f deploy/local/docker-compose.yml down
```

Remove local volumes:

```bash
docker compose -f deploy/local/docker-compose.yml down -v
```
