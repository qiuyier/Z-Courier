# V9 Roadmap

V9 is the planning track for the next public milestone after `v0.8.1`. Its
target SemVer version is `v0.9.0`, not `v9.0.0`.

`v0.8.x` made Z-Courier much easier to operate from the command line: static
configuration validation, active dependency checks, diagnostics bundles,
production dashboards, alert rules, and release artifact verification are now
in place. The next step is to make those operational capabilities approachable
from a browser.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.9.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

Z-Courier already exposes most of the useful operational data through internal
HTTP APIs, Prometheus metrics, admin CLI commands, and safe diagnosis bundles.
That is powerful, but it still assumes the operator knows which command to run,
which endpoint to query, and how to correlate sessions, routes, messages,
dependency state, and retry behavior.

V9 should build a Web admin console on top of the existing internal APIs. The
console should help an operator answer common gateway questions quickly:

- Is this gateway node ready, degraded, draining, or misconfigured?
- Which upstream routes are enabled, degraded, overloaded, or failing?
- Is a client/device online locally, online on another gateway node, or
  offline?
- What happened to a downlink message: queued, sent, delivered, failed, or
  discarded?
- Is Redis, PostgreSQL, NSQ, the auth provider, JWKS, HMAC, or peer push
  causing trouble?
- What safe information can be collected before opening an issue or debugging
  an incident?

The first console should be useful, restrained, and operational. It should not
try to be a marketing page, a full observability platform, or a replacement for
Prometheus and Grafana.

## Goals

- Add a browser-based admin console for day-to-day gateway inspection.
- Reuse the existing internal HTTP authentication model instead of adding a
  second security system.
- Provide read-only operational views first, then add guarded repair actions
  where the CLI already supports them.
- Make common troubleshooting flows discoverable without requiring operators
  to remember admin CLI commands.
- Keep the console deployable in Docker Compose and Helm without changing the
  gateway TCP protocol or SDK contracts.
- Keep sensitive values such as tokens, HMAC secrets, DSNs, Authorization
  headers, and message bodies redacted or omitted.

## Non-Goals

V9 does not target:

- A public multi-tenant SaaS dashboard.
- A new gateway wire protocol or incompatible packet change.
- Replacing Grafana for time-series dashboards.
- Replacing Alertmanager for notification delivery.
- Editing full gateway configuration from the browser.
- Managing PostgreSQL, Redis, NSQ, Prometheus, Grafana, or Kubernetes
  installations.
- A Kubernetes operator.
- User/password account management inside Z-Courier.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.

## Console Architecture

The preferred V9 shape is an embedded single-page admin console served by the
gateway's internal HTTP server.

First pass:

- Serve static console assets from the internal HTTP listener.
- Keep the API base on the same origin as internal HTTP to avoid CORS and
  browser credential complexity.
- Reuse internal HTTP auth. In local development, token auth is acceptable. In
  production, HMAC remains the preferred machine-to-machine mode, so the
  browser console should be deployed behind an operator-controlled reverse
  proxy or accessed through a secure private network.
- Make the console optional through configuration so production operators can
  disable the UI while keeping admin APIs enabled.
- Build the frontend as a small static application with a predictable build
  output that can be embedded into the gateway image and chart.

Open design questions:

- Whether the first frontend should be plain TypeScript with a tiny build
  stack, or a framework such as React with Vite.
- Whether HMAC signing should be supported directly in the browser for local
  use, or whether production should require a proxy that injects trusted
  internal auth.
- Whether the console route should default to `/admin/`, `/console/`, or
  `/internal/admin/`.

## Workstreams

### V9.1 Console Shell And Delivery

Purpose: establish the admin console runtime shape without taking on every
feature at once.

Candidate work:

- Add a static asset serving path under internal HTTP.
- Add configuration for enabling/disabling the console, configuring the base
  path, and setting cache headers.
- Add a frontend workspace with build, lint, and test commands.
- Add CI coverage for the frontend build and basic static asset embedding.
- Add Docker image packaging so the published gateway image contains the
  built console.
- Add Helm values for enabling/disabling the console and documenting the
  internal service access pattern.
- Add a minimal landing view that proves the console can call the authenticated
  internal overview endpoint.

Acceptance criteria:

- Local Docker Compose users can open the console through the internal HTTP
  port and see the queried gateway node identity.
- The gateway can still run without console assets when the feature is
  disabled or when building a slim development binary.
- CI fails if the console build is broken.
- No secrets are rendered into static assets.

### V9.2 Overview And Readiness

Purpose: provide the first operational screen an engineer checks during an
incident.

Candidate work:

- Show gateway node, version, commit, start time, uptime, and config source.
- Show readiness, drain state, and dependency summary.
- Show local online sessions and unique online clients.
- Show upstream route count and degraded route count.
- Show downlink pending, sent, failed, discarded, and retry backlog summaries.
- Show direct links to diagnostics, routes, sessions, messages, and checks.

Acceptance criteria:

- An operator can answer "is this node usable?" from one screen.
- Readiness and degraded states match existing `cmd/admin overview`,
  `cmd/admin diagnostics`, and Prometheus signals.
- Empty states are explicit, not blank.

### V9.3 Routes And Upstream Runtime State

Purpose: make MsgID routing and upstream health visible without reading YAML.

Candidate work:

- List configured upstream routes with name, enabled state, MsgID range,
  target type, target summary, runtime state, in-flight count, and last error.
- Add route lookup by MsgID.
- Highlight overlaps, reserved MsgID usage, disabled routes, and unavailable
  routes using existing validation/diagnostic output.
- Link route errors to relevant logs, metrics names, and documentation where
  possible.

Acceptance criteria:

- An operator can determine where `MsgID=2001` will be forwarded.
- Degraded and unavailable routes are visibly distinct.
- Target URLs and credentials are redacted consistently with CLI diagnostics.

### V9.4 Sessions And Cluster Routes

Purpose: make client/device lookup understandable in single-node and clustered
deployments.

Candidate work:

- Search by `ClientID` and optional `DeviceID`.
- Show whether the session is local, remote, offline, or stale.
- Show local session details: session ID, device ID, connection ID, token ID,
  connected gateway, and safe timing metadata.
- Show Redis route details when cluster registry is enabled: gateway node,
  internal address, session ID, TTL, updated time, and expiry time.
- Show clear guidance when querying the wrong gateway node for local sessions.

Acceptance criteria:

- An operator can answer "where is this client connected?" from the console.
- The console distinguishes local sessions from cluster routes.
- Stale route behavior matches the existing peer-push stale-route cleanup
  semantics.

### V9.5 Downlink Message Inspection And Repair

Purpose: expose the existing durable downlink repair flows in a safer browser
workflow.

Candidate work:

- List messages by status with pagination or bounded limits.
- Search message status by `MessageID`.
- Show status, attempts, next retry time, session ID, delivery timestamps, and
  last error.
- Keep message bodies hidden by default and avoid adding body inspection in
  V9 unless there is an explicit safe redaction story.
- Add guarded `requeue` and `discard` actions only for statuses where the
  existing admin API allows them.
- Require explicit confirmation and discard reason for destructive repair
  actions.

Acceptance criteria:

- An operator can diagnose why a downlink did not reach a client.
- Repair actions use the same audited internal APIs as `cmd/admin`.
- Delivered messages cannot be accidentally requeued or discarded.

### V9.6 Diagnostics And Dependency Checks

Purpose: make diagnosis bundles and active dependency checks accessible from
the console.

Candidate work:

- Render sanitized diagnostics in human-readable sections.
- Run active dependency checks and show status, latency, and last error.
- Provide a "download diagnosis bundle" action using the existing safe bundle
  shape.
- Add scenario-specific hints for common failures: auth rejected, upstream
  route degraded, Redis route missing, peer push failed, retry backlog growing,
  HMAC verification failed, and PostgreSQL unavailable.

Acceptance criteria:

- The browser output matches the CLI diagnosis bundle for the same gateway.
- Downloaded bundles remain safe to attach to an issue.
- Active checks are clearly marked as probes, not passive cached state.

### V9.7 Metrics Links And Operational Context

Purpose: connect the console to existing Prometheus/Grafana operations without
duplicating them.

Candidate work:

- Add metric names next to key cards and tables.
- Add configurable external links to Prometheus and Grafana dashboards.
- Show short PromQL snippets for common questions when Prometheus URL is
  configured.
- Keep time-series charts minimal in the console; rely on Grafana for full
  charting.

Acceptance criteria:

- Operators can jump from a console warning to the right dashboard or PromQL.
- The console remains useful even when Prometheus and Grafana links are not
  configured.

### V9.8 Security And Deployment Hardening

Purpose: keep a Web console from weakening the internal admin boundary.

Candidate work:

- Add configuration documentation for running the console only on private
  networks.
- Document recommended production access through VPN, bastion, private
  ingress, or an authenticating reverse proxy.
- Add cache and content-security headers appropriate for static console
  assets.
- Ensure console APIs never expose raw secrets, DSNs, HMAC keys, token values,
  or message bodies.
- Add tests for redaction and disabled-console behavior.

Acceptance criteria:

- The console is opt-in or clearly internal-only by default.
- Production documentation explains that the console is not a public endpoint.
- Redaction behavior matches CLI diagnostics.

## Suggested Implementation Order

1. Add console configuration and static asset serving.
2. Add frontend build scaffold and CI validation.
3. Build the Overview page using existing overview/diagnostics APIs.
4. Add Routes and MsgID lookup.
5. Add Sessions and cluster route lookup.
6. Add Downlink message status/list pages.
7. Add guarded requeue/discard actions.
8. Add Diagnostics and active dependency check views.
9. Add Docker, Compose, and Helm documentation.
10. Write the `v0.9.0` release guide and run release verification.

## Completion Criteria

`v0.9.0` is complete when:

- The admin console is available from the gateway internal HTTP server.
- The console can be enabled in local Docker Compose and documented Helm
  deployments.
- Operators can inspect overview, readiness, routes, sessions, cluster routes,
  downlink messages, diagnostics, and dependency checks from the browser.
- Guarded downlink repair actions are available or explicitly deferred with a
  documented reason.
- The console build is covered by CI and included in the Docker image release
  path.
- All sensitive fields remain redacted or omitted.
- Existing CLI workflows remain supported.
- A `v0.9.0` release guide documents scope, configuration, verification,
  security boundaries, known limitations, and rollback.

## Known Boundaries

- The console is an operations UI, not a business-message viewer.
- The first version should prefer clarity and safety over editing power.
- HMAC-authenticated browser access may require a deployment-side proxy rather
  than direct browser signing.
- Prometheus and Grafana remain the source of truth for historical metrics and
  alerting.
- Z-Courier still does not own application-level exactly-once processing.
