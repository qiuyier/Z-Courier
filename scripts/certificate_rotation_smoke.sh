#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NGINX_IMAGE="${ZCOURIER_EDGE_NGINX_IMAGE:-nginx:1.28-alpine}"
INTERNAL_PORT="${ZCOURIER_CERT_ROTATION_INTERNAL_PORT:-18087}"
MTLS_PORT="${ZCOURIER_CERT_ROTATION_MTLS_PORT:-19444}"
INTERNAL_TOKEN="${ZCOURIER_CERT_ROTATION_INTERNAL_TOKEN:-dev-internal-token}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-cert-rotation.XXXXXX")"
OLD_CERT_DIR="$TMP_DIR/old"
NEW_CERT_DIR="$TMP_DIR/new"
ACTIVE_CERT_DIR="$TMP_DIR/active"
TRUST_DIR="$TMP_DIR/trust"
GATEWAY_BIN="$TMP_DIR/z-courier-gateway"
GATEWAY_LOG="$ROOT_DIR/log/certificate-rotation-smoke-gateway.log"
NGINX_CONTAINER="z-courier-certificate-rotation-$$"
GATEWAY_PID=""

cleanup() {
  local status=$?

  if [[ "$status" != "0" ]]; then
    echo "certificate rotation smoke failed; dumping diagnostics" >&2
    [[ -f "$GATEWAY_LOG" ]] && tail -n 160 "$GATEWAY_LOG" >&2 || true
    docker logs "$NGINX_CONTAINER" >&2 2>/dev/null || true
  fi
  docker container rm --force "$NGINX_CONTAINER" >/dev/null 2>&1 || true
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

reload_nginx() {
  docker exec "$NGINX_CONTAINER" nginx -s reload >/dev/null
  # Nginx reload is asynchronous; allow new workers to take over the listener.
  sleep 1
}

assert_mtls_ok() {
  local trust_file="$1"
  local client_dir="$2"

  curl --noproxy '*' --fail --silent --show-error \
    --cacert "$trust_file" \
    --cert "$client_dir/tls.crt" \
    --key "$client_dir/tls.key" \
    "https://127.0.0.1:$MTLS_PORT/healthz" >/dev/null
}

assert_mtls_rejected() {
  local trust_file="$1"
  local client_dir="$2"

  if curl --noproxy '*' --fail --silent --show-error \
    --cacert "$trust_file" \
    --cert "$client_dir/tls.crt" \
    --key "$client_dir/tls.key" \
    "https://127.0.0.1:$MTLS_PORT/healthz" >/dev/null 2>&1; then
    echo "mTLS request unexpectedly succeeded" >&2
    return 1
  fi
}

install_server_material() {
  local source_dir="$1"
  cp "$source_dir/server/tls.crt" "$ACTIVE_CERT_DIR/server/tls.crt"
  cp "$source_dir/server/tls.key" "$ACTIVE_CERT_DIR/server/tls.key"
}

require_cmd curl
require_cmd docker
require_cmd go
require_cmd openssl

cd "$ROOT_DIR"
mkdir -p log "$ACTIVE_CERT_DIR/server" "$TRUST_DIR"
: >"$GATEWAY_LOG"
bash scripts/generate_edge_test_certs.sh "$OLD_CERT_DIR" >/dev/null 2>&1
bash scripts/generate_edge_test_certs.sh "$NEW_CERT_DIR" >/dev/null 2>&1

cat "$OLD_CERT_DIR/client/ca.crt" "$NEW_CERT_DIR/client/ca.crt" >"$TRUST_DIR/server-overlap-ca.crt"
cat "$OLD_CERT_DIR/server/client-ca.crt" "$NEW_CERT_DIR/server/client-ca.crt" >"$TRUST_DIR/client-overlap-ca.crt"
cp "$OLD_CERT_DIR/client/ca.crt" "$TRUST_DIR/server-old-ca.crt"
cp "$NEW_CERT_DIR/client/ca.crt" "$TRUST_DIR/server-new-ca.crt"
cp "$OLD_CERT_DIR/server/client-ca.crt" "$ACTIVE_CERT_DIR/server/client-ca.crt"
install_server_material "$OLD_CERT_DIR"
chmod 600 "$ACTIVE_CERT_DIR/server/tls.key"

go build -o "$GATEWAY_BIN" ./cmd/gateway
echo "starting certificate rotation smoke gateway..."
ZCOURIER_CONSOLE_SMOKE_ROLE=admin \
  ZCOURIER_CONSOLE_SMOKE_INTERNAL_ADDR="0.0.0.0:$INTERNAL_PORT" \
  ZCOURIER_CONSOLE_SMOKE_INTERNAL_TOKEN="$INTERNAL_TOKEN" \
  ZINX_CONFIG_FILE_PATH="$ROOT_DIR/conf/zinx.edge-proxy-smoke.json" \
  "$GATEWAY_BIN" -config "$ROOT_DIR/configs/z-courier.console-smoke.yaml" >"$GATEWAY_LOG" 2>&1 &
GATEWAY_PID="$!"
wait_until "certificate rotation smoke gateway" curl --fail --silent "http://127.0.0.1:$INTERNAL_PORT/readyz"

echo "starting Nginx private mTLS listener with old certificate..."
docker run --detach --name "$NGINX_CONTAINER" \
  --add-host host.docker.internal:host-gateway \
  --publish "127.0.0.1:$MTLS_PORT:9443" \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_GATEWAY_HOST=host.docker.internal \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_PORT="$INTERNAL_PORT" \
  --env ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx-mtls.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$ACTIVE_CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" >/dev/null

wait_until "Nginx old-certificate mTLS listener" \
  assert_mtls_ok "$TRUST_DIR/server-old-ca.crt" "$OLD_CERT_DIR/client"

echo "staging overlapping client certificate trust..."
cp "$TRUST_DIR/client-overlap-ca.crt" "$ACTIVE_CERT_DIR/server/client-ca.crt"
reload_nginx
assert_mtls_ok "$TRUST_DIR/server-overlap-ca.crt" "$OLD_CERT_DIR/client"
assert_mtls_ok "$TRUST_DIR/server-overlap-ca.crt" "$NEW_CERT_DIR/client"

echo "rotating server certificate with overlapping trust..."
install_server_material "$NEW_CERT_DIR"
reload_nginx
assert_mtls_ok "$TRUST_DIR/server-overlap-ca.crt" "$OLD_CERT_DIR/client"
assert_mtls_ok "$TRUST_DIR/server-overlap-ca.crt" "$NEW_CERT_DIR/client"

echo "retiring old certificate trust..."
cp "$NEW_CERT_DIR/server/client-ca.crt" "$ACTIVE_CERT_DIR/server/client-ca.crt"
reload_nginx
assert_mtls_ok "$TRUST_DIR/server-new-ca.crt" "$NEW_CERT_DIR/client"
assert_mtls_rejected "$TRUST_DIR/server-overlap-ca.crt" "$OLD_CERT_DIR/client"

echo "rolling back to old certificate and trust..."
install_server_material "$OLD_CERT_DIR"
cp "$OLD_CERT_DIR/server/client-ca.crt" "$ACTIVE_CERT_DIR/server/client-ca.crt"
reload_nginx
assert_mtls_ok "$TRUST_DIR/server-old-ca.crt" "$OLD_CERT_DIR/client"

echo "certificate rotation smoke passed"
