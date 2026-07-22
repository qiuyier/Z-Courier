#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NGINX_IMAGE="${ZCOURIER_EDGE_NGINX_IMAGE:-nginx:1.28-alpine}"
CADDY_IMAGE="${ZCOURIER_EDGE_CADDY_IMAGE:-caddy:2.11-alpine}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-edge-check.XXXXXX")"
CERT_DIR="$TMP_DIR/certs"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd docker
require_cmd openssl

cd "$ROOT_DIR"
bash scripts/generate_edge_test_certs.sh "$CERT_DIR" >/dev/null 2>&1

echo "rendering edge Compose references..."
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx.yml \
  config >"$TMP_DIR/production-nginx.yaml"
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-caddy.yml \
  config >"$TMP_DIR/production-caddy.yaml"
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-caddy.yml \
  -f deploy/production/docker-compose.edge-caddy-local.yml \
  config >"$TMP_DIR/production-caddy-local.yaml"
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx-mtls.yml \
  config >"$TMP_DIR/production-nginx-mtls.yaml"
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.edge-nginx.yml \
  config >"$TMP_DIR/cluster-nginx.yaml"
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.edge-caddy.yml \
  config >"$TMP_DIR/cluster-caddy.yaml"

if awk '
  /^  gateway:$/ { in_gateway=1; next }
  in_gateway && /^  [^ ]/ { in_gateway=0 }
  in_gateway && /published:/ { found=1 }
  END { exit found ? 0 : 1 }
' "$TMP_DIR/production-nginx.yaml"; then
  echo "Nginx edge Compose unexpectedly publishes the plaintext gateway service" >&2
  exit 1
fi
grep -Eq 'host_ip: 127\.0\.0\.1' "$TMP_DIR/production-caddy.yaml"
grep -Eq 'ZCOURIER_ADMIN_CONSOLE_ENABLED: "true"' "$TMP_DIR/production-nginx.yaml"
grep -Eq 'ZCOURIER_ADMIN_SESSION_ENABLED: "true"' "$TMP_DIR/cluster-nginx.yaml"

echo "validating Nginx edge configuration..."
docker run --rm \
  --add-host gateway:127.0.0.1 \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_GATEWAY_HOST=gateway \
  --env ZCOURIER_EDGE_GATEWAY_TCP_PORT=8999 \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_PORT=18080 \
  --env ZCOURIER_EDGE_SERVER_NAME=console.example.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" nginx -t

echo "validating Nginx cluster configuration..."
docker run --rm \
  --add-host gateway-a:127.0.0.1 \
  --add-host gateway-b:127.0.0.1 \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_SERVER_NAME=console.example.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx-cluster.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" nginx -t

echo "validating Nginx private mTLS configuration..."
docker run --rm \
  --add-host gateway:127.0.0.1 \
  --env NGINX_ENVSUBST_OUTPUT_DIR=/etc/nginx \
  --env ZCOURIER_EDGE_GATEWAY_HOST=gateway \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_PORT=18080 \
  --env ZCOURIER_EDGE_SERVER_NAME=console.example.test \
  --volume "$ROOT_DIR/deploy/edge/nginx/nginx-mtls.conf.template:/etc/nginx/templates/nginx.conf.template:ro" \
  --volume "$ROOT_DIR/deploy/edge/nginx/includes:/etc/nginx/zcourier:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$NGINX_IMAGE" nginx -t

echo "validating Caddy automatic and local-certificate configurations..."
docker run --rm \
  --env ZCOURIER_EDGE_CONSOLE_SITE=https://console.example.com \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_UPSTREAMS=gateway:18080 \
  --volume "$ROOT_DIR/deploy/edge/caddy/Caddyfile:/etc/caddy/Caddyfile:ro" \
  --volume "$ROOT_DIR/deploy/edge/caddy/console.caddy:/etc/caddy/console.caddy:ro" \
  "$CADDY_IMAGE" caddy validate --config /etc/caddy/Caddyfile
docker run --rm \
  --env ZCOURIER_EDGE_CONSOLE_SITE=https://console.example.com \
  --env "ZCOURIER_EDGE_GATEWAY_HTTP_UPSTREAMS=gateway-a:18080 gateway-b:18080" \
  --volume "$ROOT_DIR/deploy/edge/caddy/Caddyfile:/etc/caddy/Caddyfile:ro" \
  --volume "$ROOT_DIR/deploy/edge/caddy/console.caddy:/etc/caddy/console.caddy:ro" \
  "$CADDY_IMAGE" caddy validate --config /etc/caddy/Caddyfile
docker run --rm \
  --env ZCOURIER_EDGE_SERVER_NAME=console.example.test \
  --env ZCOURIER_EDGE_GATEWAY_HTTP_UPSTREAMS=gateway:18080 \
  --volume "$ROOT_DIR/deploy/edge/caddy/Caddyfile.local:/etc/caddy/Caddyfile:ro" \
  --volume "$ROOT_DIR/deploy/edge/caddy/console.caddy:/etc/caddy/console.caddy:ro" \
  --volume "$CERT_DIR/server:/run/secrets/z-courier-edge:ro" \
  "$CADDY_IMAGE" caddy validate --config /etc/caddy/Caddyfile

committed_pem_files="$(grep -RIlE 'BEGIN (RSA |EC )?PRIVATE KEY|BEGIN CERTIFICATE' deploy/edge 2>/dev/null || true)"
if [[ -n "$committed_pem_files" ]]; then
  echo "committed edge reference contains certificate or private-key PEM data" >&2
  exit 1
fi
if [[ -n "$(find "$CERT_DIR/server" -maxdepth 1 -type f -name '*ca.key' -print -quit)" ]]; then
  echo "edge runtime directory unexpectedly contains a CA private key" >&2
  exit 1
fi

echo "edge proxy static checks passed"
