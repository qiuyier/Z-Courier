#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATEWAY_BIN="$(mktemp "${TMPDIR:-/tmp}/z-courier-route-reload-e2e-gateway.XXXXXX")"

cleanup() {
  rm -f "$GATEWAY_BIN"
}
trap cleanup EXIT

require_port_free() {
  local port="$1"
  if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
    echo "route reload E2E port $port is already in use" >&2
    return 1
  fi
}

cd "$ROOT_DIR"
for port in 9961 18221 18222 18223; do
  require_port_free "$port"
done

echo "building route reload E2E gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "running real-TCP route reload integration verifier..."
go run ./cmd/routereloade2e \
  -gateway-bin "$GATEWAY_BIN" \
  "$@"
