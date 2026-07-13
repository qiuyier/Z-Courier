#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/local/docker-compose.yml"
CONFIG_A_SOURCE="$ROOT_DIR/configs/z-courier.cluster-a.yaml"
CONFIG_B_SOURCE="$ROOT_DIR/configs/z-courier.cluster-b.yaml"
ZINX_CONFIG_A="$ROOT_DIR/conf/zinx.cluster-a.json"
ZINX_CONFIG_B="$ROOT_DIR/conf/zinx.cluster-b.json"
LOG_DIR="$ROOT_DIR/log"
RUN_ID="$(date +%s)-$$"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-e2e-cluster.XXXXXX")"
CONFIG_A="$TMP_DIR/z-courier.cluster-a.yaml"
CONFIG_B="$TMP_DIR/z-courier.cluster-b.yaml"
GATEWAY_BIN="$TMP_DIR/gateway"

GATEWAY_A_PID=""
GATEWAY_B_PID=""

cleanup() {
  for pid in "$GATEWAY_A_PID" "$GATEWAY_B_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

check_gateways_alive() {
  if [[ -n "$GATEWAY_A_PID" ]] && ! kill -0 "$GATEWAY_A_PID" >/dev/null 2>&1; then
    echo "gateway-a exited unexpectedly" >&2
    tail -n 120 "$LOG_DIR/e2e-cluster-gateway-a.log" >&2 || true
    exit 1
  fi
  if [[ -n "$GATEWAY_B_PID" ]] && ! kill -0 "$GATEWAY_B_PID" >/dev/null 2>&1; then
    echo "gateway-b exited unexpectedly" >&2
    tail -n 120 "$LOG_DIR/e2e-cluster-gateway-b.log" >&2 || true
    exit 1
  fi
}

require_port_free() {
  local port="$1"
  if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
    echo "gateway test port $port is already in use" >&2
    exit 1
  fi
}

wait_http() {
  local name="$1"
  local url="$2"

  echo "waiting for $name..."
  for attempt in $(seq 1 60); do
    check_gateways_alive
    if curl -fsS "$url" >/dev/null 2>&1; then
      check_gateways_alive
      return 0
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

prepare_e2e_config() {
  local source="$1"
  local destination="$2"

  awk '
    BEGIN { in_delivery = 0; interval_changed = 0; capacity_changed = 0 }
    /^  delivery:$/ && interval_changed == 0 { in_delivery = 1 }
    in_delivery && /^    retry_interval:/ {
      print "    retry_interval: 1h"
      interval_changed = 1
      in_delivery = 0
      next
    }
    /^    max_pending_per_device:/ {
      print "    max_pending_per_device: 8"
      capacity_changed = 1
      next
    }
    { print }
    END {
      if (interval_changed == 0 || capacity_changed == 0) {
        exit 1
      }
    }
  ' "$source" >"$destination"
}

cd "$ROOT_DIR"
mkdir -p "$LOG_DIR"
: >"$LOG_DIR/e2e-cluster-gateway-a.log"
: >"$LOG_DIR/e2e-cluster-gateway-b.log"
prepare_e2e_config "$CONFIG_A_SOURCE" "$CONFIG_A"
prepare_e2e_config "$CONFIG_B_SOURCE" "$CONFIG_B"

docker compose -f "$COMPOSE_FILE" up -d postgres redis nsqlookupd nsqd

echo "waiting for postgres..."
for attempt in $(seq 1 60); do
  if docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U zcourier -d zcourier >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == "60" ]]; then
    echo "postgres did not become ready in time" >&2
    exit 1
  fi
  sleep 1
done

echo "waiting for redis..."
for attempt in $(seq 1 60); do
  if docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli ping >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == "60" ]]; then
    echo "redis did not become ready in time" >&2
    exit 1
  fi
  sleep 1
done

wait_http "nsqd" "http://127.0.0.1:14151/ping"

for port in 9901 9902 18182 18183; do
  require_port_free "$port"
done

echo "building gateway..."
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "starting gateway-a..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_A" "$GATEWAY_BIN" -config "$CONFIG_A" >"$LOG_DIR/e2e-cluster-gateway-a.log" 2>&1 &
GATEWAY_A_PID="$!"

echo "starting gateway-b..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_B" "$GATEWAY_BIN" -config "$CONFIG_B" >"$LOG_DIR/e2e-cluster-gateway-b.log" 2>&1 &
GATEWAY_B_PID="$!"

wait_http "gateway-a readiness" "http://127.0.0.1:18182/readyz"
wait_http "gateway-b readiness" "http://127.0.0.1:18183/readyz"

go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics \
  -device-id "e2e-cluster-device-$RUN_ID" \
  -online-push-delay 5s \
  -require-cluster-metrics \
  -expect-route-node gateway-b \
  -expect-route-internal-url http://127.0.0.1:18183 \
  -expect-session-url http://127.0.0.1:18183 \
  -expect-session-node gateway-b \
  -check-reconnect-retry \
  -check-admin-storage \
  -admin-session-peer-url http://127.0.0.1:18183 \
  -expect-policy-name integration-reliable \
  -check-queue-capacity \
  -expect-per-device-limit 8 \
  -check-retry-fairness \
  -retry-fairness-scan-limit 3 \
  -check-terminal-event \
  -expect-terminal-policy integration-terminal \
  -timeout 60s \
  "$@"
