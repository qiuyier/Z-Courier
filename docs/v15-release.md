# V15 Release Readiness Guide

This guide is the release-acceptance source of truth for `v0.15.0`. V15 adds
health-aware HTTP upstream endpoint discovery and bounded failover while
preserving Z-Courier's opaque-message-body rule.

## Release Scope

V15 adds two opt-in discovery modes for HTTP upstream routes:

- `static`: a reviewed list of complete endpoint URLs for Docker, VMs, or an
  externally managed service registry;
- `dns`: periodic A/AAAA resolution for an internal hostname, including a
  Kubernetes Service or Headless Service.

For discovery routes, the gateway holds an immutable endpoint snapshot, selects
healthy endpoints round-robin, and applies a process-local unhealthy cooldown.
A pre-response connection failure can fail over only when the route explicitly
enables it, and only up to `max_attempts`. An HTTP response, including `5xx`,
is never replayed automatically.

V15 also adds discovery diagnostics, low-cardinality Prometheus metrics,
Grafana panels, alerts, production Compose/Helm examples, Docker-free
two-upstream E2E coverage, and a Kind E2E that validates Kubernetes Headless
Service DNS refresh after a backend Pod replacement.

## Compatibility And Upgrade

V15 does not change the client packet protocol, reserved MsgIDs, PostgreSQL
schema, Redis route keys, NSQ route behavior, or existing single-URL HTTP
routes. Existing routes keep their one-attempt behavior until an operator
replaces `target.url` with an explicit `target.discovery` block.

No database migration is required. Upgrade gateway nodes normally, then enable
discovery per route only after reviewing these conditions:

1. The backend uses `MessageID` as its idempotency key wherever duplicate
   processing would be unsafe.
2. Static endpoints or the DNS name resolve from every gateway node.
3. The configured path, token, TLS server name, timeout, and network policy
   are valid for every discovered endpoint.
4. `max_attempts` and `unhealthy_cooldown` match the backend's availability and
   idempotency expectations.

For DNS, prefer a Headless Service when the gateway should select individual
backend Pods. A normal Kubernetes Service usually resolves to one ClusterIP,
which leaves endpoint balancing to kube-proxy rather than the gateway.

## Delivery Boundary

Discovery improves availability; it does not create exactly-once upstream
delivery. If a network failure occurs after the backend could have received a
request but before the gateway has response headers, processing may be
ambiguous. The gateway can make a bounded retry according to route policy, so
the backend must be idempotent by `MessageID`.

Do not enable automatic retry for a received response. In particular, a `500`
means the backend observed the request attempt; V15 returns an upstream failure
to the client instead of replaying it to another endpoint.

## Rollback

Rollback is configuration and binary rollback only:

1. Keep the previous image and the last known-good route configuration.
2. Change a discovery route back to its previous `target.url`, or disable only
   the affected route if an immediate stop is required.
3. Roll gateway Pods back gradually and verify readiness, AUTH/BIND, upstream
   ACKs, downlink push/ACK, and cluster peer delivery.
4. Do not delete PostgreSQL rows, Redis online routes, NSQ messages, or DNS
   records merely to roll back discovery.

The local unhealthy cooldown cache is process-local and disappears when a
gateway restarts. DNS and the backend load balancer remain the cross-node
sources of truth.

## Release Acceptance Matrix

Run every required check from the exact commit intended for the tag.

| Area | Required evidence | Command or workflow |
| --- | --- | --- |
| Source | Clean worktree, expected commit, no tracked secrets | `git status --short`, `git log -1 --oneline`, `bash scripts/secret_boundary_check.sh` |
| Fast validation | Go tests/race/vet, PHP SDK, Console build, config and shell validation | `bash scripts/release_check.sh` |
| Static discovery | Immutable selection, cooldown, bounded failover, preserved message identity, no replay of HTTP `500` | `bash scripts/e2e_discovery.sh` |
| DNS discovery | A/AAAA refresh, last-known-good retention, endpoint retirement, cancellation behavior | Go tests in `./internal/adapter/httpforwarder` and `./internal/server` |
| Operations | Sanitized diagnostics, Prometheus metrics, Grafana panels, alert rules and response guidance | `bash scripts/promtool_check.sh`, Console smoke, dashboard review |
| Deployment references | Compose static discovery and Helm static/Kubernetes DNS rendering | `bash scripts/discovery_deployment_check.sh` |
| Kubernetes | Real two-Pod Headless-Service DNS, Pod replacement, DNS refresh, forward without gateway restart | `bash scripts/k8s_helm_e2e.sh` |
| CI | Validate and E2E workflows green on the tag commit; manually run Kubernetes E2E when release evidence requires it | GitHub Actions summary |

Run the complete local acceptance suite when Docker, Composer, and Kind are
available:

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

The `ZCOURIER_RELEASE_RUN_K8S=1` path invokes
`scripts/k8s_helm_e2e.sh`, which now includes the V15 Headless-Service DNS
replacement check. Do not claim full Kubernetes acceptance if this path was
omitted; record the deferred evidence explicitly in the release record.

## Operational Evidence

Attach only sanitized evidence to the release record:

- exact commit SHA, gateway image digest, chart version, and workflow URLs;
- the enabled discovery route names and type, never endpoint URLs, raw DNS
  answers, tokens, or opaque request bodies;
- `z_courier_upstream_discovery_*`,
  `z_courier_upstream_endpoint_*`, and
  `z_courier_upstream_failover_*` metric snapshots;
- readiness/rollout timestamps and the DNS backend replacement result;
- discovery alert state, cooldown/endpoint-failure trends, and the canary
  rollback decision if one was made.

## Tagging And Release

After the matrix is complete and CI is green on the exact commit:

```bash
git tag -a v0.15.0 -m "v0.15.0"
git push origin v0.15.0
```

Create the GitHub Release from `v0.15.0`, using the V15 entries in
[`CHANGELOG.md`](../CHANGELOG.md) as the release notes. Verify the image and
Helm publishing workflows complete, then record their artifact URLs with the
release evidence.

Release V15 only when normal upstream traffic remains stable through the agreed
observation window and the team can explain any endpoint cooldown or failover
observed during the canary.
