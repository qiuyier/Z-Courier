#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/local/docker-compose.yml"
CONFIG_FILE="$ROOT_DIR/configs/z-courier.integration.yaml"
ZINX_CONFIG_FILE="$ROOT_DIR/conf/zinx.integration.json"
LOG_DIR="${LOADTEST_LOG_DIR:-$ROOT_DIR/log}"
REPORT_DIR="${LOADTEST_REPORT_DIR:-$ROOT_DIR/reports/loadtest-manual}"

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
    tail -n 120 "$LOG_DIR/loadtest-manual-gateway.log" >&2 || true
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
: >"$LOG_DIR/loadtest-manual-gateway.log"

MODE="${LOADTEST_MODE:-downlink}"
CLIENTS="${LOADTEST_CLIENTS:-100}"
MESSAGES="${LOADTEST_MESSAGES:-10}"
DURATION="${LOADTEST_DURATION:-60s}"
RATE="${LOADTEST_RATE:-100}"
HTTP_CONCURRENCY="${LOADTEST_HTTP_CONCURRENCY:-50}"
BODY_SIZE="${LOADTEST_BODY_SIZE:-128}"
TIMEOUT="${LOADTEST_TIMEOUT:-30s}"
MIN_QPS="${LOADTEST_MIN_QPS:-1}"
MAX_P95_MS="${LOADTEST_MAX_P95_MS:-5000}"
MAX_P99_MS="${LOADTEST_MAX_P99_MS:-10000}"
MAX_ERROR_RATE="${LOADTEST_MAX_ERROR_RATE:-0.01}"

case "$MODE" in
  upstream | downlink)
    ;;
  *)
    echo "LOADTEST_MODE must be upstream or downlink, got: $MODE" >&2
    exit 2
    ;;
esac

docker compose -f "$COMPOSE_FILE" up -d postgres nsqlookupd nsqd
wait_postgres
wait_http "nsqd" "http://127.0.0.1:14151/ping"

GATEWAY_BIN="${TMPDIR:-/tmp}/z-courier-loadtest-manual-gateway-$$"
echo "building gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "starting gateway..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_FILE" "$GATEWAY_BIN" -config "$CONFIG_FILE" >"$LOG_DIR/loadtest-manual-gateway.log" 2>&1 &
GATEWAY_PID="$!"

wait_http "gateway readiness" "http://127.0.0.1:18082/readyz"

echo "running manual $MODE load test..."
command=(
  go run ./cmd/loadtest
  -mode "$MODE"
  -host 127.0.0.1
  -port 9899
  -internal-url http://127.0.0.1:18082
  -internal-token dev-internal-token
  -token e2e-token
  -clients "$CLIENTS"
  -messages "$MESSAGES"
  -duration "$DURATION"
  -rate "$RATE"
  -body-size "$BODY_SIZE"
  -timeout "$TIMEOUT"
  -min-qps "$MIN_QPS"
  -max-p95-ms "$MAX_P95_MS"
  -max-p99-ms "$MAX_P99_MS"
  -max-error-rate "$MAX_ERROR_RATE"
  -report "$REPORT_DIR/$MODE.json"
)

if [[ "$MODE" == "upstream" ]]; then
  command+=(-upstream-msg-id 2001)
else
  command+=(-downlink-msg-id 2001 -http-concurrency "$HTTP_CONCURRENCY")
fi

"${command[@]}"

echo "manual load test passed"
echo "report:"
echo "  $REPORT_DIR/$MODE.json"
