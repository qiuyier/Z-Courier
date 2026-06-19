# Changelog

All notable changes to Z-Courier are documented in this file.

The format follows the spirit of Keep a Changelog, and this project uses
semantic versioning after the first public MVP tag.

## [Unreleased]

### Added

- V3 authentication and integration design with milestones for HTTP token
  verification, JWT/JWKS, auth caching and metrics, Go SDKs, and internal
  request hardening.
- Configurable remote HTTP token verifier with typed status mapping, strict
  response validation, timeout, redirect rejection, and in-flight protection.
- Bounded SHA-256-keyed authentication cache with positive and deterministic
  invalid-token caching, principal-expiry limits, and cache metrics.
- Local JWT verification with explicit asymmetric algorithm allowlists,
  issuer/audience/time checks, strict JWKS parsing, background refresh, key
  rotation, stale-key fallback, and unknown-key refresh protection.
- Grafana panels for authentication request rate, success rate, latency,
  in-flight verification, cache activity, and JWKS refresh health.

## [v0.2.0-rc.1] - 2026-06-17

### Added

- Explicit `MsgID = 1000` AUTH/BIND flow before business packets.
- Two-node cluster delivery with Redis online routes and gateway peer push.
- Route refresh for quiet connected clients so online routes stay discoverable.
- PostgreSQL retry claiming with leases for multi-node retry workers.
- Internal cluster peer push endpoint `POST /internal/cluster/push`.
- Internal debug APIs for route lookup and local session listing.
- Reconnect-safe downlink delivery: messages attempted while a client is
  disconnected stay pending and flush after the next bind.
- Internal downlink batch push, message status, message listing, requeue, and
  discard helpers.
- Capacity protection for upstream forwarding and internal HTTP push paths.
- Graceful shutdown with readiness drain and cluster route cleanup.
- Cluster, retry, cleanup, capacity, unique-online-client, and load-test-facing
  Prometheus metrics.
- Multi-node local E2E verifier through `scripts/e2e_cluster.sh`.
- Load-test smoke script, manual load-test workflow, Markdown report generator,
  and baseline comparison tool.
- GitHub Actions CI for validation, E2E, cluster E2E, and load-test smoke.
- Workflow-specific load-test baselines under `reports/baseline/`.
- V2 release-candidate documentation.

### Changed

- README now treats the cluster verifier as the primary V2 validation path.
- Load-test reports show `Messages` as `-` for sustained duration mode to avoid
  confusing finite message counts with rate-based runs.
- Upstream adapter wording now distinguishes built-in HTTP/NSQ support from
  future or custom adapter targets.

### Verified

- `actionlint`
- `go test ./...`
- `bash scripts/e2e.sh`
- `bash scripts/e2e_cluster.sh`
- `bash scripts/loadtest_smoke.sh`
- GitHub Actions on `main`

### Known Limitations

- This is a release candidate, not a production-stability guarantee.
- Delivery is at-least-once, not exactly-once.
- SDKs, route hot reload, and a full admin UI are not included yet.
- Production token validation should replace the local static verifier.
- Peer gateway authentication is token-based internal HTTP; mTLS is not
  implemented yet.

## [v0.1.0] - 2026-06-05

### Added

- Zinx-based TCP gateway entry point.
- Z-Courier binary packet codec with `ClientID`, `DeviceID`, `MsgID`,
  `SessionID`, `MessageID`, `TraceID`, `Token`, flags, and opaque body bytes.
- Static token verifier for local development and tests.
- Connection binding by verified `client_id` and packet `device_id`.
- Ingress pipeline with auth, allowlist/blocklist policy, per-client
  fixed-window rate limiting, session binding, access logs, and ACK handling.
- MsgID route engine with inclusive route ranges.
- HTTP upstream adapter.
- NSQ upstream adapter with multi-`nsqd` producer addresses, round-robin publish,
  and retry attempts.
- Internal HTTP API for downlink push.
- Online downlink delivery through active Zinx connections.
- Offline downlink storage with in-memory and PostgreSQL backends.
- PostgreSQL auto-migration for V1 downlink message storage.
- Downlink retry worker and bind-time pending message flush.
- Client downlink delivery ACK handling with delivered-state persistence.
- Prometheus metrics for ingress, upstream forwarding, online sessions,
  downlink push, downlink ACK, ACK latency, and rate-limit rejection.
- Local Prometheus and Grafana stack with a provisioned Z-Courier dashboard.
- Local PostgreSQL + NSQ + monitoring integration stack.
- One-command E2E verifier through `scripts/e2e.sh`.
- Development client and backend helpers.
- Architecture, configuration, protocol, local integration, and monitoring
  documentation.

### Changed

- Zinx framework logs default to Warn level in local configs to keep route
  registration output readable.
- Gateway startup logs compact registered MsgID ranges, for example
  `2`, `1000`, and `2000-2999`.

### Verified

- `go test ./...`
- `bash scripts/e2e.sh`

### Known Limitations

- This is an MVP release tag, not a production-stability guarantee.
- Cluster online-route storage is not implemented yet.
- Redis-backed discovery, route hot reload, admin APIs, and SDKs are not
  included in this release.
- Exactly-once delivery is not guaranteed; clients should de-duplicate by
  `MessageID`.
