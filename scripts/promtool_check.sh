#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROMTOOL_IMAGE="${PROMTOOL_IMAGE:-prom/prometheus:latest}"

promtool() {
  docker run --rm \
    -v "$ROOT_DIR:/workspace:ro" \
    -v "$ROOT_DIR/deploy/monitoring/prometheus/rules:/etc/prometheus/rules:ro" \
    --entrypoint promtool \
    "$PROMTOOL_IMAGE" "$@"
}

echo "checking Prometheus alert and recording rules..."
promtool check rules /workspace/deploy/monitoring/prometheus/rules/z-courier-alerts.yml

echo "checking Prometheus configs..."
promtool check config /workspace/deploy/monitoring/prometheus/prometheus.yml
promtool check config /workspace/deploy/local/prometheus/prometheus.yml
promtool check config /workspace/deploy/production/prometheus/prometheus.yml
promtool check config /workspace/deploy/production-cluster/prometheus/prometheus.yml

echo "promtool checks passed"
