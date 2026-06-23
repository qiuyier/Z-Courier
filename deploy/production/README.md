# Production Image

This directory documents the first production-oriented gateway image path for
Z-Courier. The image builds `cmd/gateway` as a static Linux binary and packages
the default `configs/` and `conf/` directories under `/app`.

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

For a real deployment, mount your own gateway and Zinx configuration files
rather than using the development defaults:

```bash
docker run --rm \
  -p 8999:8999 \
  -p 18080:18080 \
  -v "$PWD/configs/z-courier.yaml:/app/configs/z-courier.yaml:ro" \
  -v "$PWD/conf/zinx.json:/app/conf/zinx.json:ro" \
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

## CI Smoke

GitHub Actions builds the image with:

```bash
docker build --tag z-courier-gateway:ci .
```

The CI smoke check verifies that the gateway binary, default gateway config,
and default Zinx config are present in the image. It also runs the gateway help
command to prove the image entrypoint starts.
