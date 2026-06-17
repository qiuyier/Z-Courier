# V2 Release Candidate Guide

This document describes what the current Z-Courier V2 release candidate means,
how to verify it, and where the known boundaries are.

V2 keeps the core middleware rule: the gateway owns connection access,
metadata-based routing, delivery coordination, reliability state, and
observability. Business services own payload schemas and domain processing.

## Candidate Scope

The V2 release candidate includes:

- Explicit client `AUTH/BIND` using `MsgID = 1000`.
- Client downlink delivery ACK using `MsgID = 2`.
- Opaque binary packet forwarding based on protocol metadata.
- Upstream routing by `MsgID` range.
- Built-in HTTP and NSQ upstream adapters.
- Internal HTTP downlink APIs for push, batch push, message status, message
  listing, requeue, discard, debug route lookup, and debug local sessions.
- PostgreSQL reliable downlink storage with retry, ACK timeout, retry claims,
  bind-time pending flush, and retention cleanup.
- Redis-backed online route registry for `client_id + device_id`.
- Gateway-to-gateway peer push with `POST /internal/cluster/push`.
- Route refresh for quiet but still-connected clients.
- Graceful shutdown with readiness drain and route cleanup.
- Prometheus metrics and Grafana dashboard coverage for ingress, upstream,
  downlink, retry, cluster, capacity, cleanup, and load-test paths.
- Local and GitHub Actions verification for tests, E2E, cluster E2E, and
  load-test smoke.
- Manual load-test workflow with Markdown summaries, JSON artifacts, and
  non-blocking baseline comparisons.

## What RC Means

RC means the V2 feature set is closed enough for integration testing by early
users. It does not mean production hardening is complete.

Before tagging a release candidate, the exact commit should have:

```bash
actionlint
go test ./...
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
git diff --check
```

GitHub Actions should also be green on `main`.

## Quick Verification

Run the single-node verifier:

```bash
bash scripts/e2e.sh
```

It validates:

- offline downlink queueing with PostgreSQL
- bind-time pending flush
- online downlink push and client ACK
- NSQ upstream publishing
- core Prometheus metrics

Run the two-node verifier:

```bash
bash scripts/e2e_cluster.sh
```

It validates:

- client connected to `gateway-b`
- backend push sent to `gateway-a`
- Redis route lookup from `gateway-a`
- peer push from `gateway-a` to `gateway-b`
- debug route and local session APIs
- disconnect -> queued retry -> reconnect flush
- retry and cluster metrics

Run the load-test smoke verifier:

```bash
bash scripts/loadtest_smoke.sh
```

It validates conservative upstream and downlink load paths and writes reports
to `reports/loadtest-smoke/`.

## Formal Manual Load Baselines

The current committed manual baselines use a 60-second sustained run:

```bash
LOADTEST_MODE=upstream \
LOADTEST_DURATION=60s \
LOADTEST_RATE=100 \
LOADTEST_CLIENTS=100 \
LOADTEST_MESSAGES=10 \
LOADTEST_BODY_SIZE=128 \
  bash scripts/loadtest_manual.sh

LOADTEST_MODE=downlink \
LOADTEST_DURATION=60s \
LOADTEST_RATE=100 \
LOADTEST_CLIENTS=100 \
LOADTEST_MESSAGES=10 \
LOADTEST_HTTP_CONCURRENCY=50 \
LOADTEST_BODY_SIZE=128 \
  bash scripts/loadtest_manual.sh
```

The saved baseline files are:

```text
reports/baseline/loadtest-manual/upstream.json
reports/baseline/loadtest-manual/downlink.json
```

On the current local baseline machine, the 60-second reference results were:

```text
upstream: qps 99.08, error_rate 0.00%, p95 5.097ms, p99 10.775ms
downlink: qps 99.73, error_rate 0.00%, p95 7.574ms, p99 20.698ms
```

Treat these numbers as project baselines for comparison, not universal
performance promises. Hardware, Docker, OS scheduling, and local background
load can all move the numbers.

Smoke baselines intentionally remain separate:

```text
reports/baseline/loadtest-smoke/upstream.json
reports/baseline/loadtest-smoke/downlink.json
```

Do not compare smoke reports against manual 60-second baselines. They answer
different questions.

## GitHub Actions

The default CI workflow runs:

- `go test ./...`
- shell script syntax validation
- Docker Compose config validation
- Grafana dashboard JSON validation
- single-node E2E
- cluster E2E
- load-test smoke

The load-test smoke job appends a Markdown report to the GitHub Actions summary
and uploads JSON reports plus logs as artifacts.

The manual **Manual Load Test** workflow accepts mode, clients, duration, rate,
body size, concurrency, and threshold inputs. It also appends a Markdown summary
and baseline comparison to the workflow summary.

Baseline comparison is informational only. A comparison failure emits a warning
or summary note, but it does not fail the workflow.

## Known Boundaries

- Delivery is at-least-once, not exactly-once. Clients must de-duplicate by
  `MessageID`.
- The static token verifier is for local development and tests. Production
  deployments should replace it with backend-consistent token validation.
- Peer gateway authentication is token-based internal HTTP, not mTLS.
- SDKs are not included yet.
- Route hot reload and a full admin UI are not included yet.
- NSQ support is currently producer-side upstream publishing. Consumer-side
  business processing belongs to the backend system.
- The gateway does not inspect or validate business payload fields.
- PostgreSQL auto-migration is convenient for local and early integration use;
  mature production deployments should own migrations explicitly.

## Useful Debug Commands

Query where a client/device would be routed:

```bash
go run ./cmd/devbackend route \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -client-id e2e-client \
  -device-id e2e-device
```

List sessions local to one gateway process:

```bash
go run ./cmd/devbackend sessions \
  -internal-url http://127.0.0.1:18183 \
  -internal-token dev-internal-token \
  -client-id e2e-client
```

`route` and `sessions` are intentionally different. `route` answers the
cluster routing decision. `sessions` only shows connections local to the node
being queried.

Inspect load-test reports:

```bash
go run ./cmd/loadreport \
  -output reports/loadtest-manual/summary.md \
  reports/loadtest-manual/*.json

go run ./cmd/loadcompare \
  -base reports/baseline/loadtest-manual/downlink.json \
  -current reports/loadtest-manual/downlink.json \
  -output reports/loadtest-manual/compare-downlink.md
```

## Tagging Checklist

Before creating `v0.2.0-rc.1`:

1. Confirm local verification passes.
2. Push the exact commit to `main`.
3. Confirm GitHub Actions is green.
4. Review this document, `README.md`, and `CHANGELOG.md`.
5. Confirm baseline files are intentional.
6. Create and push the tag.

Suggested tag command:

```bash
git tag -a v0.2.0-rc.1 -m "v0.2.0-rc.1"
git push origin v0.2.0-rc.1
```
