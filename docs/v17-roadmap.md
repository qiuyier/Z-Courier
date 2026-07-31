# V17 Roadmap

V17 is the planning track after `v0.16.0`. Its target public SemVer version is
`v0.17.0`, not `v17.0.0`.

`v0.16.0` adds deterministic named traffic policies, bounded local admission,
and optional Redis-backed cluster quotas. The next practical operations gap is
route change management: changing a MsgID mapping, HTTP target, discovery
source, or NSQ target currently requires a gateway restart and therefore
reconnects long-lived clients.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.17.0` until it is implemented, documented, tested, and included in the
release acceptance path.

## Product Direction

V17 adds safe, node-local hot reload for upstream routes. An operator should be
able to validate and activate a complete candidate route table without
restarting the Zinx TCP server or disconnecting existing client sessions.

The central rule is:

```text
parse -> validate -> build complete candidate -> atomically activate
                                      |
                                      +-> any failure keeps the old generation
```

Each ordinary upstream request uses exactly one immutable route generation from
route-aware traffic-policy selection through forwarding completion. A reload
never moves an in-flight request to another target and never replays it.

The first release should support:

- a bounded local route file as the reload source;
- strict dry-run validation before activation;
- process `SIGHUP`, an authenticated internal admin API, and `cmd/admin`
  commands as explicit triggers;
- immutable route generations with lease-based retirement;
- HTTP, static/DNS discovery, and NSQ forwarders using their existing behavior;
- node-local status, audit, metrics, diagnostics, Console visibility, and
  canary-oriented cluster operations.

## Current Boundary

The current gateway constructs routing once during startup:

- `server.New` converts the configured routes into one `router.Engine`;
- `IngressRouter` holds a fixed pointer to that engine;
- each route owns an HTTP forwarder, DNS resolver, NSQ producer, capacity
  limiter, dependency tracker, and related metric state;
- `Gateway.Shutdown` closes the engine and all route-owned resources once;
- admin route and diagnostic handlers read a startup-time `server.Config`;
- traffic-policy selectors compile route names and MsgID ranges at startup;
- Zinx registers every accepted outer MsgID in an internal map before service
  starts.

Zinx `v1.2.7` does not provide a concurrency-safe remove or replace operation
for its MsgID router map. V17 must not mutate that map while requests are being
dispatched. MsgIDs eligible for future route activation therefore need a
bounded startup-time admission range.

## Current Implementation Status

V17.1 is implemented:

- `upstream.routes_file` is mutually exclusive with inline `upstream.routes`;
- relative route paths resolve against the main gateway config directory;
- bounded file reading, environment expansion, strict known fields, exactly one
  YAML document, `version: 1`, bounded route count, and unique route names are
  enforced;
- validation and `ToServerConfig` consume one parsed route snapshot while
  reusing the existing HTTP, static/DNS discovery, NSQ, overlap, reserved
  MsgID, and traffic-policy validation paths;
- reload admission ranges are normalized, bounded, checked against reserved
  MsgIDs, and registered in Zinx before service starts;
- source examples, focused tests, CI/release config validation, and bilingual
  configuration documentation cover the startup contract.

Runtime generation switching, request leases, and reload triggers are not part
of V17.1 and remain intentionally inactive.

## Non-Goals

V17 does not target:

- reloading the complete gateway configuration;
- changing authentication, HMAC keys, TLS files, cluster registry, PostgreSQL,
  Redis quota stores, downlink policies, or admin-session storage at runtime;
- a distributed configuration database, consensus protocol, or cluster-wide
  atomic commit;
- accepting arbitrary route configuration bodies from the browser Console;
- watching remote URLs, Git repositories, or object storage;
- active business-endpoint health probes;
- replaying or migrating requests already in flight;
- Kafka, NATS, gRPC, message transformation, or business-body inspection;
- exactly-once upstream processing.

Traffic-policy definitions remain startup-owned in V17. A rolling restart is
still required when adding or changing route-specific policies.

## Compatibility Contract

Hot reload is disabled by default. Existing `upstream.routes` configurations
retain their startup-only behavior without a file, signal handler, background
watcher, or additional runtime allocation.

The client packet protocol, bind flow, ACK schema, `MessageID`, `TraceID`, and
HTTP/NSQ upstream envelopes remain unchanged.

The reload file mode is mutually exclusive with inline `upstream.routes`. When
file mode is enabled, the same file is loaded and validated before the TCP
listener opens, so startup and later reloads cannot interpret the route schema
differently.

## Configuration Shape

```yaml
upstream:
  routes_file:
    path: /etc/z-courier/upstream-routes.yaml
    max_size_bytes: 1048576
    reload:
      enabled: true
      drain_timeout: 30s
      accepted_msg_id_ranges:
        - min: 1001
          max: 2999
```

The referenced file contains only the versioned route document:

```yaml
version: 1
routes:
  - name: orders-http
    enabled: true
    msg_id_min: 1001
    msg_id_max: 1999
    target:
      type: http
      url: http://orders.internal:8080/gateway/upstream
      token_env: ZCOURIER_UPSTREAM_INTERNAL_TOKEN
      timeout: 5s
      max_in_flight: 2000
```

Requirements:

- the file path is local and fixed by startup configuration;
- the file size, route count, route-name length, and accepted MsgID ranges are
  bounded;
- unknown fields, duplicate names, overlapping ranges, reserved MsgIDs,
  unsupported targets, and unresolved required environment variables fail
  validation;
- every active route must fit inside a startup-time accepted MsgID range;
- the route document cannot override its path, limits, accepted ranges, or
  reload permissions;
- secrets remain environment or Secret references and never appear in admin
  responses, audit records, metrics, or diagnosis bundles.

Kubernetes projected ConfigMaps and ordinary read-only bind mounts are valid
sources. V17 uses explicit reload triggers rather than an implicit file watcher,
so an operator controls exactly when a newly projected file becomes active.

## Zinx MsgID Admission Contract

At startup, the gateway registers one stable `IngressRouter` for:

- reserved protocol MsgIDs such as AUTH/BIND and downlink ACK;
- the initial route table;
- every MsgID inside the configured reload admission ranges.

The reload manager changes only the immutable route generation behind that
stable router. It never calls Zinx `AddRouter` or edits the Zinx router map after
the server starts.

Consequences:

- a candidate route outside the startup admission ranges is rejected;
- removing a route is safe because the outer MsgID remains registered;
- a packet inside an admitted range with no active route receives the existing
  stable route-not-found rejection instead of being dropped by Zinx;
- expanding the admission envelope requires a controlled gateway restart.

Admission ranges remain bounded by validation so a configuration cannot create
an unbounded Zinx router table or startup log volume.

## Route Generation And Lease Contract

The runtime owns a `RouteManager` containing one active immutable generation.
A generation contains:

- a monotonically increasing process-local generation number;
- a sanitized source fingerprint and activation timestamp;
- the route lookup table and route-aware policy resolver;
- all forwarders, resolvers, producers, limiters, and runtime trackers owned by
  that route table;
- an in-flight lease count and active, retiring, or closed lifecycle state.

An ordinary upstream request acquires a lease before route-aware admission and
releases it after its forwarding attempt finishes. The lease pins one
generation for the complete request.

A successful reload:

1. serializes with every other reload attempt;
2. reads a bounded snapshot of the configured file;
3. parses and validates the entire candidate;
4. constructs every candidate route and owned resource;
5. atomically swaps the active generation;
6. marks the previous generation as retiring;
7. closes the previous generation exactly once after its final lease returns.

Candidate construction failure closes every candidate-owned resource and keeps
the active generation untouched.

V17 does not force-close a forwarder while it still has an active lease.
Existing HTTP and NSQ timeouts bound normal retirement. The configured drain
timeout is an operations warning threshold, not permission to interrupt an
in-flight request. To bound retained resources, only one retiring generation is
allowed; another reload returns `reload_busy` until retirement completes.

Gateway shutdown first prevents new reloads, then closes the active and any
retiring generation through the same idempotent lifecycle.

## Traffic-Policy Consistency

Traffic policies can select a policy by route name. A reload must not let
admission use one route table while forwarding uses another.

V17 therefore changes route-aware selection to consume the route name resolved
from the request's generation lease. Local and Redis quota stores remain
startup-owned and retain their existing buckets across route reloads.

Candidate validation uses the active startup-owned traffic-policy definitions:

- every route name referenced by a policy must still exist;
- candidate ranges must preserve deterministic policy matching;
- ambiguous or invalid route-policy combinations reject the reload;
- new routes may use an existing default or MsgID/client policy;
- adding a new route-specific policy still requires a rolling restart.

This keeps quota semantics stable while allowing targets and compatible route
tables to change without reconnecting clients.

## Forwarding And Failure Contract

Reload never changes the delivery contract:

- a request already leased to the old generation completes against the old
  forwarder;
- a request leased after activation uses the new generation;
- a request is never duplicated merely because a reload occurred;
- HTTP responses with received headers remain non-replayable by default;
- backend services still use `MessageID` as their idempotency key;
- reload failure does not change gateway readiness when the old generation is
  still active;
- client ACKs never contain file paths, target addresses, configuration
  details, or candidate errors.

Admin callers receive stable failure codes such as:

- `reload_disabled`;
- `reload_busy`;
- `generation_conflict`;
- `invalid_candidate`;
- `candidate_build_failed`;
- `reload_failed`.

Detailed validation problems are bounded and sanitized for privileged operator
responses and logs.

## Trigger And Admin Contract

V17 provides three explicit trigger paths:

- `SIGHUP`: load, validate, build, and activate the configured file;
- `POST /internal/admin/routes/reload`: perform a dry run or activation through
  existing internal authentication and Console mutation protection;
- `cmd/admin routes validate|reload|status`: provide scriptable operator access.

The admin request may include `dry_run` and `expected_generation`. It never
contains the route document itself. `expected_generation` prevents an operator
from replacing a generation that changed after inspection.

Read-only operators can inspect route status. Reload and dry-run actions require
an explicit operator/admin permission and produce an audit event containing:

- trigger type and authenticated actor;
- old and candidate generation;
- route counts and sanitized fingerprint;
- result, stable reason, and duration;
- activation and retirement timestamps where applicable.

No audit event includes tokens, HMAC material, environment values, message
bodies, raw DSNs, or full endpoint URLs.

## Cluster Rollout Contract

Route generation is node-local. V17 deliberately does not claim a distributed
atomic reload across gateway pods.

A safe cluster rollout is:

1. project the same candidate file to every target pod;
2. dry-run each pod and compare the sanitized candidate fingerprint;
3. reload one canary pod;
4. verify forwarding, errors, latency, and retirement;
5. reload the remaining pods in bounded batches;
6. verify generation and fingerprint convergence.

Mixed generations are expected during the canary window. Existing client
connections stay on their current gateway node, while every request on a node
still uses one internally consistent generation.

Rollback uses the same mechanism: restore the previous file, dry-run it, and
activate a new generation. Generation numbers always increase even when the
route content returns to an older fingerprint.

## Observability Contract

Prometheus visibility should include:

- `z_courier_route_reload_total{trigger,result}`;
- `z_courier_route_reload_duration_seconds{result}`;
- `z_courier_route_generation`;
- `z_courier_route_retiring_generations`;
- `z_courier_route_reload_last_success_timestamp_seconds`;
- a bounded retirement-duration signal.

Trigger, result, and reason labels use fixed enums. Generation numbers,
fingerprints, file paths, endpoint URLs, client IDs, and arbitrary validation
text are never metric labels.

Existing route metrics retain validated route-name labels. Retiring a
generation must remove obsolete mutable gauge series and prevent repeated route
name churn from causing unbounded process-local metric state.

Diagnostics and diagnosis bundles show:

- enabled/disabled reload mode;
- current generation, activation age, route count, and fingerprint prefix;
- candidate/retiring state;
- last attempt time, result, trigger, and sanitized reason;
- drain age and actionable warnings.

The Console Routes view should show the same status, provide dry-run and reload
actions only to authorized roles, and require an explicit confirmation before
activation. It must remain usable on narrow desktop and mobile viewports.

## Workstreams

### V17.1 Route Source And Validation Contract

Purpose: define one strict route document and the immutable startup boundary.

Status: implemented. File/inline source exclusivity, relative path resolution,
bounded one-snapshot loading, strict versioned YAML, environment expansion,
existing route/policy validation reuse, startup admission ranges, source
examples, focused tests, CI/release checks, and bilingual configuration
guidance are complete.

- Add file-source configuration while preserving inline route compatibility.
- Reuse the existing route conversion and validation rules rather than creating
  a second parser with different semantics.
- Add bounded file reading, strict version/unknown-field handling, environment
  resolution, and sanitized fingerprints.
- Validate startup admission ranges, reserved IDs, traffic-policy references,
  route count, and source mutual exclusion.
- Register the complete accepted MsgID envelope before Zinx starts.

Acceptance criteria:

- reload remains zero-cost and disabled for existing configurations;
- startup and reload use the same route validation path;
- malformed, oversized, ambiguous, secret-invalid, or out-of-range candidates
  cannot construct a gateway generation;
- no runtime call mutates the Zinx MsgID router map.

### V17.2 Atomic Generation Manager

Purpose: switch complete route tables without disrupting in-flight forwarding.

- Introduce immutable generations, request leases, atomic activation, and
  serialized reloads.
- Move route lookup and route-aware traffic-policy resolution onto the same
  request generation.
- Make HTTP resolvers, NSQ producers, limiters, dependency trackers, and metric
  cleanup explicitly generation-owned.
- Close failed candidates and retired generations exactly once.
- Integrate active and retiring generations with graceful gateway shutdown.

Acceptance criteria:

- race-enabled tests find no route lookup, swap, acquire, release, or close
  race;
- requests leased before a swap finish on the old controlled backend;
- requests leased after a swap reach only the new controlled backend;
- a failed candidate leaves the old generation and its runtime evidence intact;
- repeated reload attempts cannot create unbounded retiring resources.

### V17.3 Triggers, Permissions, And Audit

Purpose: make reload controlled, scriptable, and attributable.

- Add `SIGHUP` handling without interfering with SIGINT/SIGTERM shutdown.
- Add dry-run, compare-generation, reload, and status admin contracts.
- Extend `cmd/admin` with route validation, reload, and status commands.
- Add explicit Console permissions and mutation guards.
- Store bounded audit evidence for success, rejection, failure, and rollback.

Acceptance criteria:

- read-only sessions cannot trigger or dry-run a reload;
- stale expected generations fail without reading a candidate into service;
- concurrent API and signal reloads serialize deterministically;
- no control-plane response or audit record leaks route credentials.

### V17.4 Operations And Console

Purpose: make rollout state and failures understandable without high-cardinality
telemetry.

- Add fixed-label metrics, runtime snapshots, diagnostics, diagnosis bundles,
  warnings, and recording/alert rules.
- Make admin route responses read from the active generation rather than the
  startup config copy.
- Extend the Console Routes view with generation, candidate, retirement,
  dry-run, reload, and rollback-oriented status.
- Add bilingual first-response guidance for failed reloads, slow retirement,
  mixed cluster generations, and metric cleanup.

Acceptance criteria:

- operators can distinguish parse, validation, candidate-build, conflict, busy,
  activation, and retirement states;
- route reload does not introduce fingerprint, generation, endpoint, or
  validation-message metric labels;
- obsolete route metric state is bounded and testable;
- responsive browser smoke covers admin, operator, and read-only roles.

### V17.5 Deployment And End-To-End Coverage

Purpose: prove hot reload through the real gateway path and supported deployment
references.

- Add Compose and Helm examples using read-only mounted route files.
- Document ConfigMap projection timing and explicit per-pod reload.
- Add a Docker-free real-TCP verifier for target switch, failed candidate,
  in-flight drain, route add/remove inside the admission envelope, and rollback.
- Add a two-node Kind/Helm scenario for dry-run convergence, canary activation,
  mixed generations, full convergence, and rollback without client reconnect.
- Cover DNS resolver and NSQ producer retirement with focused lifecycle tests.
- Integrate checks into CI, release checks, and English/Chinese release guides.

Acceptance criteria:

- the same connected client sends successfully before, during, and after a
  route generation change;
- controlled in-flight requests are neither replayed nor interrupted;
- two gateway pods can temporarily run mixed generations and then converge
  without corrupting cluster session routing or shared traffic quotas;
- source-tree, built-image, Compose, and Helm references all exercise the same
  route-file contract.

## Suggested Implementation Order

1. Finalize the file schema, compatibility rules, and MsgID admission envelope.
2. Extract one reusable route loader and validator.
3. Build the generation manager and lease lifecycle with race tests.
4. Pin traffic-policy selection and forwarding to one generation.
5. Add dry-run, `SIGHUP`, admin API, CLI, permission, and audit behavior.
6. Add metrics, diagnostics, Console, dashboards, alerts, and metric cleanup.
7. Add Compose, Helm, single-node, two-node, rollback, and release coverage.

## Completion Criteria

`v0.17.0` is complete when:

- a valid route file can be activated without restarting Zinx or disconnecting
  existing clients;
- every upstream request uses one immutable generation for policy selection and
  forwarding;
- invalid or unbuildable candidates leave the active generation untouched;
- retiring forwarders close exactly once after in-flight leases complete;
- runtime MsgID changes remain inside a bounded startup admission envelope;
- node-local canary, convergence, rollback, permissions, audit, metrics,
  diagnostics, Console, Compose, Helm, and bilingual operations guidance are
  verified in CI and release acceptance.

## Known Boundaries

- Route hot reload reduces client disruption but does not make backend changes
  transactionally atomic with gateway changes.
- Cluster-wide convergence remains an operator or deployment-controller
  responsibility.
- Route-specific traffic-policy definition changes still require a rolling
  gateway restart.
- A stuck forwarder can delay generation retirement up to its configured
  operation timeout; operators must keep transport timeouts finite.
- Expanding the accepted MsgID envelope, changing secrets, or changing
  non-route gateway dependencies requires a restart.
- A future control-plane adapter may distribute signed route documents, but it
  must preserve V17's validation, immutable-generation, lease, and rollback
  contracts.
