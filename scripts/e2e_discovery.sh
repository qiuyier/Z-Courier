#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATEWAY_BIN="$(mktemp "${TMPDIR:-/tmp}/z-courier-discovery-e2e-gateway.XXXXXX")"

cleanup() {
  rm -f "$GATEWAY_BIN"
}
trap cleanup EXIT

require_port_free() {
  local port="$1"
  if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
    echo "discovery E2E port $port is already in use" >&2
    return 1
  fi
}

cd "$ROOT_DIR"
for port in 9931 18191 18192 18193; do
  require_port_free "$port"
done

echo "building discovery E2E gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "running two-upstream discovery integration verifier..."
go run ./cmd/discoverye2e \
  -gateway-bin "$GATEWAY_BIN" \
  "$@"
