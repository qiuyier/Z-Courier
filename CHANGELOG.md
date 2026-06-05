# Changelog

All notable changes to Z-Courier are documented in this file.

The format follows the spirit of Keep a Changelog, and this project uses
semantic versioning after the first public MVP tag.

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
