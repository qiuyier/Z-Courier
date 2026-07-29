#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RUN_DOCKER="${ZCOURIER_RELEASE_RUN_DOCKER:-0}"
RUN_SLOW="${ZCOURIER_RELEASE_RUN_SLOW:-0}"
RUN_K8S="${ZCOURIER_RELEASE_RUN_K8S:-0}"
RUN_RACE="${ZCOURIER_RELEASE_RUN_RACE:-1}"
SKIP_PHP="${ZCOURIER_RELEASE_SKIP_PHP:-0}"
COMPOSER_DOCKER_IMAGE="${ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE:-}"
COMPOSER_DOCKER_CACHE_DIR="${ZCOURIER_RELEASE_COMPOSER_CACHE_DIR:-$HOME/.composer}"
DOCKER_BUILD_PLATFORM="${ZCOURIER_RELEASE_DOCKER_BUILD_PLATFORM:-}"

cd "$ROOT_DIR"

run() {
  printf "\n==> "
  printf "%q " "$@"
  printf "\n"
  "$@"
}

require_cmd() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "missing required command: $name" >&2
    exit 1
  fi
}

run_actionlint() {
  if command -v actionlint >/dev/null 2>&1; then
    run actionlint
    return
  fi

  echo "warning: actionlint is not installed; skipping workflow lint" >&2
}

run_php_checks() {
  if [[ "$SKIP_PHP" == "1" ]]; then
    echo "skipping PHP SDK checks because ZCOURIER_RELEASE_SKIP_PHP=1"
    return
  fi

  require_cmd php

  run php -d error_reporting=E_ALL sdk/php/tests/run.php

  while IFS= read -r -d '' file; do
    run php -l "$file"
  done < <(find sdk/php -name '*.php' -print0)

  run_composer --working-dir=sdk/php install --no-interaction --prefer-dist
  run_composer --working-dir=sdk/php analyse
}

run_composer() {
  if command -v composer >/dev/null 2>&1; then
    run composer "$@"
    return
  fi

  if [[ -n "$COMPOSER_DOCKER_IMAGE" ]]; then
    require_cmd docker
    mkdir -p "$COMPOSER_DOCKER_CACHE_DIR"
    run docker run --rm --interactive \
      --user "$(id -u):$(id -g)" \
      --env COMPOSER_HOME=/tmp/composer \
      --volume "$COMPOSER_DOCKER_CACHE_DIR:/tmp/composer" \
      --volume "$ROOT_DIR:/app" \
      --workdir /app \
      "$COMPOSER_DOCKER_IMAGE" composer "$@"
    return
  fi

  echo "missing required command: composer" >&2
  echo "or set ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE to a local image with composer" >&2
  exit 1
}

run_helm() {
  printf "\n==> helm "
  printf "%q " "$@"
  printf "\n"

  if command -v helm >/dev/null 2>&1; then
    helm "$@"
    return
  fi

  require_cmd docker
  docker run --rm -v "$ROOT_DIR:/work" -w /work alpine/helm:3.17.3 "$@"
}

run_release_docker_build() {
  if [[ -n "$DOCKER_BUILD_PLATFORM" ]]; then
    run docker build \
      --build-arg "BUILDPLATFORM=$DOCKER_BUILD_PLATFORM" \
      --tag z-courier-gateway:release-check .
    return
  fi

  run docker build --tag z-courier-gateway:release-check .
}

run_production_config_checks() {
  local config_file
  local -a config_env=(
    ZCOURIER_POSTGRES_PASSWORD=release-check-postgres
    ZCOURIER_REDIS_PASSWORD=release-check-redis
    ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=release-check-auth-provider
    ZCOURIER_ADMIN_CONSOLE_ENABLED=false
    ZCOURIER_ADMIN_SESSION_ENABLED=false
    ZCOURIER_INTERNAL_HMAC_SECRET=release-check-internal-hmac-secret-0123456789
    ZCOURIER_PEER_HMAC_SECRET=release-check-peer-hmac-secret-0123456789
    ZCOURIER_UPSTREAM_INTERNAL_TOKEN=release-check-upstream-token
  )

  for config_file in \
    deploy/production/config/z-courier.yaml \
    deploy/production-cluster/config/gateway-a.yaml \
    deploy/production-cluster/config/gateway-b.yaml; do
    run env "${config_env[@]}" go run ./cmd/gateway -config "$config_file" -check-config
  done
}

run_fast_checks() {
  require_cmd go
  require_cmd npm
  local shell_script

  run_actionlint
  run bash scripts/secret_boundary_check.sh

  run npm ci --prefix web/admin
  run npm run build --prefix web/admin
  run test -f web/admin/dist/index.html

  run go test -count=1 -timeout=120s ./...

  if [[ "$RUN_RACE" == "1" ]]; then
    run go test -race -count=1 -timeout=90s \
      ./pkg/sdk/protocol ./pkg/sdk/client ./pkg/sdk/backend ./pkg/sdk/signing \
      ./internal/auth ./internal/downlink \
      ./internal/server ./internal/config ./internal/pipeline
  fi

  run go vet ./...
  run_php_checks

  run go run ./cmd/gateway -config configs/z-courier.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.integration.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.discovery-e2e.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.traffic-policy-e2e.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.traffic-policy-redis-a.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.traffic-policy-redis-b.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.cluster-a.yaml -check-config
  run go run ./cmd/gateway -config configs/z-courier.cluster-b.yaml -check-config
  run_production_config_checks
  run env \
    ZCOURIER_CONSOLE_SMOKE_ROLE=admin \
    ZCOURIER_CONSOLE_SMOKE_INTERNAL_ADDR=127.0.0.1:18084 \
    ZCOURIER_CONSOLE_SMOKE_INTERNAL_TOKEN=dev-internal-token \
    go run ./cmd/gateway -config configs/z-courier.console-smoke.yaml -check-config

  for shell_script in scripts/*.sh; do
    run bash -n "$shell_script"
  done

  run bash scripts/e2e_traffic_policy.sh
  run git diff --check
}

run_docker_checks() {
  require_cmd docker

  run docker compose -f deploy/local/docker-compose.yml config
  run docker compose -f deploy/monitoring/docker-compose.yml config
  run docker compose --env-file deploy/production/.env.example -f deploy/production/docker-compose.yml config
  run docker compose --env-file deploy/production-cluster/.env.example -f deploy/production-cluster/docker-compose.yml config
  run bash scripts/compose_terminal_webhook_tls_check.sh
  run bash scripts/edge_proxy_check.sh

  run bash scripts/promtool_check.sh
  run bash scripts/helm_terminal_http_check.sh
  run bash scripts/helm_hmac_rotation_check.sh

  run_helm lint deploy/helm/z-courier
  run_helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
  run_helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-kind-smoke.yaml
  run_helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-k8s-e2e.yaml
  run_helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-hmac-rotation.yaml
  run_helm template z-courier deploy/helm/z-courier
  run_helm package deploy/helm/z-courier --destination /tmp

  run_release_docker_build
  run docker run --rm --entrypoint /bin/sh z-courier-gateway:release-check -c \
    'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json && test -f /app/web/admin/dist/index.html'
  run env ZCOURIER_DISCOVERY_CHECK_IMAGE=z-courier-gateway:release-check \
    bash scripts/discovery_deployment_check.sh
}

run_slow_checks() {
  run bash scripts/e2e_discovery.sh
  run bash scripts/e2e_traffic_policy_redis.sh
  run bash scripts/e2e.sh
  run bash scripts/e2e_cluster.sh
  run npm --prefix web/admin exec -- playwright install chromium
  run bash scripts/console_smoke.sh
  run env ZCOURIER_EDGE_SMOKE_SKIP_BUILD=1 bash scripts/edge_proxy_smoke.sh
  run bash scripts/certificate_rotation_smoke.sh
  run bash scripts/loadtest_smoke.sh
  run docker tag z-courier-gateway:release-check z-courier-gateway:production
  run docker tag z-courier-gateway:release-check z-courier-gateway:production-cluster
  run env PRODUCTION_SMOKE_SKIP_BUILD=1 bash scripts/production_smoke.sh
  run env PRODUCTION_CLUSTER_SMOKE_SKIP_BUILD=1 bash scripts/production_cluster_smoke.sh

  if [[ "$RUN_K8S" == "1" ]]; then
    run bash scripts/k8s_helm_smoke.sh
    run bash scripts/k8s_helm_e2e.sh
  else
    echo "skipping kind/Helm E2E because ZCOURIER_RELEASE_RUN_K8S is not 1"
  fi
}

run_fast_checks

if [[ "$RUN_DOCKER" == "1" ]]; then
  run_docker_checks
else
  echo "skipping Docker-backed checks because ZCOURIER_RELEASE_RUN_DOCKER is not 1"
fi

if [[ "$RUN_SLOW" == "1" ]]; then
  if [[ "$RUN_DOCKER" != "1" ]]; then
    echo "ZCOURIER_RELEASE_RUN_SLOW=1 requires ZCOURIER_RELEASE_RUN_DOCKER=1" >&2
    exit 1
  fi
  run_slow_checks
else
  echo "skipping long-running smoke/E2E checks because ZCOURIER_RELEASE_RUN_SLOW is not 1"
fi

echo
echo "release checks passed"
