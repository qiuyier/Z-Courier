# V10 Roadmap

V10 is the planning track for the next public milestone after `v0.9.1`. Its
target SemVer version is `v0.10.0`, not `v10.0.0`.

`v0.9.x` added the embedded Web admin console and Chinese documentation set.
The console can already inspect overview, routes, sessions, messages,
dependency checks, diagnostics, diagnosis bundles, metrics context, and guarded
message repair flows. The next step is to make the console safer and more
useful for real operational work, not just inspection.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.10.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

Z-Courier is becoming an open-source middleware project that needs to be
operable by people who did not write the gateway code. `v0.9.x` made the
system visible in a browser. V10 should make the browser experience suitable
for controlled operations:

- Let operators access the console without pasting long-lived internal tokens
  into the browser for every session.
- Distinguish read-only inspection from actions that can affect client
  connections, downlink delivery, retry state, or cluster routing.
- Make common incident actions discoverable: find a client, inspect its local
  or remote route, test downlink delivery, and understand retry behavior.
- Keep dangerous actions guarded, audited, and easy to review later.
- Preserve the internal-only security model. The console is still not a public
  internet endpoint.

The guiding rule for V10 is: add operational power only when the gateway can
explain what will happen, confirm operator intent, and record the result.

## Goals

- Add a short-lived admin console session layer on top of existing internal
  HTTP authentication.
- Add a small role and permission model for read-only and operator actions.
- Improve session search and cluster route workflows for live incident
  debugging.
- Add a downlink debug playground for safe delivery tests.
- Make retry and offline queue state easier to inspect and, where safe,
  operate from the console.
- Extend audit coverage for browser-initiated admin actions.
- Keep Docker Compose, Helm, and production documentation clear about the
  console access boundary.

## Non-Goals

V10 does not target:

- A public multi-tenant SaaS dashboard.
- Full user management, password reset flows, invitations, or organization
  administration inside Z-Courier.
- Editing the full gateway configuration from the browser.
- Dynamic upstream route hot reload as the main milestone.
- Viewing arbitrary business message bodies in the console.
- Replacing Prometheus, Grafana, Alertmanager, or a SIEM.
- A new client TCP protocol version or SDK-breaking packet change.
- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.

## Security Model

V10 should keep the existing internal HTTP boundary and add a browser-friendly
session only after a request has passed trusted internal authentication.

First-pass shape:

- The operator authenticates to the internal HTTP server using the configured
  internal token or HMAC-authenticated deployment path.
- The gateway issues a short-lived admin console session cookie or token.
- The browser uses that session for console API calls.
- The session records a principal, role, issued time, expiry time, and optional
  source metadata.
- Admin actions are checked against role permissions before execution.
- Sensitive values remain redacted or omitted from all console APIs.

The session layer is not a replacement for production network controls. A
production deployment should still place the console behind private networking,
VPN, bastion access, private ingress, or an authenticating reverse proxy.

## Workstreams

### V10.1 Admin Console Session Layer

Purpose: avoid treating the browser as a permanent holder of the internal HTTP
token while preserving the existing internal auth boundary.

Candidate work:

- Add admin session configuration: enabled state, session TTL, cookie name,
  secure-cookie behavior, and optional same-site policy.
- Add login, session introspection, and logout endpoints under internal HTTP.
- Store admin sessions in memory for the first implementation, with explicit
  documentation that sessions are node-local.
- Add a console login screen that exchanges a valid internal credential for a
  short-lived session.
- Make expired sessions return a consistent unauthorized response that the
  frontend can handle without infinite refresh loops.

Acceptance criteria:

- A local operator can log in to the console, refresh the page, and stay
  authenticated until the session expires or is logged out.
- The internal token is not persisted in local storage.
- Disabled session mode keeps the existing development token workflow
  available if configured.
- Tests cover login success, login rejection, session expiry, and logout.

### V10.2 Roles And Permissions

Purpose: separate safe inspection from actions that change gateway state.

Candidate roles:

- `readonly`: inspect overview, routes, sessions, messages, checks,
  diagnostics, and monitoring links.
- `operator`: all readonly permissions plus session disconnect, downlink test
  push, retry scan trigger, and guarded message repair.
- `admin`: operator permissions plus future high-impact admin actions.

Candidate work:

- Add a small permission package or policy table shared by internal HTTP
  handlers.
- Attach role information to admin sessions and audit entries.
- Return permission metadata to the console so the UI can hide or disable
  unavailable actions.
- Keep server-side enforcement as the source of truth.

Acceptance criteria:

- Read-only sessions cannot call mutation endpoints even if the browser sends
  a crafted request.
- The console clearly distinguishes disabled actions from failed actions.
- Permission failures are logged and counted with stable metrics.

### V10.3 Session Operations

Purpose: make client and device incident handling fast from the console.

Candidate work:

- Improve session search by `ClientID`, `DeviceID`, and `SessionID`.
- Add a session detail view that combines local session state and cluster route
  state.
- Add an operator-only disconnect action for local sessions.
- Add clear remote-session guidance when the current gateway does not own the
  connection.
- Add peer route freshness and TTL display where Redis-backed registry is
  enabled.

Acceptance criteria:

- An operator can answer "where is this client connected?" from one view.
- An operator can disconnect a local test session and see the session disappear
  from local state.
- Remote sessions are not presented as locally disconnectable unless a future
  peer admin operation is implemented.
- Disconnect actions are audited with principal, role, target, and result.

### V10.4 Downlink Debug Playground

Purpose: provide a controlled way to test whether gateway downlink delivery is
working for a specific client/device.

Candidate work:

- Add a console form for `ClientID`, optional `DeviceID`, `MsgID`,
  `MessageID`, flags, body, and ACK wait mode.
- Reuse existing backend SDK/internal HTTP downlink semantics instead of
  inventing a browser-only path.
- Generate safe default `MessageID` and `TraceID` values for test pushes.
- Show the result as queued, sent, delivered, rejected, timeout, or failed.
- Keep request and response bodies bounded and clearly marked as operator test
  data.

Acceptance criteria:

- A developer can connect `cmd/devclient`, send a test downlink from the
  console, and see the client receive it.
- ACK wait behavior matches the existing backend SDK behavior.
- Failed pushes show actionable error text and relevant route/session context.
- Test pushes are audited and counted.

### V10.5 Retry And Offline Queue Operations

Purpose: make durable downlink behavior explainable when messages are not
delivered immediately.

Candidate work:

- Add retry queue summary cards: pending, leased, failed, discarded, delivered,
  and next scan timing.
- Add message lookup by `MessageID` and list views with bounded pagination.
- Show attempts, next retry time, lease owner, last error, and status
  transitions where available.
- Add operator-only trigger for one retry scan.
- Keep requeue and discard actions guarded by confirmations and existing
  status rules.

Acceptance criteria:

- An operator can explain why a message is waiting instead of delivered.
- Manual retry scan does not bypass lease or max-attempt safety rules.
- Delivered messages cannot be accidentally requeued or discarded.
- Retry operations are audited and covered by tests.

### V10.6 Audit Trail And Metrics

Purpose: make console operations reviewable after an incident.

Status: first implementation landed as a node-local bounded in-memory audit
store, `GET /internal/admin/audit`, console Audit page, and
`z_courier_admin_action_total`.

Candidate work:

- Extend audit entries for admin session login/logout, permission rejection,
  session disconnect, downlink test push, retry scan, requeue, and discard.
- Add bounded audit list and lookup APIs for recent admin actions.
- Add Prometheus counters for admin action attempts, successes, failures, and
  permission rejections.
- Add console filters by action, principal, target client, target session, and
  result.

Acceptance criteria:

- A browser action can be traced to an audit entry.
- Audit responses redact secrets and large bodies.
- Audit list APIs have bounded limits and stable ordering.

### V10.7 Console UX Hardening

Purpose: make the console feel reliable during real operations.

Candidate work:

- Add global session-expired handling.
- Add consistent loading, empty, error, and permission-denied states.
- Add confirmation dialogs for all mutation actions.
- Add copy buttons for IDs, message IDs, trace IDs, and metric names.
- Add accessible labels and keyboard-friendly form flows for operational
  actions.
- Tighten responsive layouts for narrow screens without hiding critical state.

Acceptance criteria:

- Failed API calls do not cause refresh loops.
- Long IDs and error text do not overflow their panels.
- Mutation actions always show final success or failure state.
- The console remains usable without Prometheus or Grafana links configured.

### V10.8 Documentation And Release Readiness

Purpose: make the V10 operational model understandable to new users.

Candidate work:

- Write the `v0.10.0` release guide.
- Document admin session configuration and production access patterns.
- Update Docker Compose and Helm examples.
- Add a short operator tutorial for session lookup, downlink testing, retry
  inspection, and audit review.
- Add upgrade notes from `v0.9.x`.

Acceptance criteria:

- A user can enable the console session layer locally and in Helm from docs.
- Release checks cover the new admin session and console workflows.
- CI, E2E, Kubernetes smoke, Docker image smoke, PHP SDK, Go tests, frontend
  build, and Helm validation remain green.

## Suggested Implementation Order

1. Add the admin session API and frontend login flow.
2. Add role and permission enforcement on selected console APIs.
3. Improve session search/detail and local disconnect.
4. Add downlink debug playground.
5. Add retry/offline queue views and manual retry scan.
6. Extend audit APIs, metrics, and console audit views.
7. Harden console UX states and responsive layouts.
8. Write the `v0.10.0` release guide and run release verification.

This order keeps V10 grounded in safety first. The console should not gain
mutation buttons before authentication, permission checks, and audit behavior
are clear.

## Completion Criteria

`v0.10.0` is complete when:

- The console supports short-lived admin sessions without storing the internal
  token in browser local storage.
- Read-only and operator roles are enforced server-side.
- Operators can search sessions, inspect cluster routes, and disconnect local
  sessions from the console.
- Operators can send a bounded test downlink and understand the delivery
  result.
- Retry/offline queue state is visible and safe retry actions are available or
  explicitly deferred with a documented reason.
- Browser-initiated admin actions are audited.
- Console errors, expired sessions, permission denials, and empty states are
  handled cleanly.
- Docker Compose, Helm, and production documentation explain the new settings.
- A `v0.10.0` release guide documents scope, configuration, verification,
  security boundaries, known limitations, and rollback.

## Known Boundaries

- The console remains an internal operations surface.
- First-pass admin sessions may be node-local; clustered shared sessions can be
  considered later if real deployments need it.
- The console should not become a business message browser.
- Mutating gateway configuration from the browser is intentionally deferred.
- Historical metrics and alerting remain Prometheus and Grafana responsibilities.
