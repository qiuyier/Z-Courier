#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_ID="$(date +%s)-$$"

source "$ROOT_DIR/scripts/e2e_env.sh"
e2e_start_gateway

go run ./cmd/e2e \
  -device-id "e2e-device-$RUN_ID" \
  -expect-policy-name integration-reliable \
  "$@"

echo "running public Go SDK integration verifier..."
go run ./cmd/sdke2e \
  -device-id "sdk-e2e-device-$RUN_ID" \
  -expect-policy-name integration-reliable

echo "running public PHP SDK integration verifier..."
ZCOURIER_E2E_REUSE_GATEWAY=1 bash scripts/php_sdk_e2e.sh
