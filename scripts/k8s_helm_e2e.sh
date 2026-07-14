#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/z-courier"
VALUES_FILE="$CHART_DIR/examples/values-k8s-e2e.yaml"
DEPS_FILE="$CHART_DIR/examples/k8s-e2e-dependencies.yaml"
CLUSTER_NAME="${K8S_HELM_E2E_CLUSTER:-z-courier-helm-e2e}"
NAMESPACE="${K8S_HELM_E2E_NAMESPACE:-z-courier-e2e}"
RELEASE_NAME="${K8S_HELM_E2E_RELEASE:-z-courier}"
IMAGE="${K8S_HELM_E2E_IMAGE:-z-courier-gateway:kind-e2e}"
HELM_IMAGE="${K8S_HELM_E2E_HELM_IMAGE:-alpine/helm:3.17.3}"
POSTGRES_IMAGE="${K8S_HELM_E2E_POSTGRES_IMAGE:-postgres:16-alpine}"
REDIS_IMAGE="${K8S_HELM_E2E_REDIS_IMAGE:-redis:8-alpine}"
NSQ_IMAGE="${K8S_HELM_E2E_NSQ_IMAGE:-nsqio/nsq:latest}"
KEEP_CLUSTER="${K8S_HELM_E2E_KEEP_CLUSTER:-0}"
RENDERED_FILE="${K8S_HELM_E2E_RENDERED_FILE:-/tmp/z-courier-kind-helm-e2e.yaml}"
CLIENT_LOCAL_PORT="${K8S_HELM_E2E_CLIENT_PORT:-9899}"
INTERNAL_A_LOCAL_PORT="${K8S_HELM_E2E_INTERNAL_A_PORT:-18082}"
INTERNAL_B_LOCAL_PORT="${K8S_HELM_E2E_INTERNAL_B_PORT:-18083}"
POSTGRES_LOCAL_PORT="${K8S_HELM_E2E_POSTGRES_PORT:-15432}"
NSQD_LOCAL_PORT="${K8S_HELM_E2E_NSQD_PORT:-24150}"
E2E_TIMEOUT="${K8S_HELM_E2E_TIMEOUT:-120s}"
INTERNAL_HMAC_KEY_ID="${K8S_HELM_E2E_INTERNAL_HMAC_KEY_ID:-e2e-internal-2026-01}"
INTERNAL_HMAC_SECRET="${K8S_HELM_E2E_INTERNAL_HMAC_SECRET:-kind-internal-hmac-secret-0123456789abcdef}"
RUN_ID="$(date +%s)-$$"
PORT_FORWARD_PIDS=()

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 127
  fi
}

cleanup() {
  for pid in "${PORT_FORWARD_PIDS[@]:-}"; do
    if [ -n "$pid" ]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done

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
    -f deploy/helm/z-courier/examples/values-k8s-e2e.yaml >"$RENDERED_FILE"
}

load_dependency_image() {
  local image="$1"
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "pulling dependency image: $image"
    docker pull "$image"
  fi
  echo "loading dependency image into kind: $image"
  kind load docker-image "$image" --name "$CLUSTER_NAME"
}

check_port_forward_alive() {
  local pid="$1"
  local log_file="$2"
  if ! kill -0 "$pid" >/dev/null 2>&1; then
    echo "port-forward process exited unexpectedly: $log_file" >&2
    cat "$log_file" >&2 || true
    return 1
  fi
}

start_port_forward() {
  local name="$1"
  local resource="$2"
  local mapping="$3"
  local log_file="/tmp/z-courier-k8s-e2e-${name}.log"

  echo "port-forwarding $resource $mapping"
  kubectl -n "$NAMESPACE" port-forward "$resource" "$mapping" >"$log_file" 2>&1 &
  local pid="$!"
  PORT_FORWARD_PIDS+=("$pid")
  sleep 1
  check_port_forward_alive "$pid" "$log_file"
}

wait_http() {
  local url="$1"
  local expected="$2"
  local deadline=$((SECONDS + 90))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if curl -fsS "$url" 2>/dev/null | grep -q "$expected"; then
      return 0
    fi
    sleep 1
  done

  echo "timed out waiting for $url to contain $expected" >&2
  return 1
}

wait_tcp() {
  local host="$1"
  local port="$2"
  local deadline=$((SECONDS + 60))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if bash -c ":</dev/tcp/$host/$port" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "timed out waiting for TCP $host:$port" >&2
  return 1
}

need_cmd docker
need_cmd kind
need_cmd kubectl
need_cmd curl
need_cmd go

trap cleanup EXIT

cd "$ROOT_DIR"

echo "building gateway image: $IMAGE"
docker build --tag "$IMAGE" .

echo "creating kind cluster: $CLUSTER_NAME"
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER_NAME" --wait 90s

echo "loading gateway image into kind: $IMAGE"
kind load docker-image "$IMAGE" --name "$CLUSTER_NAME"
load_dependency_image "$POSTGRES_IMAGE"
load_dependency_image "$REDIS_IMAGE"
load_dependency_image "$NSQ_IMAGE"

echo "creating namespace: $NAMESPACE"
kubectl create namespace "$NAMESPACE"

echo "applying e2e dependencies"
kubectl -n "$NAMESPACE" apply -f "$DEPS_FILE"
kubectl -n "$NAMESPACE" rollout status deployment/postgres --timeout=180s
kubectl -n "$NAMESPACE" rollout status deployment/redis --timeout=180s
kubectl -n "$NAMESPACE" rollout status deployment/nsqd --timeout=180s
kubectl -n "$NAMESPACE" wait \
  --for=condition=ready pod \
  -l "app.kubernetes.io/component=e2e-dependency" \
  --timeout=180s

echo "rendering Helm chart"
helm_template

echo "applying chart manifest"
kubectl -n "$NAMESPACE" apply -f "$RENDERED_FILE"

echo "waiting for gateway StatefulSet"
kubectl -n "$NAMESPACE" rollout status "statefulset/$RELEASE_NAME" --timeout=240s
kubectl -n "$NAMESPACE" wait \
  --for=condition=ready pod \
  -l "app.kubernetes.io/instance=$RELEASE_NAME" \
  --timeout=240s

echo "checking gateway pods and services"
kubectl -n "$NAMESPACE" get pods -o wide
kubectl -n "$NAMESPACE" get svc "$RELEASE_NAME-client" "$RELEASE_NAME-internal" "$RELEASE_NAME-headless" postgres redis nsqd

start_port_forward "postgres" "svc/postgres" "$POSTGRES_LOCAL_PORT:5432"
start_port_forward "nsqd" "svc/nsqd" "$NSQD_LOCAL_PORT:4150"
start_port_forward "gateway-a-internal" "pod/$RELEASE_NAME-0" "$INTERNAL_A_LOCAL_PORT:18080"
start_port_forward "gateway-b-internal" "pod/$RELEASE_NAME-1" "$INTERNAL_B_LOCAL_PORT:18080"
start_port_forward "gateway-b-client" "pod/$RELEASE_NAME-1" "$CLIENT_LOCAL_PORT:8999"

wait_http "http://127.0.0.1:$INTERNAL_A_LOCAL_PORT/readyz" "ready"
wait_http "http://127.0.0.1:$INTERNAL_B_LOCAL_PORT/readyz" "ready"
wait_http "http://127.0.0.1:$INTERNAL_A_LOCAL_PORT/metrics" "z_courier"
wait_http "http://127.0.0.1:$INTERNAL_B_LOCAL_PORT/metrics" "z_courier"
wait_tcp "127.0.0.1" "$CLIENT_LOCAL_PORT"
wait_tcp "127.0.0.1" "$NSQD_LOCAL_PORT"

echo "running e2e verifier against Helm chart"
go run ./cmd/e2e \
  -gateway-port "$CLIENT_LOCAL_PORT" \
  -internal-url "http://127.0.0.1:$INTERNAL_A_LOCAL_PORT" \
  -metrics-url "http://127.0.0.1:$INTERNAL_A_LOCAL_PORT/metrics,http://127.0.0.1:$INTERNAL_B_LOCAL_PORT/metrics" \
  -postgres-dsn "postgres://zcourier:zcourier@127.0.0.1:$POSTGRES_LOCAL_PORT/zcourier?sslmode=disable" \
  -internal-auth-mode hmac \
  -internal-hmac-key-id "$INTERNAL_HMAC_KEY_ID" \
  -internal-hmac-secret "$INTERNAL_HMAC_SECRET" \
  -device-id "k8s-e2e-device-$RUN_ID" \
  -online-push-delay 2s \
  -require-cluster-metrics \
  -expect-route-node "$RELEASE_NAME-1" \
  -expect-route-internal-url "http://$RELEASE_NAME-1.$RELEASE_NAME-headless.$NAMESPACE.svc.cluster.local:18080" \
  -expect-session-url "http://127.0.0.1:$INTERNAL_B_LOCAL_PORT" \
  -expect-session-node "$RELEASE_NAME-1" \
  -check-reconnect-retry \
  -expect-policy-name k8s-reliable \
  -check-terminal-event \
  -terminal-nsqd-address "127.0.0.1:$NSQD_LOCAL_PORT" \
  -expect-terminal-policy k8s-terminal \
  -timeout "$E2E_TIMEOUT" \
  "$@"

echo "k8s helm e2e passed"
