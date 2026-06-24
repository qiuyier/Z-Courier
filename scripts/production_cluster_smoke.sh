#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/production-cluster/docker-compose.yml"
ENV_FILE="${PRODUCTION_CLUSTER_SMOKE_ENV_FILE:-$ROOT_DIR/deploy/production-cluster/.env.example}"
PROMETHEUS_URL="${PRODUCTION_CLUSTER_SMOKE_PROMETHEUS_URL:-http://127.0.0.1:9091}"
TIMEOUT_SECONDS="${PRODUCTION_CLUSTER_SMOKE_TIMEOUT_SECONDS:-120}"

compose() {
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

cleanup() {
  local status=$?

  if [[ "$status" != "0" ]]; then
    echo "production cluster smoke failed; dumping docker compose logs" >&2
    compose logs --no-color --tail=200 >&2 || true
  fi

  if [[ "${PRODUCTION_CLUSTER_SMOKE_KEEP_STACK:-0}" != "1" ]]; then
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
  local service="$1"
  compose exec -T "$service" /bin/sh -c 'wget -q -O- http://127.0.0.1:18080/readyz'
}

gateway_metrics_ready() {
  local service="$1"
  compose exec -T "$service" /bin/sh -c 'wget -q -O- http://127.0.0.1:18080/metrics | grep -q z_courier_sessions_online'
}

prometheus_ready() {
  curl -fsS "$PROMETHEUS_URL/-/ready"
}

prometheus_targets_up() {
  local body
  local rest
  local up_count=0
  body="$(curl -fsS "$PROMETHEUS_URL/api/v1/targets?state=active")"
  rest="$body"
  while [[ "$rest" == *'"health":"up"'* ]]; do
    rest="${rest#*\"health\":\"up\"}"
    up_count=$((up_count + 1))
  done

  [[ "$body" == *'"scrapeUrl":"http://gateway-a:18080/metrics"'* ]] &&
    [[ "$body" == *'"scrapeUrl":"http://gateway-b:18080/metrics"'* ]] &&
    [[ "$up_count" -ge 2 ]]
}

cd "$ROOT_DIR"

echo "rendering production cluster compose config..."
compose config >/dev/null

echo "starting production cluster reference stack..."
compose up -d --build

wait_until "gateway-a readiness" gateway_ready gateway-a
wait_until "gateway-b readiness" gateway_ready gateway-b
wait_until "gateway-a metrics" gateway_metrics_ready gateway-a
wait_until "gateway-b metrics" gateway_metrics_ready gateway-b
wait_until "prometheus readiness" prometheus_ready
wait_until "prometheus gateway targets" prometheus_targets_up

echo "production cluster smoke passed"
