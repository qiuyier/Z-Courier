# V12 Release Readiness Guide

This document is the release-readiness source of truth for the target
`v0.12.0` release. V12 is an internal project phase; the public SemVer version
is `v0.12.0`, not `v12.0.0`.

## Release Scope

V12 strengthens the reliable-downlink lifecycle without changing the client
packet version or making the gateway inspect business payloads:

- idempotent backend submission with explicit compatible replay and immutable
  identity conflict outcomes;
- named, MsgID-selected delivery policies stored as immutable message
  snapshots;
- bounded retry delay, backoff, jitter, age, and attempt limits;
- durable terminal reasons and an optional PostgreSQL outbox for body-free
  terminal events;
- global and per-device pending queue capacity;
- fair retry selection across client/device backlogs;
- policy and terminal-state visibility, guarded requeue actions, audit events,
  metrics, dashboards, alerts, diagnostics, and cluster E2E coverage.

Delivery remains at-least-once. Clients and business services must still use
`MessageID` for durable application-level de-duplication where correctness
depends on it.

## Compatibility

Upgrading from `v0.11.0` does not require a client wire-protocol migration:

- packet version `1` is unchanged;
- reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND;
- existing Go and PHP protocol/client SDKs remain compatible;
- backend SDK response fields are additive;
- Redis online-route and admin-session key formats are unchanged;
- NSQ upstream behavior is unchanged;
- the memory downlink store requires no migration.

The PostgreSQL downlink schema does change. All changes are additive and keep
defaults that allow a V11 binary to write to the upgraded message table.

## Deployment Configuration

The production Compose references and Helm chart expose V12 delivery policies
and terminal publication without enabling new behavior by default:

- production Compose includes a disabled `production-critical` policy example;
- Helm `downlink.policies` defaults to an empty list;
- `downlink.terminal.publisher.type` defaults to `none`;
- Helm chart `0.7.0` is aligned with gateway image `v0.12.0`.

Before enabling a policy, assign a reviewed non-overlapping MsgID range. All
gateway nodes sharing the PostgreSQL downlink store must use the same policy,
capacity, and terminal-publisher configuration. To export terminal events, set
the publisher type to `nsq`, configure the NSQD addresses and topic, and verify
a controlled policy exhaustion in staging. The event envelope contains no
business message body, and its consumer must remain idempotent.

## PostgreSQL Schema Changes

The authoritative migration is
[`internal/downlink/migrations/v0.12.0.sql`](../internal/downlink/migrations/v0.12.0.sql).
The gateway embeds and executes this exact file when
`downlink.storage.postgres.auto_migrate` is true.

The migration adds these message-table field groups:

| Field group | Purpose | Legacy-row behavior |
| --- | --- | --- |
| `identity_fingerprint` | Detect immutable `MessageID` identity conflicts | Empty fingerprints are calculated and persisted on the first compatible V12 replay |
| `policy_*` | Preserve the selected delivery policy for the message lifetime | Empty/zero snapshots use the configured legacy-compatible default policy |
| `terminal_*` | Preserve terminal reason and dead-letter publication state | Existing rows start with no terminal reason and publication `disabled` |

It also creates `z_courier_downlink_terminal_events` and its due/uniqueness
indexes. The table is a transactional outbox containing routing and delivery
metadata only; it does not contain the message body.

The application migration runs inside one PostgreSQL transaction and uses a
transaction-level advisory lock. This serializes concurrent gateway startup
against the same database.

## Migration Ownership

For local development and small deployments, automatic migration is the
shortest path:

```yaml
downlink:
  storage:
    type: postgres
    postgres:
      auto_migrate: true
```

For mature production environments, apply the reviewed migration before the
gateway rollout and then start the gateway with `auto_migrate: false`:

```bash
psql "$ZCOURIER_POSTGRES_DSN" \
  --set ON_ERROR_STOP=1 \
  --single-transaction \
  --file internal/downlink/migrations/v0.12.0.sql
```

Run the file once from the exact source commit intended for the release. Do
not let multiple external migration jobs execute it concurrently; the
application advisory lock applies only when the gateway owns the migration.
The SQL is idempotent, but one reviewed migration owner keeps deployment
ordering and audit evidence clear.

Verify the primary objects after migration:

```sql
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'z_courier_downlink_messages'
  AND (column_name = 'identity_fingerprint'
    OR column_name LIKE 'policy_%'
    OR column_name LIKE 'terminal_%')
ORDER BY column_name;

SELECT to_regclass(
  current_schema() || '.z_courier_downlink_terminal_events'
) AS terminal_events_table;
```

## Recommended Upgrade From V11

1. Pin and record the exact V11 image, configuration, Helm revision, and
   database restore point.
2. Back up PostgreSQL and verify that the backup can be restored in staging.
3. Run the V11-to-V12 migration compatibility test against a non-production
   database.
4. Apply the V12 PostgreSQL migration before replacing gateway binaries.
5. Deploy one V12 gateway canary with V12 policies, terminal publication, and
   queue limits left at their legacy-compatible or disabled settings.
6. Verify readiness, AUTH/BIND, upstream forwarding, downlink push/ACK,
   reconnect retry, idempotent replay, conflict rejection, and status lookup.
7. Replace the remaining gateway nodes while keeping the mixed-version window
   short.
8. After every node runs V12, enable reviewed delivery policies, terminal-event
   publication, queue capacity, and retry fairness in that order.
9. Trigger one controlled policy exhaustion and verify the message state,
   terminal outbox publication, Prometheus metrics, and operator audit trail.
10. Watch capacity rejection, retry fairness, terminal publication failure,
    PostgreSQL latency, and pending backlog during canary traffic.

### Mixed-Version Boundary

The additive schema lets V11 and V12 binaries start against the same database,
but mixed-version behavior is not feature-equivalent:

- a request handled by V11 does not enforce V12 immutable identity conflict
  rejection;
- a V11 terminal transition does not create a V12 terminal outbox event;
- V11 does not enforce V12 queue admission limits or policy snapshots.

Do not rely on V12 guarantees until all write-serving gateway nodes run V12.
If those guarantees are mandatory during the rollout, pause backend downlink
submissions or route them only to the V12 canary until the rollout completes.

## Rollback To V11

Use a binary/configuration rollback, not a destructive schema rollback:

1. Stop enabling new V12-only policies or operator repair work.
2. Pause the terminal publisher and allow active publication requests to
   finish where practical.
3. Roll gateway nodes back to the pinned `v0.11.0` image.
4. Restore the V11 delivery configuration and keep the V12 PostgreSQL columns,
   indexes, and terminal-event table in place.
5. Verify readiness, AUTH/BIND, upstream forwarding, downlink push/ACK,
   reconnect retry, and cluster peer delivery.
6. Preserve pending terminal-event rows for a later forward recovery.

V11 inserts continue to work because every V12-only message column has a
compatible default. V11 ignores the terminal-event table. Removing the V12
columns or table during an incident adds lock and data-loss risk and is not the
recommended rollback path.

Rollback intentionally disables V12 semantics. Messages submitted while V11
is active have no stored V12 policy snapshot or fingerprint until a later V12
read/replay path fills compatible metadata. Messages that become terminal
under V11 do not receive historical V12 outbox events automatically. Record
the rollback interval if terminal-event completeness matters operationally.

## Migration Verification

With the local PostgreSQL container available:

```bash
docker compose -f deploy/local/docker-compose.yml up -d postgres

ZCOURIER_TEST_POSTGRES_DSN='postgres://zcourier:zcourier@127.0.0.1:15432/zcourier?sslmode=disable' \
go test ./internal/downlink \
  -run '^TestPostgresStoreV11SchemaUpgradeAndRollbackCompatibilityIntegration$' \
  -count=1 -v
```

The test creates an isolated V11 schema, preserves a pre-upgrade message,
runs the embedded migration twice, verifies every V12 object, exercises lazy
identity fingerprinting, and simulates a V11 insert after binary rollback.
`scripts/e2e_cluster.sh` runs the same test before starting its two gateways.

## Release Acceptance Matrix

Run every required check on the exact commit intended for the tag.

| Area | Required evidence | Command or workflow |
| --- | --- | --- |
| Source | Clean worktree and expected `HEAD` | `git status --short` and `git log -1 --oneline` |
| Fast validation | Go, race, vet, PHP, frontend build, configs, shell syntax | `bash scripts/release_check.sh` |
| PostgreSQL upgrade | V11 upgrade, idempotent rerun, preserved rows, rollback-compatible write | Migration integration test above; also included in cluster E2E |
| Single-node lifecycle | Offline queue, retry, ACK, policy exhaustion, terminal event | `bash scripts/e2e.sh` |
| Two-node lifecycle | Shared storage, idempotency, capacity, fairness, terminal publication, repair audit | `bash scripts/e2e_cluster.sh` |
| Browser operations | Admin roles, guarded repair, readonly boundary | `bash scripts/console_smoke.sh` |
| Production references | Single/cluster Compose startup and health | `bash scripts/production_smoke.sh` and `bash scripts/production_cluster_smoke.sh` |
| Kubernetes | Helm lint/template/package plus kind policy selection, exhaustion, and terminal-event consumption | `bash scripts/k8s_helm_smoke.sh` and `bash scripts/k8s_helm_e2e.sh` |
| Performance | Load reports reviewed; baseline comparison remains informational | `bash scripts/loadtest_smoke.sh` plus the manual load-test workflow |
| GitHub | CI and Kubernetes workflows green on the tag commit | GitHub Actions run summary |
| Artifacts | Docker image, Helm package/OCI chart, checksums and release notes verified | Tag publication workflows |

The full local release command is:

```bash
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

When Composer is available only through a local image, add:

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82
```

Do not tag `v0.12.0` until this matrix is complete and CI is green on the exact
commit. Load-test baseline comparisons remain a summary/warning signal rather
than a release failure gate.

## Known Boundaries

- V12 provides gateway-submission idempotency, not exactly-once business
  processing.
- The terminal event is not a transaction with an external NSQ consumer;
  publication uses a durable retrying outbox.
- The outbox contains metadata, never the arbitrary business body.
- Global queue admission serializes a PostgreSQL decision path and should be
  enabled only after workload-specific benchmarking.
- Destructive downgrade SQL is intentionally not supplied; preserve additive
  schema during rollback.
