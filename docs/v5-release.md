# V5 Release Guide

This document defines the `v0.5.0` release scope, upgrade notes, verification
steps, operational checklist, and known boundaries. V5 is an internal project
phase; the public SemVer version is `v0.5.0`, not `v5.0.0`.

## Release Scope

`v0.5.0` moves Z-Courier from a working gateway and SDK foundation toward a
more operable open-source middleware package. This release focuses on
deployment artifacts and production operations. It preserves the V4 wire
protocol, Go SDK, PHP SDK, authentication, cluster routing, and reliable
delivery model.

Included in scope:

- Production-oriented gateway Docker image path for `cmd/gateway`, including a
  release-checkable binary layout and config packaging.
- Single-node production reference Compose stack with gateway, PostgreSQL,
  Redis, NSQ, Prometheus, HMAC-protected internal HTTP, HTTP token verification,
  and durable downlink storage configuration.
- Two-node production cluster reference Compose stack with shared PostgreSQL,
  shared Redis online routing, shared NSQ upstream, HMAC peer push, and
  Prometheus scraping for both gateway nodes.
- Strict `${ENV_NAME}` expansion for gateway YAML configuration, plus
  `.env.example` files and gitignored real production `.env` files.
- Production smoke verifiers for the single-node and two-node reference stacks.
- Admin overview and upstream route APIs for gateway identity, readiness,
  dependency summary, cluster summary, internal HTTP settings, downlink
  settings, and route target metadata.
- `cmd/admin` operator CLI for overview, routes, route lookup, local session
  listing, downlink message status, message listing, guarded single-message
  requeue, and guarded single-message discard.
- Structured admin mutation audit logs for message repair operations, including
  action, result, HTTP status, gateway node, message id, reason, message status,
  auth mode, and HMAC key id without exposing tokens or secrets.
- Production runbook covering health checks, admin inspection, failed-message
  repair, cluster route diagnosis, dependency failures, HMAC failures, capacity
  symptoms, audit logs, Prometheus queries, and load-test baseline review.
- Release documentation that collects the `v0.5.0` upgrade path, verification
  commands, GitHub release notes, and tagging checklist.

## Not Included

`v0.5.0` does not include:

- A browser admin console.
- Exactly-once delivery. Z-Courier remains at-least-once.
- A new packet version or incompatible V1 wire-format changes.
- Node.js, Python, or Java SDKs.
- Kubernetes manifests or a Helm chart.
- Built-in TLS or mTLS listeners.
- Built-in token issuance or identity-provider ownership.
- Automatic database migration ownership for mature production deployments.
- Batch requeue or batch discard from `cmd/admin`.

These remain good future work, but they are not required for this release.

## Compatibility And Upgrade

Existing `v0.4.0` deployments remain compatible:

- The packet version remains `1`.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing Go and PHP SDK behavior remains compatible.
- Existing upstream routes, Redis cluster routes, PostgreSQL downlink storage,
  authentication providers, HMAC modes, metrics, and dashboards continue to
  work.
- No gateway wire-protocol migration is required.

Recommended upgrade path from `v0.4.0`:

1. Keep the current gateway configuration and client SDK versions unchanged.
2. Build the new production image and run the local image smoke check.
3. Review the new production `.env.example` files and replace every secret.
4. Decide whether backend internal HTTP should use token mode or HMAC mode.
5. If using HMAC, use different key rings for backend internal HTTP and cluster
   peer push.
6. Start a staging single-node or cluster reference stack and verify
   `/healthz`, `/readyz`, `/metrics`, and Prometheus targets.
7. Run `cmd/admin overview`, `routes`, `route`, `sessions`, `messages`, and
   `message` against staging.
8. Exercise one safe failed-message repair path with `requeue` or `discard`
   and confirm the `admin message action audit` log.
9. Canary production traffic and watch online sessions, downlink push, ACK,
   retry, cluster peer push, upstream forwarding, and HMAC signature metrics.
10. Keep application-level durable de-duplication by `MessageID` for important
    business paths.

## Production Secret Checklist

Before using the production reference outside a private test environment,
replace every value in the copied `.env` file.

Single-node reference:

```bash
cp deploy/production/.env.example deploy/production/.env
$EDITOR deploy/production/.env
```

Cluster reference:

```bash
cp deploy/production-cluster/.env.example deploy/production-cluster/.env
$EDITOR deploy/production-cluster/.env
```

Review at least:

- PostgreSQL password
- Redis password
- auth-provider shared token
- backend internal HTTP HMAC secret
- gateway peer HMAC secret
- upstream internal token
- any real backend or auth service URLs

Do not commit real `.env` files. Do not reuse the same HMAC key for
backend-to-gateway internal HTTP and gateway-to-gateway peer push.

## Runtime Notes

- Internal HTTP should stay on a private network. Public clients should reach
  only the TCP gateway listener or a TLS-terminating proxy in front of it.
- `/healthz`, `/readyz`, and `/metrics` remain unauthenticated for probes and
  Prometheus. Protect them with network boundaries.
- Admin APIs live under `/internal/*` and use the configured internal auth mode:
  token or HMAC.
- `cmd/admin requeue` and `cmd/admin discard` are guarded mutations. They
  require `-confirm`, and `discard` also requires `-reason`.
- `cmd/admin sessions` lists sessions local to the queried gateway node.
  Cluster-wide ownership should be inspected with `cmd/admin route`.
- Production references demonstrate deployment shape; real production should
  own network policy, secret management, database operations, TLS/mTLS, and
  backup/restore.

## Verification

Run from the repository root on the exact commit intended for the tag:

```bash
actionlint
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=90s \
  ./pkg/sdk/protocol ./pkg/sdk/client ./pkg/sdk/backend ./pkg/sdk/signing \
  ./internal/auth ./internal/downlink \
  ./internal/server ./internal/config
go vet ./...
php -d error_reporting=E_ALL sdk/php/tests/run.php
find sdk/php -name '*.php' -print0 | xargs -0 -n1 php -l
composer --working-dir=sdk/php install --no-interaction --prefer-dist
composer --working-dir=sdk/php analyse
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
bash scripts/production_smoke.sh
bash scripts/production_cluster_smoke.sh
docker build --tag z-courier-gateway:release-check .
docker run --rm --entrypoint /bin/sh z-courier-gateway:release-check -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
git diff --check
```

GitHub Actions must be green for the exact `main` commit before tagging.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow in upstream and downlink modes.
- Review workflow summaries and `cmd/loadcompare` output.
- Compare only against matching smoke or manual baselines.
- Treat baseline comparison as informational unless the release process
  explicitly promotes it to a hard gate.

## Operational Smoke

After starting a staging or production-like stack, run:

```bash
go run ./cmd/admin overview
go run ./cmd/admin routes
go run ./cmd/admin messages -status failed -limit 10
```

For a known connected test client:

```bash
go run ./cmd/admin route \
  -client-id e2e-client \
  -device-id e2e-device

go run ./cmd/admin sessions \
  -client-id e2e-client
```

If a safe failed message exists:

```bash
go run ./cmd/admin message \
  -message-id message-1

go run ./cmd/admin requeue \
  -message-id message-1 \
  -confirm
```

Confirm the gateway logs contain:

```text
admin message action audit
```

and that the log does not expose internal tokens or HMAC secrets.

## GitHub Release Notes

### Highlights

- Production-oriented Docker image path and deployment references.
- Single-node and two-node production Compose examples.
- Production smoke verifiers for deployment reference stacks.
- `cmd/admin` operator CLI for overview, routes, route/session inspection,
  message inspection, and guarded message repair.
- Admin overview and upstream-route APIs.
- Structured audit logs for admin message mutation operations.
- Production runbook for health checks, Prometheus queries, message repair, and
  common incident paths.

### Upgrade Notes

No wire-format or SDK migration is required from `v0.4.0`. Existing Go and PHP
SDK clients remain compatible. Existing backend integrations can continue using
`pkg/sdk/backend`.

Production adopters should review the new deployment examples, replace every
secret in copied `.env` files, prefer HMAC for backend internal HTTP, and keep
internal HTTP private. Use separate HMAC keys for backend internal HTTP and
cluster peer push.

### Known Boundaries

- Delivery remains at-least-once; applications must de-duplicate important
  operations by `MessageID`.
- The gateway treats message bodies as opaque bytes.
- TLS, mTLS, or an encrypted service mesh remains a deployment responsibility.
- Production references are examples, not a complete platform.
- Kubernetes and Helm examples are not included yet.
- Node.js, Python, and Java SDKs are not included yet.
- `cmd/admin` supports single-message repair only.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm GitHub Actions is green on that exact commit.
3. Confirm `CHANGELOG.md` contains the final `v0.5.0` date and scope.
4. Confirm production smoke verifiers are green.
5. Confirm release notes match the final scope.
6. Create and push the annotated tag:

```bash
git tag -a v0.5.0 -m "v0.5.0"
git push origin v0.5.0
```

7. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
