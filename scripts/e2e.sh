#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/local/docker-compose.yml"
CONFIG_FILE="$ROOT_DIR/configs/z-courier.integration.yaml"
ZINX_CONFIG_FILE="$ROOT_DIR/conf/zinx.integration.json"
RUN_ID="$(date +%s)-$$"

GATEWAY_PID=""

cleanup() {
  if [[ -n "$GATEWAY_PID" ]] && kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    kill "$GATEWAY_PID" >/dev/null 2>&1 || true
    wait "$GATEWAY_PID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

check_gateway_alive() {
  if [[ -n "$GATEWAY_PID" ]] && ! kill -0 "$GATEWAY_PID" >/dev/null 2>&1; then
    echo "gateway exited unexpectedly" >&2
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

cd "$ROOT_DIR"

docker compose -f "$COMPOSE_FILE" up -d postgres redis nsqlookupd nsqd nsqadmin prometheus grafana

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

echo "waiting for nsqd..."
for attempt in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:14151/ping >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == "60" ]]; then
    echo "nsqd did not become ready in time" >&2
    exit 1
  fi
  sleep 1
done

echo "starting gateway..."
ZINX_CONFIG_FILE_PATH="$ZINX_CONFIG_FILE" go run ./cmd/gateway -config "$CONFIG_FILE" &
GATEWAY_PID="$!"

wait_http "gateway readiness" "http://127.0.0.1:18082/readyz"
go run ./cmd/e2e \
  -device-id "e2e-device-$RUN_ID" \
  "$@"

echo "running public Go SDK integration verifier..."
go run ./cmd/sdke2e \
  -device-id "sdk-e2e-device-$RUN_ID"
