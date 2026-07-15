#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/z-courier-compose-terminal-tls.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

assert_mount_count() {
  local manifest="$1"
  local expected="$2"
  local actual

  actual="$(awk '
    $1 == "target:" && $2 == "/run/secrets/terminal-webhook" {
      getline
      if ($1 == "read_only:" && $2 == "true") {
        count++
      }
    }
    END { print count + 0 }
  ' "$manifest")"
  if [[ "$actual" != "$expected" ]]; then
    echo "expected $expected read-only terminal webhook TLS mounts in $manifest, got $actual" >&2
    exit 1
  fi
}

cd "$ROOT_DIR"

docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  config >"$TMP_DIR/production-default.yaml"
docker compose \
  --env-file deploy/production/.env.example \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.terminal-webhook-tls.yml \
  config >"$TMP_DIR/production-tls.yaml"
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  config >"$TMP_DIR/cluster-default.yaml"
docker compose \
  --env-file deploy/production-cluster/.env.example \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.terminal-webhook-tls.yml \
  config >"$TMP_DIR/cluster-tls.yaml"

assert_mount_count "$TMP_DIR/production-default.yaml" 0
assert_mount_count "$TMP_DIR/production-tls.yaml" 1
assert_mount_count "$TMP_DIR/cluster-default.yaml" 0
assert_mount_count "$TMP_DIR/cluster-tls.yaml" 2

echo "compose terminal webhook TLS mount check passed"
