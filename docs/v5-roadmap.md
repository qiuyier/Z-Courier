# V5 Roadmap

V5 is the planning track for the next public milestone after `v0.4.0`. Its
target SemVer version is `v0.5.0`, not `v5.0.0`. The goal is to move Z-Courier
from a working gateway and SDK foundation toward an open-source middleware that
is easier to deploy, operate, and integrate from more languages.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.5.0` until it is implemented, documented, tested, and included in the
release guide for that version.

The current `v0.5.0` release guide is [v5-release.md](v5-release.md). Its
scope is intentionally narrowed to deployment artifacts and operations/admin
readiness; additional SDKs and Kubernetes/Helm support remain future work.

## Product Direction

V1 through V4 established the core:

- A Zinx-based TCP gateway and Z-Courier packet protocol.
- Reliable at-least-once downlink delivery with ACK, retry, and persistence.
- Redis-backed cluster online routing and gateway peer push.
- Static, HTTP, and JWT/JWKS authentication with HMAC-protected internal APIs.
- Go and PHP client SDKs backed by shared protocol fixtures and live E2E tests.
- Prometheus, Grafana, E2E, cluster E2E, load-test smoke, and release checks.

V5 should make those capabilities easier for someone else to adopt without
having the same project history in their head.

## Goals

- Publish production-oriented deployment artifacts and configuration examples.
- Add operator-facing APIs and CLI flows for inspecting and managing runtime
  state.
- Expand the SDK ecosystem beyond Go and PHP while preserving the V4 protocol
  contract.
- Make transport security and trust-boundary guidance more concrete.
- Improve performance validation so regressions are visible before release.
- Keep the gateway protocol and existing SDKs backward compatible.

## Non-Goals

V5 does not target:

- Exactly-once delivery. Z-Courier remains at-least-once, with durable
  `MessageID` de-duplication owned by applications.
- A new packet version or incompatible V1 wire-format changes.
- Replacing Zinx as the TCP server framework.
- A full browser admin console as the first operations milestone.
- Java SDK support unless the Node.js and Python direction lands first.
- Built-in token issuance or identity-provider ownership.

## Workstreams

### V5.1 Deployment Artifacts

Purpose: let users start from a supported deployment shape rather than copying
local development commands.

Candidate work:

- Build a production Docker image for `cmd/gateway`.
- Add image build and smoke verification in GitHub Actions.
- Provide example runtime configs for single-node and clustered deployments.
- Add a production-focused Docker Compose example separate from local
  integration Compose.
- Add Kubernetes manifests or a small Helm chart for gateway, config, service,
  metrics, and optional dependencies.
- Document required external dependencies: PostgreSQL, Redis, NSQ or alternate
  upstream target, Prometheus, and Grafana.

Acceptance criteria:

- A new user can run a documented single-node deployment from a built image.
- A documented cluster deployment can expose TCP, internal HTTP, and metrics
  ports with separate configuration for public and private networks.
- CI proves the image starts and passes at least one integration smoke path.

### V5.2 Operations And Admin Surface

Purpose: make production state understandable and manageable without querying
Redis, PostgreSQL, or logs by hand.

Candidate work:

- Add read-only internal APIs for route ranges, enabled upstream adapters,
  gateway identity, readiness/drain state, and dependency health.
- Extend message administration APIs for listing, status, requeue, discard, and
  failure reasons with pagination and stable response contracts.
- Extend session and cluster route inspection APIs to make local vs remote
  ownership explicit.
- Add `cmd/admin` or extend `cmd/devbackend` into a safer operator CLI.
- Add audit-friendly logs and metrics for admin actions.

Acceptance criteria:

- Operators can answer "where would this client/device be pushed?" from a
  documented command.
- Operators can inspect and repair failed downlink messages without manual SQL.
- Admin APIs are authenticated with the existing internal token or HMAC modes.
- A production runbook documents health checks, message repair, cluster route
  diagnosis, dependency failures, HMAC failures, audit logs, and Prometheus
  queries.

### V5.3 SDK Expansion

Purpose: let common application stacks connect to Z-Courier without
reimplementing framing and ACK behavior.

Recommended order:

1. Node.js SDK.
2. Python SDK.
3. Java SDK only after the Node.js and Python contracts settle.

Candidate work:

- Extract a language-agnostic SDK checklist from the Go and PHP implementations.
- Add Node.js protocol codec, frame parser, persistent client, reconnect, and
  fixture conformance tests.
- Add Python protocol codec, frame parser, persistent client, reconnect, and
  fixture conformance tests.
- Add E2E coverage for at least one new SDK before considering it release
  ready.
- Keep SDK APIs clear about at-least-once delivery, manual ACK, and durable
  application de-duplication.

Acceptance criteria:

- New SDKs consume the same `testdata/protocol/v1` fixtures.
- New SDKs pass bind, upstream ACK, downlink ACK, reconnect, and shutdown tests.
- CI prevents language-specific protocol drift.

### V5.4 Security And Transport Hardening

Purpose: move TLS and trust-boundary guidance from "deployment responsibility"
to documented, testable deployment patterns.

Candidate work:

- Document recommended TLS termination patterns for public TCP traffic.
- Document mTLS or service-mesh patterns for internal HTTP and peer push.
- Provide sample reverse-proxy or sidecar configurations.
- Add config validation guidance for HMAC key rotation and token/JWKS modes.
- Evaluate whether built-in TLS listeners are necessary or whether deployment
  examples are enough for `v0.5.0`.

Acceptance criteria:

- Users have at least one documented secure path for internet-facing TCP
  clients and one for private internal HTTP.
- HMAC and JWT documentation clearly explains key ownership, rotation, and
  failure modes.
- Security examples do not require changing the V1 wire format.

### V5.5 Performance Baselines And Compatibility Matrix

Purpose: keep performance and SDK compatibility visible as the project grows.

Candidate work:

- Keep load-test baseline comparisons informational before making them a hard
  release gate.
- Preserve workflow-specific baseline paths for smoke and manual load tests.
- Add a compatibility matrix for gateway version, protocol version, Go SDK,
  PHP SDK, and future SDK versions.
- Publish recommended manual load-test profiles for laptop, CI, and larger
  runner environments.
- Add release notes that include performance context when meaningful.

Acceptance criteria:

- Load-test reports remain visible in GitHub Actions summaries.
- Baseline comparison failures warn maintainers before they fail releases.
- SDK compatibility is documented before adding more languages.

## Suggested Build Order

1. Start with V5.1 deployment artifacts, because every later feature needs a
   repeatable way to run the system.
2. Add V5.2 admin APIs and CLI flows, because they make deployment debugging
   humane.
3. Add one new SDK, preferably Node.js, while the V4 fixture contract is still
   fresh.
4. Harden security deployment examples around TLS, mTLS, JWT, and HMAC.
5. Turn performance baselines and compatibility documentation into release
   checklist items.

This order can change if user demand points strongly at one SDK or deployment
target, but deployment and observability should stay ahead of broad SDK
expansion.

## Completion Criteria

V5 is complete only when:

- `v0.5.0` has a release guide with exact scope, upgrade notes, known
  boundaries, and verification commands.
- At least one deployment artifact path is documented and verified in CI.
- Operators have documented commands or APIs for route/session/message
  inspection.
- Any new SDK uses the shared protocol fixtures and has live gateway E2E
  coverage.
- Security deployment guidance is concrete enough to copy into a real
  environment.
- Existing Go/PHP SDK behavior, V4 wire compatibility, cluster E2E, and
  load-test smoke checks remain green.

For `v0.5.0`, the release guide narrows completion to deployment artifacts,
admin/operator workflows, production smoke checks, audit logs, and the
production runbook. New SDKs, Kubernetes manifests, Helm charts, and built-in
TLS listeners are deferred.

## Open Questions

- Should `v0.5.0` include only deployment and operations work, leaving new SDKs
  for `v0.6.0`?
- Should Kubernetes support start as raw manifests or a Helm chart?
- Should the operator CLI be a new `cmd/admin` binary or a production-hardened
  evolution of `cmd/devbackend`?
- Which Node.js runtime should be the first supported target?
- Should performance baselines stay informational indefinitely, or become a
  release gate after enough history is collected?
