# Production Cluster Reference

This directory provides a two-node Z-Courier production reference stack. It is
meant to demonstrate how multiple gateway nodes share online routes and durable
downlink storage while keeping internal HTTP private.

Copy `.env.example` to `.env`, replace every value, and do not commit the real
`.env` file before using this outside a private test environment.

## Layout

```text
deploy/production-cluster/
  docker-compose.yml
  config/gateway-a.yaml
  config/gateway-b.yaml
  conf/zinx.json
  prometheus/prometheus.yml
```

The stack includes:

- `gateway-a` and `gateway-b`: two Z-Courier gateway nodes
- `postgres`: shared durable downlink storage
- `redis`: shared online route registry
- `nsqlookupd` and `nsqd`: shared NSQ upstream target
- `prometheus`: metrics scraping for both gateway nodes

## Ports

| Host Port | Container | Purpose |
| ---: | --- | --- |
| `8999` | `gateway-a:8999` | TCP client gateway |
| `9000` | `gateway-b:8999` | TCP client gateway |
| `9091` | `prometheus:9090` | Prometheus UI |

The internal HTTP port `18080` is exposed only inside the Compose network. In a
real deployment, backend services should call it through a private network or a
private load balancer, not over the public internet.

## Start

From the repository root:

```bash
cp deploy/production-cluster/.env.example deploy/production-cluster/.env
$EDITOR deploy/production-cluster/.env

docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  up -d --build
```

Stop the stack:

```bash
docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  down
```

Remove local data volumes as well:

```bash
docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  down -v
```

## How Cluster Push Works

Both gateway nodes write online routes into Redis with the same key prefix:

```yaml
cluster:
  enabled: true
  registry:
    type: redis
    redis:
      key_prefix: zcourier:production-cluster
```

When a client connects to `gateway-b`, `gateway-b` stores the route for
`client_id + device_id` in Redis with:

```text
gateway_node = gateway-prod-b
internal_addr = http://gateway-b:18080
```

If a backend sends a downlink request to `gateway-a`, `gateway-a` first checks
its local sessions. If the client is not local, it reads Redis, finds
`gateway-b`, and sends an HMAC-signed peer push to `http://gateway-b:18080`.

This is the same runtime behavior tested by the local cluster E2E, but using
container service names instead of host loopback ports.

Internal HTTP is a private control-plane surface for backend downlink, peer
push, health, metrics, admin APIs, and the optional browser console. The
production cluster reference config keeps `admin_console.enabled: false` on
both gateway nodes. If you enable it, expose `/console/` only through VPN,
bastion, private ingress, or an authenticating reverse proxy. Do not publish
`/console/` or `/internal/*` directly to the public internet.

## Required Environment

Copy `deploy/production-cluster/.env.example` to
`deploy/production-cluster/.env` and replace every value:

| Variable | Purpose |
| --- | --- |
| `ZCOURIER_POSTGRES_PASSWORD` | PostgreSQL password and gateway DSN |
| `ZCOURIER_REDIS_PASSWORD` | Redis password and gateway registry password |
| `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` | Token sent to your auth backend |
| `ZCOURIER_INTERNAL_HMAC_SECRET` | Backend-to-gateway HMAC key |
| `ZCOURIER_PEER_HMAC_SECRET` | Gateway-to-gateway peer HMAC key |
| `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` | Optional HTTP upstream token |

Use different HMAC keys for backend internal HTTP and gateway peer push.

## Backend And Auth Services

The reference configs expect these private service names:

```text
auth-backend:8080
business-backend:8080
```

They are not included in this stack because token verification and business
message handling belong to your application. Add those services to the same
`zcourier-private` network or replace the URLs with your real private service
addresses.

## Verify

Render the Compose configuration:

```bash
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  config
```

Build and start the stack:

```bash
docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  up -d --build
```

Run the production cluster smoke verifier:

```bash
bash scripts/production_cluster_smoke.sh
```

The script renders the Compose config with `.env.example`, builds and starts the
two-node reference stack, checks both gateway nodes from inside the Compose
network, verifies Prometheus can see both gateway targets, and then removes the
stack and volumes. Set `PRODUCTION_CLUSTER_SMOKE_KEEP_STACK=1` to keep the stack
running after the script exits.

Check Prometheus readiness:

```bash
curl http://127.0.0.1:9091/-/ready
```

Prometheus scrapes:

```text
gateway-a:18080
gateway-b:18080
```

Prometheus also loads the bundled Z-Courier recording and alert rules from
`deploy/monitoring/prometheus/rules/z-courier-alerts.yml`. Open
`Status -> Rules` in Prometheus to review active rules and firing alerts.

This reference stack evaluates alerts but does not send notifications. Connect
Alertmanager or your platform alerting system before using the rules for paging.

For a full runtime cluster delivery check, use the local verifier:

```bash
bash scripts/e2e_cluster.sh
```

The verifier starts its own local test stack and proves the same route lookup
and peer push behavior with deterministic test clients.

CI runs the production cluster smoke verifier as a deployment-reference boot
check. It does not replace the local cluster E2E; the production reference stack
does not include real `auth-backend` or `business-backend` services.
