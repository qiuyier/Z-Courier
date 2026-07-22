#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

tracked_runtime_material="$(git ls-files | grep -E '(^|/)(secrets|certs)/.+\.(key|pem|crt|p12|pfx)$' || true)"
if [[ -n "$tracked_runtime_material" ]]; then
  echo "tracked runtime certificate or key material is forbidden:" >&2
  echo "$tracked_runtime_material" >&2
  exit 1
fi

tracked_pem="$(git grep -I -n -F -e '-----BEGIN ' -- . ':!testdata/**' ':!scripts/secret_boundary_check.sh' || true)"
if [[ -n "$tracked_pem" ]]; then
  echo "tracked PEM certificate or private-key content is forbidden:" >&2
  echo "$tracked_pem" >&2
  exit 1
fi

for ignored_directory in \
  deploy/production/secrets \
  deploy/production-cluster/secrets \
  deploy/edge/secrets; do
  if ! git check-ignore -q "$ignored_directory/.keep"; then
    echo "secret directory is not protected by .gitignore: $ignored_directory" >&2
    exit 1
  fi
done

echo "secret boundary check passed"
