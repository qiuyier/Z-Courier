#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROMTOOL_IMAGE="${PROMTOOL_IMAGE:-prom/prometheus:latest}"
ALERTMANAGER_IMAGE="${ALERTMANAGER_IMAGE:-prom/alertmanager:latest}"

promtool() {
  docker run --rm \
    -v "$ROOT_DIR:/workspace:ro" \
    -v "$ROOT_DIR/deploy/monitoring/prometheus/rules:/etc/prometheus/rules:ro" \
    --entrypoint promtool \
    "$PROMTOOL_IMAGE" "$@"
}

amtool() {
  docker run --rm \
    -v "$ROOT_DIR:/workspace:ro" \
    --entrypoint amtool \
    "$ALERTMANAGER_IMAGE" "$@"
}

echo "checking Prometheus alert and recording rules..."
promtool check rules /workspace/deploy/monitoring/prometheus/rules/z-courier-alerts.yml

echo "checking Prometheus configs..."
promtool check config /workspace/deploy/monitoring/prometheus/prometheus.yml
promtool check config /workspace/deploy/local/prometheus/prometheus.yml
promtool check config /workspace/deploy/production/prometheus/prometheus.yml
promtool check config /workspace/deploy/production-cluster/prometheus/prometheus.yml

echo "checking Alertmanager config..."
amtool check-config /workspace/deploy/monitoring/alertmanager/alertmanager.yml
for config in "$ROOT_DIR"/deploy/monitoring/alertmanager/examples/*.yml; do
  amtool check-config "/workspace/${config#"$ROOT_DIR"/}"
done

echo "promtool and Alertmanager checks passed"
