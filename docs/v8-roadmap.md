# V8 Roadmap

V8 is the planning track for the next public milestone after `v0.7.0`. Its
target SemVer version is `v0.8.0`, not `v8.0.0`.

`v0.7.0` completed the release artifact loop: Docker image publishing, Helm
chart release assets, and GHCR OCI chart publishing. V8 should build on that by
making production operation, diagnosis, and release confidence easier after a
real deployment is running.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.8.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

Z-Courier already has a working gateway, reliable downlink path, cluster route
registry, SDKs, production image, Helm chart, metrics, dashboards, and release
workflows. The next useful step is not another large surface area. It is making
operators confident when they need to answer:

- Is this gateway instance healthy enough to receive traffic?
- Which dependency or route is failing?
- Is the effective runtime configuration what I intended?
- Is a client/device local, remote, offline, or stale?
- Are retries, backlogs, overloads, and peer pushes behaving normally?
- What data can I collect safely when opening an issue or debugging an outage?

V8 should therefore focus on production operations governance: diagnostics,
configuration validation, alerting, safer admin workflows, and release
confidence.

## Goals

- Add a production-grade diagnostic surface for runtime state and dependency
  status.
- Add explicit configuration validation so bad deployments fail before serving
  traffic.
- Improve admin and CLI workflows for collecting safe troubleshooting context.
- Provide Prometheus alert rules and dashboard panels for production signals,
  not only raw metrics.
- Strengthen release and upgrade confidence without changing the V1 wire
  protocol.
- Keep existing Go/PHP SDKs, Helm installs, Docker image paths, and
  configuration files backward compatible unless a migration is documented.

## Non-Goals

V8 does not target:

- A new packet version or incompatible wire-format change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.
- A browser admin console.
- A Kubernetes operator.
- Installing PostgreSQL, Redis, NSQ, Prometheus, Grafana, cert-manager, or a
  service mesh from the Z-Courier Helm chart.
- Replacing Zinx or changing the TCP connection model.
- New client SDK languages as a primary milestone.
- Built-in public TLS termination as the first transport-security milestone.

## Workstreams

### V8.1 Runtime Diagnostics

Purpose: give operators one authenticated place to inspect gateway runtime
state without jumping across Redis, PostgreSQL, NSQ, logs, and Prometheus for
the first answer.

Candidate work:

- Add an authenticated diagnostic endpoint under internal HTTP.
- Return gateway node identity, build version, commit, start time, uptime, and
  readiness/drain state.
- Return sanitized effective configuration summaries for auth, cluster,
  internal HTTP, downlink retry, upstream routes, and metrics.
- Return dependency checks with status, last error, and last observed latency
  for PostgreSQL, Redis, NSQ producers, HTTP upstreams, JWT/JWKS providers, and
  remote HTTP auth providers when configured.
- Return route freshness details for Redis-backed online routes, including
  local session count, unique client count, registry TTL, refresh interval, and
  stale-route indicators.
- Return capacity indicators such as internal HTTP in-flight, upstream
  in-flight, limiter rejections, retry backlog, and failed message count.

Acceptance criteria:

- A documented command answers whether a gateway is ready, degraded, or
  misconfigured.
- Sensitive values such as tokens, HMAC secrets, DSNs, and URL credentials are
  omitted or redacted.
- Diagnostics work in token and HMAC internal HTTP auth modes.
- The endpoint is covered by tests for redaction and stable response shape.

### V8.2 Configuration Validation

Purpose: catch unsafe or invalid deployments before they become confusing
runtime behavior.

Candidate work:

- Add a config validation command, for example `cmd/gateway -check-config`.
- Validate `z-courier.yaml` and `zinx.json` together.
- Detect upstream route overlaps, disabled routes with missing targets,
  reserved MsgID conflicts, invalid MsgID ranges, and unsupported route target
  types.
- Validate internal HTTP auth mode, HMAC key ring shape, JWT/JWKS settings,
  static token uniqueness, retry timing, Redis/PostgreSQL required fields, and
  cluster internal address rules.
- Add production guardrail warnings for wildcard internal HTTP exposure without
  HMAC, empty static token sets, missing Redis when cluster is enabled, and
  in-memory storage in production examples.
- Add Helm template or values checks where the chart can prevent obvious
  mistakes before runtime.

Acceptance criteria:

- CI can run config validation against local, integration, production, and Helm
  example configurations.
- Operators can run one command before deployment and receive actionable
  errors or warnings.
- Validation does not require connecting to external dependencies unless an
  explicit active-check mode is requested.

### V8.3 Admin Diagnosis Bundle

Purpose: make incident handoff and open-source issue reporting safer and more
repeatable.

Candidate work:

- Extend `cmd/admin diagnose` beyond the first gateway-API bundle with optional
  Prometheus query hints and scenario-specific collectors.
- Consider a human-readable summary alongside the JSON output.
- Keep redacting all secrets and large message bodies.
- Keep a clear distinction between local state and cluster-discovered remote
  routes.
- Add runbook examples for common scenarios: client cannot receive downlink,
  Redis route stale, peer push failing, upstream route failing, auth rejected,
  retry backlog growing, and internal HMAC verification failing.

Acceptance criteria:

- The diagnosis bundle can be attached to an issue without leaking configured
  secrets.
- The CLI exits non-zero for degraded or failed health states when requested,
  so scripts can use it.
- Documentation shows token and HMAC examples.

### V8.4 Alerting And Dashboard Governance

Purpose: turn existing metrics into production signals.

First-pass status:

- Added bundled Prometheus recording and alert rules for the main gateway,
  auth, upstream, downlink, retry, cluster, HMAC, and JWKS failure modes.
- Added a Grafana `Z-Courier Production Signals` dashboard for alert-oriented
  operational views.
- Added runbook links to alert annotations.

Candidate work:

- Extend alert examples after real production traffic calibrates thresholds.
- Add documented PromQL for dependency checks that are currently available
  through `cmd/admin check` but not exported as Prometheus metrics.
- Continue dashboard governance around stale cluster routes, retry backlog,
  failed messages, overload rejections, HMAC failures, auth failures, and peer
  push latency.
- Keep load-test baseline comparisons informational unless the release process
  explicitly promotes them to a hard gate.

Candidate alerts:

- Gateway not ready.
- Downlink failed message growth.
- Retry backlog growth.
- Peer push error rate.
- Cluster route refresh failures.
- Auth or HMAC rejection spike.
- Internal HTTP overload rejection spike.
- Upstream forwarding error rate.
- PostgreSQL, Redis, NSQ, or JWKS dependency degraded.

Acceptance criteria:

- A production user can import dashboards and alert rules without reading the
  code first.
- Alerts explain the probable source and link to a documented operator action.
- Existing metrics remain backward compatible or have documented replacement
  names.

### V8.5 Resilience Controls

Purpose: make overload and dependency degradation explicit instead of relying
only on generic timeouts and retries.

Candidate work:

- Review internal HTTP and upstream overload paths for consistent status codes,
  error bodies, metrics, and logs.
- Add retry jitter where useful to avoid synchronized retry bursts.
- Add clearer circuit-breaker or degraded-state behavior for repeated upstream,
  Redis, PostgreSQL, or auth-provider failures.
- Make drain behavior more visible through diagnostics and metrics.
- Document safe tuning ranges for retry delay, lease duration, scan limits,
  bind flush limits, and in-flight limits.

Acceptance criteria:

- Operators can tell the difference between rate limiting, overload, dependency
  degradation, and application-level upstream failure.
- Degraded states are visible through metrics and diagnostics.
- Resilience changes preserve at-least-once delivery semantics.

## Suggested Build Order

1. Start with configuration validation, because it catches release and
   deployment errors before runtime.
2. Add runtime diagnostics, because all later operator workflows need one
   stable source of truth.
3. Add `cmd/admin diagnose` on top of the diagnostic APIs.
4. Add Prometheus alert rules and dashboard panels that consume the new and
   existing signals.
5. Review resilience controls after diagnostics can prove the current behavior.

This order keeps V8 grounded in production feedback instead of adding a large
new feature before the system can explain itself under pressure.

## Completion Criteria

V8 is complete only when:

- `v0.8.0` has a release guide with exact scope, upgrade notes, known
  boundaries, and verification commands.
- Config validation runs in CI for representative local, integration,
  production, and Helm examples.
- Runtime diagnostics expose sanitized state for gateway identity,
  configuration, dependencies, capacity, readiness, and cluster routing.
- `cmd/admin` can collect a safe diagnosis bundle.
- Alert rules and dashboard updates cover the main production failure modes.
- Existing CI, E2E, cluster E2E, Kubernetes smoke, Kubernetes E2E, load-test
  smoke, Go SDK tests, PHP SDK tests, Docker release, and Helm release paths
  remain green.

## Open Questions

- Should active dependency checks run only on demand, or also periodically in
  the gateway process?
- Should degraded dependency state affect `/readyz`, or should readiness stay
  focused on local process ability to accept traffic?
- Should config validation warnings be allowed in CI, or should examples be
  warning-free?
- Should `cmd/admin diagnose` include optional Prometheus queries, or should it
  stay strictly gateway-API based?
- Which alert rules are safe as defaults, and which should remain examples
  because production traffic patterns differ?
