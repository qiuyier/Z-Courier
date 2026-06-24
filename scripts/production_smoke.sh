#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/production/docker-compose.yml"
ENV_FILE="${PRODUCTION_SMOKE_ENV_FILE:-$ROOT_DIR/deploy/production/.env.example}"
PROMETHEUS_URL="${PRODUCTION_SMOKE_PROMETHEUS_URL:-http://127.0.0.1:9090}"
TIMEOUT_SECONDS="${PRODUCTION_SMOKE_TIMEOUT_SECONDS:-120}"

compose() {
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

cleanup() {
  local status=$?

  if [[ "$status" != "0" ]]; then
    echo "production smoke failed; dumping docker compose logs" >&2
    compose logs --no-color --tail=200 >&2 || true
  fi

  if [[ "${PRODUCTION_SMOKE_KEEP_STACK:-0}" != "1" ]]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

wait_until() {
  local name="$1"
  shift

  echo "waiting for $name..."
  for attempt in $(seq 1 "$TIMEOUT_SECONDS"); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$attempt" == "$TIMEOUT_SECONDS" ]]; then
      echo "$name did not become ready in time" >&2
      return 1
    fi
    sleep 1
  done
}

gateway_ready() {
  compose exec -T gateway /bin/sh -c 'wget -q -O- http://127.0.0.1:18080/readyz'
}

gateway_metrics_ready() {
  compose exec -T gateway /bin/sh -c 'wget -q -O- http://127.0.0.1:18080/metrics | grep -q z_courier_sessions_online'
}

prometheus_ready() {
  curl -fsS "$PROMETHEUS_URL/-/ready"
}

prometheus_target_up() {
  local body
  body="$(curl -fsS "$PROMETHEUS_URL/api/v1/targets?state=active")"
  [[ "$body" == *'"scrapeUrl":"http://gateway:18080/metrics"'* ]] &&
    [[ "$body" == *'"health":"up"'* ]]
}

cd "$ROOT_DIR"

echo "rendering production compose config..."
compose config >/dev/null

echo "starting production reference stack..."
compose up -d --build

wait_until "gateway readiness" gateway_ready
wait_until "gateway metrics" gateway_metrics_ready
wait_until "prometheus readiness" prometheus_ready
wait_until "prometheus gateway target" prometheus_target_up

echo "production smoke passed"
