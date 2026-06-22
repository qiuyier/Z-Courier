#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_ID="$(date +%s)-$$"

source "$ROOT_DIR/scripts/e2e_env.sh"

if ! command -v php >/dev/null 2>&1; then
  echo "php is required for the PHP SDK integration verifier" >&2
  exit 1
fi
if ! php -r 'exit(PHP_VERSION_ID >= 80200 ? 0 : 1);'; then
  echo "PHP 8.2 or newer is required" >&2
  exit 1
fi

if [[ "${ZCOURIER_E2E_REUSE_GATEWAY:-0}" != "1" ]]; then
  e2e_start_gateway
fi

php sdk/php/tests/e2e.php \
  --device-id="php-sdk-e2e-device-$RUN_ID" \
  "$@"
