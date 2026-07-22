#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NGINX_IMAGE="${ZCOURIER_EDGE_NGINX_IMAGE:-nginx:1.28-alpine}"
CADDY_IMAGE="${ZCOURIER_EDGE_CADDY_IMAGE:-caddy:2.11-alpine}"
INTERNAL_PORT="${ZCOURIER_EDGE_SMOKE_INTERNAL_PORT:-18086}"
NGINX_HTTPS_PORT="${ZCOURIER_EDGE_SMOKE_NGINX_HTTPS_PORT:-18443}"
NGINX_TCP_PORT="${ZCOURIER_EDGE_SMOKE_NGINX_TCP_PORT:-19922}"
CADDY_HTTPS_PORT="${ZCOURIER_EDGE_SMOKE_CADDY_HTTPS_PORT:-18444}"
MTLS_HTTPS_PORT="${ZCOURIER_EDGE_SMOKE_MTLS_HTTPS_PORT:-19443}"
INTERNAL_TOKEN="${ZCOURIER_EDGE_SMOKE_INTERNAL_TOKEN:-dev-internal-token}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-edge-smoke.XXXXXX")"
CERT_DIR="$TMP_DIR/certs"
GATEWAY_BIN="$TMP_DIR/z-courier-gateway"
GATEWAY_LOG="$ROOT_DIR/log/edge-proxy-smoke-gateway.log"
NGINX_CONTAINER="z-courier-edge-nginx-smoke-$$"
CADDY_CONTAINER="z-courier-edge-caddy-smoke-$$"
MTLS_CONTAINER="z-courier-edge-mtls-smoke-$$"
GATEWAY_PID=""

cleanup() {
  local status=$?

  if [[ "$status" != "0" ]]; then
    echo "edge proxy smoke failed; dumping diagnostics" >&2
    [[ -f "$GATEWAY_LOG" ]] && tail -n 160 "$GATEWAY_LOG" >&2 || true
    docker logs "$NGINX_CONTAINER" >&2 2>/dev/null || true
    docker logs "$CADDY_CONTAINER" >&2 2>/dev/null || true
    docker logs "$MTLS_CONTAINER" >&2 2>/dev/null || true
  fi
  docker container rm --force "$NGINX_CONTAINER" "$CADDY_CONTAINER" "$MTLS_CONTAINER" >/dev/null 2>&1 || true
  if [[ -n "$GATEWAY_PID" ]] && kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    kill "$GATEWAY_PID" >/dev/null 2>&1 || true
    wait "$GATEWAY_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

wait_until() {
  local name="$1"
  shift

  echo "waiting for $name..."
  for _ in $(seq 1 60); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "$name did not become ready in time" >&2
  return 1
}

https_ready() {
  local port="$1"
  local host="${2:-127.0.0.1}"
  curl --noproxy '*' --fail --silent --show-error \
    --cacert "$CERT_DIR/client/ca.crt" \
    --resolve "$host:$port:127.0.0.1" \
    "https://$host:$port/console/" >/dev/null
}

assert_public_denials() {
  local port="$1"
  local host="${2:-127.0.0.1}"
  local path status

  for path in /internal/push /internal/push/batch /internal/cluster/push /metrics /healthz /readyz; do
    status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' \
      --cacert "$CERT_DIR/client/ca.crt" \
      --resolve "$host:$port:127.0.0.1" \
      "https://$host:$port$path")"
    if [[ "$status" != "404" ]]; then
      echo "public edge path $path returned HTTP $status, want 404" >&2
      return 1
    fi
  done
}

require_cmd curl
require_cmd docker
require_cmd go
require_cmd npm
require_cmd openssl

cd "$ROOT_DIR"
mkdir -p log
: >"$GATEWAY_LOG"
bash scripts/generate_edge_test_certs.sh "$CERT_DIR" >/dev/null 2>&1

if [[ "${ZCOURIER_EDGE_SMOKE_SKIP_BUILD:-0}" != "1" ]]; then
  npm run build --prefix web/admin
fi
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "starting edge smoke gateway..."
ZCOURIER_CONSOLE_SMOKE_ROLE=admin \
  ZCOURIER_CONSOLE_SMOKE_INTERNAL_ADDR="0.0.0.0:$INTERNAL_PORT" \
  ZCOURIER_CONSOLE_SMOKE_INTERNAL_TOKEN="$INTERNAL_TOKEN" \
  ZINX_CONFIG_FILE_PATH="$ROOT_DIR/conf/zinx.edge-proxy-smoke.json" \
  "$GATEWAY_BIN" -config "$ROOT_DIR/configs/z-courier.console-smoke.yaml" >"$GATEWAY_LOG" 2>&1 &
GATEWAY_PID="$!"
wait_until "edge smoke gateway" curl --fail --silent "http://127.0.0.1:$INTERNAL_PORT/readyz"

echo "starting Nginx public edge..."
docker run --detach --name "$NGINX_CONTAINER" \
  --add-host host.docker.internal:host-gateway \
  --publish "127.0.0.1:$NGINX_HTTPS_PORT:8443" \
  --publish "127.0.0.1:$NGINX_TCP_PORT:8999" \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_GATEWAY_HOST=host.docker.internal \
  --env ZCOURIER_EDGE_GATEWAY_TCP_PORT=9922 \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_PORT="$INTERNAL_PORT" \
  --env ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" >/dev/null
wait_until "Nginx Console HTTPS" https_ready "$NGINX_HTTPS_PORT"

curl --noproxy '*' --silent --dump-header "$TMP_DIR/nginx-headers.txt" --output /dev/null \
  --cacert "$CERT_DIR/client/ca.crt" "https://127.0.0.1:$NGINX_HTTPS_PORT/console/"
rg -qi '^strict-transport-security: max-age=31536000' "$TMP_DIR/nginx-headers.txt"
rg -qi '^x-content-type-options: nosniff' "$TMP_DIR/nginx-headers.txt"
assert_public_denials "$NGINX_HTTPS_PORT"

echo "running Console browser flows through Nginx HTTPS..."
ZCOURIER_CONSOLE_BASE_URL="https://127.0.0.1:$NGINX_HTTPS_PORT/console/" \
  ZCOURIER_CONSOLE_INTERNAL_TOKEN="$INTERNAL_TOKEN" \
  ZCOURIER_CONSOLE_EXPECTED_ROLE=admin \
  ZCOURIER_CONSOLE_IGNORE_HTTPS_ERRORS=1 \
  npm run smoke --prefix web/admin

echo "running SDK AUTH/BIND through Nginx TCP TLS..."
go run ./cmd/devclient \
  -host 127.0.0.1 \
  -port "$NGINX_TCP_PORT" \
  -client-id console-smoke-client \
  -device-id edge-proxy-smoke \
  -token console-smoke-token \
  -tls \
  -tls-ca-file "$CERT_DIR/client/ca.crt" \
  -tls-server-name edge-proxy.test \
  -exit-after-bind

docker container rm --force "$NGINX_CONTAINER" >/dev/null

echo "starting Caddy Console HTTPS reference..."
docker run --detach --name "$CADDY_CONTAINER" \
  --add-host host.docker.internal:host-gateway \
  --publish "127.0.0.1:$CADDY_HTTPS_PORT:443" \
  --env ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_UPSTREAMS="host.docker.internal:$INTERNAL_PORT" \
  --volume "$ROOT_DIR/deploy/edge/caddy/Caddyfile.local:/etc/caddy/Caddyfile:ro" \
  --volume "$ROOT_DIR/deploy/edge/caddy/console.caddy:/etc/caddy/console.caddy:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$CADDY_IMAGE" >/dev/null
wait_until "Caddy Console HTTPS" https_ready "$CADDY_HTTPS_PORT" edge-proxy.test
curl --noproxy '*' --silent --dump-header "$TMP_DIR/caddy-headers.txt" --output /dev/null \
  --cacert "$CERT_DIR/client/ca.crt" \
  --resolve "edge-proxy.test:$CADDY_HTTPS_PORT:127.0.0.1" \
  "https://edge-proxy.test:$CADDY_HTTPS_PORT/console/"
rg -qi '^strict-transport-security: max-age=31536000' "$TMP_DIR/caddy-headers.txt"
rg -qi '^x-content-type-options: nosniff' "$TMP_DIR/caddy-headers.txt"
assert_public_denials "$CADDY_HTTPS_PORT" edge-proxy.test

login_status="$(curl --noproxy '*' --silent --output "$TMP_DIR/caddy-login.json" --write-out '%{http_code}' \
  --cacert "$CERT_DIR/client/ca.crt" \
  --resolve "edge-proxy.test:$CADDY_HTTPS_PORT:127.0.0.1" \
  --cookie-jar "$TMP_DIR/caddy-cookies.txt" \
  --header 'Content-Type: application/json' \
  --data "{\"token\":\"$INTERNAL_TOKEN\"}" \
  "https://edge-proxy.test:$CADDY_HTTPS_PORT/internal/admin/session/login")"
if [[ "$login_status" != "200" ]]; then
  echo "Caddy Console login returned HTTP $login_status" >&2
  exit 1
fi
overview_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' \
  --cacert "$CERT_DIR/client/ca.crt" \
  --resolve "edge-proxy.test:$CADDY_HTTPS_PORT:127.0.0.1" \
  --cookie "$TMP_DIR/caddy-cookies.txt" \
  "https://edge-proxy.test:$CADDY_HTTPS_PORT/internal/admin/overview")"
if [[ "$overview_status" != "200" ]]; then
  echo "Caddy Console overview returned HTTP $overview_status" >&2
  exit 1
fi

docker container rm --force "$CADDY_CONTAINER" >/dev/null

echo "starting separate Nginx private mTLS listener..."
docker run --detach --name "$MTLS_CONTAINER" \
  --add-host host.docker.internal:host-gateway \
  --publish "127.0.0.1:$MTLS_HTTPS_PORT:9443" \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_GATEWAY_HOST=host.docker.internal \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_PORT="$INTERNAL_PORT" \
  --env ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx-mtls.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" >/dev/null

wait_until "Nginx private mTLS listener" curl --noproxy '*' --fail --silent \
  --cacert "$CERT_DIR/client/ca.crt" \
  --cert "$CERT_DIR/client/tls.crt" \
  --key "$CERT_DIR/client/tls.key" \
  "https://127.0.0.1:$MTLS_HTTPS_PORT/healthz"
if curl --noproxy '*' --fail --silent \
  --cacert "$CERT_DIR/client/ca.crt" \
  "https://127.0.0.1:$MTLS_HTTPS_PORT/healthz" >/dev/null 2>&1; then
  echo "private mTLS listener accepted a request without a client certificate" >&2
  exit 1
fi
push_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' \
  --cacert "$CERT_DIR/client/ca.crt" \
  --cert "$CERT_DIR/client/tls.crt" \
  --key "$CERT_DIR/client/tls.key" \
  --header 'Content-Type: application/json' \
  --data '{}' \
  "https://127.0.0.1:$MTLS_HTTPS_PORT/internal/push")"
if [[ "$push_status" != "401" ]]; then
  echo "mTLS machine request returned HTTP $push_status, want gateway auth rejection 401" >&2
  exit 1
fi

echo "edge proxy smoke passed"
