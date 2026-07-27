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
| `9091` | `prometheus:9090` | Prometheus UI (override with `ZCOURIER_PRODUCTION_PROMETHEUS_PORT`) |

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

Both gateway configs also contain the same disabled `production-critical`
delivery-policy example and keep `downlink.terminal.publisher.type: none`.
Keep policy ranges, queue capacity, and terminal-publisher settings identical
on every node sharing PostgreSQL. Terminal events can be published to NSQ or a
signed HTTPS webhook. For HTTP, every node must use the same endpoint, key ID,
secret, and retry settings. The receiver verifies `ZCOURIER-HMAC-SHA256` and
de-duplicates by stable `event_id`; terminal events never contain the business
message body.

For private-CA or mTLS publication, prepare the host directories configured by
`ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR_A` and
`ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR_B`. Each directory uses the in-container
names `ca.crt`, `tls.crt`, and `tls.key`; the directories may contain the same
certificate or distinct per-node client certificates. Enable the identical
`http.tls` paths in both gateway configs and start with the override:

```bash
docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.terminal-webhook-tls.yml \
  up -d --build
```

Both mounts are read-only. Never commit `deploy/production-cluster/secrets/`.

## TLS Edge Proxy

The cluster Nginx overlay removes both plaintext gateway host ports, terminates
client TCP TLS, load-balances long-lived connections with least-connections,
and exposes only the exact Console HTTPS allowlist:

```bash
bash scripts/generate_edge_test_certs.sh deploy/production-cluster/secrets/edge

docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.edge-nginx.yml \
  up -d --build
```

The generated PKI is local-only. Replace it before production use. The cluster
Console can move between nodes because both configs use Redis admin sessions
and PostgreSQL audit storage. Internal HMAC remains enabled, so the first
browser session login still requires a deployment-side identity/signing
service. The proxy never stores the gateway HMAC key.

The Caddy cluster overlay is available for Console HTTPS only. Standard Caddy
does not provide raw client TCP proxying; pair it with a managed layer-4 load
balancer or another reviewed TCP TLS proxy. Full instructions are in
[../edge/README.md](../edge/README.md).

## Required Environment

Copy `deploy/production-cluster/.env.example` to
`deploy/production-cluster/.env` and replace every value:

| Variable | Purpose |
| --- | --- |
| `ZCOURIER_POSTGRES_PASSWORD` | PostgreSQL password and gateway DSN |
| `ZCOURIER_REDIS_PASSWORD` | Redis password and gateway registry password |
| `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` | Token sent to your auth backend |
| `ZCOURIER_ADMIN_CONSOLE_ENABLED` | Base-stack Console switch; edge overlays set it to true |
| `ZCOURIER_ADMIN_SESSION_ENABLED` | Base-stack browser-session switch; edge overlays set it to true |
| `ZCOURIER_INTERNAL_HMAC_SECRET` | Backend-to-gateway HMAC key |
| `ZCOURIER_PEER_HMAC_SECRET` | Gateway-to-gateway peer HMAC key |
| `ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET` | Optional outbound terminal-webhook HMAC key |
| `ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR_A` | Optional gateway-a host TLS directory |
| `ZCOURIER_TERMINAL_WEBHOOK_TLS_DIR_B` | Optional gateway-b host TLS directory |
| `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` | Optional HTTP upstream token |
| `ZCOURIER_EDGE_SERVER_NAME` | Edge certificate DNS name |
| `ZCOURIER_EDGE_TLS_DIR` | Read-only edge server certificate directory |
| `ZCOURIER_EDGE_CLIENT_TLS_PORT` | Published Nginx client TLS port |
| `ZCOURIER_EDGE_CONSOLE_HTTPS_PORT` | Published Console HTTPS port |

Use different HMAC keys for backend internal HTTP, gateway peer push, and the
outbound terminal webhook.

## Backend And Auth Services

The reference configs expect these private service names:

```text
auth-backend:8080
business-backend-a:8080
business-backend-b:8080
```

They are not included in this stack because token verification and business
message handling belong to your application. Add those services to the same
`zcourier-private` network or replace the static endpoint lists with your real
private service addresses. Both endpoints are active round-robin peers.
Transport failover is bounded to two attempts and does not replay an HTTP
response such as `5xx`.

## Verify

Render the Compose configuration:

```bash
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  config
```

Validate the optional TLS override:

```bash
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.terminal-webhook-tls.yml \
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
does not include real `auth-backend` or business-backend services.
