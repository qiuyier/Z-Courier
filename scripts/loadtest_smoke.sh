#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/local/docker-compose.yml"
CONFIG_FILE="$ROOT_DIR/configs/z-courier.integration.yaml"
ZINX_CONFIG_FILE="$ROOT_DIR/conf/zinx.integration.json"
LOG_DIR="${LOADTEST_SMOKE_LOG_DIR:-$ROOT_DIR/log}"
REPORT_DIR="${LOADTEST_SMOKE_REPORT_DIR:-$ROOT_DIR/reports/loadtest-smoke}"

GATEWAY_PID=""
GATEWAY_BIN=""

cleanup() {
  if [[ -n "$GATEWAY_PID" ]] && kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    kill "$GATEWAY_PID" >/dev/null 2>&1 || true
    wait "$GATEWAY_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$GATEWAY_BIN" ]]; then
    rm -f "$GATEWAY_BIN"
  fi
}
trap cleanup EXIT

check_gateway_alive() {
  if [[ -n "$GATEWAY_PID" ]] && ! kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    echo "gateway exited unexpectedly" >&2
    tail -n 120 "$LOG_DIR/loadtest-smoke-gateway.log" >&2 || true
    exit 1
  fi
}

wait_http() {
  local name="$1"
  local url="$2"

  echo "waiting for $name..."
  for attempt in $(seq 1 60); do
    check_gateway_alive
    if curl -fsS "$url" >/dev/null 2>&1; then
      check_gateway_alive
      return 0
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

wait_postgres() {
  echo "waiting for postgres..."
  for attempt in $(seq 1 60); do
    if docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U zcourier -d zcourier >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "postgres did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

cd "$ROOT_DIR"
mkdir -p "$LOG_DIR" "$REPORT_DIR"
: >"$LOG_DIR/loadtest-smoke-gateway.log"

UPSTREAM_CLIENTS="${LOADTEST_SMOKE_UPSTREAM_CLIENTS:-10}"
UPSTREAM_MESSAGES="${LOADTEST_SMOKE_UPSTREAM_MESSAGES:-5}"
UPSTREAM_MIN_QPS="${LOADTEST_SMOKE_UPSTREAM_MIN_QPS:-1}"
UPSTREAM_MAX_P95_MS="${LOADTEST_SMOKE_UPSTREAM_MAX_P95_MS:-5000}"
UPSTREAM_MAX_P99_MS="${LOADTEST_SMOKE_UPSTREAM_MAX_P99_MS:-10000}"

DOWNLINK_CLIENTS="${LOADTEST_SMOKE_DOWNLINK_CLIENTS:-10}"
DOWNLINK_MESSAGES="${LOADTEST_SMOKE_DOWNLINK_MESSAGES:-5}"
DOWNLINK_CONCURRENCY="${LOADTEST_SMOKE_DOWNLINK_CONCURRENCY:-10}"
DOWNLINK_MIN_QPS="${LOADTEST_SMOKE_DOWNLINK_MIN_QPS:-1}"
DOWNLINK_MAX_P95_MS="${LOADTEST_SMOKE_DOWNLINK_MAX_P95_MS:-5000}"
DOWNLINK_MAX_P99_MS="${LOADTEST_SMOKE_DOWNLINK_MAX_P99_MS:-10000}"

BODY_SIZE="${LOADTEST_SMOKE_BODY_SIZE:-64}"
TIMEOUT="${LOADTEST_SMOKE_TIMEOUT:-30s}"
MAX_ERROR_RATE="${LOADTEST_SMOKE_MAX_ERROR_RATE:-0}"

docker compose -f "$COMPOSE_FILE" up -d postgres nsqlookupd nsqd
wait_postgres
wait_http "nsqd" "http://127.0.0.1:14151/ping"

GATEWAY_BIN="${TMPDIR:-/tmp}/z-courier-loadtest-smoke-gateway-$$"
echo "building gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "starting gateway..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_FILE" "$GATEWAY_BIN" -config "$CONFIG_FILE" >"$LOG_DIR/loadtest-smoke-gateway.log" 2>&1 &
GATEWAY_PID="$!"

wait_http "gateway readiness" "http://127.0.0.1:18082/readyz"

echo "running upstream smoke..."
go run ./cmd/loadtest \
  -mode upstream \
  -host 127.0.0.1 \
  -port 9899 \
  -token e2e-token \
  -clients "$UPSTREAM_CLIENTS" \
  -messages "$UPSTREAM_MESSAGES" \
  -upstream-msg-id 2001 \
  -body-size "$BODY_SIZE" \
  -timeout "$TIMEOUT" \
  -min-qps "$UPSTREAM_MIN_QPS" \
  -max-p95-ms "$UPSTREAM_MAX_P95_MS" \
  -max-p99-ms "$UPSTREAM_MAX_P99_MS" \
  -max-error-rate "$MAX_ERROR_RATE" \
  -report "$REPORT_DIR/upstream.json"

echo "running downlink smoke..."
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18082 \
  -internal-token dev-internal-token \
  -clients "$DOWNLINK_CLIENTS" \
  -messages "$DOWNLINK_MESSAGES" \
  -http-concurrency "$DOWNLINK_CONCURRENCY" \
  -body-size "$BODY_SIZE" \
  -timeout "$TIMEOUT" \
  -min-qps "$DOWNLINK_MIN_QPS" \
  -max-p95-ms "$DOWNLINK_MAX_P95_MS" \
  -max-p99-ms "$DOWNLINK_MAX_P99_MS" \
  -max-error-rate "$MAX_ERROR_RATE" \
  -report "$REPORT_DIR/downlink.json"

echo "loadtest smoke passed"
echo "reports:"
echo "  $REPORT_DIR/upstream.json"
echo "  $REPORT_DIR/downlink.json"
