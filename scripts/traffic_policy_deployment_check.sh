#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
LOCAL_VALUES="$CHART_DIR/examples/values-traffic-policy-local.yaml"
REDIS_VALUES="$CHART_DIR/examples/values-traffic-policy-redis.yaml"
HELM_IMAGE="${ZCOURIER_HELM_IMAGE:-alpine/helm:3.17.3}"
GATEWAY_IMAGE="${ZCOURIER_TRAFFIC_POLICY_CHECK_IMAGE:-}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-traffic-policy-deployment.XXXXXX")"
DEFAULT_MANIFEST="$TMP_DIR/default.yaml"
LOCAL_MANIFEST="$TMP_DIR/local.yaml"
REDIS_MANIFEST="$TMP_DIR/redis.yaml"
INVALID_MANIFEST="$TMP_DIR/invalid.yaml"
DEFAULT_CONFIG="$TMP_DIR/default-z-courier.yaml"
LOCAL_CONFIG="$TMP_DIR/local-z-courier.yaml"
REDIS_CONFIG="$TMP_DIR/redis-z-courier.yaml"
INVALID_CONFIG="$TMP_DIR/invalid-z-courier.yaml"
PRODUCTION_CONFIG="$ROOT_DIR/deploy/production/config/z-courier.yaml"
CLUSTER_A_CONFIG="$ROOT_DIR/deploy/production-cluster/config/gateway-a.yaml"
CLUSTER_B_CONFIG="$ROOT_DIR/deploy/production-cluster/config/gateway-b.yaml"

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

extract_pipeline_section() {
  local config="$1"
  local section="$2"
  local output="$3"

  awk -v section="$section" '
    $0 == "pipeline:" {
      pipeline = 1
      next
    }
    pipeline && /^[^ ]/ {
      exit
    }
    pipeline && $0 == "  " section ":" {
      capture = 1
    }
    capture && $0 ~ /^  [^ ]/ && $0 != "  " section ":" {
      exit
    }
    capture {
      print
    }
  ' "$config" >"$output"

  if [[ ! -s "$output" ]]; then
    echo "failed to extract pipeline.$section from $config" >&2
    exit 1
  fi
}

section_scalar() {
  local config="$1"
  local section="$2"
  local field="$3"

  awk -v section="$section" -v field="$field" '
    $0 == "pipeline:" {
      pipeline = 1
      next
    }
    pipeline && /^[^ ]/ {
      exit
    }
    pipeline && $0 == "  " section ":" {
      selected = 1
      next
    }
    selected && /^  [^ ]/ {
      exit
    }
    selected && index($0, "    " field ":") == 1 {
      value = $0
      sub(/^[^:]+:[[:space:]]*/, "", value)
      gsub(/^"|"$/, "", value)
      print value
      exit
    }
  ' "$config"
}

assert_scalar() {
  local config="$1"
  local section="$2"
  local field="$3"
  local expected="$4"
  local actual

  actual="$(section_scalar "$config" "$section" "$field")"
  if [[ "$actual" != "$expected" ]]; then
    echo "$config pipeline.$section.$field = $actual, want $expected" >&2
    exit 1
  fi
}

gateway_check() {
  local config="$1"
  local -a config_env=(
    POD_NAME=z-courier-0
    POD_NAMESPACE=z-courier
    ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=traffic-policy-check-auth-provider
    ZCOURIER_ADMIN_CONSOLE_ENABLED=false
    ZCOURIER_ADMIN_SESSION_ENABLED=false
    ZCOURIER_INTERNAL_HMAC_SECRET=traffic-policy-check-internal-hmac-secret-0123456789
    ZCOURIER_PEER_HMAC_SECRET=traffic-policy-check-peer-hmac-secret-0123456789
    ZCOURIER_POSTGRES_PASSWORD=traffic-policy-check-postgres
    ZCOURIER_REDIS_PASSWORD=traffic-policy-check-redis
    ZCOURIER_UPSTREAM_INTERNAL_TOKEN=traffic-policy-check-upstream
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

expect_helm_lint_failure() {
  local description="$1"
  shift

  if helm_command lint "$CHART_DIR" "$@" >/dev/null 2>&1; then
    echo "Helm schema unexpectedly accepted $description" >&2
    exit 1
  fi
}

cd "$ROOT_DIR"

helm_command lint "$CHART_DIR"
helm_command lint "$CHART_DIR" -f "$LOCAL_VALUES"
helm_command lint "$CHART_DIR" -f "$REDIS_VALUES"
helm_command template z-courier "$CHART_DIR" >"$DEFAULT_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$LOCAL_VALUES" >"$LOCAL_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$REDIS_VALUES" >"$REDIS_MANIFEST"

extract_gateway_config "$DEFAULT_MANIFEST" "$DEFAULT_CONFIG"
extract_gateway_config "$LOCAL_MANIFEST" "$LOCAL_CONFIG"
extract_gateway_config "$REDIS_MANIFEST" "$REDIS_CONFIG"
extract_pipeline_section "$DEFAULT_CONFIG" traffic_policies "$TMP_DIR/default-traffic-policies.yaml"
extract_pipeline_section "$LOCAL_CONFIG" traffic_policies "$TMP_DIR/local-traffic-policies.yaml"
extract_pipeline_section "$REDIS_CONFIG" traffic_policies "$TMP_DIR/redis-traffic-policies.yaml"

assert_scalar "$DEFAULT_CONFIG" rate_limit enabled true
assert_scalar "$DEFAULT_CONFIG" traffic_policies enabled false
assert_scalar "$DEFAULT_CONFIG" traffic_policies mode local
assert_scalar "$LOCAL_CONFIG" rate_limit enabled false
assert_scalar "$LOCAL_CONFIG" traffic_policies enabled true
assert_scalar "$LOCAL_CONFIG" traffic_policies mode local
assert_scalar "$LOCAL_CONFIG" traffic_policies max_keys 100000
assert_scalar "$REDIS_CONFIG" rate_limit enabled false
assert_scalar "$REDIS_CONFIG" traffic_policies enabled true
assert_scalar "$REDIS_CONFIG" traffic_policies mode redis

grep -Fq 'default_policy: ""' "$LOCAL_CONFIG"
grep -Fq 'name: "local-standard"' "$LOCAL_CONFIG"
grep -Fq 'capacity: 1000' "$LOCAL_CONFIG"
grep -Fq 'refill_interval: "1s"' "$LOCAL_CONFIG"
if grep -Fq '    redis:' "$TMP_DIR/local-traffic-policies.yaml"; then
  echo "local traffic-policy config unexpectedly contains Redis settings" >&2
  exit 1
fi

grep -Fq 'addr: "redis-master.z-courier.svc.cluster.local:6379"' "$REDIS_CONFIG"
# This assertion intentionally matches the unexpanded runtime placeholder.
# shellcheck disable=SC2016
grep -Fq 'password: "${ZCOURIER_REDIS_PASSWORD}"' "$REDIS_CONFIG"
grep -Fq 'key_prefix: "zcourier:production:traffic-policy"' "$REDIS_CONFIG"
grep -Fq 'failure_mode: "fail_closed"' "$REDIS_CONFIG"

expect_helm_lint_failure "legacy and named ingress limiters enabled together" \
  -f "$LOCAL_VALUES" \
  --set pipeline.rateLimit.enabled=true
expect_helm_lint_failure "Redis traffic policies without an address" \
  -f "$REDIS_VALUES" \
  --set-string pipeline.trafficPolicies.redis.addr=
expect_helm_lint_failure "Redis settings in local mode" \
  -f "$REDIS_VALUES" \
  --set-string pipeline.trafficPolicies.mode=local
expect_helm_lint_failure "an unsupported traffic-policy key" \
  -f "$LOCAL_VALUES" \
  --set-string pipeline.trafficPolicies.policies[0].key=device_id

helm_command template z-courier "$CHART_DIR" -f "$LOCAL_VALUES" \
  --set-string pipeline.trafficPolicies.policies[0].match.routes[0]=missing-route \
  >"$INVALID_MANIFEST"
extract_gateway_config "$INVALID_MANIFEST" "$INVALID_CONFIG"
if gateway_check "$INVALID_CONFIG" >/dev/null 2>&1; then
  echo "gateway unexpectedly accepted a traffic policy referencing an unknown route" >&2
  exit 1
fi

gateway_check "$DEFAULT_CONFIG"
gateway_check "$LOCAL_CONFIG"
gateway_check "$REDIS_CONFIG"

assert_scalar "$PRODUCTION_CONFIG" rate_limit enabled false
assert_scalar "$PRODUCTION_CONFIG" traffic_policies enabled true
assert_scalar "$PRODUCTION_CONFIG" traffic_policies mode local
assert_scalar "$CLUSTER_A_CONFIG" traffic_policies mode redis
assert_scalar "$CLUSTER_B_CONFIG" traffic_policies mode redis

extract_pipeline_section "$CLUSTER_A_CONFIG" traffic_policies "$TMP_DIR/cluster-a-traffic-policies.yaml"
extract_pipeline_section "$CLUSTER_B_CONFIG" traffic_policies "$TMP_DIR/cluster-b-traffic-policies.yaml"
if ! cmp -s \
  "$TMP_DIR/cluster-a-traffic-policies.yaml" \
  "$TMP_DIR/cluster-b-traffic-policies.yaml"; then
  echo "production cluster gateway traffic-policy settings differ" >&2
  diff -u \
    "$TMP_DIR/cluster-a-traffic-policies.yaml" \
    "$TMP_DIR/cluster-b-traffic-policies.yaml" >&2 || true
  exit 1
fi

gateway_check "$PRODUCTION_CONFIG"
gateway_check "$CLUSTER_A_CONFIG"
gateway_check "$CLUSTER_B_CONFIG"

echo "traffic policy deployment check passed"
