#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
VALUES_FILE="$CHART_DIR/examples/values-hmac-rotation.yaml"
HELM_IMAGE="${ZCOURIER_HELM_IMAGE:-alpine/helm:3.17.3}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-helm-hmac-rotation.XXXXXX")"
DEFAULT_MANIFEST="$TMP_DIR/default.yaml"
ROTATION_MANIFEST="$TMP_DIR/rotation.yaml"
ROTATION_CONFIG="$TMP_DIR/z-courier.yaml"

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

cd "$ROOT_DIR"

helm_command lint "$CHART_DIR" -f "$VALUES_FILE"
helm_command template z-courier "$CHART_DIR" >"$DEFAULT_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$VALUES_FILE" >"$ROTATION_MANIFEST"

if grep -Fq 'ZCOURIER_INTERNAL_HMAC_PREVIOUS_SECRET' "$DEFAULT_MANIFEST" ||
  grep -Fq 'ZCOURIER_PEER_HMAC_PREVIOUS_SECRET' "$DEFAULT_MANIFEST"; then
  echo "default Helm manifest unexpectedly contains HMAC rotation keys" >&2
  exit 1
fi

# These assertions intentionally match unexpanded runtime placeholders.
# shellcheck disable=SC2016
grep -Fq '"prod-internal-2026-02": "${ZCOURIER_INTERNAL_HMAC_SECRET}"' "$ROTATION_MANIFEST"
# shellcheck disable=SC2016
grep -Fq '"prod-internal-2026-01": "${ZCOURIER_INTERNAL_HMAC_PREVIOUS_SECRET}"' "$ROTATION_MANIFEST"
grep -Fq 'key_id: "prod-peer-2026-02"' "$ROTATION_MANIFEST"
# shellcheck disable=SC2016
grep -Fq '"prod-peer-2026-02": "${ZCOURIER_PEER_HMAC_SECRET}"' "$ROTATION_MANIFEST"
# shellcheck disable=SC2016
grep -Fq '"prod-peer-2026-01": "${ZCOURIER_PEER_HMAC_PREVIOUS_SECRET}"' "$ROTATION_MANIFEST"
grep -Fq 'name: ZCOURIER_INTERNAL_HMAC_PREVIOUS_SECRET' "$ROTATION_MANIFEST"
grep -Fq 'key: internal-hmac-previous-secret' "$ROTATION_MANIFEST"
grep -Fq 'name: ZCOURIER_PEER_HMAC_PREVIOUS_SECRET' "$ROTATION_MANIFEST"
grep -Fq 'key: peer-hmac-previous-secret' "$ROTATION_MANIFEST"

if helm_command template z-courier "$CHART_DIR" -f "$VALUES_FILE" \
  --set-string 'internalHttp.auth.hmac.additionalKeys[0].keyID=prod-internal-2026-02' \
  >/dev/null 2>&1; then
  echo "Helm unexpectedly accepted a duplicate internal HMAC key ID" >&2
  exit 1
fi
if helm_command template z-courier "$CHART_DIR" -f "$VALUES_FILE" \
  --set-string 'cluster.peer.auth.hmac.additionalKeys[0].keyID=prod-peer-2026-02' \
  >/dev/null 2>&1; then
  echo "Helm unexpectedly accepted a duplicate peer HMAC key ID" >&2
  exit 1
fi

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
' "$ROTATION_MANIFEST" >"$ROTATION_CONFIG"

if [[ ! -s "$ROTATION_CONFIG" ]]; then
  echo "failed to extract z-courier.yaml from HMAC rotation manifest" >&2
  exit 1
fi

env \
  POD_NAME=z-courier-0 \
  POD_NAMESPACE=default \
  ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=helm-check-auth-provider \
  ZCOURIER_INTERNAL_HMAC_SECRET=helm-check-internal-new-secret-0123456789 \
  ZCOURIER_INTERNAL_HMAC_PREVIOUS_SECRET=helm-check-internal-old-secret-0123456789 \
  ZCOURIER_PEER_HMAC_SECRET=helm-check-peer-new-secret-0123456789 \
  ZCOURIER_PEER_HMAC_PREVIOUS_SECRET=helm-check-peer-old-secret-0123456789 \
  ZCOURIER_POSTGRES_PASSWORD=helm-check-postgres \
  ZCOURIER_REDIS_PASSWORD=helm-check-redis \
  ZCOURIER_UPSTREAM_INTERNAL_TOKEN=helm-check-upstream-token \
  go run ./cmd/gateway -config "$ROTATION_CONFIG" -check-config

echo "helm HMAC rotation check passed"
