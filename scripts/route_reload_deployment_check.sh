#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
ROUTE_VALUES="$CHART_DIR/examples/values-route-file.yaml"
HELM_IMAGE="${ZCOURIER_HELM_IMAGE:-alpine/helm:3.17.3}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-route-reload-deployment.XXXXXX")"
BASE_MANIFEST="$TMP_DIR/base.yaml"
CANDIDATE_MANIFEST="$TMP_DIR/candidate.yaml"
CANDIDATE_VALUES="$TMP_DIR/candidate-values.yaml"
DEFAULT_MANIFEST="$TMP_DIR/default.yaml"
PRODUCTION_MANIFEST="$TMP_DIR/production.yaml"
BASE_CONFIG="$TMP_DIR/base-z-courier.yaml"
BASE_ROUTES="$TMP_DIR/upstream-routes.yaml"
CANDIDATE_ROUTES="$TMP_DIR/candidate-upstream-routes.yaml"
CHECK_CONFIG="$TMP_DIR/z-courier.yaml"
PRODUCTION_HELM_CONFIG="$TMP_DIR/production-z-courier.yaml"
PRODUCTION_HELM_ROUTES="$TMP_DIR/production-upstream-routes.yaml"
PRODUCTION_HELM_CHECK_CONFIG="$TMP_DIR/production-check-z-courier.yaml"
PRODUCTION_COMPOSE="$TMP_DIR/production-compose.json"
CLUSTER_COMPOSE="$TMP_DIR/cluster-compose.json"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 127
  fi
}

helm_command() {
  if command -v helm >/dev/null 2>&1; then
    helm "$@"
    return
  fi
  docker run --rm -v "$ROOT_DIR:/work" -w /work "$HELM_IMAGE" "$@"
}

extract_config_map_key() {
  local manifest="$1"
  local key="$2"
  local output="$3"

  awk -v marker="  ${key}: |" '
    $0 == marker {
      capture = 1
      next
    }
    capture && /^---$/ {
      exit
    }
    capture && /^  [^ ]/ {
      exit
    }
    capture {
      sub(/^    /, "")
      print
    }
  ' "$manifest" >"$output"

  if [[ ! -s "$output" ]]; then
    echo "failed to extract $key from $manifest" >&2
    exit 1
  fi
}

gateway_check() {
  local config="$1"
  env \
    POD_NAME=z-courier-0 \
    POD_NAMESPACE=z-courier \
    ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=route-reload-check-auth \
    ZCOURIER_ADMIN_CONSOLE_ENABLED=false \
    ZCOURIER_ADMIN_SESSION_ENABLED=false \
    ZCOURIER_INTERNAL_HMAC_SECRET=route-reload-check-internal-hmac-0123456789 \
    ZCOURIER_PEER_HMAC_SECRET=route-reload-check-peer-hmac-0123456789 \
    ZCOURIER_POSTGRES_PASSWORD=route-reload-check-postgres \
    ZCOURIER_REDIS_PASSWORD=route-reload-check-redis \
    ZCOURIER_UPSTREAM_INTERNAL_TOKEN=route-reload-check-upstream \
    go run ./cmd/gateway -config "$config" -check-config
}

assert_compose_route_mount() {
  local compose_json="$1"
  local service="$2"
  local expected_source="$3"

  jq -e \
    --arg service "$service" \
    --arg source "$expected_source" \
    '.services[$service].volumes | any(
      .type == "bind" and
      .source == $source and
      .target == "/app/routes" and
      .read_only == true
    )' "$compose_json" >/dev/null
}

need_cmd docker
need_cmd go
need_cmd jq

cd "$ROOT_DIR"

helm_command lint "$CHART_DIR" -f "$ROUTE_VALUES"
helm_command lint "$CHART_DIR" -f "$CHART_DIR/examples/values-production.yaml"
if helm_command lint "$CHART_DIR" -f "$ROUTE_VALUES" \
  --set-json 'upstream.routesFile.reload.acceptedMsgIDRanges=[]' \
  >/dev/null 2>&1; then
  echo "Helm schema accepted reload without an admission range" >&2
  exit 1
fi
helm_command lint "$CHART_DIR" -f "$ROUTE_VALUES" \
  --set upstream.routesFile.reload.enabled=false \
  --set-json 'upstream.routesFile.reload.acceptedMsgIDRanges=[]' \
  >/dev/null
helm_command template z-courier "$CHART_DIR" >"$DEFAULT_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$ROUTE_VALUES" >"$BASE_MANIFEST"
helm_command template z-courier "$CHART_DIR" \
  -f "$CHART_DIR/examples/values-production.yaml" >"$PRODUCTION_MANIFEST"
sed \
  's#http://business-backend:8080/gateway/upstream#http://candidate-backend:8080/gateway/upstream#' \
  "$CHART_DIR/values.yaml" >"$CANDIDATE_VALUES"
helm_command template z-courier "$CHART_DIR" \
  -f "$CANDIDATE_VALUES" \
  -f "$ROUTE_VALUES" \
  >"$CANDIDATE_MANIFEST"

if grep -Fq 'name: z-courier-upstream-routes' "$DEFAULT_MANIFEST"; then
  echo "default Helm values unexpectedly rendered a route ConfigMap" >&2
  exit 1
fi
grep -Fq 'name: z-courier-upstream-routes' "$BASE_MANIFEST"
grep -Fq 'mountPath: /app/routes' "$BASE_MANIFEST"
grep -Fq 'readOnly: true' "$BASE_MANIFEST"
if grep -Fq 'subPath: upstream-routes.yaml' "$BASE_MANIFEST"; then
  echo "route ConfigMap must not use subPath because projected updates would stop" >&2
  exit 1
fi

base_checksum="$(awk '$1 == "checksum/config:" { print $2; exit }' "$BASE_MANIFEST")"
candidate_checksum="$(awk '$1 == "checksum/config:" { print $2; exit }' "$CANDIDATE_MANIFEST")"
if [[ -z "$base_checksum" || "$base_checksum" != "$candidate_checksum" ]]; then
  echo "route-only Helm changes unexpectedly alter the StatefulSet config checksum" >&2
  exit 1
fi

extract_config_map_key "$BASE_MANIFEST" z-courier.yaml "$BASE_CONFIG"
extract_config_map_key "$BASE_MANIFEST" upstream-routes.yaml "$BASE_ROUTES"
extract_config_map_key "$CANDIDATE_MANIFEST" upstream-routes.yaml "$CANDIDATE_ROUTES"
grep -Fq 'path: /app/routes/upstream-routes.yaml' "$BASE_CONFIG"
grep -Fq 'accepted_msg_id_ranges:' "$BASE_CONFIG"
grep -Fq 'version: 1' "$BASE_ROUTES"
grep -Fq 'http://candidate-backend:8080/gateway/upstream' "$CANDIDATE_ROUTES"
if cmp -s "$BASE_ROUTES" "$CANDIDATE_ROUTES"; then
  echo "route candidate did not change the projected route document" >&2
  exit 1
fi

sed 's#path: /app/routes/upstream-routes.yaml#path: upstream-routes.yaml#' \
  "$BASE_CONFIG" >"$CHECK_CONFIG"
gateway_check "$CHECK_CONFIG"

extract_config_map_key "$PRODUCTION_MANIFEST" z-courier.yaml "$PRODUCTION_HELM_CONFIG"
extract_config_map_key \
  "$PRODUCTION_MANIFEST" upstream-routes.yaml "$PRODUCTION_HELM_ROUTES"
sed 's#path: /app/routes/upstream-routes.yaml#path: production-upstream-routes.yaml#' \
  "$PRODUCTION_HELM_CONFIG" >"$PRODUCTION_HELM_CHECK_CONFIG"
gateway_check "$PRODUCTION_HELM_CHECK_CONFIG"

docker compose --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml config --format json >"$PRODUCTION_COMPOSE"
docker compose --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml config --format json >"$CLUSTER_COMPOSE"
assert_compose_route_mount \
  "$PRODUCTION_COMPOSE" gateway "$ROOT_DIR/deploy/production/routes"
assert_compose_route_mount \
  "$CLUSTER_COMPOSE" gateway-a "$ROOT_DIR/deploy/production-cluster/routes"
assert_compose_route_mount \
  "$CLUSTER_COMPOSE" gateway-b "$ROOT_DIR/deploy/production-cluster/routes"

gateway_check deploy/production/config/z-courier.yaml
gateway_check deploy/production-cluster/config/gateway-a.yaml
gateway_check deploy/production-cluster/config/gateway-b.yaml

echo "route reload deployment check passed"
