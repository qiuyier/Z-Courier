#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${ZCOURIER_CONSOLE_SMOKE_CONFIG:-$ROOT_DIR/configs/z-courier.console-smoke.yaml}"
ZINX_CONFIG_FILE="${ZCOURIER_CONSOLE_SMOKE_ZINX_CONFIG:-$ROOT_DIR/conf/zinx.console-smoke.json}"
INTERNAL_TOKEN="${ZCOURIER_CONSOLE_SMOKE_INTERNAL_TOKEN:-dev-internal-token}"
LOG_DIR="${ZCOURIER_CONSOLE_SMOKE_LOG_DIR:-$ROOT_DIR/log}"
GATEWAY_BIN="${ZCOURIER_CONSOLE_SMOKE_GATEWAY_BIN:-/tmp/z-courier-console-smoke-gateway}"

mkdir -p "$LOG_DIR"

cd "$ROOT_DIR"

if [[ "${ZCOURIER_CONSOLE_SMOKE_SKIP_BUILD:-0}" != "1" ]]; then
  npm run build --prefix web/admin
fi

go build -o "$GATEWAY_BIN" ./cmd/gateway

wait_http() {
  local name="$1"
  local url="$2"
  local gateway_pid="$3"

  echo "waiting for $name..."
  for attempt in $(seq 1 60); do
    if ! kill -0 "$gateway_pid" >/dev/null 2>&1; then
      echo "gateway exited while waiting for $name" >&2
      return 1
    fi
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "$name did not become ready in time" >&2
  return 1
}

stop_gateway() {
  local gateway_pid="$1"

  if kill -0 "$gateway_pid" >/dev/null 2>&1; then
    kill "$gateway_pid" >/dev/null 2>&1 || true
    wait "$gateway_pid" >/dev/null 2>&1 || true
  fi
}

run_role() {
  local role="$1"
  local internal_addr="$2"
  local log_file="$LOG_DIR/console-smoke-gateway-${role}.log"
  local gateway_pid=""
  local status=0

  echo "starting console smoke gateway for role=$role..."
  : >"$log_file"
  ZCOURIER_CONSOLE_SMOKE_ROLE="$role" \
    ZCOURIER_CONSOLE_SMOKE_INTERNAL_ADDR="$internal_addr" \
    ZCOURIER_CONSOLE_SMOKE_INTERNAL_TOKEN="$INTERNAL_TOKEN" \
    ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_FILE" \
    "$GATEWAY_BIN" -config "$CONFIG_FILE" >"$log_file" 2>&1 &
  gateway_pid="$!"

  if ! wait_http "gateway readiness ($role)" "http://$internal_addr/readyz" "$gateway_pid"; then
    tail -n 160 "$log_file" >&2 || true
    stop_gateway "$gateway_pid"
    return 1
  fi

  ZCOURIER_CONSOLE_BASE_URL="http://$internal_addr/console/" \
    ZCOURIER_CONSOLE_INTERNAL_TOKEN="$INTERNAL_TOKEN" \
    ZCOURIER_CONSOLE_EXPECTED_ROLE="$role" \
    npm run smoke --prefix web/admin || status=$?

  if [[ "$status" != "0" ]]; then
    tail -n 160 "$log_file" >&2 || true
  fi

  stop_gateway "$gateway_pid"
  return "$status"
}

run_role admin "${ZCOURIER_CONSOLE_SMOKE_ADMIN_ADDR:-127.0.0.1:18084}"
run_role readonly "${ZCOURIER_CONSOLE_SMOKE_READONLY_ADDR:-127.0.0.1:18085}"

echo "admin console smoke passed"
