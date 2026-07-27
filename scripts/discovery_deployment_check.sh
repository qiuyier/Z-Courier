#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
STATIC_VALUES="$CHART_DIR/examples/values-static-discovery.yaml"
DNS_VALUES="$CHART_DIR/examples/values-dns-discovery.yaml"
HELM_IMAGE="${ZCOURIER_HELM_IMAGE:-alpine/helm:3.17.3}"
GATEWAY_IMAGE="${ZCOURIER_DISCOVERY_CHECK_IMAGE:-}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-discovery-deployment.XXXXXX")"
STATIC_MANIFEST="$TMP_DIR/static.yaml"
DNS_MANIFEST="$TMP_DIR/dns.yaml"
STATIC_CONFIG="$TMP_DIR/static-z-courier.yaml"
DNS_CONFIG="$TMP_DIR/dns-z-courier.yaml"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

helm_command() {
  if command -v helm >/dev/null 2>&1; then
    helm "$@"
    return
  fi
  docker run --rm -v "$ROOT_DIR:/work" -w /work "$HELM_IMAGE" "$@"
}

extract_gateway_config() {
  local manifest="$1"
  local output="$2"

  awk '
    /^  z-courier.yaml: \|$/ {
      capture = 1
      next
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
    echo "failed to extract z-courier.yaml from $manifest" >&2
    exit 1
  fi
}

gateway_check() {
  local config="$1"
  local -a config_env=(
    POD_NAME=z-courier-0
    POD_NAMESPACE=z-courier
    ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=discovery-check-auth-provider
    ZCOURIER_INTERNAL_HMAC_SECRET=discovery-check-internal-hmac-secret-0123456789
    ZCOURIER_PEER_HMAC_SECRET=discovery-check-peer-hmac-secret-0123456789
    ZCOURIER_POSTGRES_PASSWORD=discovery-check-postgres
    ZCOURIER_REDIS_PASSWORD=discovery-check-redis
    ZCOURIER_UPSTREAM_INTERNAL_TOKEN=discovery-check-upstream
  )

  if [[ -z "$GATEWAY_IMAGE" ]]; then
    env "${config_env[@]}" go run ./cmd/gateway -config "$config" -check-config
    return
  fi

  local -a docker_env=()
  local item
  for item in "${config_env[@]}"; do
    docker_env+=(--env "$item")
  done
  docker run --rm \
    "${docker_env[@]}" \
    --volume "$config:/tmp/z-courier.yaml:ro" \
    "$GATEWAY_IMAGE" \
    -config /tmp/z-courier.yaml \
    -check-config
}

cd "$ROOT_DIR"

helm_command lint "$CHART_DIR" -f "$STATIC_VALUES"
helm_command lint "$CHART_DIR" -f "$DNS_VALUES"
helm_command template z-courier "$CHART_DIR" -f "$STATIC_VALUES" >"$STATIC_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$DNS_VALUES" >"$DNS_MANIFEST"

extract_gateway_config "$STATIC_MANIFEST" "$STATIC_CONFIG"
extract_gateway_config "$DNS_MANIFEST" "$DNS_CONFIG"

grep -Fq 'type: "static"' "$STATIC_CONFIG"
grep -Fq 'http://business-backend-a:8080/gateway/upstream' "$STATIC_CONFIG"
grep -Fq 'http://business-backend-b:8080/gateway/upstream' "$STATIC_CONFIG"
grep -Fq 'max_attempts: 2' "$STATIC_CONFIG"

grep -Fq 'type: "dns"' "$DNS_CONFIG"
grep -Fq 'path: "/gateway/upstream"' "$DNS_CONFIG"
grep -Fq 'hostname: "business-backend-headless.z-courier.svc.cluster.local"' "$DNS_CONFIG"
grep -Fq 'refresh_interval: "10s"' "$DNS_CONFIG"

if helm_command lint "$CHART_DIR" -f "$STATIC_VALUES" \
  --set-string upstream.routes[0].target.url=http://legacy.invalid/gateway/upstream \
  >/dev/null 2>&1; then
  echo "Helm schema unexpectedly accepted url together with static discovery" >&2
  exit 1
fi

if helm_command lint "$CHART_DIR" -f "$DNS_VALUES" \
  --set-string upstream.routes[0].target.discovery.hostname= \
  >/dev/null 2>&1; then
  echo "Helm schema unexpectedly accepted empty DNS discovery hostname" >&2
  exit 1
fi

gateway_check "$STATIC_CONFIG"
gateway_check "$DNS_CONFIG"

echo "discovery deployment check passed"
