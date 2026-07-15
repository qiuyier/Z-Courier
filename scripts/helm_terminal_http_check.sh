#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
VALUES_FILE="$CHART_DIR/examples/values-terminal-http.yaml"
MTLS_VALUES_FILE="$CHART_DIR/examples/values-terminal-http-mtls.yaml"
HELM_IMAGE="${ZCOURIER_HELM_IMAGE:-alpine/helm:3.17.3}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-helm-terminal-http.XXXXXX")"
DEFAULT_MANIFEST="$TMP_DIR/default.yaml"
HTTP_MANIFEST="$TMP_DIR/http.yaml"
MTLS_MANIFEST="$TMP_DIR/mtls.yaml"
CA_MANIFEST="$TMP_DIR/ca.yaml"
HTTP_CONFIG="$TMP_DIR/z-courier.yaml"

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
helm_command lint "$CHART_DIR" -f "$MTLS_VALUES_FILE"
helm_command template z-courier "$CHART_DIR" >"$DEFAULT_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$VALUES_FILE" >"$HTTP_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$MTLS_VALUES_FILE" >"$MTLS_MANIFEST"
helm_command template z-courier "$CHART_DIR" -f "$MTLS_VALUES_FILE" \
  --set-string downlink.terminal.publisher.http.tls.secret.clientCertKey= \
  --set-string downlink.terminal.publisher.http.tls.secret.clientKeyKey= \
  >"$CA_MANIFEST"

if helm_command lint "$CHART_DIR" -f "$MTLS_VALUES_FILE" \
  --set-string downlink.terminal.publisher.http.tls.secret.clientKeyKey= \
  >/dev/null 2>&1; then
  echo "Helm schema unexpectedly accepted a client certificate without its private key" >&2
  exit 1
fi

if grep -Fq 'ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET' "$DEFAULT_MANIFEST"; then
  echo "default Helm manifest unexpectedly references terminal webhook secret" >&2
  exit 1
fi
if grep -Fq 'terminal-webhook-tls' "$DEFAULT_MANIFEST"; then
  echo "default Helm manifest unexpectedly references terminal webhook TLS material" >&2
  exit 1
fi

grep -Fq 'type: "http"' "$HTTP_MANIFEST"
grep -Fq 'secret: "${ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET}"' "$HTTP_MANIFEST"
grep -Fq 'name: ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET' "$HTTP_MANIFEST"
grep -Fq 'key: terminal-webhook-hmac-secret' "$HTTP_MANIFEST"
if grep -Fq 'terminal-webhook-tls' "$HTTP_MANIFEST"; then
  echo "ordinary HTTP Helm manifest unexpectedly references terminal webhook TLS material" >&2
  exit 1
fi

grep -Fq 'ca_file: "/run/secrets/terminal-webhook/ca.crt"' "$MTLS_MANIFEST"
grep -Fq 'client_cert_file: "/run/secrets/terminal-webhook/tls.crt"' "$MTLS_MANIFEST"
grep -Fq 'client_key_file: "/run/secrets/terminal-webhook/tls.key"' "$MTLS_MANIFEST"
grep -Fq 'server_name: "terminal-events.example.internal"' "$MTLS_MANIFEST"
grep -Fq 'name: terminal-webhook-tls' "$MTLS_MANIFEST"
grep -Fq 'mountPath: "/run/secrets/terminal-webhook"' "$MTLS_MANIFEST"
grep -Fq 'secretName: "z-courier-terminal-webhook-tls"' "$MTLS_MANIFEST"
grep -Fq 'defaultMode: 0440' "$MTLS_MANIFEST"
grep -Fq 'fsGroup: 101' "$MTLS_MANIFEST"
grep -Fq 'key: "ca.crt"' "$MTLS_MANIFEST"
grep -Fq 'key: "tls.crt"' "$MTLS_MANIFEST"
grep -Fq 'key: "tls.key"' "$MTLS_MANIFEST"

grep -Fq 'ca_file: "/run/secrets/terminal-webhook/ca.crt"' "$CA_MANIFEST"
if grep -Fq 'client_cert_file:' "$CA_MANIFEST" || grep -Fq 'client_key_file:' "$CA_MANIFEST"; then
  echo "custom-CA Helm manifest unexpectedly renders an mTLS client key pair" >&2
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
' "$HTTP_MANIFEST" >"$HTTP_CONFIG"

if [[ ! -s "$HTTP_CONFIG" ]]; then
  echo "failed to extract z-courier.yaml from rendered Helm manifest" >&2
  exit 1
fi

env \
  POD_NAME=z-courier-0 \
  POD_NAMESPACE=default \
  ZCOURIER_AUTH_PROVIDER_SHARED_TOKEN=helm-check-auth-provider \
  ZCOURIER_INTERNAL_HMAC_SECRET=helm-check-internal-hmac-secret-0123456789 \
  ZCOURIER_PEER_HMAC_SECRET=helm-check-peer-hmac-secret-0123456789 \
  ZCOURIER_POSTGRES_PASSWORD=helm-check-postgres \
  ZCOURIER_REDIS_PASSWORD=helm-check-redis \
  ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET=helm-check-terminal-webhook-secret-0123456789 \
  ZCOURIER_UPSTREAM_INTERNAL_TOKEN=helm-check-upstream-token \
  go run ./cmd/gateway -config "$HTTP_CONFIG" -check-config

echo "helm terminal HTTP publisher check passed"
