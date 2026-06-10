#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/local/docker-compose.yml"
CONFIG_A="$ROOT_DIR/configs/z-courier.cluster-a.yaml"
CONFIG_B="$ROOT_DIR/configs/z-courier.cluster-b.yaml"
ZINX_CONFIG_A="$ROOT_DIR/conf/zinx.cluster-a.json"
ZINX_CONFIG_B="$ROOT_DIR/conf/zinx.cluster-b.json"
LOG_DIR="$ROOT_DIR/log"

GATEWAY_A_PID=""
GATEWAY_B_PID=""

cleanup() {
  for pid in "$GATEWAY_A_PID" "$GATEWAY_B_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
}
trap cleanup EXIT

wait_http() {
  local name="$1"
  local url="$2"

  echo "waiting for $name..."
  for attempt in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

cd "$ROOT_DIR"
mkdir -p "$LOG_DIR"

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

echo "starting gateway-a..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_A" go run ./cmd/gateway -config "$CONFIG_A" >"$LOG_DIR/e2e-cluster-gateway-a.log" 2>&1 &
GATEWAY_A_PID="$!"

echo "starting gateway-b..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_B" go run ./cmd/gateway -config "$CONFIG_B" >"$LOG_DIR/e2e-cluster-gateway-b.log" 2>&1 &
GATEWAY_B_PID="$!"

wait_http "gateway-a metrics" "http://127.0.0.1:18182/metrics"
wait_http "gateway-b metrics" "http://127.0.0.1:18183/metrics"

go run ./cmd/e2e \
  -gateway-port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -metrics-url http://127.0.0.1:18182/metrics,http://127.0.0.1:18183/metrics \
  -timeout 45s \
  "$@"
