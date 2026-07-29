# V16 Roadmap

V16 is the planning track after `v0.15.0`. Its target public SemVer version is
`v0.16.0`, not `v16.0.0`.

`v0.15.0` makes HTTP upstream routing resilient to endpoint changes through
static and DNS discovery. The next practical reliability gap is admission
control: a single noisy client, route, or gateway node should not consume the
capacity needed by every other client.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.16.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

V16 replaces the current fixed-window, process-local per-client limiter with
named traffic policies. A policy decides whether an authenticated ingress
packet may continue through the gateway; it never reads or interprets the
opaque business `Body`.

The release should provide two explicit modes:

- `local`: a bounded in-memory token bucket for simple deployments and a
  zero-external-dependency default;
- `redis`: an opt-in atomic shared quota for clusters that need a client or
  route limit to hold across gateway nodes.

Per-route `max_in_flight` remains a separate downstream concurrency guard. A
traffic policy controls admission before forwarding begins; it does not create
an inbound durable queue, promise delivery, or change the downstream retry
system.

## Current Boundary

Today the ingress pipeline is:

```text
auth -> allowlist/blocklist -> fixed-window per-client rate limit
-> session bind -> access log -> route forwarding
```

The current bucket map is process-local and unbounded by policy. In a cluster,
the same client can use a separate limit on each gateway node. V16 must retain
the existing disabled-by-default behavior while making memory, key lifetime,
and distributed semantics explicit.

## Non-Goals

V16 does not target:

- inspecting, classifying, storing, or limiting by business message body;
- durable inbound queues, delayed admission, or exactly-once upstream
  delivery;
- arbitrary runtime policy mutation from the Console;
- replacing route `max_in_flight`, HTTP discovery failover, or downlink retry;
- mandatory Redis for standalone gateway deployments;
- a global cross-region quota service or a cloud-provider rate-limit API.

## Policy Contract

A named policy is selected after authentication and list checks, before a
session is bound or an upstream route is called. It may match only stable,
non-body fields:

- authenticated `client_id`;
- `MsgID` range;
- resolved upstream route name for ordinary upstream packets;
- an optional default policy when no named selector matches.

Selectors have deterministic priority. Invalid overlapping policies must fail
startup unless the order and winner are unambiguous. Every policy defines its
algorithm, key scope, refill/limit values, and overload action. The initial
algorithm is token bucket; fixed-window behavior remains available only as a
compatibility configuration during migration if needed.

Candidate shape:

```yaml
pipeline:
  traffic_policies:
    enabled: true
    mode: local
    max_keys: 100000
    idle_ttl: 10m
    default_policy: standard
    policies:
      - name: standard
        enabled: true
        priority: 100
        match:
          msg_id_min: 1001
          msg_id_max: 2999
        key: client_id
        token_bucket:
          capacity: 100
          refill_tokens: 100
          refill_interval: 1s
      - name: orders
        enabled: true
        priority: 200
        match:
          routes: [orders-http]
        key: client_id
        token_bucket:
          capacity: 20
          refill_tokens: 20
          refill_interval: 1s
```

These field names are now the V16.1 local-policy contract. Config remains
declarative and rollout-owned; the Console can show policy state and outcomes
but cannot alter quotas.

## Current Implementation Status

The current Unreleased implementation completes the V16.1 configuration and
selection contract together with the bounded local admission core:

- named policies select deterministically by authenticated ClientID, MsgID, and
  enabled upstream route, with larger priorities winning;
- ambiguous same-priority overlaps, unknown routes, invalid buckets, missing
  defaults, and simultaneous legacy/new limiters fail startup;
- `local` uses a concurrency-safe token bucket bounded by `max_keys`, with
  `idle_ttl` cleanup and stable `rate_limited`/`overloaded` outcomes;
- only the authenticated `client_id` key is supported before session binding;
  device-scoped keys remain deferred until their trust boundary is preserved;
- `redis` is deliberately rejected rather than accepted as an inactive mode.
- a Docker-free real-TCP verifier covers local burst, refill, precedence,
  no-policy pass-through, bounded-key overload, idle eviction, stable client
  rejection, and rejection-before-forwarding; CI and release checks run the
  same scenario.
- admission now uses a narrow quota-store contract with explicit allowed,
  rate-limited, overloaded, and admission-unavailable decisions; the bounded
  local implementation preserves its existing LRU, TTL, refill, and
  concurrency semantics behind that contract.

Redis quotas, dedicated admission observability, Console views, deployment
surfaces, two-node Redis E2E, and full release guidance remain later V16
workstreams.

## Failure And Client Contract

Rejected traffic receives the existing rejected ACK path with a stable,
body-free reason. V16 distinguishes at least:

- `rate_limited`: the selected policy has no available token;
- `overloaded`: a concurrency or bounded-admission resource is exhausted;
- `admission_unavailable`: an opted-in distributed limiter cannot safely make
  its configured decision.

No token count, Redis key, endpoint address, raw error, or business body is
included in the client ACK. Structured logs, audit records, and metrics include
only policy name, scope, route where known, and low-cardinality result labels.

For Redis mode, the operator explicitly selects failure behavior. The safe
default for a policy requiring a global quota is fail-closed with
`admission_unavailable`. A reviewed local fallback may be allowed only when the
operator accepts temporary per-node rather than cluster-wide enforcement.

## Workstreams

### V16.1 Policy Configuration And Selection

Purpose: define deterministic, backward-compatible policy selection before
changing limiter behavior.

- Add policy types, selector validation, and a migration path from
  `pipeline.rate_limit`.
- Resolve route names from MsgID without evaluating business bodies.
- Reject ambiguous overlaps, invalid token-bucket values, unsupported key
  scopes, missing defaults, and Redis mode without Redis configuration.
- Document policy order, bind-packet handling, and disabled-policy behavior in
  English and Chinese.

Acceptance criteria:

- Existing `pipeline.rate_limit` configuration retains its documented behavior
  until an operator migrates to traffic policies.
- The selected policy is deterministic for every supported packet type.
- Invalid configuration fails startup without logging tokens or packet bodies.

### V16.2 Bounded Local Admission

Purpose: provide predictable standalone protection without an external store.

Status: implemented with focused unit/concurrency coverage and a real TCP
single-node integration verifier.

- Implement a concurrency-safe token-bucket limiter with injectable time.
- Bound live keys with `max_keys` and remove idle buckets after `idle_ttl`.
- Define deterministic behavior when the local key capacity is exhausted.
- Preserve disabled-policy and no-policy fast paths without allocation.

Acceptance criteria:

- A high-cardinality client-ID flood cannot grow limiter memory without bound.
- Refill behavior, burst capacity, and rejection boundaries are deterministic
  under concurrent requests.
- A disconnected or idle client eventually releases its local bucket.

### V16.3 Optional Redis Cluster Quotas

Purpose: enforce one quota across gateway nodes without making Redis mandatory.

Status: in progress. The store contract and local adapter are implemented;
Redis configuration, atomic operations, lifecycle wiring, and outage behavior
remain pending.

- Add a narrow quota-store interface and Redis implementation using an atomic,
  time-bounded operation.
- Namespace keys by policy and configured gateway cluster prefix; do not expose
  raw client IDs in metric labels.
- Bound Redis key TTL to bucket lifetime and make reconnect/retry behavior
  explicit.
- Implement selected fail-closed or reviewed local-fallback behavior when Redis
  is unavailable.

Acceptance criteria:

- Concurrent requests through two gateway nodes cannot exceed a shared quota.
- Redis recovery resumes shared enforcement without gateway restart.
- A Redis outage produces the configured stable ACK reason and audit-safe
  evidence.

### V16.4 Admission Observability And Operations

Purpose: make rejection understandable without exposing tenant data.

- Add low-cardinality metrics for admitted/rejected packets, policy selection,
  token-bucket state class, quota-store results, and local key capacity.
- Extend diagnostics, diagnosis bundles, Grafana dashboards, and alerts with
  sanitized policy health and rejection trends.
- Add read-only Console views for configured policy summaries and recent
  aggregate outcomes.
- Add bilingual operator guidance for tuning, canary rollout, Redis outage,
  and rollback.

Acceptance criteria:

- Operators can distinguish a client quota, route quota, local key-capacity
  event, route overload, and Redis quota-store outage.
- No metric label, Console response, diagnosis bundle, or alert contains a
  client ID, device ID, token, Redis key, or business body.

### V16.5 End-To-End And Release Coverage

Purpose: prove admission is bounded and predictable in the real gateway path.

- Add focused limiter, configuration, and Redis atomicity tests.
- Add single-node E2E for burst, refill, policy precedence, and bounded-key
  behavior.
- Add two-node Redis E2E proving a client cannot bypass a shared quota by
  reconnecting to another gateway.
- Add Compose, Helm, CI, release-check, and English/Chinese release guidance.

Acceptance criteria:

- CI verifies local and Redis modes without weakening existing HTTP/NSQ,
  discovery, downlink, or cluster tests.
- A release check proves local memory remains bounded and Redis keys expire.
- Operators can deploy, observe, and roll back traffic policies with no packet
  protocol migration.

## Suggested Implementation Order

1. Add configuration types, precedence validation, and documentation.
2. Build bounded local token buckets with deterministic tests.
3. Wire policy selection into the ingress chain and preserve legacy behavior.
4. Add Redis shared quotas and two-node E2E coverage.
5. Add metrics, diagnostics, Console, dashboards, alerts, deployment examples,
   and release acceptance.

## Completion Criteria

`v0.16.0` is complete when:

- ingress limits are selected from deterministic, body-agnostic named policies;
- local admission memory and bucket lifetime are bounded;
- Redis-backed cluster quotas are optional, atomic, and have explicit outage
  behavior;
- existing rate limits, route concurrency controls, and client protocol remain
  backward-compatible; and
- configuration, E2E coverage, Compose, Helm, observability, and bilingual
  operations/release documentation cover the supported model.

## Known Boundaries

- Traffic policies protect gateway admission; backend services still need their
  own concurrency, idempotency, and business authorization controls.
- Token buckets shape accepted packet rate; they do not reserve an upstream
  endpoint or guarantee backend processing.
- Redis shared quotas trade an external dependency for cross-node consistency.
  Operators should use local mode when that dependency is not justified.
- A future control-plane adapter may distribute policy configuration, but it
  must preserve V16's explicit selection and failure contract.
