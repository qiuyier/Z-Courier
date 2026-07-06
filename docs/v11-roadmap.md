# V11 Roadmap

V11 is the planning track for the next public milestone after `v0.10.0`. Its
target SemVer version is `v0.11.0`, not `v11.0.0`.

`v0.10.0` turned the embedded Web admin console into a guarded operations
surface with browser sessions, roles, permission checks, session operations,
downlink debug pushes, retry/offline queue operations, audit visibility, and
browser smoke coverage. The next step is to make those operations work better
in production and clustered deployments.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.11.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

Z-Courier now has the basic shape of a gateway control plane. V11 should make
that control plane more durable, more cluster-aware, and safer under real
operations pressure.

The guiding rule for V11 is: keep browser operations useful across gateway
nodes without making the console a public SaaS dashboard or a business-message
browser.

V11 should focus on:

- Persisting important admin operation history instead of losing it on restart.
- Sharing admin console session state across nodes when Redis is available.
- Giving operators a cluster-wide view of sessions, routes, and message repair
  context.
- Making cross-node operations explicit, audited, and hard to trigger by
  accident.
- Tightening browser security for cookie-backed admin sessions.
- Keeping Docker Compose, Helm, and release checks aligned with the production
  control-plane story.

## Goals

- Add an optional persistent admin audit store backed by PostgreSQL.
- Add an optional Redis-backed admin session store for clustered deployments.
- Improve console APIs and UI so operators can inspect cluster-wide sessions
  and routes without manually switching gateway nodes.
- Define and implement a safe first pass for remote gateway operations, where
  appropriate.
- Add CSRF and browser-session hardening for guarded mutation endpoints.
- Improve pagination, filtering, and retention behavior for admin-facing data.
- Keep all new behavior opt-in or backward-compatible for existing deployments.

## Non-Goals

V11 does not target:

- A public multi-tenant SaaS dashboard.
- Full identity provider integration, SSO, SAML, OAuth, or OIDC login flows.
- A full user management product with invitations and password reset flows.
- Editing the full gateway configuration from the browser.
- Hot-reloading arbitrary gateway configuration as the main milestone.
- Showing arbitrary business message bodies in the console.
- Replacing Prometheus, Grafana, Alertmanager, or external audit/SIEM systems.
- A new client TCP protocol version or SDK-breaking packet change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.

## Workstreams

### V11.1 Persistent Admin Audit Store

Purpose: preserve admin action history across gateway restarts and make
incident review possible after the fact.

Candidate work:

- Add admin audit storage configuration with `memory` and `postgres` options.
- Create a PostgreSQL audit table with bounded, sanitized fields.
- Persist login, logout, permission denial, retry scan, requeue, discard, test
  push, session disconnect, and future remote operation actions.
- Add retention settings and cleanup worker behavior.
- Keep the in-memory audit store as the simple default for local development.

Acceptance criteria:

- A persisted audit entry survives gateway restart.
- Audit writes do not block critical packet forwarding paths.
- Audit entries redact secrets, HMAC material, tokens, DSNs, and large bodies.
- Operators can filter audit entries by time, action, principal, role, target,
  result, and gateway node.
- PostgreSQL audit schema is covered by tests and documented rollback notes.

### V11.2 Redis Admin Session Store

Purpose: make browser console sessions work predictably when an operator is
served by different gateway nodes.

Candidate work:

- Add admin session storage configuration with `memory` and `redis` options.
- Store session metadata in Redis with TTL and explicit invalidation on logout.
- Prefix keys by deployment or cluster namespace to avoid local collisions.
- Preserve secure cookie, same-site, expiry, and role behavior from V10.
- Keep memory sessions as the default for single-node local development.

Acceptance criteria:

- A session created on gateway-a is recognized on gateway-b when Redis session
  storage is enabled.
- Logout invalidates the shared session.
- Session expiration follows the configured TTL.
- Redis outage behavior is explicit and safe: existing requests fail closed
  rather than silently becoming admin.
- Documentation explains when to use memory versus Redis session storage.

### V11.3 Cluster-Wide Console Views

Purpose: let operators inspect online clients and routes from one console
without caring which node currently owns the TCP connection.

Candidate work:

- Add cluster-aware session and route query APIs.
- Combine local session state with Redis route registry data.
- Show owning gateway node, internal address, route TTL, route age, and whether
  the current gateway owns the connection.
- Add cluster-wide counters for online routes and unique clients where
  available.
- Make stale or missing route data visually distinct from healthy online
  routes.

Acceptance criteria:

- An operator can answer "where is this client connected?" from one console.
- A remote session is clearly marked as remote and not confused with a local
  connection.
- Cluster views degrade gracefully when the registry is disabled or unhealthy.
- APIs remain bounded and paginated.
- Existing local-only session APIs keep working.

Implemented surface:

- `/internal/debug/cluster/routes` lists bounded online routes from the cluster
  registry.
- The admin console Sessions page can switch between Local Sessions and Cluster
  Routes.
- Cluster route cards show owning node, internal address, TTL, and whether the
  queried gateway also owns the local TCP session.
- Remote mutation actions stay out of this view and are handled by V11.4.

### V11.4 Remote Operation Safety Model

Purpose: decide which operations can safely cross gateway nodes and make those
operations explicit.

Candidate work:

- Define which remote operations are allowed in V11, such as remote test push
  reuse or remote session disconnect.
- Route allowed remote operations through existing peer HTTP authentication.
- Require confirmations that include the target client, device, session, and
  owning node.
- Record both the requesting node and owning node in audit entries.
- Keep unsupported remote operations disabled with clear explanations.

Acceptance criteria:

- Remote operations never silently fall back to the wrong gateway node.
- Peer authentication and permission checks are enforced server-side.
- A failed remote operation reports whether the failure was route lookup,
  peer authentication, peer timeout, permission denial, or target not found.
- All remote operation attempts are audited.
- The console distinguishes local operations from cross-node operations.

Implemented surface:

- Downlink test push responses include `delivery_path`, origin/target gateway
  metadata, and structured failure fields.
- Peer dispatch failures are classified as `peer_auth_failed`, `peer_timeout`,
  `peer_target_not_found`, `peer_not_configured`, and related codes.
- Admin test-push audit logs include route metadata and use the structured
  failure code as the audit result when one is available.
- The console Push Result panel displays local versus cluster-peer delivery and
  the target gateway details.
- Remote session disconnect is intentionally not enabled in this phase.

### V11.5 Console Security Hardening

Purpose: reduce browser-specific risks after introducing cookie-backed admin
sessions.

Candidate work:

- Add CSRF protection for session-authenticated mutation endpoints.
- Define allowed methods and content types for admin mutation APIs.
- Add stricter origin or referer checks where appropriate.
- Add rate limiting for login and high-impact admin actions.
- Add clearer security headers for console and admin API responses.
- Document production reverse proxy expectations.

Acceptance criteria:

- Session-authenticated mutation requests require CSRF protection.
- Read-only GET endpoints remain easy to use from the console.
- Failed CSRF checks are denied, logged, counted, and audited where useful.
- Browser smoke tests cover successful mutation and CSRF rejection paths.
- Existing token/HMAC internal API clients remain compatible where they do not
  use browser sessions.

### V11.6 Admin Data Pagination And Retention

Purpose: keep admin APIs predictable as production data grows.

Candidate work:

- Standardize limit, cursor, and sort behavior for audit, sessions, routes,
  messages, and retry views.
- Add clear maximum limits and response metadata.
- Avoid returning large unbounded arrays from admin endpoints.
- Document retention behavior for audit and downlink message views.
- Add UI affordances for paging and filtering.

Acceptance criteria:

- Admin list endpoints have bounded limits and stable ordering.
- The console can navigate large result sets without freezing or overflowing
  panels.
- Tests cover limit caps, cursor behavior, empty pages, and invalid filters.
- Documentation states default retention and operational tradeoffs.

### V11.7 Observability And Release Readiness

Purpose: make V11 operational changes visible and releasable with the same
confidence as V10.

Candidate work:

- Add metrics for persistent audit writes, audit failures, Redis admin session
  operations, CSRF denials, and remote operation outcomes.
- Update Grafana dashboards and Prometheus rules where useful.
- Extend diagnostics and diagnosis bundles with sanitized admin store state.
- Add E2E coverage for Redis-backed admin sessions and persistent audit.
- Write the `v0.11.0` release guide and upgrade notes.

Acceptance criteria:

- Operators can tell whether persistent audit and Redis admin sessions are
  healthy from diagnostics or metrics.
- Release checks cover the new storage modes.
- Docker Compose, Helm, CI, E2E, browser smoke, Go tests, PHP SDK checks,
  frontend build, and Helm validation remain green.
- A rollback path from `v0.11.0` to `v0.10.0` is documented.

## Suggested Implementation Order

1. Add persistent admin audit storage behind a configuration flag.
2. Add audit list filtering, pagination, retention, and documentation.
3. Add Redis-backed admin session storage.
4. Add cluster-wide session and route views.
5. Define and implement the safe first pass of remote operations.
6. Add CSRF protection and browser-session hardening.
7. Standardize pagination and retention for admin data APIs.
8. Extend metrics, dashboards, diagnostics, and release checks.
9. Write the `v0.11.0` release guide and run release verification.

This order keeps V11 anchored in durability first. Once the system can persist
and review admin actions, it is safer to expand cross-node operational power.

## Completion Criteria

`v0.11.0` is complete when:

- Admin audit can be persisted in PostgreSQL and queried after restart.
- Admin sessions can be shared through Redis in clustered deployments.
- Operators can inspect cluster-wide session and route state from one console.
- Any supported remote operations are explicit, permission-checked, peer-auth
  protected, audited, and clearly labeled in the UI.
- Browser-session mutation endpoints have CSRF protection.
- Admin list APIs have bounded pagination and stable filtering behavior.
- Metrics and diagnostics expose the health of the new admin storage and
  cluster-control features.
- Docker Compose, Helm, and production documentation explain the new settings.
- A `v0.11.0` release guide documents scope, configuration, verification,
  security boundaries, known limitations, and rollback.

## Known Boundaries

- The console remains an internal operations surface.
- Redis-backed admin sessions are for cluster convenience, not public identity
  federation.
- Persistent audit is an operational audit trail, not a full SIEM replacement.
- The console should not become a business message browser.
- Mutating arbitrary gateway configuration from the browser remains deferred.
- Historical metrics and alerting remain Prometheus and Grafana
  responsibilities.
