# V12 Roadmap

V12 is the planning track for the next public milestone after `v0.11.0`. Its
target SemVer version is `v0.12.0`, not `v12.0.0`.

`v0.11.0` made the admin control plane durable, cluster-aware, and safer for
browser operations. V12 returns to the delivery path: a reliable gateway needs
to tell backend systems exactly what happens when they retry a request, when a
client stays offline, and when a message can no longer be delivered.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.12.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

Z-Courier already provides durable, at-least-once downlink delivery backed by
PostgreSQL, client ACKs, retry leasing, cluster peer push, and operator repair
tools. The remaining reliability gap is policy clarity: backend retries should
be safe, retry work should be bounded and fair, and terminal failure should
have a predictable operational outcome.

The guiding rule for V12 is: make delivery behavior deterministic from gateway
metadata alone. The gateway continues to route and deliver opaque bytes; it
does not parse or make business decisions from message bodies.

V12 should focus on:

- Making backend downlink submission idempotent by `MessageID`.
- Replacing one-size-fits-all retry behavior with bounded, route-selected
  delivery policies.
- Making terminal failure observable and optionally exportable without leaking
  business payloads.
- Preventing one offline client or device from monopolizing durable queue space
  or retry worker capacity.
- Giving operators a clear, auditable repair path after a message fails.

## Goals

- Define exact duplicate and conflict semantics for `POST /internal/push`.
- Add named delivery policies selected by downlink `MsgID` range.
- Support bounded exponential backoff with jitter and a maximum delivery age.
- Add optional terminal-failure event publication through an asynchronous
  adapter, starting with NSQ and a generic HTTP webhook contract where useful.
- Add queue capacity limits and fair retry selection across client/device keys.
- Extend the admin console, metrics, diagnostics, alerts, and E2E coverage for
  the new delivery lifecycle.
- Keep all changes opt-in or backward-compatible with existing deployments.

## Non-Goals

V12 does not target:

- Exactly-once delivery. Z-Courier remains at-least-once.
- Parsing, filtering, indexing, or displaying arbitrary business message
  bodies.
- A message transformation, workflow, or rules engine.
- A distributed transaction across PostgreSQL, NSQ, HTTP backends, and client
  delivery.
- Replacing application-side durable de-duplication for payments, orders, or
  other business-critical processing.
- A public event broker, Kafka replacement, or multi-tenant queue service.
- Changing the client packet version or breaking existing Go/PHP SDKs.

## Delivery Contract

The V12 downlink contract is intentionally narrow:

| Situation | Gateway result |
| --- | --- |
| New `MessageID` | Persist and attempt delivery using the selected policy. |
| Same `MessageID`, same immutable identity | Return the existing message state; do not enqueue or send a second stored message. |
| Same `MessageID`, different immutable identity | Reject with a conflict; never overwrite the existing message. |
| Retryable delivery failure | Persist retry metadata and schedule the next attempt under the selected policy. |
| Policy exhausted | Mark the message terminally failed and run the configured terminal-failure action. |

The immutable identity comparison should include at least `client_id`,
`device_id`, `msg_id`, `ack_required`, and a body digest. `trace_id` and
transient route/session fields are diagnostic metadata, not part of identity.
The response should explicitly identify a newly accepted request, idempotent
replay, or conflict.

## Workstreams

### V12.1 Idempotent Downlink Submission

Purpose: make backend retry after a timeout safe and observable.

Candidate work:

- Define a canonical immutable identity fingerprint for a downlink request.
- Persist the fingerprint with the durable message record.
- Make memory and PostgreSQL stores return `created`, `existing`, or
  `conflict` from an atomic save operation.
- Return the existing state and delivery metadata for a compatible replay.
- Return a stable conflict code and HTTP status for incompatible `MessageID`
  reuse.
- Preserve this behavior through cluster peer delivery and backend SDK helpers.

Acceptance criteria:

- Concurrent requests with the same compatible `MessageID` create exactly one
  durable message record.
- A compatible replay never increments attempts or triggers an extra initial
  delivery write.
- Incompatible reuse is rejected without modifying the original message.
- PostgreSQL and memory stores have the same externally visible behavior.
- Unit, PostgreSQL integration, single-node and cluster HTTP E2E, and Go
  backend SDK tests cover all three outcomes.

Current implementation:

- Reliable stores atomically classify save attempts as `created`, `existing`,
  or `conflict` using a SHA-256 fingerprint of immutable request identity.
- PostgreSQL persists the fingerprint in
  `z_courier_downlink_messages.identity_fingerprint`; old rows are compatible
  and lazily backfilled when replayed.
- Compatible replay returns the persisted message state and skips direct or
  peer redelivery. Immutable identity conflicts return HTTP `409` with
  `message_id_conflict`.
- Go backend SDK responses expose `SubmissionState` and `MessageStatus`.
- Memory, service, HTTP, SDK, and opt-in real PostgreSQL concurrency tests cover
  the store and service contracts.
- Single-node and two-node cluster E2E verify `created`, compatible `existing`
  replay, `message_id_conflict`, original-body preservation, and absence of an
  extra client delivery. In the cluster run, the initial online write traverses
  gateway-a to gateway-b through peer push while the replay stays suppressed.
- The public Go backend SDK E2E covers all three submission outcomes. The PHP
  SDK remains a client protocol SDK and therefore does not own the backend
  `/internal/push` submission contract.

### V12.2 Delivery Policies And Backoff

Purpose: let operators tune delivery behavior by message class without
inspecting message bodies.

Candidate work:

- Add named `downlink.policies` with MsgID-range selectors.
- Define explicit precedence and reject overlapping enabled selectors during
  static configuration validation.
- Support `max_attempts`, `max_age`, `ack_timeout`, initial retry delay,
  exponential multiplier, maximum delay, and bounded jitter.
- Keep the current delivery settings as a compatible default policy.
- Persist the policy name and relevant terminal reason with each message so
  later configuration edits do not make historical state ambiguous.

Acceptance criteria:

- Every accepted message has one deterministic policy.
- A message cannot retry past its attempt or age limit.
- Backoff is bounded, jittered, and testable through injected time/randomness.
- Policy selection is visible in status APIs, diagnostics, and console views.
- Existing configurations without policies preserve current retry behavior.

Current implementation:

- V12.2.1 defines an immutable delivery-policy set with an implicit `default`
  policy and deterministic inclusive MsgID-range resolution.
- YAML policies inherit omitted values from legacy `downlink.delivery` and
  support `max_attempts`, `max_age`, `ack_timeout`, initial retry delay,
  backoff multiplier, maximum retry delay, and bounded jitter.
- Static validation rejects invalid names, durations, limits, duplicate names,
  reversed ranges, overlapping enabled ranges, and unbounded exponential
  backoff.
- V12.2.2 snapshots the selected policy and all retry parameters with every new
  reliable message. PostgreSQL auto-migration adds the snapshot columns while
  pre-V12.2.2 rows retain a current-policy fallback.
- The retry state machine executes per-message ACK deadlines, bounded
  exponential backoff plus jitter, `max_attempts`, and `max_age`. Exhaustion
  records stable `max_attempts_exceeded` or `max_age_exceeded` reasons without
  inventing delivery attempts that did not happen.
- Status/list APIs and the Go backend SDK expose `policy_name`; diagnostics
  report loaded policy names, and the admin console displays policy selection
  in message lookup/list views.
- Unit tests inject clock and jitter behavior, the opt-in PostgreSQL test covers
  migration/snapshot compatibility, and single/cluster E2E validate policy
  persistence and API visibility for MsgID `2001`.

### V12.3 Terminal Failure And Dead-Letter Events

Purpose: make exhausted delivery a first-class business-operational signal.

Candidate work:

- Define terminal reasons such as `max_attempts_exceeded`,
  `max_age_exceeded`, `ack_timeout_exceeded`, and operator discard.
- Add an optional terminal-failure publisher with `none`, `nsq`, and HTTP
  webhook adapters.
- Publish a bounded metadata envelope containing message identity, status,
  policy, attempts, timestamps, terminal reason, and gateway context.
- Exclude the original Body by default. Any future payload inclusion must be
  explicit, size-bounded, and separately reviewed.
- Persist publication state so failed dead-letter publication can be retried
  without repeatedly changing the delivery message state.

Current implementation (V12.3.1):

- `failed` and operator-`discarded` transitions persist a stable reason and a
  versioned terminal event in the same store operation. PostgreSQL performs
  the message transition and outbox insert in one transaction.
- The initial adapters are `none` and direct-to-`nsqd` NSQ publication. HTTP
  webhook publication remains future work.
- The envelope contains bounded identity, policy, attempt, timestamp, reason,
  and gateway metadata and deliberately has no message Body or credentials.
- Publication claims, attempts, error, next-attempt time, and success time are
  independent from delivery retry state. Shared PostgreSQL claims allow
  multiple gateway workers without intentionally publishing the same due row.
- Status/list APIs, the Go backend SDK, diagnostics, and console message views
  expose terminal reason and publication state.
- Unit tests cover envelope safety and independent retry, the PostgreSQL test
  covers transactional outbox persistence, and single/cluster E2E consume the
  NSQ event and assert that the original Body is absent.
- Delivery remains at least once. Receivers de-duplicate by `MessageID` plus
  terminal state; old rows that were terminal before this migration are not
  exported retroactively.

Acceptance criteria:

- A terminal message has one stable terminal reason.
- A disabled publisher does not change current delivery behavior.
- Publisher failure is visible and retried independently of client delivery.
- A successful terminal event is idempotent from the receiver's perspective by
  `MessageID` plus terminal state.
- Dead-letter metadata contains no internal token, HMAC material, DSN, or
  message body by default.

### V12.4 Queue Capacity And Retry Fairness

Purpose: protect healthy traffic from long-lived offline backlogs.

Candidate work:

- Add global and per-`client_id + device_id` pending-message limits.
- Define reject-versus-terminal-failure behavior when capacity is exhausted.
- Add bounded database queries that select due retries fairly across devices
  instead of scanning one hot device indefinitely.
- Expose per-policy and capacity decisions through metrics and diagnostics.
- Document capacity sizing in terms of active devices, offline duration,
  message rate, retention, and PostgreSQL storage.

Current implementation (V12.4.1 through V12.4.3):

- Adds backward-compatible global and per-`client_id + device_id` pending
  admission limits; zero keeps the corresponding limit disabled.
- Compatible `MessageID` replay and immutable-identity conflict detection run
  before capacity checks. New over-capacity messages return HTTP `429` with an
  explicit scope, limit, and observed pending count and are not persisted.
- Memory admission is atomic within one process. PostgreSQL uses stable
  transaction advisory locks plus indexed pending counts, so gateway nodes
  sharing the same database cannot intentionally consume the last slot
  together.
- Manual requeue obeys the same limits. Capacity rejection never evicts an
  existing message and never creates a terminal event.
- The Go backend SDK preserves capacity metadata on retryable `APIError`s.
  Diagnostics, the admin console, Prometheus, Grafana, alerts, and the
  production runbook expose the configured limits and rejection scope.
- Unit tests cover global/device limits, idempotency, requeue, and concurrent
  memory admission. Real PostgreSQL integration and two-node cluster E2E cover
  the shared-store path.
- V12.4.2 adds bounded FIFO oversampling followed by round-robin selection by
  `client_id + device_id`. Memory and PostgreSQL stores share the same result
  contract, while PostgreSQL retains leased claims and `SKIP LOCKED` cluster
  safety. Selection mode, selected-device count, and maximum selected work per
  device are exposed in logs, metrics, diagnostics, and the admin retry-scan
  response.
- V12.4.3 adds a deterministic two-node E2E fixture with an `8:2:2` offline
  backlog. A real admin retry scan limited to three messages must select one
  message from each device, persist one retry attempt per device, and expose
  the fairness and claim metrics. The verifier also keeps capacity,
  reconnect, terminal-event, NSQ, and cluster routing checks in the same run.

Acceptance criteria:

- One device cannot consume the configured shared queue budget alone.
- Capacity rejection is explicit to the backend and is never silently dropped.
- Retry scans make progress for multiple due devices under a large backlog.
- Limits work consistently with local and cluster peer delivery.
- Static validation rejects invalid or contradictory limit settings.

### V12.5 Operator And Observability Loop

Purpose: give operators enough context to repair delivery safely.

Candidate work:

- Show policy, identity replay/conflict outcome, terminal reason, and
  dead-letter publication state in status APIs and the console.
- Add guarded bulk actions only where the action is deterministic and audited,
  such as retrying selected terminal messages under their recorded policy.
- Add metrics for idempotent replays, identity conflicts, capacity rejection,
  policy exhaustion, dead-letter publication, and retry fairness.
- Extend Grafana panels, Prometheus rules, diagnostics, and diagnosis bundles.
- Add load and E2E cases for duplicate submission, hot-device backlog, policy
  exhaustion, terminal event publication, and cluster operation.

Current implementation (V12.5.1):

- Adds `POST /internal/messages/requeue` and `Client.RequeueBatch` for up to 100
  unique selected `failed` messages. Items run independently in request order,
  retain recorded-policy semantics, obey queue capacity, and return per-item
  results with HTTP `207 Multi-Status` for partial or total item failure.
- Reuses the `message:repair` permission, records one bounded batch summary plus
  one audit event per item, and exposes
  `z_courier_downlink_bulk_requeue_total` alongside existing per-item requeue
  metrics.
- Adds failed-list selection, a confirmation checkpoint, and persistent
  per-item outcome feedback to the admin console. Readonly sessions cannot
  select or submit the operation, and the server independently enforces the
  same permission.

Current implementation (V12.5.2):

- Extends the authoritative two-node cluster verifier with a deterministic
  PostgreSQL fixture. Gateway-a persists two policy-backed messages, the
  production terminal-store transition marks them failed, and the fixture adds
  seven pending fillers for the same device under a limit of eight.
- Reuses the Redis-backed admin session created on gateway-a to submit the
  guarded batch through gateway-b. The first item fills the last shared slot;
  the second returns `queue_capacity_exceeded`, producing a real HTTP `207`
  partial result without rolling back the success.
- Queries both gateway nodes for the resulting states and immutable policy,
  checks the exact shared pending count, reads the batch summary plus both item
  audits from PostgreSQL, and requires the corresponding batch, item, capacity,
  and admin-action Prometheus samples.

Acceptance criteria:

- Operators can answer why a message failed and whether it was exported.
- All repair actions are permission-checked and audited.
- Alerting distinguishes delivery failure from dead-letter publisher failure.
- No console view displays arbitrary business bodies.

### V12.6 Release Readiness

Purpose: release reliability changes with production confidence.

Candidate work:

- Update local Compose, production Compose, Helm values, and Kubernetes E2E
  dependencies for policies and terminal-failure adapters.
- Add upgrade/rollback notes for PostgreSQL schema additions.
- Add `v0.12.0` release guidance and SDK migration examples.
- Keep CI, Docker image checks, Helm chart checks, browser smoke, E2E, and
  manual load-test summaries aligned with the new outcomes.

Current implementation (V12.6.3):

- Extracts the authoritative PostgreSQL downlink schema into the versioned
  `internal/downlink/migrations/v0.12.0.sql` file and embeds that same file in
  the gateway migration path, so manual and automatic migration cannot drift.
- Adds an isolated V11-to-V12 PostgreSQL integration fixture that preserves a
  legacy row, reruns the migration idempotently, verifies all V12 columns and
  terminal-outbox objects, exercises lazy identity fingerprinting, and proves
  that a V11-style insert remains valid after binary rollback. The two-node
  E2E runs this check before starting either gateway.
- Adds English and Chinese release-readiness guidance covering migration
  ownership, mixed-version limitations, non-destructive rollback, direct
  verification commands, and an evidence-oriented release acceptance matrix.
- Exposes named delivery policies and terminal-publisher settings through the
  production Compose references and Helm chart `0.7.0`, while preserving
  disabled policy examples and the `none` publisher default.
- Extends kind Helm E2E to select a named policy, exhaust a dedicated terminal
  policy, consume the body-free NSQ event, and verify its persisted publication
  state. Release checks now statically validate all production gateway configs.
- Extends the GitHub Actions validation job with the same single-node and
  two-node production gateway configs, using non-secret CI fixtures for every
  required environment placeholder so production config drift fails on push.

Acceptance criteria:

- Upgrade and rollback are documented and tested where schema changes occur.
- A fresh two-node deployment can validate idempotency, policy exhaustion, and
  terminal event publication end to end.
- Release checks remain green on the exact tag commit.

## Suggested Implementation Order

1. Define the idempotent write result and add the PostgreSQL fingerprint
   migration.
2. Implement compatible replay and immutable-identity conflict handling.
3. Add policies while retaining the legacy default behavior.
4. Add bounded backoff and terminal failure reasons.
5. Add queue limits and fair retry selection.
6. Add the terminal-failure publisher with durable publication state.
7. Add console, metrics, alerts, diagnostics, load tests, and release docs.

This order puts the producer contract first. Once `MessageID` replay is safe,
policy and dead-letter behavior can build on one durable message lifecycle.

## Completion Criteria

`v0.12.0` is complete when:

- Repeating a compatible downlink request is idempotent and visible.
- Reusing a `MessageID` with different immutable identity is rejected.
- Every downlink message has a deterministic, bounded delivery policy.
- Terminal failure has a durable reason and optional reliable metadata export.
- Queue capacity and retry work remain fair under offline backlog.
- Operators can inspect and safely repair terminal messages without seeing
  business payloads.
- Metrics, diagnostics, alerts, Compose, Helm, CI, E2E, and release guidance
  cover the new lifecycle.

## Known Boundaries

- Client delivery remains at-least-once and clients must de-duplicate received
  messages by `MessageID`.
- Backend idempotency protects gateway submission, not business processing.
- Dead-letter publication is an operational event path, not a transaction with
  the original client delivery.
- The gateway remains protocol- and payload-agnostic beyond metadata required
  for routing and reliability.
- The admin console remains an internal operations surface.
