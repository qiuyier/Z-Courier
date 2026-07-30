#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_A_SOURCE="$ROOT_DIR/configs/z-courier.traffic-policy-redis-a.yaml"
CONFIG_B_SOURCE="$ROOT_DIR/configs/z-courier.traffic-policy-redis-b.yaml"
ZINX_CONFIG_A="$ROOT_DIR/conf/zinx.traffic-policy-redis-a.json"
ZINX_CONFIG_B="$ROOT_DIR/conf/zinx.traffic-policy-redis-b.json"
LOG_DIR="$ROOT_DIR/log"
RUN_ID="$(date +%s)-$$"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-traffic-policy-redis-e2e.XXXXXX")"
CONFIG_A="$TMP_DIR/gateway-a.yaml"
CONFIG_B="$TMP_DIR/gateway-b.yaml"
GATEWAY_BIN="$TMP_DIR/gateway"
REDIS_IMAGE="${ZCOURIER_TRAFFIC_POLICY_REDIS_E2E_IMAGE:-redis:8-alpine}"
REDIS_CONTAINER="z-courier-traffic-policy-redis-e2e-$RUN_ID"

GATEWAY_A_PID=""
GATEWAY_B_PID=""

cleanup() {
  local status=$?
  trap - EXIT

  for pid in "$GATEWAY_A_PID" "$GATEWAY_B_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    for log_file in \
      "$LOG_DIR/e2e-traffic-policy-redis-gateway-a.log" \
      "$LOG_DIR/e2e-traffic-policy-redis-gateway-b.log"; do
      if [[ -f "$log_file" ]]; then
        echo "--- $(basename "$log_file") ---" >&2
        tail -n 160 "$log_file" >&2 || true
      fi
    done
    docker logs "$REDIS_CONTAINER" >&2 || true
  fi
  docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
  exit "$status"
}
trap cleanup EXIT

require_port_free() {
  local port="$1"
  if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
    echo "Redis traffic policy E2E port $port is already in use" >&2
    return 1
  fi
}

check_gateways_alive() {
  if [[ -n "$GATEWAY_A_PID" ]] && ! kill -0 "$GATEWAY_A_PID" >/dev/null 2>&1; then
    echo "Redis traffic policy gateway-a exited unexpectedly" >&2
    return 1
  fi
  if [[ -n "$GATEWAY_B_PID" ]] && ! kill -0 "$GATEWAY_B_PID" >/dev/null 2>&1; then
    echo "Redis traffic policy gateway-b exited unexpectedly" >&2
    return 1
  fi
}

wait_gateway() {
  local name="$1"
  local url="$2"

  echo "waiting for $name..."
  for attempt in $(seq 1 100); do
    check_gateways_alive
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$attempt" == "100" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 0.1
  done
}

wait_redis() {
  echo "waiting for dedicated Redis..."
  for attempt in $(seq 1 100); do
    if docker exec "$REDIS_CONTAINER" redis-cli ping 2>/dev/null | grep -q PONG; then
      return 0
    fi
    if [[ "$attempt" == "100" ]]; then
      echo "dedicated Redis did not become ready in time" >&2
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

start_redis() {
  docker run \
    --detach \
    --rm \
    --name "$REDIS_CONTAINER" \
    --publish 127.0.0.1:16389:6379 \
    "$REDIS_IMAGE" \
    redis-server --save "" --appendonly no >/dev/null
  wait_redis
}

prepare_config() {
  local source="$1"
  local destination="$2"

  sed \
    -e "s/idle_ttl: 30s/idle_ttl: 2s/" \
    -e "s/key_prefix: zcourier:e2e:traffic-policy/key_prefix: zcourier:e2e:traffic-policy:$RUN_ID/" \
    "$source" >"$destination"
}

verify_redis_key_expiration() {
  local pattern="zcourier:e2e:traffic-policy:$RUN_ID:*"
  local key
  local ttl

  key="$(
    docker exec "$REDIS_CONTAINER" redis-cli --scan --pattern "$pattern" |
      tr -d '\r'
  )"
  if [[ -z "$key" || "$key" == *$'\n'* ]]; then
    echo "expected exactly one bounded Redis quota key, got: $key" >&2
    return 1
  fi

  ttl="$(docker exec "$REDIS_CONTAINER" redis-cli pttl "$key" | tr -d '\r')"
  if [[ ! "$ttl" =~ ^[0-9]+$ ]] || ((ttl <= 0 || ttl > 2000)); then
    echo "Redis quota key PTTL = $ttl, want within (0, 2000] ms" >&2
    return 1
  fi

  echo "waiting for Redis traffic policy key to expire..."
  for attempt in $(seq 1 40); do
    if [[ "$(docker exec "$REDIS_CONTAINER" redis-cli exists "$key" | tr -d '\r')" == "0" ]]; then
      return 0
    fi
    if [[ "$attempt" == "40" ]]; then
      echo "Redis traffic policy key did not expire after its idle TTL" >&2
      return 1
    fi
    sleep 0.1
  done
}

cd "$ROOT_DIR"
mkdir -p "$LOG_DIR"
for port in 9951 9952 18211 18212 18213 16389; do
  require_port_free "$port"
done

prepare_config "$CONFIG_A_SOURCE" "$CONFIG_A"
prepare_config "$CONFIG_B_SOURCE" "$CONFIG_B"

echo "starting dedicated Redis..."
start_redis

echo "building Redis traffic policy E2E gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

: >"$LOG_DIR/e2e-traffic-policy-redis-gateway-a.log"
: >"$LOG_DIR/e2e-traffic-policy-redis-gateway-b.log"

echo "starting Redis traffic policy gateway-a..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_A" \
  "$GATEWAY_BIN" -config "$CONFIG_A" \
  >"$LOG_DIR/e2e-traffic-policy-redis-gateway-a.log" 2>&1 &
GATEWAY_A_PID="$!"

echo "starting Redis traffic policy gateway-b..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_B" \
  "$GATEWAY_BIN" -config "$CONFIG_B" \
  >"$LOG_DIR/e2e-traffic-policy-redis-gateway-b.log" 2>&1 &
GATEWAY_B_PID="$!"

wait_gateway "Redis traffic policy gateway-a" "http://127.0.0.1:18211/readyz"
wait_gateway "Redis traffic policy gateway-b" "http://127.0.0.1:18213/readyz"

go run ./cmd/trafficpolicye2e \
  -mode redis-shared \
  -gateway-address 127.0.0.1:9951 \
  -gateway-b-address 127.0.0.1:9952 \
  -backend-address 127.0.0.1:18212 \
  -timeout 15s

echo "stopping dedicated Redis to verify fail-closed admission..."
docker stop --time 3 "$REDIS_CONTAINER" >/dev/null
for attempt in $(seq 1 50); do
  if ! docker inspect "$REDIS_CONTAINER" >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == "50" ]]; then
    echo "dedicated Redis container was not removed in time" >&2
    exit 1
  fi
  sleep 0.1
done
check_gateways_alive

go run ./cmd/trafficpolicye2e \
  -mode redis-unavailable \
  -gateway-address 127.0.0.1:9951 \
  -backend-address 127.0.0.1:18212 \
  -timeout 15s

echo "restarting dedicated Redis to verify recovery..."
start_redis
check_gateways_alive

go run ./cmd/trafficpolicye2e \
  -mode redis-recovered \
  -gateway-address 127.0.0.1:9952 \
  -backend-address 127.0.0.1:18212 \
  -timeout 15s

check_gateways_alive
verify_redis_key_expiration

echo "checking Redis traffic policy admission metrics..."
METRICS_A_URL="http://127.0.0.1:18211/metrics"
METRICS_B_URL="http://127.0.0.1:18213/metrics"
assert_metric_series "$METRICS_A_URL" \
  z_courier_traffic_policy_selection_total \
  'mode="redis"' 'policy="shared-upstream"' 'result="selected"'
assert_metric_series "$METRICS_A_URL" \
  z_courier_traffic_policy_quota_store_total \
  'mode="redis"' 'policy="shared-upstream"' 'result="allowed"'
assert_metric_series "$METRICS_A_URL" \
  z_courier_traffic_policy_quota_store_total \
  'mode="redis"' 'result="admission_unavailable"'
assert_metric_series "$METRICS_B_URL" \
  z_courier_traffic_policy_quota_store_total \
  'mode="redis"' 'result="rate_limited"'
assert_metric_series "$METRICS_B_URL" \
  z_courier_traffic_policy_quota_store_duration_seconds_count \
  'mode="redis"' 'result="allowed"'

echo "checking Redis traffic policy diagnostics..."
DIAGNOSTICS_A="$(
  curl -fsS \
    -H 'X-ZCourier-Internal-Token: traffic-policy-redis-internal-token' \
    http://127.0.0.1:18211/internal/admin/diagnostics
)"
DIAGNOSTICS_B="$(
  curl -fsS \
    -H 'X-ZCourier-Internal-Token: traffic-policy-redis-internal-token' \
    http://127.0.0.1:18213/internal/admin/diagnostics
)"
jq -e '
  .traffic_policy.enabled == true and
  .traffic_policy.mode == "redis" and
  .traffic_policy.store_status == "unavailable" and
  .traffic_policy.failure_mode == "fail_closed" and
  .traffic_policy.decisions.admission_unavailable >= 1 and
  .traffic_policy.last_result == "admission_unavailable" and
  any(.dependencies[]; .name == "traffic_policy_store" and .status == "unavailable") and
  any(.warnings[]; .code == "traffic_policy_store_unavailable")
' <<<"$DIAGNOSTICS_A" >/dev/null
jq -e '
  .traffic_policy.enabled == true and
  .traffic_policy.mode == "redis" and
  .traffic_policy.store_status == "configured" and
  .traffic_policy.decisions.allowed >= 1 and
  .traffic_policy.decisions.rate_limited >= 1 and
  .traffic_policy.last_result == "allowed" and
  any(.dependencies[]; .name == "traffic_policy_store" and .status == "configured")
' <<<"$DIAGNOSTICS_B" >/dev/null
if [[ "$DIAGNOSTICS_A$DIAGNOSTICS_B" == *"127.0.0.1:16389"* ]] ||
  [[ "$DIAGNOSTICS_A$DIAGNOSTICS_B" == *"zcourier:e2e:traffic-policy"* ]]; then
  echo "Redis traffic policy diagnostics leaked Redis connection details" >&2
  exit 1
fi

echo "Redis traffic policy e2e passed"
