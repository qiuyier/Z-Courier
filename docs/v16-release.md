# V16 Release Readiness Guide

This guide is the release-acceptance source of truth for `v0.16.0`. V16
replaces one-size-fits-all ingress limiting with optional named token-bucket
traffic policies while preserving the opaque-message-body boundary.

## Release Scope

V16 adds:

- deterministic policy selection by authenticated ClientID, protocol MsgID,
  and enabled upstream route;
- a concurrency-safe process-local token bucket with bounded live keys and
  idle eviction;
- an optional Redis Store that atomically enforces one quota across gateway
  nodes and fails closed when it cannot decide;
- stable `rate_limited`, `overloaded`, and `admission_unavailable` outcomes;
- low-cardinality metrics, sanitized diagnostics, read-only Console state,
  Grafana panels, recording rules, and alerts;
- real TCP local and two-node Redis E2E coverage;
- production Compose and Helm local/Redis deployment examples with strict
  schema and generated-config validation.

Traffic policies inspect only trusted metadata. They never parse, log, or place
the opaque business message body into a quota key, metric label, diagnosis
bundle, or alert.

## Compatibility And Upgrade

V16 does not change the client packet format, reserved MsgIDs, SDK wire
contract, PostgreSQL schema, durable downlink state, NSQ topics, cluster online
route keys, or internal HTTP APIs. No data migration is required.

Existing `pipeline.rate_limit` configuration keeps its previous fixed-window,
process-local behavior. The Helm defaults also retain that limiter and leave
`pipeline.trafficPolicies` disabled. The legacy and named limiters cannot be
enabled together.

Use this rolling upgrade order:

1. Deploy the V16 binary or image while keeping the existing `rate_limit`
   configuration.
2. Verify readiness, AUTH/BIND, normal upstream/downlink behavior, and the
   existing ingress rejection baseline.
3. Add the reviewed traffic-policy configuration to every node, disable
   `rate_limit`, and roll the gateways again.
4. For Redis mode, keep the Redis database, key prefix, policies, selectors,
   priorities, and buckets identical on every node. Confirm Redis is reachable
   before the first policy-enabled Pod starts.
5. Shift a small traffic slice and observe policy decisions, upstream success,
   latency, local-key utilization, and Redis availability before full rollout.

Do not apply a config containing `traffic_policies` to a pre-V16 binary: strict
YAML decoding will reject an unknown field. Upgrade the binary first, then
migrate the limiter configuration.

## Admission Contract

Policy priority is deterministic: larger priorities win, and ambiguous
same-priority overlaps fail startup. Non-empty match dimensions use AND
semantics. An empty `default_policy` lets unmatched packets pass without
allocating a bucket.

Local mode bounds process memory with `max_keys`. It never evicts an active
bucket merely to admit a new identity, because doing so would reset the quota.
A new identity is rejected with `overloaded` when capacity remains full after
idle cleanup.

Redis mode uses server time and one Lua operation for refill plus admission.
ClientID components are SHA-256 hashed in Redis keys, and every key has a
bounded idle TTL. Redis mode supports only `fail_closed`; an unavailable Store
returns `admission_unavailable` before upstream forwarding instead of creating
independent per-node quotas.

Traffic-policy admission protects the gateway path. Backends still need their
own concurrency control, authorization, and persistent MessageID idempotency.

## Deployment References

The production references intentionally demonstrate both supported modes:

- `deploy/production/config/z-courier.yaml` uses a bounded local policy for one
  gateway process;
- both files under `deploy/production-cluster/config/` use the same Redis
  namespace and shared policy;
- `values-traffic-policy-local.yaml` and
  `values-traffic-policy-redis.yaml` are focused Helm examples;
- `values-production.yaml` uses Redis because it deploys multiple replicas.
- Helm chart `0.8.0` recommends gateway image `v0.16.0`; the production values
  pin the same image, and release workflows reject mismatched metadata.

The included capacity and refill values are reviewable starting points, not
universal production defaults. Size them from measured ingress bursts,
sustained rate, and downstream capacity.

## Rollback

Rollback does not require deleting Redis quota keys or migrating protocol,
PostgreSQL, NSQ, or cluster-route data:

1. Stop the traffic canary if rejection or dependency behavior is unsafe.
2. On every node, disable `traffic_policies` and restore the previous
   `rate_limit` values, or restore the complete last known-good config.
3. Roll back gateway Pods or containers gradually and verify readiness,
   AUTH/BIND, upstream ACKs, downlink delivery/ACK, and peer push.
4. Keep Redis quota keys until their idle TTL removes them naturally.
5. Confirm traffic-policy alerts settle and the legacy rejection baseline is
   restored.

Do not enable local fallback during a Redis incident. That would multiply one
cluster quota into independent per-node quotas and change admission semantics
at the worst possible time.

## Release Acceptance Matrix

Run every required check from the exact commit intended for the tag.

| Area | Required evidence | Command or workflow |
| --- | --- | --- |
| Source | Clean worktree, expected commit, no tracked secrets | `git status --short`, `git log -1 --oneline`, `bash scripts/secret_boundary_check.sh` |
| Unit and race | Selector/config validation, local concurrency/capacity, Redis atomicity/TTL, gateway integration | `bash scripts/release_check.sh` |
| Local real path | Burst, refill, priority, pass-through, bounded keys, idle eviction, no upstream forwarding after rejection | `bash scripts/e2e_traffic_policy.sh` |
| Redis real path | One quota across two gateways, positive PTTL and actual key expiration, fail-closed outage, recovery without restart | `bash scripts/e2e_traffic_policy_redis.sh` |
| Deployment | Helm default/local/Redis schema and rendering, invalid combinations, Compose references, built-image config loading | `bash scripts/traffic_policy_deployment_check.sh` |
| Release metadata | Chart `0.8.0`, `appVersion`, production image tag, and release tag agree | `bash scripts/helm_release_metadata_check.sh v0.16.0` |
| Operations | Sanitized diagnostics/Console, Grafana panels, recording rules and alert behavior | `bash scripts/promtool_check.sh`, `bash scripts/console_smoke.sh` |
| Regression | HTTP/NSQ upstream, discovery, reliable downlink, cluster peer push, SDKs, production Compose | CI E2E and Production Smoke jobs |
| Kubernetes | Helm smoke/E2E remain green with the V16 schema and generated ConfigMap | `bash scripts/k8s_helm_smoke.sh`, `bash scripts/k8s_helm_e2e.sh` |
| CI | All required jobs green on the tag commit | GitHub Actions summary |

Run the complete local acceptance suite when Docker, Composer, and Kind are
available:

```bash
ZCOURIER_RELEASE_VERSION=v0.16.0 \
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

The fast path runs local bounded-key E2E. The slow Docker path runs the
two-gateway Redis verifier, including actual key expiration. Do not claim full
Redis or Kubernetes acceptance when the corresponding path was skipped; record
deferred evidence explicitly.

## Operational Evidence

Attach only sanitized evidence to the release record:

- exact commit SHA, image digest, chart version, and workflow URLs;
- enabled policy names, mode, key scope, capacity, and refill settings;
- aggregate `z_courier_traffic_policy_*` metrics and the four bundled alert
  states;
- local key utilization or Redis Store status, canary timestamps, and rollback
  decision;
- E2E and deployment-check outcomes.

Do not attach ClientIDs, DeviceIDs, tokens, Redis addresses or raw keys,
business bodies, or raw internal errors.

## Tagging And Release

After the matrix is complete and CI is green on the exact commit:

```bash
git tag -a v0.16.0 -m "v0.16.0"
git push origin v0.16.0
```

Create the GitHub Release from `v0.16.0`, using the V16 entries in
[`CHANGELOG.md`](../CHANGELOG.md) as the release notes. Verify image and Helm
publishing workflows, then record their immutable artifact URLs.

Release V16 only after the canary observation window is stable and the team can
explain every meaningful rate-limited, overloaded, or admission-unavailable
event.
