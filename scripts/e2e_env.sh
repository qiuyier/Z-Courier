#!/usr/bin/env bash

E2E_ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
E2E_COMPOSE_FILE="$E2E_ROOT_DIR/deploy/local/docker-compose.yml"
E2E_CONFIG_FILE="$E2E_ROOT_DIR/configs/z-courier.integration.yaml"
E2E_ZINX_CONFIG_FILE="$E2E_ROOT_DIR/conf/zinx.integration.json"
E2E_GATEWAY_PID=""

e2e_cleanup_gateway() {
  if [[ -n "$E2E_GATEWAY_PID" ]] && kill -0 "$E2E_GATEWAY_PID" >/dev/null 2>&1; then
    kill "$E2E_GATEWAY_PID" >/dev/null 2>&1 || true
    wait "$E2E_GATEWAY_PID" >/dev/null 2>&1 || true
  fi
}

e2e_check_gateway_alive() {
  if [[ -n "$E2E_GATEWAY_PID" ]] && ! kill -0 "$E2E_GATEWAY_PID" >/dev/null 2>&1; then
    echo "gateway exited unexpectedly" >&2
    exit 1
  fi
}

e2e_wait_http() {
  local name="$1"
  local url="$2"

  echo "waiting for $name..."
  for attempt in $(seq 1 60); do
    e2e_check_gateway_alive
    if curl -fsS "$url" >/dev/null 2>&1; then
      e2e_check_gateway_alive
      return 0
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

e2e_start_gateway() {
  cd "$E2E_ROOT_DIR"
  trap e2e_cleanup_gateway EXIT

  docker compose -f "$E2E_COMPOSE_FILE" up -d postgres redis nsqlookupd nsqd nsqadmin prometheus grafana

  echo "waiting for postgres..."
  for attempt in $(seq 1 60); do
    if docker compose -f "$E2E_COMPOSE_FILE" exec -T postgres pg_isready -U zcourier -d zcourier >/dev/null 2>&1; then
      break
    fi
    if [[ "$attempt" == "60" ]]; then
      echo "postgres did not become ready in time" >&2
      return 1
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
      return 1
    fi
    sleep 1
  done

  echo "starting gateway..."
  ZINX_CONFIG_FILE_PATH="$E2E_ZINX_CONFIG_FILE" go run ./cmd/gateway -config "$E2E_CONFIG_FILE" &
  E2E_GATEWAY_PID="$!"

  e2e_wait_http "gateway readiness" "http://127.0.0.1:18082/readyz"
}
