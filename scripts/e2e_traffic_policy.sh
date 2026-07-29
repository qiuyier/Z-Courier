#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="$ROOT_DIR/configs/z-courier.traffic-policy-e2e.yaml"
ZINX_CONFIG_FILE="$ROOT_DIR/conf/zinx.traffic-policy-e2e.json"
GATEWAY_LOG="$ROOT_DIR/log/e2e-traffic-policy-gateway.log"
GATEWAY_BIN="$(mktemp "${TMPDIR:-/tmp}/z-courier-traffic-policy-e2e-gateway.XXXXXX")"
GATEWAY_PID=""

cleanup() {
  local status=$?
  trap - EXIT

  if [[ -n "$GATEWAY_PID" ]] && kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    kill "$GATEWAY_PID" >/dev/null 2>&1 || true
    wait "$GATEWAY_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$status" -ne 0 && -f "$GATEWAY_LOG" ]]; then
    echo "--- traffic policy gateway log ---" >&2
    tail -n 200 "$GATEWAY_LOG" >&2 || true
  fi
  rm -f "$GATEWAY_BIN"
  exit "$status"
}
trap cleanup EXIT

require_port_free() {
  local port="$1"
  if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
    echo "traffic policy E2E port $port is already in use" >&2
    return 1
  fi
}

check_gateway_alive() {
  if [[ -n "$GATEWAY_PID" ]] && ! kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    echo "traffic policy E2E gateway exited unexpectedly" >&2
    return 1
  fi
}

wait_gateway() {
  echo "waiting for traffic policy gateway..."
  for attempt in $(seq 1 100); do
    check_gateway_alive
    if curl -fsS http://127.0.0.1:18201/readyz >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$attempt" == "100" ]]; then
      echo "traffic policy gateway did not become ready in time" >&2
      return 1
    fi
    sleep 0.1
  done
}

cd "$ROOT_DIR"
mkdir -p "$ROOT_DIR/log"
for port in 9941 18201 18202; do
  require_port_free "$port"
done

echo "building traffic policy E2E gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "starting traffic policy E2E gateway..."
: >"$GATEWAY_LOG"
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_FILE" \
  "$GATEWAY_BIN" -config "$CONFIG_FILE" >"$GATEWAY_LOG" 2>&1 &
GATEWAY_PID="$!"
wait_gateway

echo "running traffic policy integration verifier..."
go run ./cmd/trafficpolicye2e "$@"
