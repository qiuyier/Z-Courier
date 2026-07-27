# V15 Roadmap

V15 is the planning track for the next public milestone after `v0.14.1`. Its
target SemVer version is `v0.15.0`, not `v15.0.0`.

`v0.14.1` completes the production transport-security work: gateway edges can
use TLS/mTLS, signing keys and certificates have a reversible rotation path,
and deployment references have been exercised through Compose and Kubernetes.
The next practical gap is outbound routing. A static upstream URL works for a
single service instance, but it does not describe how a gateway should follow
a rolling deployment, an instance failure, or a Kubernetes Service.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.15.0` until it is implemented, documented, tested, and included in the
release guide for that version.

Implementation status:

- V15.1 defines and validates the static/DNS discovery and bounded failover
  configuration contract.
- V15.2.1 makes static discovery operational with immutable snapshots,
  round-robin selection, process-local cooldown, and bounded transport
  failover.
- V15.2.2 makes DNS A/AAAA discovery operational with periodic immutable
  snapshots, last-known-good retention, retired-endpoint cleanup, preserved
  logical Host/SNI, and lifecycle-aware cancellation.
- V15.3.1 adds explicit forwarding failure classes, bounded failover decisions,
  audit-safe endpoint and attempt metadata, stable client ACK reasons, and
  cancellation-safe request handling.
- V15.4.1 adds low-cardinality route-level metrics for discovery refreshes,
  active and unhealthy endpoint counts, selection/cooldown behavior, classified
  endpoint failures, attempt distributions, and terminal failover decisions.
- V15.4.2 adds sanitized process-local discovery snapshots to admin diagnostics
  and diagnosis bundles without exposing endpoint identity or raw errors.
- V15.4.3 renders those snapshots as a read-only Console route view, adds
  discovery and failover panels to the operations and production Grafana
  dashboards, and ships readiness-gated availability alerts with promtool
  behavior tests and bilingual response guidance.

## Product Direction

V15 adds health-aware endpoint selection to the existing HTTP upstream
forwarder. The gateway remains body-agnostic: it selects a destination and
forwards the unchanged envelope; it does not inspect, transform, persist, or
infer business semantics from `Body`.

The first release should support two discovery sources:

- `static`: an explicit list of HTTP endpoint URLs, for Docker, VMs, and
  deployments that already have their own service registry;
- `dns`: DNS A/AAAA resolution of a hostname, suitable for Kubernetes Service
  DNS, headless Services, and ordinary internal DNS.

The discovery abstraction should be deliberately small so a future Consul,
Kubernetes EndpointSlice, or custom registry adapter can be added outside the
gateway's core transport logic. V15 must not require a cluster-wide registry,
a Kubernetes client dependency, or a control-plane database.

## Non-Goals

V15 does not target:

- changing the client packet protocol, `MsgID` route selection, or the
  opaque-message-body rule;
- durable upstream queuing, exactly-once HTTP delivery, or automatic replay of
  an already accepted business request;
- arbitrary configuration mutation from the admin console;
- Consul, etcd, Kubernetes EndpointSlice, or cloud-provider discovery in the
  first implementation;
- active health probes that execute customer business endpoints;
- replacing the existing NSQ producer route or changing its semantics.

## Delivery Safety Contract

An HTTP route still has at-least-once transport risk whenever an upstream
request may have reached a backend before a network failure is observed. The
gateway must never hide that fact.

- Endpoint selection happens before a request is sent.
- A connection error or timeout before response headers may try another
  currently healthy endpoint only when the route opt-in is enabled.
- A response with received headers is never replayed by default, including
  `5xx`; the backend has already observed a request attempt.
- The backend must treat `MessageID` as its idempotency key for any operation
  where duplicate processing is unsafe.
- Existing single-URL routes retain their current one-attempt behavior.
  Operators opt into failover by migrating that route to discovery mode.

This keeps the gateway honest about delivery ambiguity while still letting a
new TCP connection avoid an endpoint known to be unavailable.

## Configuration Shape

The existing `target.url` remains supported. A multi-endpoint route is
explicit and mutually exclusive with `url`:

```yaml
upstream:
  routes:
    - name: orders-http
      enabled: true
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        discovery:
          type: dns
          scheme: http
          hostname: orders.default.svc.cluster.local
          port: 8080
          refresh_interval: 10s
        path: /gateway/upstream
        timeout: 2s
        failover:
          enabled: true
          max_attempts: 2
          unhealthy_cooldown: 15s
```

For static discovery, `endpoints` contains complete URLs. DNS produces
addresses only; the route supplies scheme, port, and path explicitly so TLS
server-name behavior remains predictable. Validation rejects empty endpoint
sets, ambiguous `url` plus `discovery`, unsupported schemes, zero ports,
invalid intervals, and failover counts outside a bounded range.

## Workstreams

### V15.1 Discovery Contract And Configuration

Purpose: define stable operator-facing behavior before altering forwarding.

- Add discovery and failover configuration types with strict validation.
- Preserve existing `http.url` configuration and its default behavior.
- Define endpoint identity, DNS refresh lifetime, empty-resolution behavior,
  and sanitized configuration errors.
- Document DNS operational requirements, including Kubernetes Service versus
  headless Service behavior and DNS TTL caveats.

Acceptance criteria:

- Existing static single-URL routes produce identical requests and metrics.
- Invalid discovery configuration fails gateway startup without exposing
  credentials or opaque payloads.
- A route with no usable endpoint returns a clear, bounded upstream failure.

### V15.2 Endpoint Selection And Health Memory

Purpose: select useful endpoints without creating an unbounded registry cache.

- Introduce a narrow resolver interface returning immutable endpoint snapshots.
- Implement static and DNS resolvers with bounded refresh, cancellation, and
  last-known-good snapshots during a transient DNS failure.
- Use round-robin selection among healthy endpoints.
- Track local endpoint failures with an expiring unhealthy cooldown; a success
  restores eligibility immediately.
- Keep health state process-local. DNS and the backend load balancer remain the
  cross-node sources of truth.

Acceptance criteria:

- Endpoint selection is race-safe and does not grow with historical DNS
  answers.
- DNS refresh removes retired endpoints after a successful resolution.
- A failed endpoint is skipped during cooldown when another healthy endpoint
  exists, then becomes eligible again.

### V15.3 Explicit Failover Semantics

Purpose: improve connection-failure availability without claiming exactly once.

- Classify retryable pre-response transport failures separately from received
  HTTP responses.
- Keep existing single-URL routes at one attempt. Discovery routes with
  failover enabled default to two attempts and remain explicitly bounded.
- Add the endpoint and attempt count to structured logs and audit-safe error
  metadata, never to client responses if it would expose internal topology.
- Reuse the existing request `MessageID`; do not create a new business identity
  for a failover attempt.

Acceptance criteria:

- One unreachable endpoint followed by a healthy endpoint succeeds only when
  failover is opted in.
- A backend `500` is not replayed automatically.
- Timeout and cancellation close resources promptly and do not retry forever.

### V15.4 Operations, Visibility, And Deployment

Purpose: make dynamic routing observable and usable without a hidden control
plane.

- Add route-level metrics for discovery refreshes, resolved endpoints, endpoint
  selections, failures, cooldown skips, and failover attempts/decisions.
- Expose sanitized route/discovery state through diagnostics and a read-only
  admin-console view.
- Add Grafana panels and alert guidance for sustained empty discovery results
  and all-endpoint failures.
- Add Docker and Helm examples using static lists and Kubernetes DNS; document
  the same configuration in English and Chinese.

Acceptance criteria:

- Operators can distinguish DNS failure, empty discovery, endpoint cooldown,
  and backend response failure without viewing a business body or secret.
- The console remains read-only for route discovery configuration.
- Compose, Helm, CI, and image validation cover the supported configurations.

### V15.5 End-To-End And Release Coverage

Purpose: verify that failover remains predictable in the real gateway path.

- Add focused resolver and forwarder tests with deterministic time/refresh
  controls.
- Add a two-upstream integration verifier covering initial selection,
  connection failure failover, cooldown, recovery, and non-replay of `5xx`.
- Add a Kubernetes DNS E2E using a Service or headless Service without adding a
  Kubernetes client library to the gateway.
- Add release acceptance checks and a bilingual `v0.15.0` release guide.

Acceptance criteria:

- CI demonstrates both static and DNS discovery.
- A rolling backend replacement is observed after the configured refresh
  interval without gateway restart.
- Existing HTTP and NSQ upstream E2E paths remain green.

## Suggested Implementation Order

1. Add configuration types, validation, and documentation for static lists.
2. Extract HTTP endpoint selection behind a resolver interface.
3. Implement static resolution, endpoint cooldown, and focused failover tests.
4. Add DNS resolution and deterministic refresh tests.
5. Add metrics, diagnostics, console visibility, deployment examples, and E2E.

## Completion Criteria

`v0.15.0` is complete when:

- HTTP routes can safely select endpoints from static lists and DNS;
- existing single-URL HTTP routes and NSQ routes remain backward-compatible;
- endpoint failover has explicit bounded semantics and never promises exactly
  once delivery;
- operators can observe discovery and endpoint health without seeing secrets
  or business message bodies; and
- configuration, tests, Compose, Helm, CI, and English/Chinese documentation
  cover the supported model.

## Known Boundaries

- DNS availability and authoritative record correctness remain deployment
  responsibilities.
- DNS is not a substitute for backend authentication, TLS/mTLS, network policy,
  or application-level idempotency.
- Process-local cooldown state is intentionally not shared through Redis; a
  backend load balancer or DNS remains the correct coordination layer.
- A later registry adapter must preserve the resolver snapshot and failover
  contract rather than inventing route-specific delivery behavior.
