# Z-Courier

A high-performance message push gateway based on the `zinx` network framework.

[中文文档](README.zh-CN.md)

## Features
- High concurrency based on zinx
- Lightweight and efficient message routing
- Fast JSON serialization based on Sonic
- Structured logging based on Zap
- Pluggable static, remote HTTP, or local JWT/JWKS authentication with bounded
  verification caching, timeout, key rotation, and in-flight protection
- Built-in HTTP upstream routing with static or refreshable DNS endpoint
  discovery and bounded transport failover, plus NSQ and adapter interfaces
  for gRPC, Kafka, NATS, and custom targets
- Reliable downlink delivery with ACK, retry, and idempotency hooks
- Two-node cluster delivery with Redis online routes and gateway peer push
- Reconnect-safe delivery: disconnected clients keep messages pending until the
  next bind flushes them
- Prometheus metrics and Grafana dashboards for authentication, ingress,
  upstream, downlink, cluster, retry, capacity, and load-test paths
- Public Go SDKs for protocol encoding, persistent client connections, and
  backend downlink/message APIs
- Composer-compatible PHP protocol and blocking client SDK with binary-safe
  packet/frame codecs
- Optional timestamped HMAC authentication and replay protection for internal
  backend requests and gateway peer push, with separate key rings
- Production-oriented Docker image build path with CI smoke validation
- Static config validation, admin diagnostics, dependency checks, safe diagnosis
  bundles, Prometheus alert rules, Alertmanager examples, and production
  Grafana dashboards
- Optional embedded Web admin console for read-only operations, diagnostics,
  session/route/message inspection, and guarded downlink repair actions
- Reviewed Nginx client TCP TLS and Console HTTPS references, a standard Caddy
  Console HTTPS reference, exact public route allowlists, and an optional
  separate machine mTLS listener
- Graceful shutdown with readiness drain and cluster route cleanup
- MIT Licensed

## Architecture

See [docs/architecture.md](docs/architecture.md) for the initial open-source
middleware architecture.

V2 cluster design is tracked in
[docs/v2-cluster-architecture.md](docs/v2-cluster-architecture.md).

Release history is tracked in [CHANGELOG.md](CHANGELOG.md).

Chinese usage, architecture, configuration, protocol, SDK, deployment, and
operations documentation starts at [README.zh-CN.md](README.zh-CN.md) and
[docs/zh-CN/README.md](docs/zh-CN/README.md).

The historical V2 release-candidate guide is
[docs/v2-release-candidate.md](docs/v2-release-candidate.md).

The completed V3 authentication and integration phase is described in
[docs/v3-auth-integration.md](docs/v3-auth-integration.md). Its `v0.3.0`
release and upgrade checklist is in
[docs/v3-release.md](docs/v3-release.md).

V4 client SDK and cross-language protocol work is specified in
[docs/v4-client-sdk.md](docs/v4-client-sdk.md). The first non-Go SDK is
PHP, with shared wire fixtures and live gateway E2E coverage. Practical rollout
guidance is in [docs/v4-sdk-migration.md](docs/v4-sdk-migration.md), with
runnable clients under [examples/go-client](examples/go-client/main.go) and
[sdk/php/examples](sdk/php/examples/client.php). The `v0.4.0` release scope and
checklist are in [docs/v4-release.md](docs/v4-release.md).

V5 deployment and operations work is tracked in
[docs/v5-roadmap.md](docs/v5-roadmap.md). Operator CLI and admin API notes are
in [docs/v5-admin-ops.md](docs/v5-admin-ops.md). Production troubleshooting and
operations flows are collected in
[docs/v5-production-runbook.md](docs/v5-production-runbook.md). The `v0.5.0`
release scope and checklist are in [docs/v5-release.md](docs/v5-release.md).

V6 Kubernetes and Helm work is tracked in
[docs/v6-kubernetes-helm.md](docs/v6-kubernetes-helm.md). Helm chart versioning
rules are in [docs/v6-helm-versioning.md](docs/v6-helm-versioning.md), and the
`v0.6.0` release guide is in [docs/v6-release.md](docs/v6-release.md).

V7 Docker image publishing is tracked in
[docs/v7-docker-image-release.md](docs/v7-docker-image-release.md), and the
`v0.7.0` release guide is in [docs/v7-release.md](docs/v7-release.md).

V8 production operations governance is tracked in
[docs/v8-roadmap.md](docs/v8-roadmap.md), and the `v0.8.0` release-prep
checklist is in [docs/v8-release.md](docs/v8-release.md).

V9 Web admin console work is tracked in
[docs/v9-roadmap.md](docs/v9-roadmap.md), and the `v0.9.0` release-prep
checklist is in [docs/v9-release.md](docs/v9-release.md).

V10 admin console operations work is tracked in
[docs/v10-roadmap.md](docs/v10-roadmap.md), and the `v0.10.0` release guide is
in [docs/v10-release.md](docs/v10-release.md). The Chinese release
retrospective and usage guide is in
[docs/zh-CN/v10-release-retrospective.md](docs/zh-CN/v10-release-retrospective.md).

V11 production control-plane planning is tracked in
[docs/v11-roadmap.md](docs/v11-roadmap.md), and the `v0.11.0` release guide is
in [docs/v11-release.md](docs/v11-release.md). It covers persistent audit,
Redis-backed admin sessions, cluster-wide console views, remote operation
safety, browser-session hardening, bounded admin data APIs, upgrade, and
rollback.

V12 reliable delivery planning is tracked in
[docs/v12-roadmap.md](docs/v12-roadmap.md). It focuses on idempotent downlink
submission, metadata-driven delivery policies, terminal failure handling,
dead-letter events, and bounded fair retry behavior. PostgreSQL upgrade,
rollback, and release acceptance guidance is in
[docs/v12-release.md](docs/v12-release.md), with a Chinese version in
[docs/zh-CN/v12-release.md](docs/zh-CN/v12-release.md).

V13 signed terminal-webhook planning is tracked in
[docs/v13-roadmap.md](docs/v13-roadmap.md). It extends the existing durable,
body-free terminal-event outbox with an opt-in signed HTTP publisher for
deployments that do not use NSQ.

V14 production transport-security planning is tracked in
[docs/v14-roadmap.md](docs/v14-roadmap.md). It covers private-CA and mTLS
terminal webhooks, TLS-capable Go and PHP clients, reviewed Nginx and Caddy edge
templates, secret-file deployment, and zero-downtime key and certificate
rotation. Runnable edge references and their security boundaries are documented
in [deploy/edge/README.md](deploy/edge/README.md), with a Chinese guide in
[docs/zh-CN/edge-proxy.md](docs/zh-CN/edge-proxy.md). The ordered HMAC and
certificate rotation procedure, including rollback, is in
[docs/rotation-runbook.md](docs/rotation-runbook.md) and
[docs/zh-CN/rotation-runbook.md](docs/zh-CN/rotation-runbook.md). The V14
release acceptance matrix is in [docs/v14-release.md](docs/v14-release.md) and
[docs/zh-CN/v14-release.md](docs/zh-CN/v14-release.md).

V15 upstream service-discovery planning is tracked in
[docs/v15-roadmap.md](docs/v15-roadmap.md). It defines static and DNS endpoint
discovery, bounded health-aware failover, and operational visibility without
changing the opaque client-message protocol. The release acceptance guides are
[docs/v15-release.md](docs/v15-release.md) and
[docs/zh-CN/v15-release.md](docs/zh-CN/v15-release.md).

V16 traffic-policy planning is tracked in
[docs/v16-roadmap.md](docs/v16-roadmap.md). It defines bounded local admission,
optional Redis-backed cluster quotas, explicit overload semantics, and
operator-facing observability without inspecting business message bodies.

## Quick Start

Run the single-node integration verifier from the repository root:

```bash
bash scripts/e2e.sh
```

Run the Docker-free two-HTTP-upstream discovery verifier:

```bash
bash scripts/e2e_discovery.sh
```

It starts two controlled HTTP backends and a real gateway process, connects
through the public TCP Go SDK, and verifies round-robin selection, bounded
pre-response failover, stable `MessageID` and body bytes across attempts,
failed-endpoint cooldown and recovery, and non-replay of received HTTP `500`
responses. The script uses TCP `9931`, internal HTTP `18191`, and backend
ports `18192` and `18193`; they must be free.

Run the Docker-free local traffic-policy verifier:

```bash
bash scripts/e2e_traffic_policy.sh
```

It proves burst, refill, policy precedence, bounded-key overload, idle
eviction, and rejection-before-forwarding through the real TCP gateway path.
The script uses TCP `9941`, internal HTTP `18201`, and backend port `18202`.

Run the Docker-backed two-gateway Redis traffic-policy verifier:

```bash
bash scripts/e2e_traffic_policy_redis.sh
```

It starts a dedicated disposable Redis container and two gateways, then proves
one ClientID cannot exceed a shared quota by switching nodes, Redis outages
fail closed without forwarding, and enforcement recovers without restarting
either gateway. It uses TCP `9951`/`9952`, internal HTTP `18211`/`18213`,
backend `18212`, and Redis `16389`.

`scripts/e2e.sh` starts PostgreSQL, NSQ, Prometheus, Alertmanager, Grafana, and the
gateway, then validates:

- offline downlink queueing with PostgreSQL
- client bind and offline message flush
- online push and client delivery ACK
- upstream forwarding to NSQ
- Prometheus metrics exposure
- public Go SDK bind, upstream ACK, downlink automatic ACK, TLS reconnect, and
  continued delivery through an ephemeral private-CA TLS edge
- public PHP SDK bind, upstream ACK, downlink automatic ACK, TLS reconnect with
  a fresh SessionID, and continued delivery through the same TLS edge

Run only the PHP SDK live-gateway verifier with the same local integration
environment:

```bash
bash scripts/php_sdk_e2e.sh
```

Run the two-node cluster verifier:

```bash
bash scripts/e2e_cluster.sh
```

It starts two local gateway processes sharing PostgreSQL and Redis, connects the
test client to `gateway-b`, sends `/internal/push` to `gateway-a`, and verifies
that HMAC-signed peer push delivers the message across nodes. It also verifies
an overlapping terminal-webhook HMAC rotation: `gateway-a` signs with the old
key while `gateway-b` signs with the new key, and the receiver accepts and
identifies both. The verifier also covers debug route/session APIs, disconnect
-> queued retry -> reconnect flush, NSQ upstream publishing, and
cluster/retry/signature metrics.

The cluster verifier is the fastest way to confirm the current multi-node
behavior:

```text
gateway-a internal HTTP: http://127.0.0.1:18182
gateway-b internal HTTP: http://127.0.0.1:18183
client TCP target:       127.0.0.1:9902
```

Useful manual admin commands after starting the two-node stack:

```bash
go run ./cmd/admin overview \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin diagnostics \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin check \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -probe-timeout 2s

go run ./cmd/admin diagnose \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -output reports/diagnose/gateway-a.json

go run ./cmd/admin routes \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token

go run ./cmd/admin route \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device

go run ./cmd/admin sessions \
  -internal-url http://127.0.0.1:18183 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device

go run ./cmd/admin messages \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -status failed
```

`overview` summarizes the queried gateway node. `diagnostics` returns a richer
runtime troubleshooting snapshot. `check` actively probes configured runtime
dependencies. `diagnose` collects a safe JSON bundle for incident reports.
`routes` shows enabled MsgID route ranges. `route` answers where the cluster
would send a client/device. `sessions` answers which sessions are local to the
gateway node you queried. The web console also exposes a Cluster Routes scope
that reads the online route registry so one gateway console can inspect clients
connected to another node. `message` and `messages` inspect persisted downlink
delivery state.

Local service URLs and the manual workflow are documented in
[deploy/local/README.md](deploy/local/README.md).

The first production-oriented gateway image path is documented in
[deploy/production/README.md](deploy/production/README.md).

The two-node production cluster reference is documented in
[deploy/production-cluster/README.md](deploy/production-cluster/README.md).

The first Kubernetes Helm chart is in
[deploy/helm/z-courier](deploy/helm/z-courier), with the `v0.6.0` deployment
plan in [docs/v6-kubernetes-helm.md](docs/v6-kubernetes-helm.md), chart
versioning in [docs/v6-helm-versioning.md](docs/v6-helm-versioning.md), and the
release checklist in [docs/v6-release.md](docs/v6-release.md).

The production runbook for health checks, admin inspection, failed-message
repair, cluster routing, dependency failures, HMAC failures, and Prometheus
queries is in [docs/v5-production-runbook.md](docs/v5-production-runbook.md).

For a full release check, run:

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

For a faster local pass that avoids Docker-backed and long-running checks, run
`bash scripts/release_check.sh`. This is the checklist used before creating a
version tag.

## Release Status

V2 was published as `v0.2.0`. Implemented behavior:

- Shared Redis online route registry for `client_id + device_id`
- Gateway peer push with `POST /internal/cluster/push`
- PostgreSQL reliable downlink store with retry, ACK timeout, and claim leasing
- Bind-time pending flush and reconnect-safe delivery
- Internal debug APIs for route lookup and local sessions
- Multi-node E2E and reconnect/retry metrics assertions
- Load-test smoke and manual load-test GitHub Actions workflows

GitHub Actions includes unit tests, local E2E, cluster E2E, load-test smoke, and
non-blocking load-test baseline comparisons in workflow summaries.

The V3 implementation for `v0.3.0` is complete. It adds configurable static,
remote HTTP, and local JWT/JWKS verification; bounded auth caching and metrics;
public protocol and backend Go SDKs; and optional HMAC signing for backend and
cluster peer internal requests. See [docs/go-sdk.md](docs/go-sdk.md),
[docs/v3-auth-integration.md](docs/v3-auth-integration.md), and
[docs/v3-release.md](docs/v3-release.md).

V4 was published as `v0.4.0`. It adds a high-level Go client, a PHP
protocol/client SDK, shared cross-language golden fixtures, SDK E2E coverage,
and runnable migration examples. See [docs/v4-client-sdk.md](docs/v4-client-sdk.md),
[docs/v4-sdk-migration.md](docs/v4-sdk-migration.md), and
[docs/v4-release.md](docs/v4-release.md).

V5 was published as `v0.5.0`. It added production-oriented Docker deployment
artifacts, single-node and two-node production Compose references, admin
inspection and message repair APIs, audit logs, and the production runbook.
See [docs/v5-roadmap.md](docs/v5-roadmap.md),
[docs/v5-production-runbook.md](docs/v5-production-runbook.md), and
[docs/v5-release.md](docs/v5-release.md).

V6 was published as `v0.6.0`. It adds the first Kubernetes Helm chart, kind
smoke and E2E validation, Helm release assets, GHCR OCI chart publishing, and
chart versioning guidance. See
[docs/v6-kubernetes-helm.md](docs/v6-kubernetes-helm.md),
[docs/v6-helm-versioning.md](docs/v6-helm-versioning.md), and
[docs/v6-release.md](docs/v6-release.md).

V7 was published as `v0.7.0`. It focuses on publishing the gateway image
itself to GHCR, then wiring that official image into the Helm production path.
See [docs/v7-docker-image-release.md](docs/v7-docker-image-release.md) and
[docs/v7-release.md](docs/v7-release.md).

V8 was published as `v0.8.0`, with `v0.8.1` aligning Helm chart metadata and
default image tags. It focuses on production operations governance: static
configuration validation, runtime diagnostics, admin diagnosis bundles,
dependency checks, Prometheus alert rules, Alertmanager examples,
production-signal dashboards, readiness drain visibility, upstream route health
state, and retry jitter. See [docs/v8-roadmap.md](docs/v8-roadmap.md) and
[docs/v8-release.md](docs/v8-release.md).

V9 was published as `v0.9.0`, with `v0.9.1` adding the Chinese documentation
set. It adds the optional embedded Web admin console for overview, routes,
sessions, messages, dependency checks, diagnostics, diagnosis bundles, metrics
context, and guarded downlink repair. See
[docs/v9-roadmap.md](docs/v9-roadmap.md) and
[docs/v9-release.md](docs/v9-release.md).

V10 was published as `v0.10.0`. It turns the admin console from an
inspection surface into a controlled operations surface: short-lived admin
sessions, read-only/operator permissions, session operations, downlink debug
pushes, retry/offline queue views, audit trail, console UX hardening, and
browser smoke coverage. See [docs/v10-roadmap.md](docs/v10-roadmap.md) and
[docs/v10-release.md](docs/v10-release.md). Chinese release notes and usage
guidance are available in
[docs/zh-CN/v10-release-retrospective.md](docs/zh-CN/v10-release-retrospective.md).

V11 targets `v0.11.0` and makes the admin control plane more production-ready
for clustered deployments: persistent audit, Redis-backed admin sessions,
cluster-wide views, safe remote operations, CSRF hardening, bounded admin data
pagination, and admin storage observability. See
[docs/v11-roadmap.md](docs/v11-roadmap.md) and
[docs/v11-release.md](docs/v11-release.md).

V12 is the `v0.12.0` planning track. It returns to the reliable delivery path:
idempotent backend submission, policy-selected retry, terminal failure events,
queue capacity, and fair retry behavior, all without inspecting business
payloads. See [docs/v12-roadmap.md](docs/v12-roadmap.md).

## Development

Z-Courier targets Go 1.26.

Run tests:

```bash
go test ./...
```

Run the PHP protocol conformance suite without third-party dependencies:

```bash
php sdk/php/tests/run.php
```

The single-node E2E command also runs the public Go and PHP SDK verifiers
against the same live gateway.

Run a small upstream load test against a local gateway:

```bash
go run ./cmd/loadtest \
  -mode upstream \
  -port 9899 \
  -token e2e-token \
  -clients 100 \
  -messages 10 \
  -upstream-msg-id 2001
```

`cmd/loadtest` suppresses Zinx internal connection logs by default so the output
focuses on the summary. Pass `-zinx-log` when debugging low-level client
connections.

The summary includes ACK/HTTP latency percentiles and grouped failure reasons:

```text
latency upstream_ack count=1000 min=1.2ms avg=3.4ms p50=2.8ms p95=8.9ms p99=12.1ms max=20.4ms
failure reasons:
  overloaded=42
```

Run a downlink HTTP load test:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18082 \
  -clients 100 \
  -messages 10 \
  -http-concurrency 50
```

Run a sustained downlink load test for dashboard observation:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -duration 60s \
  -rate 500 \
  -clients 100 \
  -http-concurrency 100 \
  -report reports/loadtest-downlink.json
```

When `-duration` is set, `-rate` is required and means target messages per
second. Without `-duration`, `cmd/loadtest` keeps the original finite behavior
and sends `clients * messages` messages.

Pass `-report` to save the same summary as JSON. Missing report directories are
created automatically.

Run a local load-test smoke check:

```bash
bash scripts/loadtest_smoke.sh
```

The smoke script starts the local PostgreSQL and NSQ dependencies, starts one
integration gateway, then runs conservative upstream and downlink threshold
checks. Reports are written to `reports/loadtest-smoke/`.

GitHub Actions also runs this smoke check after validation. The JSON reports and
gateway log are uploaded as the `loadtest-smoke-reports` artifact. The workflow
also writes a Markdown summary directly into the GitHub Actions summary page.

Run a larger manual load test locally with the same script used by the manual
GitHub Actions workflow:

```bash
LOADTEST_MODE=downlink \
LOADTEST_DURATION=60s \
LOADTEST_RATE=500 \
LOADTEST_CLIENTS=100 \
LOADTEST_HTTP_CONCURRENCY=100 \
LOADTEST_MIN_QPS=450 \
LOADTEST_MAX_P95_MS=50 \
LOADTEST_MAX_P99_MS=100 \
LOADTEST_MAX_ERROR_RATE=0.01 \
  bash scripts/loadtest_manual.sh
```

In GitHub Actions, open **Manual Load Test**, choose **Run workflow**, then set
the same inputs there. The report and gateway log are uploaded as an artifact
named `manual-loadtest-<mode>`, and a Markdown summary is shown on the workflow
run page.

Convert existing JSON reports into Markdown locally:

```bash
go run ./cmd/loadreport \
  -output reports/loadtest-manual/summary.md \
  reports/loadtest-manual/*.json
```

Compare a current report with a saved baseline:

```bash
mkdir -p reports/baseline/loadtest-manual
cp reports/loadtest-manual/downlink.json reports/baseline/loadtest-manual/downlink.json

go run ./cmd/loadcompare \
  -base reports/baseline/loadtest-manual/downlink.json \
  -current reports/loadtest-manual/downlink.json \
  -output reports/loadtest-manual/compare.md
```

`cmd/loadcompare` compares QPS, error rate, p95, and p99 for reports with the
same mode. For GitHub Actions, prefer workflow-specific baselines such as
`reports/baseline/loadtest-smoke/downlink.json` or
`reports/baseline/loadtest-manual/downlink.json`. The workflows fall back to
`reports/baseline/<mode>.json` for compatibility. Comparisons are informational
only; they append to the workflow summary and do not fail the workflow.

Use threshold flags when you want the load test to behave like an acceptance
check. The command exits with code 1 when any check fails, but still writes the
JSON report first:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -internal-url http://127.0.0.1:18182 \
  -duration 60s \
  -rate 500 \
  -clients 100 \
  -http-concurrency 100 \
  -min-qps 450 \
  -max-p95-ms 50 \
  -max-p99-ms 100 \
  -max-error-rate 0.01 \
  -report reports/loadtest-downlink.json
```

`-max-error-rate 0` means no failures are allowed. The error rate is reported as
a fraction, so `0.01` means 1%.

For real online downlink delivery, keep matching clients connected first. Without
online clients, the gateway still accepts the request and writes the offline
retry path when storage is enabled.

Run the local single-node integration verifier:

```bash
bash scripts/e2e.sh
```

It starts PostgreSQL, NSQ, Prometheus, Alertmanager, Grafana, the gateway, and
validates the reliable downlink path with PostgreSQL storage. See
[deploy/local/README.md](deploy/local/README.md) for the manual workflow and
local URLs.

Run the local cluster integration verifier:

```bash
bash scripts/e2e_cluster.sh
```

The cluster verifier includes shared PostgreSQL capacity and retry fairness,
Redis-backed admin sessions, persistent admin audit, terminal HTTP publication
with old/new HMAC-key overlap, and a cross-node guarded bulk requeue that must
return one success plus one capacity failure without losing the successful item.

Start the gateway:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

Zinx loads its framework config from `conf/zinx.json` by default. To use another
file, set `ZINX_CONFIG_FILE_PATH` before starting the gateway.

Z-Courier loads its gateway config from `configs/z-courier.yaml` by default. You
can override it with `-config` or the `ZCOURIER_CONFIG` environment variable.
See [docs/configuration.md](docs/configuration.md) for all current config
fields.

The current binary packet format is documented in
[docs/protocol.md](docs/protocol.md).

The gateway always registers `MsgID = 1000` for AUTH/BIND and `MsgID = 2` for
downlink delivery ACKs, then registers additional Zinx routes from
`route_msg_ids` and enabled upstream route ranges. The router decodes the
Z-Courier protocol packet from the Zinx request body, verifies the token, binds
the connection on AUTH/BIND, logs the metadata, forwards business packets when
an upstream route matches, and returns an ACK packet with `MsgID = 1`.

The default development token is:

```text
dev-token -> client_id: dev-client
```

`DeviceID` must be present in the protocol packet. `ClientID` from the packet is
treated as a claimed identity only; the gateway binds the session using the
identity returned by token verification.

The internal downlink API listens on `127.0.0.1:18080` by default:

```bash
curl -X POST http://127.0.0.1:18080/internal/push \
  -H 'Content-Type: application/json' \
  -H 'X-ZCourier-Internal-Token: dev-internal-token' \
  -d '{
    "client_id": "dev-client",
    "device_id": "device-1",
    "msg_id": 2001,
    "message_id": "message-1",
    "trace_id": "trace-1",
    "ack_required": true,
    "body": "aGVsbG8="
  }'
```

`body` is base64-encoded in the HTTP JSON request because the gateway treats it
as opaque bytes.

Downlink push requests are accepted into the configured downlink store before
the gateway tries to deliver them to an online client. The default development
store is in-memory:

```yaml
downlink:
  storage:
    type: memory
```

Use PostgreSQL for durable downlink messages:

```yaml
downlink:
  storage:
    type: postgres
    postgres:
      dsn: postgres://user:pass@postgres:5432/z_courier?sslmode=disable
      auto_migrate: true
      max_open_conns: 10
      max_idle_conns: 5
      conn_max_lifetime: 30m
  delivery:
    retry_interval: 5s
    retry_delay: 30s
    retry_jitter: 5s
    ack_timeout: 30s
    retry_lease: 30s
    max_attempts: 5
    scan_limit: 100
    bind_flush_limit: 100
    retry_fairness:
      enabled: true
      candidate_multiplier: 4
  retention:
    delivered_ttl: 24h
    failed_ttl: 168h
    discarded_ttl: 168h
    cleanup_interval: 1h
    cleanup_limit: 1000
```

When the target client is online, `/internal/push` returns `200` with
`delivery_state = sent`. When the message is stored but the client is offline,
it returns `202` with `delivery_state = queued`. The `memory` store is useful
for local development, but queued messages are lost on gateway restart.

Stored messages are retried in three ways:

- The retry worker scans due pending messages every `retry_interval`.
- Failed sends are scheduled after `retry_delay` plus a random `retry_jitter`
  window to avoid synchronized retry bursts across clients and gateway nodes.
- When a client session is newly bound, the gateway immediately flushes pending
  messages for that `client_id` + `device_id`, up to `bind_flush_limit`.
- Sent messages that require client ACK are retried after `ack_timeout` if the
  ACK does not arrive.
- With `retry_fairness.enabled`, bounded scans select due work round-robin by
  `client_id + device_id`, preventing one hot offline device from monopolizing
  the retry batch.

Failed retry attempts update `attempts`, `last_error`, and `next_retry_at`.
After `max_attempts`, the message is marked `failed`.

The retention worker deletes expired terminal messages from the downlink store:
`delivered` after `delivered_ttl`, `failed` after `failed_ttl`, and
`discarded` after `discarded_ttl`. Pending and sent messages are not deleted by
retention cleanup.

Failed messages can be inspected and manually handled through the admin CLI.
Mutation commands require explicit `-confirm`, and discard also requires a
reason:

```bash
go run ./cmd/admin messages \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -status failed

go run ./cmd/admin message \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1

go run ./cmd/admin requeue \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -confirm

go run ./cmd/admin discard \
  -internal-url http://127.0.0.1:18080 \
  -internal-token dev-internal-token \
  -message-id message-1 \
  -reason "handled manually" \
  -confirm
```

Clients confirm downlink delivery by sending a Z-Courier protocol packet with
`MsgID = 2`. The ACK packet is authenticated like other client packets and is
not forwarded upstream. Its JSON body is:

```json
{
  "message_id": "message-1",
  "code": "delivered"
}
```

Run the development client in another terminal:

```bash
go run ./cmd/devclient
```

The development client uses the public `pkg/sdk/client` package. It connects
with `dev-token`, performs `MsgID = 1000` AUTH/BIND for `device-1`, and prints
the accepted canonical binding. With both gateway and devclient running, the
`curl` command above should make the client print a `MsgID = 2001` packet whose
body is `hello`. Because the request sets `ack_required: true`, the SDK also
sends a `MsgID = 2` delivery ACK back to the gateway automatically.

To test upstream forwarding, start the development backend:

```bash
go run ./cmd/devbackend
```

Then set the `dev-http-upstream` route in `configs/z-courier.yaml` to
`enabled: true` and start the gateway:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

When `devclient` sends its bind packet with `MsgID = 1000`, the gateway will
bind the connection and ACK it locally. It will not forward the bind packet
upstream. Send a separate business packet in the route range, for example
`MsgID = 1001-1999`, to verify HTTP upstream forwarding:

```bash
go run ./cmd/devclient -upstream-msg-id 1001
```

To publish upstream packets into NSQ, enable or add an NSQ route:

```yaml
upstream:
  routes:
    - name: dev-nsq-upstream
      enabled: true
      msg_id_min: 2000
      msg_id_max: 2999
      target:
        type: nsq
        nsqd_addrs:
          - 127.0.0.1:4150
          - 127.0.0.1:4250
        topic: message_events
        write_timeout: 1s
        publish_mode: round_robin
        retry_attempts: 1
```

The NSQ message body is the same JSON envelope used by the HTTP upstream
adapter. Its `body` field is base64-encoded by JSON because the gateway treats
payload bytes as opaque data.

`addr` is still supported for a single `nsqd` node. Use `nsqd_addrs` for
multi-node producer publishing; `retry_attempts` makes the adapter try the next
configured `nsqd` when the first publish attempt fails.

The upstream gateway pipeline can be configured with client/MsgID allowlists,
blocklists, the legacy fixed-window per-client limiter, or bounded named
token-bucket traffic policies:

```yaml
pipeline:
  allowlist:
    client_ids: []
    msg_ids: []
  blocklist:
    client_ids: []
    msg_ids: []
  rate_limit:
    enabled: false
    max_requests: 100
    window: 1s
  traffic_policies:
    enabled: false
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: ""
    policies: []
```

The two limiter modes are mutually exclusive. See
[Configuration](docs/configuration.md#named-traffic-policies) for deterministic
policy selection, key-capacity behavior, and the current local-mode boundary.

Prometheus metrics are exposed from the internal HTTP server:

```bash
curl http://127.0.0.1:18080/metrics
```

The first metrics include ingress packet totals, rejected ingress packets,
upstream forwarding totals and latency, online sessions, downlink push totals,
downlink ACK totals and latency, and rate-limit rejects.

Start a local Prometheus + Alertmanager + Grafana monitoring stack:

```bash
docker compose -f deploy/monitoring/docker-compose.yml up -d
```

Prometheus is available at `http://127.0.0.1:9090`, Alertmanager is available
at `http://127.0.0.1:9093`, and Grafana is available at
`http://127.0.0.1:3000` with the default local credentials `admin` / `admin`.
See [deploy/monitoring/README.md](deploy/monitoring/README.md) for alert rules,
dashboards, and scrape-target details.

The embedded admin console can show monitoring shortcuts and PromQL snippets
when `admin_console.monitoring` is configured. The local development config
points the console at `http://127.0.0.1:19090` for Prometheus and
`http://127.0.0.1:13000` for Grafana when using
`deploy/local/docker-compose.yml`. These links are operator conveniences only;
Grafana remains the source of truth for historical charts and alert context.

## Project Structure
- `cmd/gateway`: Gateway entry point
- `cmd/admin`: Operator CLI for gateway overview, diagnostics, dependency
  checks, diagnosis bundles, route/session inspection, message status/list
  queries, and guarded message repair
- `cmd/devclient`: Public Go SDK-based client for manual end-to-end testing
- `cmd/sdke2e`: Automated live-gateway verifier for the public Go client SDK
- `cmd/devbackend`: Development backend and internal API debugging CLI
- `sdk/php`: Composer-compatible PHP protocol and client SDK
- `configs`: Z-Courier gateway configuration
- `conf`: Zinx runtime configuration
- `deploy`: Local deployment examples such as monitoring
- `docs`: Design and operation documents
- `internal/adapter`: Upstream target adapters
- `internal/auth`: Token verification interfaces and development verifier
- `internal/capacity`: In-flight capacity limiters for protected paths
- `internal/cluster`: Online route registries and cluster route types
- `internal/config`: Z-Courier config loading and conversion
- `internal/downlink`: Internal push API and online delivery service
- `internal/metrics`: Prometheus metric definitions and helpers
- `internal/pipeline`: Ingress gateway middleware chain
- `internal/protocol`: Packet codec and protocol types
- `internal/router`: MsgID route engine
- `internal/server`: Zinx server bootstrap
- `internal/session`: Connection binding and online state
