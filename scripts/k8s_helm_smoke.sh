#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
VALUES_FILE="$CHART_DIR/examples/values-kind-smoke.yaml"
CLUSTER_NAME="${K8S_HELM_SMOKE_CLUSTER:-z-courier-helm-smoke}"
NAMESPACE="${K8S_HELM_SMOKE_NAMESPACE:-z-courier-smoke}"
RELEASE_NAME="${K8S_HELM_SMOKE_RELEASE:-z-courier}"
IMAGE="${K8S_HELM_SMOKE_IMAGE:-z-courier-gateway:kind-smoke}"
HELM_IMAGE="${K8S_HELM_SMOKE_HELM_IMAGE:-alpine/helm:3.17.3}"
KEEP_CLUSTER="${K8S_HELM_SMOKE_KEEP_CLUSTER:-0}"
RENDERED_FILE="${K8S_HELM_SMOKE_RENDERED_FILE:-/tmp/z-courier-kind-helm.yaml}"
PORT_FORWARD_PID=""

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 127
  fi
}

cleanup() {
  if [ -n "$PORT_FORWARD_PID" ]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  if [ "$KEEP_CLUSTER" != "1" ]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  else
    echo "keeping kind cluster: $CLUSTER_NAME"
  fi
}

helm_template() {
  if command -v helm >/dev/null 2>&1; then
    helm template "$RELEASE_NAME" "$CHART_DIR" -f "$VALUES_FILE" >"$RENDERED_FILE"
    return
  fi

  docker run --rm \
    -v "$ROOT_DIR:/work" \
    -w /work \
    "$HELM_IMAGE" \
    template "$RELEASE_NAME" deploy/helm/z-courier \
    -f deploy/helm/z-courier/examples/values-kind-smoke.yaml >"$RENDERED_FILE"
}

wait_http() {
  local url="$1"
  local expected="$2"
  local deadline=$((SECONDS + 60))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if curl -fsS "$url" 2>/dev/null | grep -q "$expected"; then
      return 0
    fi
    sleep 1
  done

  echo "timed out waiting for $url to contain $expected" >&2
  return 1
}

need_cmd docker
need_cmd kind
need_cmd kubectl
need_cmd curl

trap cleanup EXIT

cd "$ROOT_DIR"

echo "building gateway image: $IMAGE"
docker build --tag "$IMAGE" .

echo "creating kind cluster: $CLUSTER_NAME"
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER_NAME" --wait 90s

echo "loading image into kind: $IMAGE"
kind load docker-image "$IMAGE" --name "$CLUSTER_NAME"

echo "rendering Helm chart"
helm_template

echo "applying chart manifest"
kubectl create namespace "$NAMESPACE"
kubectl -n "$NAMESPACE" apply -f "$RENDERED_FILE"

echo "waiting for gateway StatefulSet"
kubectl -n "$NAMESPACE" rollout status "statefulset/$RELEASE_NAME" --timeout=180s
kubectl -n "$NAMESPACE" wait \
  --for=condition=ready pod \
  -l "app.kubernetes.io/instance=$RELEASE_NAME" \
  --timeout=180s

echo "checking services"
kubectl -n "$NAMESPACE" get svc "$RELEASE_NAME-client" "$RELEASE_NAME-internal" "$RELEASE_NAME-headless"

echo "port-forwarding internal service"
kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE_NAME-internal" 18080:18080 >/tmp/z-courier-kind-port-forward.log 2>&1 &
PORT_FORWARD_PID="$!"

wait_http "http://127.0.0.1:18080/readyz" "ready"
wait_http "http://127.0.0.1:18080/metrics" "z_courier"

echo "k8s helm smoke passed"
