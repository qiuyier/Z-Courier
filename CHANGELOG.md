# Changelog

All notable changes to Z-Courier are documented in this file.

The format follows the spirit of Keep a Changelog, and this project uses
semantic versioning after the first public MVP tag.

## [Unreleased]

### Added

- V17 roadmap for bounded route-file hot reload, immutable route generations,
  in-flight request leases, failure-safe activation and retirement,
  node-local canary rollout, and operator-facing reload controls.

## [v0.16.0] - 2026-07-31

### Added

- V16 roadmap for named ingress traffic policies, bounded local token-bucket
  admission, optional Redis-backed cluster quotas, explicit overload behavior,
  observability, and production release acceptance.
- V16.1 deterministic named-policy selection by authenticated ClientID, MsgID,
  and enabled upstream route, plus a concurrency-safe local token bucket with
  bounded keys, idle eviction, strict ambiguity/migration validation, stable
  overload reasons, and bilingual configuration guidance.
- V16.2 Docker-free real-TCP integration coverage for local policy burst,
  refill, precedence, no-policy pass-through, bounded-key overload, and idle
  eviction, including stable rejected ACKs and proof that rejected packets do
  not reach the upstream; CI and release checks run the same verifier.
- V16.3.1 narrow quota-store contract with explicit allowed, rate-limited,
  overloaded, and admission-unavailable decisions; the existing bounded local
  token bucket now implements that contract without changing configuration or
  packet behavior, and handler/store tests cover delegation, failures, key
  partitioning, cancellation, concurrency, and idle eviction.
- V16.3.2 staged Redis quota configuration plus an atomic Lua Store using Redis
  server time, hashed client identities, bounded key TTL, hard operation
  timeouts, explicit fail-closed behavior without hidden retries, recovery
  without Store restart, and concurrent in-memory plus optional real-Redis
  shared-quota tests, providing the foundation activated by V16.3.3.
- V16.3.3 operational Redis traffic policies with startup health checking,
  complete Gateway construction/shutdown ownership, stable fail-closed
  `admission_unavailable` responses, and Docker-backed two-gateway E2E proving
  one cross-node quota, rejection-before-forwarding during Redis outages, and
  recovery without restarting either gateway.
- V16.4.1 low-cardinality Prometheus visibility for policy selection,
  quota-store outcomes and latency, and local live-key capacity. Metric labels
  are restricted to fixed enumerations and validated static policy names, with
  focused handler, Store-lifecycle, cardinality-boundary, and real TCP
  local/Redis exposition coverage.
- V16.4.2 sanitized process-local traffic-policy snapshots in admin diagnostics
  and diagnosis bundles, covering aggregate decisions, recent fixed-enum state,
  local key utilization, dependency status, and actionable warnings without
  exposing selector ClientIDs, Redis connection details, quota keys, packet
  bodies, or raw errors; local and Redis E2E now verify the snapshot boundary.
- V16.4.3 dedicated read-only Traffic Policies view in Console Diagnostics,
  covering disabled, local, Redis, degraded, and unavailable states, aggregate
  decisions, recent runtime events, local key utilization, and per-policy
  bucket summaries with real-gateway admin/readonly smoke coverage.
- V16.4.4 traffic-policy panels in both bundled Grafana dashboards, recording
  rules, low-noise alerts for Redis fail-closed decisions, sustained local-key
  capacity, overload, and high rate-limited ratios, promtool behavior tests,
  a synchronized Helm PrometheusRule example, and bilingual tuning, canary,
  outage, and rollback guidance.
- V16.5 production delivery closure with complete Helm traffic-policy values,
  strict schema and local/Redis examples, bounded-local single-node and
  Redis-shared cluster Compose references, deterministic source/built-image
  deployment validation, CI and release-check integration, real Redis
  quota-key expiration evidence, and bilingual upgrade, rollback, and release
  acceptance guidance.
- Helm chart `0.8.0` aligned with gateway `v0.16.0`, plus automated checks that
  keep Chart app metadata, production image values, and release tags in sync
  before GitHub Release assets or GHCR OCI packages are published.
- Bilingual English/Chinese titles and operator-facing legend prefixes across
  the bundled overview and production-signal Grafana dashboards, without
  changing panel IDs, PromQL, thresholds, units, or alert behavior.

## [v0.15.0] - 2026-07-28

### Added

- V15.1 HTTP upstream discovery configuration for mutually exclusive legacy
  URL, static endpoint-list, and DNS modes, with bounded refresh, failover, and
  unhealthy-cooldown validation plus English and Chinese operator guidance.
- V15.2.1 immutable static endpoint snapshots, concurrent round-robin
  selection, process-local unhealthy cooldown, and bounded opt-in transport
  failover that never replays a received HTTP response.
- V15.2.2 refreshable DNS A/AAAA discovery with immutable endpoint snapshots,
  last-known-good retention, retired-endpoint cleanup, logical HTTP Host and
  TLS SNI preservation, and lifecycle-aware resolver cancellation.
- V15.3.1 explicit forwarding failure classes and typed audit-safe metadata,
  bounded failover decisions, sanitized endpoint and attempt fields in
  structured logs, stable `upstream_failed` client ACKs, and prompt
  timeout/cancellation handling.
- V15.4.1 low-cardinality Prometheus visibility for static/DNS discovery
  refreshes, active and unhealthy endpoint counts, endpoint selection and
  cooldown behavior, classified attempt failures, per-message attempt counts,
  and terminal failover decisions without endpoint or error-text labels.
- V15.4.2 sanitized per-route discovery runtime snapshots in admin diagnostics
  and diagnosis bundles, covering current endpoint counts plus recent refresh,
  selection, cooldown, classified failure, forwarding, and failover state
  without placing addresses, hostnames, URLs, secrets, or raw errors in the
  discovery snapshot.
- V15.4.3 read-only discovery runtime visualization in Console Diagnostics,
  complete discovery/failover panels in both Grafana dashboards, and
  readiness-gated alerts for sustained empty discovery or actively unavailable
  endpoint sets with promtool behavior tests and bilingual runbook guidance.
- V15.4.4 production Compose static endpoint lists with bounded failover, Helm
  static and Kubernetes DNS discovery rendering plus strict schema coverage,
  deterministic source/built-image configuration checks, and bilingual
  deployment guidance.
- V15.5.1 Docker-free two-upstream integration coverage through the real TCP
  gateway path, proving initial round-robin selection, bounded pre-response
  transport failover with byte-identical message identity, failed-endpoint
  cooldown, endpoint recovery, and non-replay of received HTTP `500`
  responses; CI and release checks run the same verifier.
- V15.5.2 Kind Helm E2E coverage for Kubernetes Headless-Service DNS
  discovery: two backend Pod addresses resolve, one backend is replaced, DNS
  refreshes, and forwarding continues without restarting the gateway.

## [v0.14.0] - 2026-07-24

### Added

- V14 production transport-security roadmap covering terminal-webhook custom
  CA and mTLS, TLS-capable Go/PHP clients, constrained Nginx/Caddy edge
  templates, secret-file deployment, and reversible HMAC/certificate rotation.
- V14.1 terminal HTTP publisher TLS hardening with a dedicated TLS 1.2+
  transport, optional private CA, client-certificate authentication, strict
  startup/config validation, and real private-CA plus mTLS handshake tests.
- V14 production secret-file deployment wiring with opt-in Compose overrides,
  externally managed Helm TLS Secrets, conditional read-only mounts, schema
  validation, deterministic render checks, and bilingual operations guidance.
- V14.2 Go client SDK TLS with mandatory certificate verification, system or
  private CA roots, server-name overrides, injectable raw dialers, bounded
  handshakes, reconnect re-handshakes, runnable example flags, and real TLS
  identity/reconnect tests while plaintext remains the default.
- V14.2 PHP client TLS parity with system or private CA verification,
  server-name overrides, injectable raw connectors, bounded OpenSSL handshakes,
  reconnect re-handshakes, runtime-generated certificate tests, and example
  environment configuration.
- Private-CA TLS edge E2E for both public SDKs, covering AUTH/BIND, upstream,
  reliable downlink ACK, same-device replacement, TLS reconnect, and continued
  traffic without changing the gateway packet protocol.
- V14.3 Nginx single-node and cluster edge references for client TCP TLS and
  allowlisted Console HTTPS, standard Caddy automatic/local Console HTTPS,
  separate optional machine mTLS, generated disposable PKI, opt-in Compose
  overlays, bilingual deployment guidance, and CI/release smoke coverage.
- V14.4 HMAC rotation foundation with old/new overlap tests across internal
  HTTP, cluster peer, and terminal webhook boundaries, plus opt-in Helm
  `additionalKeys`, duplicate-key rejection, external Secret injection, a
  rotation-stage values example, and deterministic CI/release validation.

## [v0.13.0] - 2026-07-15

### Added

- V13 roadmap for an opt-in, signed HTTP publisher for the existing durable,
  body-free terminal-event outbox, with receiver de-duplication, cluster claim,
  configuration, operations, deployment, and release acceptance requirements.
- V13 signed HTTP terminal-event E2E coverage with a verifying receiver,
  per-event first-attempt failure injection, durable retry, stable event ID and
  body-exclusion assertions, and single-node plus two-node successful-delivery
  checks while Kubernetes retains the existing NSQ publication regression.
- V13 production deployment references for signed terminal webhooks: optional
  Compose secret injection, identical single/cluster configuration guidance,
  conditional Helm ConfigMap/StatefulSet/Secret rendering, values schema, and
  a dedicated CI/release render check that leaves `none` and `nsq` unchanged.

## [v0.12.0] - 2026-07-15

### Added

- V12 reliable-downlink idempotency foundation: immutable request identity
  fingerprints, atomic `created`/`existing`/`conflict` store outcomes, safe
  compatible replay, explicit HTTP `409 message_id_conflict`, and additive Go
  backend SDK response metadata. Single-node, two-node cluster, and public Go
  backend SDK E2E coverage verifies replay suppression and conflict behavior.
- V12.2 delivery policies with a legacy-compatible default, named inclusive
  MsgID-range selectors, immutable per-message policy snapshots, per-policy ACK
  deadlines, bounded exponential backoff and jitter, attempt/age exhaustion,
  PostgreSQL migration compatibility, and policy visibility across status APIs,
  the Go backend SDK, diagnostics, console views, and single/cluster E2E.
- V12.3.1 terminal failure and dead-letter events with atomic PostgreSQL outbox
  persistence, stable terminal reasons, optional direct NSQ publication,
  independent bounded publication retries and cluster claims, body-free
  versioned envelopes, status/SDK/diagnostics/console visibility, and
  single-node plus cluster E2E consumption checks.
- V12.4.1 reliable-downlink queue admission limits with global and per-device
  pending budgets, idempotency-first HTTP `429` rejection semantics, atomic
  memory and shared-PostgreSQL decisions, capacity-aware requeue, SDK metadata,
  diagnostics, Grafana/alert coverage, real PostgreSQL concurrency tests, and
  two-node cluster E2E.
- V12.5.1 guarded bulk requeue for up to 100 selected terminal failed messages,
  with independent capacity-aware execution, per-item HTTP `207` results,
  operator/admin permission enforcement, summary plus item audit events, Go
  backend SDK support, and a confirmation/result workflow in the admin console.
- V12.5.2 deterministic two-node PostgreSQL E2E for guarded bulk requeue. A
  Redis-backed admin session created on gateway-a executes on gateway-b, one
  item succeeds while the shared per-device budget rejects the next, both
  gateways observe the same persisted states and recorded policy, and
  PostgreSQL audit rows plus Prometheus outcomes are verified.
- V12.6.1 release-readiness foundation with one embedded, versioned PostgreSQL
  migration shared by automatic and manual upgrade paths; V11 upgrade and
  rollback-write compatibility integration coverage in cluster E2E; and
  English/Chinese schema, mixed-version, rollback, and release-matrix guidance.
- V12.6.2 production deployment parity for named delivery policies and terminal
  publication: production Compose references, Helm chart `0.7.0` values/schema,
  static production config validation, and kind E2E coverage for policy
  selection, exhaustion, NSQ terminal-event consumption, and persisted publish
  state while retaining disabled/`none` defaults. The local E2E gateway now
  runs as a directly tracked binary so cleanup cannot leave an orphan retry
  worker racing the following cluster fairness test.
- V12.6.3 GitHub Actions parity for production config validation. Every push now
  expands non-secret CI fixtures and statically checks the single-node plus both
  cluster gateway configs alongside development and integration configs. Load
  test scripts now run directly tracked gateway binaries so later release
  checks cannot be routed to an orphan process left behind by `go run`.
  Full release checks also reuse the already verified gateway image for
  production smoke stacks, avoiding redundant Dockerfile builds and registry
  metadata failures without changing standalone smoke behavior.

## [v0.11.0] - 2026-07-10

### Added

- V11 roadmap covering production-grade admin control-plane work, including
  persistent audit storage, Redis-backed admin sessions, cluster-wide console
  views, remote operation safety, CSRF hardening, and bounded admin data
  pagination.
- Optional PostgreSQL admin audit store with automatic schema creation,
  filtering, stable cursor pagination, diagnostics, health probes, metrics,
  dashboard panels, and alert rules.
- Optional Redis admin session store so one browser session can be recognized
  across gateway nodes, with TTL, logout invalidation, diagnostics, health
  probes, metrics, and cluster E2E coverage.
- Cluster route listing in the admin console, including owning gateway,
  internal address, route age, TTL, and local-versus-remote state.
- Cluster-aware downlink test-push preflight and delivery results with explicit
  local or peer delivery paths, origin and target gateway metadata, structured
  peer failure codes, and audited outcomes.
- Stable cursor pagination for admin audit events and stored downlink messages,
  including bounded API limits and forward/back navigation in the console.
- `v0.11.0` release guide covering upgrade, production admin storage,
  verification, security boundaries, known limitations, and rollback.

### Changed

- Admin diagnostics and diagnosis bundles now expose sanitized audit/session
  storage state and warn about memory-only audit or node-local sessions in
  clustered deployments.
- Helm chart `0.6.0` and production values align with the `v0.11.0` gateway
  image and its Redis session/PostgreSQL audit configuration.

### Fixed

- PostgreSQL admin audit and downlink auto-migrations now use transaction-level
  advisory locks, preventing concurrent gateway startup from racing while
  creating shared tables and indexes.

### Security

- Cookie-authenticated admin mutations now require a derived CSRF token,
  supported JSON content type, and valid browser origin or referer context.
- CSRF failures are denied, logged, audited, and exposed through Prometheus.
- Cross-node test push continues to use authenticated peer HTTP and reports
  peer authentication, timeout, configuration, and target failures explicitly.

## [v0.10.0] - 2026-07-05

### Added

- V10 roadmap covering the `v0.10.0` admin console operations planning track,
  including admin sessions, permissions, session operations, downlink debug
  pushes, retry/offline queue views, audit trail, and console UX hardening.
- Backend admin console session foundation with configurable TTL/cookie
  settings, `login`/`me`/`logout` internal endpoints, and session-cookie access
  for existing console admin/debug/message APIs.
- Admin console login flow that exchanges the internal token for a short-lived
  HTTP-only session cookie, restores existing sessions on refresh, and removes
  browser-side token persistence from normal console API calls.
- Admin console session roles and permission checks for `readonly`,
  `operator`, and `admin`, including server-side protection for guarded message
  repair actions and frontend action disabling from returned permission
  metadata.
- Helm chart metadata and production values for `0.5.0`, aligned with the
  `v0.10.0` gateway image and V10 admin console operations release.

## [v0.9.1] - 2026-07-02

### Added

- Chinese documentation set for `v0.9.1`, including project introduction,
  architecture, configuration, protocol, Go SDK usage, internal HTTP HMAC
  signing, local development, production deployment, Kubernetes/Helm,
  admin operations, and production troubleshooting.

### Fixed

- Corrected the protocol documentation to match the current public SDK wire
  format: the fixed header is 41 bytes and includes `Magic` plus 2-byte
  `Flags`.

## [v0.9.0] - 2026-07-02

### Added

- V9 roadmap covering the browser-based admin console, including overview,
  route inspection, session and cluster lookup, downlink message repair,
  diagnostics, dependency checks, deployment, and security boundaries for the
  `v0.9.0` planning track.
- Embedded Web admin console served from the gateway internal HTTP listener,
  with a React/Vite frontend under `web/admin`.
- Admin console overview, routes, sessions, messages, checks, and diagnostics
  pages backed by the existing internal admin APIs.
- Browser workflows for local session search, cluster route lookup, downlink
  message lookup/listing, guarded requeue/discard actions, active dependency
  checks, and sanitized diagnosis bundle download.
- Admin console monitoring links and PromQL context snippets for Prometheus
  and Grafana workflows.
- Docker image build packaging for compiled console assets.
- Helm and gateway configuration for opt-in console deployment.
- V9 release guide and reusable `scripts/release_check.sh` release verification
  helper.

### Changed

- CI now builds the admin console and verifies the Docker image contains
  `/app/web/admin/dist/index.html`.
- Production Compose and Helm documentation now clarify that `/console/` and
  `/internal/*` are private admin-plane endpoints.

### Security

- Console static responses now set CSP, no-referrer, nosniff, frame-deny,
  permission-deny, and cache-control headers.
- Production and Helm defaults keep the admin console disabled unless an
  operator opts in.

## [v0.8.1] - 2026-07-01

### Fixed

- Helm chart metadata now publishes chart version `0.3.0` with `appVersion`
  `v0.8.1`, so default Helm installs use the current gateway image instead of
  the previous `v0.7.0` image.
- The production Helm values example now pins
  `ghcr.io/qiuyier/z-courier-gateway:v0.8.1`.

## [v0.8.0] - 2026-06-30

### Added

- V8 roadmap covering production diagnostics, configuration validation, admin
  diagnosis bundles, alerting, dashboards, resilience controls, and completion
  criteria for the `v0.8.0` planning track.
- Static gateway config validation through `cmd/gateway -check-config`, with CI
  coverage for local, integration, and cluster example configurations.
- Runtime admin diagnostics for gateway identity, readiness/drain state,
  sanitized config summaries, dependency summaries, upstream route runtime
  state, cluster/session state, capacity indicators, and warnings.
- Active dependency checks through `cmd/admin check`.
- Safe diagnosis bundle collection through `cmd/admin diagnose`, including
  overview, diagnostics, active dependency checks, routes, failed message
  summaries, and optional client/device route plus local session inspection.
- Bundled Prometheus recording and alert rules, Alertmanager configuration and
  notification examples, and a Grafana production-signal dashboard.
- Readiness drain diagnostics and `z_courier_gateway_readiness` metrics.
- HTTP upstream route health tracking with healthy, degraded, and unavailable
  runtime states plus `z_courier_upstream_route_degraded`.
- Downlink retry jitter configuration to avoid synchronized retry bursts after
  delivery failures.
- V8 release guide covering scope, compatibility, verification commands,
  release notes, known boundaries, and tagging checklist.

### Changed

- CI now validates Prometheus rules/configs, Alertmanager configs, Grafana
  dashboard JSON, Docker Compose configs, Helm chart rendering, and gateway
  static configs as part of the main validation job.
- Gateway and admin diagnostics now expose clearer readiness and dependency
  state without leaking tokens, HMAC secrets, DSNs, or message bodies.

### Fixed

- PHP SDK receive loops now keep a blocking read posture across reconnect and
  ACK timeout transitions, avoiding spurious downlink receive timeouts in CI.
- Reliable online downlink pushes now claim newly stored messages before
  sending, preventing bind-time pending flushes from duplicating the same
  message during the save/send/mark-sent window.

## [v0.7.0] - 2026-06-26

### Added

- GitHub Actions Docker image release workflow for publishing the gateway image
  to GHCR on GitHub Release events or manual tag backfills, including post-push
  manifest platform verification.
- V7 Docker image release plan covering GHCR tags, stable `latest` behavior,
  multi-architecture image publishing, manual backfill, Helm defaults, and
  verification.
- V7 release guide covering Docker image publishing scope, compatibility,
  verification commands, release artifacts, GHCR image checks, Helm chart
  checks, release notes, and tagging checklist.

### Changed

- Docker image release publishing now uses Docker Buildx to publish
  `linux/amd64` and `linux/arm64` manifests for the same image tag.
- Helm chart version bumped to `0.2.0` because the default gateway image
  repository now points at the official GHCR package.
- Helm default and production example image repository now use
  `ghcr.io/qiuyier/z-courier-gateway`.

## [v0.6.0] - 2026-06-26

### Added

- Initial Kubernetes Helm chart for deploying Z-Courier gateway pods through a
  StatefulSet, headless peer-push service, separate client and internal
  services, ConfigMap-rendered gateway/Zinx config, existing Secret integration,
  and optional ServiceMonitor support.
- Production values example for the Helm chart without committing real secrets.
- GitHub Actions Helm chart validation for default values, the production
  values example, and the kind smoke values example.
- Local kind Helm smoke script and values file for verifying gateway startup
  from the Kubernetes chart.
- Numeric Kubernetes security context defaults for the gateway container so
  `runAsNonRoot` can be verified by kubelet.
- GitHub Actions Kubernetes smoke workflow for running the Helm chart in a kind
  cluster when Kubernetes deployment files or gateway runtime code changes.
- Helm values schema validation for default, production, and kind smoke values.
- GitHub Actions Helm chart packaging artifact for CI-produced `.tgz` chart
  archives.
- GitHub Release workflow for uploading the Helm chart archive and checksum as
  release assets.
- GitHub Release workflow for publishing the Helm chart to GHCR as an OCI Helm
  chart.
- Kubernetes NetworkPolicy example for production-style gateway ingress and
  dependency egress boundaries.
- Kubernetes Helm E2E script and manual workflow covering PostgreSQL downlink
  storage, Redis online routing, cross-pod peer push, NSQ upstream forwarding,
  reconnect retry, and metrics.
- Helm chart static-token auth rendering for self-contained Kubernetes E2E
  validation.
- HMAC internal HTTP support in the E2E verifier.
- Helm chart versioning guide covering chart/app version policy, compatibility
  matrix, OCI install semantics, and release checklist.
- V6 Kubernetes and Helm planning document for the `v0.6.0` milestone.
- V6 release guide covering Kubernetes/Helm scope, compatibility, production
  secret checks, verification commands, release artifacts, GHCR OCI checks, and
  tagging checklist.

## [v0.5.0] - 2026-06-25

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
- Read-only admin overview and upstream route APIs plus `cmd/admin` commands for
  gateway overview, route ranges, route lookup, local session inspection, and
  downlink message status/list queries.
- Guarded `cmd/admin requeue` and `cmd/admin discard` commands for single-message
  downlink repair, with explicit confirmation and discard reason requirements.
- Structured audit logs for admin downlink message mutations, including action,
  result, gateway node, message id, auth mode, HMAC key id, and HTTP status
  without exposing internal tokens or secrets.
- V5 production runbook covering health checks, admin CLI inspection,
  failed-message repair, cluster route diagnosis, dependency failures, HMAC
  failures, audit logs, Prometheus queries, and load-test baseline review.
- V5 release guide covering `v0.5.0` scope, upgrade notes, production secret
  checklist, verification commands, operational smoke checks, GitHub release
  notes, known boundaries, and tagging steps.

### Fixed

- Removed the external Dockerfile frontend directive so production image builds
  do not depend on pulling `docker/dockerfile:1` from a registry mirror.

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
