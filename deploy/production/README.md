# Production Image

This directory documents the first production-oriented gateway image path for
Z-Courier. The image builds `cmd/gateway` as a static Linux binary and packages
the default `configs/` and `conf/` directories under `/app`.

The files in this directory are a production reference, not a secret-safe
drop-in deployment. Copy `.env.example` to `.env`, replace every value, and do
not commit the real `.env` file.

## Reference Layout

```text
deploy/production/
  docker-compose.yml
  config/z-courier.yaml
  conf/zinx.json
  prometheus/prometheus.yml
```

The reference stack includes:

- `gateway`: the Z-Courier gateway image built from the repository Dockerfile
- `postgres`: durable downlink storage
- `redis`: cluster online-route registry, ready for cluster mode
- `nsqlookupd` and `nsqd`: NSQ upstream target
- `prometheus`: gateway metrics scraping over the private Compose network

It intentionally does not publish the gateway internal HTTP port to the host.
Only the TCP client gateway port is published by default.

## Build

From the repository root:

```bash
docker build -t z-courier-gateway:local .
```

The Dockerfile defaults to `golang:1.26-alpine` for the build stage and
`alpine:3.22` for the runtime stage. It also supports Docker's platform build
arguments:

```bash
docker build --platform linux/amd64 -t z-courier-gateway:local .
docker build --platform linux/arm64 -t z-courier-gateway:local-arm64 .
```

## Published Image

The release image publishing workflow publishes the gateway image to GHCR:

```text
ghcr.io/qiuyier/z-courier-gateway:<release-tag>
```

Published gateway images are multi-architecture manifests for `linux/amd64`
and `linux/arm64`.

For stable releases, the workflow can also publish:

```text
ghcr.io/qiuyier/z-courier-gateway:latest
```

Use immutable version tags for production deployments. The local Compose
reference still builds from the repository Dockerfile so it can validate local
changes before they are released.

## Run

The image entrypoint is:

```text
z-courier-gateway -config /app/configs/z-courier.yaml
```

The runtime environment also sets:

```text
ZCOURIER_CONFIG=/app/configs/z-courier.yaml
ZINX_CONFIG_FILE_PATH=/app/conf/zinx.json
```

Start the production reference stack:

```bash
cp deploy/production/.env.example deploy/production/.env
$EDITOR deploy/production/.env

docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  up -d --build
```

Stop it:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  down
```

Remove local data volumes as well:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  down -v
```

For a real deployment, mount your own gateway and Zinx configuration files
rather than using the development defaults:

```bash
docker run --rm \
  -p 8999:8999 \
  -v "$PWD/deploy/production/config/z-courier.yaml:/app/configs/z-courier.yaml:ro" \
  -v "$PWD/deploy/production/conf/zinx.json:/app/conf/zinx.json:ro" \
  z-courier-gateway:local
```

If internal HTTP or metrics must be reachable outside the container, configure
`internal_http.addr` with a container-listening address such as
`0.0.0.0:18080`. The development config uses loopback-oriented defaults and is
not a production security model.

## Ports

| Port | Purpose |
| ---: | --- |
| `8999` | Zinx TCP client gateway |
| `18080` | Internal HTTP, readiness, metrics, and downlink APIs |

Use private networking for internal HTTP in production. Public clients should
only reach the TCP listener or a TLS-terminating proxy in front of it.
The embedded admin console is disabled in the production reference config.
If you enable `admin_console.enabled`, expose it only through VPN, bastion,
private ingress, or an authenticating reverse proxy. Do not publish `/console/`
or `/internal/*` directly to the public internet.

The reference Compose file publishes:

| Host Port | Service |
| ---: | --- |
| `8999` | gateway TCP listener |
| `9090` | Prometheus UI |

`18080` is available only inside the Compose network as `gateway:18080`.

## Configuration Notes

- `configs/z-courier.yaml` controls gateway routes, authentication, upstream
  adapters, downlink storage, cluster routes, and internal HTTP.
- `conf/zinx.json` controls the underlying Zinx TCP server settings.
- PostgreSQL, Redis, NSQ, Prometheus, and Grafana are still external
  dependencies. The gateway image does not package them.
- Production deployments should use PostgreSQL for durable downlink storage and
  Redis for cluster online routes.
- HMAC mode is recommended for backend internal HTTP and gateway peer push when
  requests cross a shared private network.

The reference gateway config uses:

- HTTP token verification:
  `auth.http.url: http://auth-backend:8080/gateway/auth/verify`
- HMAC for `/internal/*` APIs:
  `internal_http.auth.mode: hmac`
- PostgreSQL downlink storage:
  `downlink.storage.type: postgres`
- A disabled delivery-policy example:
  `downlink.policies[0].enabled: false`
- Terminal-event publication disabled by default:
  `downlink.terminal.publisher.type: none`
- Redis registry settings that are ready for cluster mode:
  `cluster.registry.type: redis`
- Admin console disabled by default:
  `admin_console.enabled: false`
- HTTP upstream for `MsgID 1001-1999`
- NSQ upstream for `MsgID 2000-2999`
- A basic per-client ingress rate limit

If you do not have an `auth-backend` service on the same Docker network, client
AUTH/BIND will fail. That is intentional: production token semantics should be
owned by your business backend or identity service. For quick local experiments,
use the development configs instead.

If you keep `production-http-upstream` enabled, add `business-backend` to the
same private network or change the route URL to your real backend address.

Before enabling the example policy, assign a reviewed MsgID range and make sure
it does not overlap another enabled policy. Terminal failures can be exported
to NSQ or to a signed HTTPS webhook. For HTTP, set
`ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET`, replace the example `nsq` block with
the commented `http` block, and keep `allow_insecure_http` disabled in
production. The receiver must verify `ZCOURIER-HMAC-SHA256` and de-duplicate by
stable `event_id`. Terminal events contain no business message body.

## Required Environment

Before production use, copy `deploy/production/.env.example` to
`deploy/production/.env` and replace every value:

| Variable | Purpose |
| --- | --- |
| `ZCOURIER_POSTGRES_PASSWORD` | PostgreSQL password and gateway DSN |
| `ZCOURIER_REDIS_PASSWORD` | Redis password and gateway registry password |
| `ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN` | Token sent to your auth backend |
| `ZCOURIER_INTERNAL_HMAC_SECRET` | Backend-to-gateway HMAC key |
| `ZCOURIER_PEER_HMAC_SECRET` | Gateway peer HMAC key |
| `ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET` | Optional outbound terminal-webhook HMAC key |
| `ZCOURIER_UPSTREAM_INTERNAL_TOKEN` | Optional HTTP upstream token |

Use different HMAC keys for backend internal HTTP, gateway peer push, and the
outbound terminal webhook. Do not reuse the example values.

## Verify

Render the Compose configuration:

```bash
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  config
```

Build the gateway image:

```bash
docker build -t z-courier-gateway:production .
```

Run the production reference smoke verifier:

```bash
bash scripts/production_smoke.sh
```

The script renders the Compose config with `.env.example`, builds and starts the
reference stack, checks gateway readiness and metrics from inside the Compose
network, verifies Prometheus can see the gateway target, and then removes the
stack and volumes. Set `PRODUCTION_SMOKE_KEEP_STACK=1` to keep the stack running
after the script exits.

If you started the stack manually, check Prometheus readiness:

```bash
curl http://127.0.0.1:9090/-/ready
```

The gateway metrics target is `gateway:18080` inside the Compose network.
Prometheus also loads the bundled Z-Courier recording and alert rules from
`deploy/monitoring/prometheus/rules/z-courier-alerts.yml`. Open
`Status -> Rules` in Prometheus to review active rules and firing alerts.

This reference stack evaluates alerts but does not send notifications. Connect
Alertmanager or your platform alerting system before using the rules for paging.

## CI Smoke

GitHub Actions builds the image with:

```bash
docker build --tag z-courier-gateway:ci .
```

The CI smoke check verifies that the gateway binary, default gateway config,
and default Zinx config are present in the image. It also runs the gateway help
command to prove the image entrypoint starts.

CI also runs:

```bash
bash scripts/production_smoke.sh
```

That production smoke check proves the production reference Compose stack can
boot with environment placeholders resolved and expose gateway metrics to
Prometheus.
