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

assert_metric_series() {
  local url="$1"
  local metric="$2"
  shift 2

  local body
  body="$(curl -fsS "$url")"
  local line
  while IFS= read -r line; do
    if [[ "$line" != "$metric{"* && "$line" != "$metric "* ]]; then
      continue
    fi
    local matched=true
    local expected
    for expected in "$@"; do
      if [[ "$line" != *"$expected"* ]]; then
        matched=false
        break
      fi
    done
    if [[ "$matched" == true ]]; then
      return 0
    fi
  done <<<"$body"

  echo "metric $metric missing expected fragments: $*" >&2
  return 1
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

echo "checking traffic policy admission metrics..."
METRICS_URL="http://127.0.0.1:18201/metrics"
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_selection_total \
  'mode="local"' 'policy="standard"' 'result="selected"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_selection_total \
  'mode="local"' 'policy="none"' 'result="no_match"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_quota_store_total \
  'key_scope="client_id"' 'mode="local"' 'policy="standard"' 'result="allowed"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_quota_store_total \
  'mode="local"' 'result="rate_limited"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_quota_store_total \
  'mode="local"' 'result="overloaded"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_quota_store_duration_seconds_count \
  'mode="local"' 'result="allowed"'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_local_keys \
  'mode="local"' '} 1'
assert_metric_series "$METRICS_URL" \
  z_courier_traffic_policy_local_key_limit \
  'mode="local"' '} 2'

echo "checking traffic policy diagnostics..."
DIAGNOSTICS="$(
  curl -fsS \
    -H 'X-ZCourier-Internal-Token: traffic-policy-internal-token' \
    http://127.0.0.1:18201/internal/admin/diagnostics
)"
jq -e '
  .traffic_policy.enabled == true and
  .traffic_policy.mode == "local" and
  .traffic_policy.store_status == "configured" and
  .traffic_policy.policy_count == 2 and
  .traffic_policy.key_scope == "client_id" and
  .traffic_policy.no_match_total >= 1 and
  .traffic_policy.decisions.allowed >= 1 and
  .traffic_policy.decisions.rate_limited >= 1 and
  .traffic_policy.decisions.overloaded >= 1 and
  .traffic_policy.local.live_keys == 1 and
  .traffic_policy.local.max_keys == 2 and
  .traffic_policy.local.utilization == 0.5 and
  any(.dependencies[]; .name == "traffic_policy_store" and .status == "configured")
' <<<"$DIAGNOSTICS" >/dev/null

DIAGNOSE="$(
  curl -fsS \
    -H 'X-ZCourier-Internal-Token: traffic-policy-internal-token' \
    http://127.0.0.1:18201/internal/admin/diagnose
)"
jq -e '
  .sections.diagnostics.body.traffic_policy.mode == "local" and
  .sections.diagnostics.body.traffic_policy.local.live_keys == 1
' <<<"$DIAGNOSE" >/dev/null
