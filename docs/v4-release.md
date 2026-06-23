# V4 Release Guide

This document defines the `v0.4.0` release scope, upgrade rules, verification
steps, and known boundaries. V4 is an internal project phase; the public SemVer
version is `v0.4.0`, not `v4.0.0`.

## Release Scope

`v0.4.0` turns the stable Z-Courier wire protocol into reusable client SDKs
while preserving the V3 gateway, authentication, cluster, and reliable-delivery
model:

- A transport-independent V1 protocol contract covering the outer Zinx frame
  and inner Z-Courier packet layout.
- Shared Go/PHP golden fixtures for valid and invalid packets, including
  malformed frames, binary payloads, UTF-8 metadata, reserved MsgIDs, and
  boundary-sized fields.
- A high-level Go client under `pkg/sdk/client` with TCP framing, AUTH/BIND,
  canonical binding identity, ACK correlation, downlink callbacks, delivery
  ACKs, reconnect, readiness, and clean shutdown.
- `cmd/devclient` migrated onto the public Go client package instead of direct
  Zinx and manual protocol handling.
- A Composer-compatible PHP 8.2 SDK under `sdk/php` with binary-safe protocol
  codecs, a blocking persistent client, typed exceptions, callback dispatch,
  delivery ACKs, reconnect, and process-local de-duplication.
- Public runnable persistent-client examples for Go and PHP.
- SDK integration and migration guidance covering field ownership, token
  refresh, ACK handling, durable `MessageID` de-duplication, reconnect, and
  same-identity replacement.
- Live gateway E2E coverage for Go and PHP SDK bind, upstream ACK, reliable
  downlink, delivery ACK, connection replacement, reconnect with a fresh
  `SessionID`, and continued traffic after reconnect.
- PHP 8.2 syntax, unit, E2E, and maximum-level PHPStan checks in CI.

## Compatibility And Upgrade

Existing V1, V2, and V3 gateway deployments remain compatible:

- The packet version remains `1`; no wire-format migration is required.
- Reserved MsgIDs remain `1` for gateway ACK, `2` for downlink delivery ACK,
  and `1000` for AUTH/BIND.
- Existing authentication providers, upstream routes, Redis cluster routes,
  PostgreSQL downlink storage, HMAC modes, metrics, and dashboards continue to
  work without configuration changes.
- Backend integrations that call internal HTTP APIs can keep using
  `pkg/sdk/backend`; client SDKs are for persistent TCP device connections.
- The token verifier may canonicalize `ClientID`. Applications must treat the
  accepted SDK binding as authoritative instead of assuming the claimed
  configured identity was accepted unchanged.

Recommended adoption path:

1. Keep the current gateway configuration and routes unchanged.
2. Verify each business `MsgID` has an upstream route before migrating clients.
3. Deploy one SDK client with a test `DeviceID` and confirm bind, upstream,
   downlink, delivery ACK, and reconnect.
4. Add durable business de-duplication by `MessageID` before acknowledging
   important downlink messages.
5. Stop old client connections before starting the SDK with the same
   `ClientID + DeviceID`; Z-Courier intentionally replaces older sessions for
   the same identity.
6. Canary a small identity set and monitor ACK, retry, online-session,
   reconnect, and gateway error logs before broad rollout.

The detailed migration guide is in
[v4-sdk-migration.md](v4-sdk-migration.md).

## Runtime Notes

- Go SDK users should import `pkg/sdk/client` and keep one long-lived client
  instance per connected device identity.
- PHP SDK users should run the client from CLI, Supervisor, systemd, Docker, or
  Kubernetes workers. PHP-FPM request workers are not suitable for receiving
  unsolicited downlink messages over a persistent TCP connection.
- The PHP SDK targets PHP 8.2 or newer and uses native blocking streams; an
  event-loop adapter is not part of `v0.4.0`.
- SDK reconnect does not replay business sends automatically. If the socket
  fails during a send, the application must decide whether retry is safe and
  must reuse the same `MessageID` for idempotency.
- The SDK de-duplication hooks are bounded and process-local. They are useful
  as a temporary client-side guard, but database uniqueness or equivalent
  durable de-duplication remains the application's responsibility.

## Security And Reliability Boundaries

- Z-Courier remains an at-least-once delivery system, not exactly-once.
- The gateway and SDK treat message bodies as opaque bytes and do not validate
  business semantics.
- SDKs verify gateway ACK structure and protocol framing, but they do not
  replace application-level authorization or business validation.
- TLS, mTLS, or encrypted service-mesh transport remains a deployment
  responsibility.
- Token issuance stays outside Z-Courier. SDK token providers should load,
  refresh, and rotate credentials from the application's identity system.
- Durable downlink de-duplication must happen before the application returns
  success from a downlink handler or sends a manual delivery ACK.

## Release Verification

Run from the repository root on the exact commit intended for the tag:

```bash
actionlint
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=90s \
  ./pkg/sdk/protocol ./pkg/sdk/client ./pkg/sdk/backend ./pkg/sdk/signing \
  ./internal/auth ./internal/downlink ./internal/server ./internal/config
go vet ./...
php -d error_reporting=E_ALL sdk/php/tests/run.php
find sdk/php -name '*.php' -print0 | xargs -0 -n1 php -l
composer --working-dir=sdk/php install --no-interaction --prefer-dist
composer --working-dir=sdk/php analyse
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
git diff --check
```

`scripts/e2e.sh` must run the base gateway verifier, the public Go SDK verifier,
and the PHP SDK live-gateway verifier. `scripts/e2e_cluster.sh` must remain
green to prove V4 did not regress V2/V3 cluster routing and reliable delivery.
GitHub Actions must be green for the exact `main` commit before tagging.

Optional release-confidence checks:

- Run the **Manual Load Test** workflow with 60-second upstream and downlink
  modes and review the Markdown summaries and baseline comparisons.
- Run the runnable Go and PHP examples against a local integration gateway:

```bash
export ZCOURIER_CLIENT_TOKEN=e2e-token

go run ./examples/go-client \
  -address 127.0.0.1:8999 \
  -client-id e2e-client \
  -device-id go-release-example

composer --working-dir=sdk/php install
ZCOURIER_DEVICE_ID=php-release-example php sdk/php/examples/client.php
```

## GitHub Release Notes

### Highlights

- Public high-level Go client SDK for persistent gateway connections.
- Composer-compatible PHP 8.2 protocol and blocking client SDK.
- Shared cross-language wire fixtures for protocol compatibility.
- Automatic AUTH/BIND, ACK correlation, downlink dispatch, delivery ACK, and
  reconnect handling in SDKs.
- Go and PHP live-gateway E2E coverage in CI.
- Runnable Go/PHP examples and a practical migration guide.

### Upgrade Notes

No gateway configuration migration is required for existing V3 deployments.
Applications can migrate clients incrementally by replacing manual TCP/Zinx
protocol handling with `pkg/sdk/client` or `sdk/php`. Backend services that only
call internal HTTP push APIs should keep using `pkg/sdk/backend`.

Use the accepted SDK binding as the active identity, add durable
`MessageID` de-duplication for important business paths, and do not run the PHP
persistent client inside PHP-FPM request workers.

### Known Boundaries

- Delivery remains at-least-once; clients and backend applications must
  de-duplicate important operations by `MessageID`.
- The PHP SDK is blocking and worker-oriented; no ReactPHP or Swoole adapter is
  included in this release.
- Java, Node.js, and Python SDKs are not included yet, but can reuse the V4
  protocol contract and fixture suite later.
- TLS or mTLS remains a deployment responsibility.
- SDK process-local LRU de-duplication does not survive restart.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm GitHub Actions is green on that exact commit.
3. Confirm `CHANGELOG.md` contains the final `v0.4.0` date and scope.
4. Create and push the annotated tag:

```bash
git tag -a v0.4.0 -m "v0.4.0"
git push origin v0.4.0
```

5. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
