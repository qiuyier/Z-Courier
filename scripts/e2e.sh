#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_ID="$(date +%s)-$$"

source "$ROOT_DIR/scripts/e2e_env.sh"
e2e_start_gateway

go run ./cmd/e2e \
  -device-id "e2e-device-$RUN_ID" \
  -expect-policy-name integration-reliable \
  -check-terminal-event \
  -terminal-publisher http \
  -terminal-webhook-failures 1 \
  -expect-terminal-policy integration-terminal \
  "$@"

e2e_start_tls_proxy

echo "running public Go SDK integration verifier..."
go run ./cmd/sdke2e \
  -tcp-address "$E2E_TLS_PROXY_ADDRESS" \
  -tls \
  -tls-ca-file "$E2E_TLS_CA_FILE" \
  -device-id "sdk-e2e-device-$RUN_ID" \
  -expect-policy-name integration-reliable

echo "running public PHP SDK integration verifier..."
ZCOURIER_E2E_REUSE_GATEWAY=1 bash scripts/php_sdk_e2e.sh \
  --tcp-address="$E2E_TLS_PROXY_ADDRESS" \
  --tls \
  --tls-ca-file="$E2E_TLS_CA_FILE"
