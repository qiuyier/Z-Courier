# Local Integration Stack

This stack is for validating the Z-Courier reliable downlink path with
PostgreSQL storage, Redis, NSQ, Prometheus, and Grafana.

For the full V2 release-candidate checklist, see
[../../docs/v2-release-candidate.md](../../docs/v2-release-candidate.md).

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

The local cluster configs also enable the embedded admin console:

- `gateway-a`: `http://127.0.0.1:18182/console/`
- `gateway-b`: `http://127.0.0.1:18183/console/`

Use `dev-internal-token` to sign in. The two configs intentionally use
different admin session cookie names so signing in to one local node does not
invalidate the other node's console session on `127.0.0.1`.

The verifier connects the client to `gateway-b`, sends `/internal/push` to
`gateway-a`, and checks that Redis route lookup plus peer push delivers the
message to the client on `gateway-b`. The cluster config uses a short Redis
route TTL and waits before the online push, so the test also verifies the
gateway-side route refresher keeps quiet clients discoverable. It also
disconnects and reconnects the client to verify that a message pushed while the
client is disconnected stays pending and is flushed after the next bind.

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
mkdir -p reports/baseline/loadtest-manual
cp reports/loadtest-manual/upstream.json reports/baseline/loadtest-manual/upstream.json

go run ./cmd/loadcompare \
  -base reports/baseline/loadtest-manual/upstream.json \
  -current reports/loadtest-manual/upstream.json \
  -output reports/loadtest-manual/compare.md
```

CI and the manual load-test workflow use the same convention. The preferred
paths are `reports/baseline/loadtest-smoke/<mode>.json` and
`reports/baseline/loadtest-manual/<mode>.json`; workflows also fall back to
`reports/baseline/<mode>.json` for compatibility. If a matching baseline exists,
the workflow appends a comparison to the GitHub Actions summary and uploads the
comparison Markdown with the report artifact. If no baseline exists, the
workflow only prints a skip notice.

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

open http://127.0.0.1:18182/console/
open http://127.0.0.1:18183/console/

go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics \
  -online-push-delay 5s \
  -require-cluster-metrics \
  -expect-route-node gateway-b \
  -expect-route-internal-url http://127.0.0.1:18183 \
  -expect-session-url http://127.0.0.1:18183 \
  -expect-session-node gateway-b \
  -check-reconnect-retry
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

`route` and `sessions` are intentionally different. `route` answers where the
current gateway would send traffic for a client/device. It checks local session
state and then the shared Redis online route. `sessions` only lists sessions
local to the gateway process you queried. If the client is connected to
`gateway-b`, then querying `sessions` on `gateway-a` should return zero rows,
while querying `route` on `gateway-a` should show a cluster route pointing to
`gateway-b`.

To manually observe the reconnect reliability path:

1. Start both gateways and connect `devclient` to `gateway-b`.
2. Push once through `gateway-a` and confirm the client receives it.
3. Stop `devclient`.
4. Push another message through `gateway-a`; it should return `202 queued`.
5. Start `devclient` again with the same `client_id` and `device_id`.
6. The queued message should be flushed after the new bind.

The automated version of that flow is:

```bash
go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics \
  -expect-route-node gateway-b \
  -expect-route-internal-url http://127.0.0.1:18183 \
  -expect-session-url http://127.0.0.1:18183 \
  -expect-session-node gateway-b \
  -check-reconnect-retry
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
- The cluster verifier checks `/internal/debug/route` on `gateway-a` and
  `/internal/debug/sessions` on `gateway-b`.
- The cluster verifier checks disconnect -> queued retry -> reconnect flush ->
  delivered.
- When reconnect retry checks are enabled, the verifier requires non-zero
  metrics for queued downlink push, registry lookup, registry unbind, retry
  scan, retry claim duration, and successful peer push.

Useful metric queries while the local stack is running:

```bash
curl -s http://127.0.0.1:18182/metrics | rg 'z_courier_cluster_(registry|peer|stale)'
curl -s http://127.0.0.1:18182/metrics | rg 'z_courier_downlink_retry'
curl -s http://127.0.0.1:18183/metrics | rg 'z_courier_sessions_online|z_courier_clients_online'
```

## Local URLs

- Prometheus: `http://127.0.0.1:19090`
- Alertmanager: `http://127.0.0.1:19093`
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
