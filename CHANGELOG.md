# Changelog

All notable changes to Z-Courier are documented in this file.

The format follows the spirit of Keep a Changelog, and this project uses
semantic versioning after the first public MVP tag.

## [Unreleased]

### Added

- V5 roadmap covering deployment artifacts, operations/admin APIs, additional
  SDKs, security deployment patterns, performance baseline governance, and
  `v0.5.0` completion criteria.
- Production-oriented gateway Dockerfile, build context ignore rules,
  deployment image documentation, and CI image build smoke validation.
- Single-node production reference deployment with gateway, PostgreSQL, Redis,
  NSQ, Prometheus, HMAC-protected internal HTTP, HTTP token verification, and
  durable downlink storage configuration.
- Two-node production cluster reference deployment with Redis online routes,
  shared PostgreSQL storage, shared NSQ upstream, HMAC peer push, and
  Prometheus scraping for both gateway nodes.
- Strict `${ENV_NAME}` expansion for gateway YAML configuration, production
  `.env.example` files, and gitignored real production `.env` files.
- Production reference smoke verifiers for single-node and two-node Compose
  stacks, with CI coverage for gateway readiness and Prometheus target health.

## [v0.4.0] - 2026-06-23

### Added

- V4 client SDK design covering the Zinx outer transport frame, the existing
  inner packet, shared cross-language protocol fixtures, a high-level Go
  client, and the first PHP protocol/client SDK.
- Initial `pkg/sdk/client` transport framing with bounded big-endian Zinx frame
  encoding, fragmented TCP stream reads, outer/inner MsgID validation, typed
  errors, and shared cross-language fixture coverage.
- Initial high-level Go client lifecycle with validated configuration, static or
  dynamic token providers, context-aware TCP dialing, AUTH/BIND negotiation,
  canonical binding identity, pre-ACK packet buffering, and interruptible
  shutdown.
- Persistent Go client read loop with concurrency-safe business sends, bounded
  inbound delivery, MessageID-based ACK correlation, typed ACK failures, raw
  receive, and deterministic pending-send failure on disconnect.
- Go client downlink handlers with automatic or manual `MsgID=2` delivery ACK,
  bounded process-local LRU de-duplication, panic-safe error reporting, and
  handler cancellation during shutdown.
- Opt-in Go client reconnect with bounded exponential backoff and jitter, fresh
  token and AUTH/BIND per attempt, readiness waiting, retry classification,
  cancellation on close, and no implicit replay of failed business sends.
- Public Go SDK-backed development client and live gateway E2E coverage for
  bind, upstream ACK, downlink automatic ACK, connection replacement,
  reconnect with a fresh session, and continued traffic after reconnect.
- Composer-compatible PHP 8.2 protocol SDK with immutable packet and ACK value
  objects, binary-safe inner and outer codecs, incremental TCP frame parsing,
  typed conformance errors, full 64-bit decimal handling, and shared Go/PHP
  fixture verification.
- Initial PHP blocking client lifecycle with validated configuration, static or
  callback token providers, native stream connections, AUTH/BIND negotiation,
  canonical binding identity, stable typed failures, timeouts, and idempotent
  close behavior.
- PHP business sends with canonical bound identity, generated message and trace
  identifiers, optional ACK waiting, MessageID correlation, typed rejection and
  timeout failures, and bounded preservation of interleaved downlink packets.
- PHP downlink receive and callback APIs with automatic or manual `MsgID=2`
  delivery ACK, callback failure isolation, receive timeouts that preserve the
  connection, and bounded process-local LRU de-duplication by MessageID.
- Opt-in PHP reconnect with bounded exponential backoff and jitter, fresh token
  and AUTH/BIND per attempt, deterministic failure classification, interruptible
  shutdown, receive-loop resumption, and no implicit replay of failed sends.
- Live PHP SDK gateway E2E coverage for bind, NSQ-routed upstream ACK, reliable
  downlink delivery ACK, connection replacement, fresh-session reconnect, and
  continued bidirectional traffic, with PHP 8.2 checks in GitHub Actions.
- Maximum-level PHPStan analysis for the PHP 8.2 SDK source, enforced in CI
  without an ignore baseline.
- Runnable Go and PHP persistent-client examples plus migration guidance for
  field ownership, token refresh, ACK handling, durable MessageID
  de-duplication, same-identity replacement, and safe rollout.
- V4 release guide covering scope, compatibility, runtime notes, release
  verification, release notes, and the `v0.4.0` tagging checklist.

### Changed

- `cmd/devclient` now uses `pkg/sdk/client` instead of directly depending on
  Zinx and manually implementing framing, bind, receive, and delivery ACK.

## [v0.3.0] - 2026-06-20

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
- Public `pkg/sdk/protocol` Go package with canonical packet encoding, reserved
  MsgIDs, ACK helpers, exported errors, golden wire-format coverage, and an
  internal compatibility facade.
- Public `pkg/sdk/backend` Go client for downlink push, batch, message status,
  list, requeue, and discard operations with typed errors, context deadlines,
  response limits, and canonical JSON contract types shared by the gateway.
- Optional backend-to-gateway HMAC-SHA256 request signing with timestamp and
  bounded nonce replay protection, multi-key rotation, SDK integration,
  low-cardinality verification metrics, and a cross-language canonical format.
- Optional gateway-to-gateway peer HMAC signing with an independent key ring,
  bounded replay protection, token-mode compatibility, E2E coverage, and
  dedicated verification metrics.
- Grafana panels for authentication request rate, success rate, latency,
  in-flight verification, cache activity, JWKS refresh health, and internal
  request signature results.

### Changed

- Authentication provider selection is configuration-driven while legacy
  `auth.static_tokens` files remain compatible.
- The two-node integration verifier now exercises HMAC-signed cluster peer push
  and requires successful peer signature metrics.

### Security

- Raw authentication tokens are not used as cache keys; bounded cache entries
  use SHA-256 digests.
- JWT mode accepts only explicitly configured asymmetric algorithms and keeps
  private signing keys outside the gateway.
- Internal HMAC modes cover method, path, canonical query, and exact body bytes,
  then enforce timestamp and nonce replay checks.

### Verified

- `actionlint`
- `go test ./...`
- focused race tests for signing, downlink, server, and configuration packages
- `go vet ./...`
- `bash scripts/e2e.sh`
- `bash scripts/e2e_cluster.sh`
- `bash scripts/loadtest_smoke.sh`

### Known Limitations

- Delivery remains at-least-once; clients must de-duplicate by `MessageID`.
- HMAC authenticates requests but does not encrypt them; production internal
  traffic still requires TLS, mTLS, or an encrypted service mesh.
- Nonce replay caches are process-local rather than shared across gateway nodes.
- SDK support is currently Go-first; other language SDKs are not included.

## [v0.2.0] - 2026-06-18

- Promoted the tested `v0.2.0-rc.1` commit to the stable `v0.2.0` tag without
  code changes.

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
