# V3 Release Guide

This document defines the `v0.3.0` release scope, upgrade rules, verification
steps, and known boundaries. V3 is an internal project phase; the public SemVer
version is `v0.3.0`, not `v3.0.0`.

## Release Scope

`v0.3.0` adds production-oriented authentication and integration surfaces while
preserving the V2 connection, routing, cluster, and reliable-delivery model:

- Configuration-selectable static, remote HTTP, and local JWT/JWKS token
  verification.
- A stable `Principal` contract with distinct invalid-credential,
  provider-unavailable, and misconfiguration errors.
- Bounded positive and negative authentication caching keyed by SHA-256 token
  digests rather than raw credentials.
- Authentication timeout, in-flight protection, cache, JWKS refresh, and
  low-cardinality Prometheus metrics with Grafana panels.
- Strict asymmetric JWT algorithm allowlists, issuer/audience/time checks,
  unknown-key refresh throttling, rotation, and stale-key fallback.
- Public `pkg/sdk/protocol`, `pkg/sdk/backend`, and `pkg/sdk/signing` Go packages.
- Optional canonical HMAC-SHA256 authentication for backend-to-gateway internal
  HTTP requests.
- Optional canonical HMAC-SHA256 authentication for gateway peer push with a
  separate peer key ring.
- Timestamp and bounded nonce replay protection for both HMAC trust domains.

## Compatibility And Upgrade

Existing V2 configurations remain valid:

- Legacy `auth.static_tokens` still selects static verification.
- `internal_http.auth.mode` defaults to `token`.
- `cluster.peer.auth.mode` defaults to `token`.
- Existing packet encoding, `MsgID = 1000` bind, `MsgID = 2` ACK, route ranges,
  Redis routes, and PostgreSQL message state remain compatible.

Production deployments can upgrade one trust boundary at a time:

1. Choose `auth.type: http` when the backend owns opaque-token semantics, or
   `auth.type: jwt` when the gateway should verify JWTs locally.
2. Keep internal HTTP token mode initially, then configure backend HMAC keys and
   update callers to `pkg/sdk/backend` HMAC mode.
3. Keep cluster peer token mode initially, then deploy a common accepted peer
   key ring to every gateway before switching each node to peer HMAC mode.
4. Confirm authentication, JWKS, and HMAC result metrics before removing old
   tokens or keys.

Configuration examples and validation rules are in
[configuration.md](configuration.md). Go integration examples are in
[go-sdk.md](go-sdk.md), and the cross-language HMAC contract is in
[internal-http-signing.md](internal-http-signing.md).

## Security Boundaries

- Z-Courier verifies tokens but does not issue login tokens or act as an identity
  provider.
- JWT private keys remain in the issuer. The gateway retrieves only public JWKS
  material.
- HMAC keys for backend requests and gateway peers must be different. Access to
  one trust domain must not grant access to the other.
- HMAC provides authentication, integrity, and replay resistance, not
  encryption. Use TLS, mTLS, or an encrypted service mesh in production.
- The current nonce stores are process-local. Backend requests distributed
  across several gateway nodes do not yet have cluster-global replay state.
- `${ENV_NAME}` in YAML examples is a deployment-template convention. The
  Z-Courier YAML loader does not expand environment variables itself.
- Reliable delivery is at-least-once. Clients must de-duplicate by `MessageID`.

## Release Verification

Run from the repository root on the exact commit intended for the tag:

```bash
actionlint
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=90s \
  ./pkg/sdk/signing ./internal/auth ./internal/downlink \
  ./internal/server ./internal/config
go vet ./...
bash scripts/e2e.sh
bash scripts/e2e_cluster.sh
bash scripts/loadtest_smoke.sh
git diff --check
```

The two-node verifier must exercise HMAC-signed peer push and expose
`z_courier_cluster_peer_signature_total{result="success"}`. GitHub Actions must
be green for the exact `main` commit before tagging.

## GitHub Release Notes

### Highlights

- Pluggable static, remote HTTP, and local JWT/JWKS authentication.
- Bounded auth caching, provider protection, key rotation, and complete auth
  observability.
- Public Go protocol, backend, and signing SDK packages.
- Replay-resistant HMAC signing for backend internal APIs and cluster peer push.
- Backward-compatible token modes and V2 wire/delivery behavior.

### Upgrade Notes

No configuration migration is required for existing token-mode deployments.
HTTP/JWT providers and HMAC internal authentication are opt-in. Review the
security boundaries above before enabling HMAC in a load-balanced deployment.

### Known Boundaries

- At-least-once delivery requires client-side `MessageID` de-duplication.
- HMAC nonce replay state is local to each gateway process.
- TLS or mTLS remains a deployment responsibility.
- Public SDKs are currently provided for Go.

## Tagging Checklist

1. Commit and push the release documentation to `main`.
2. Confirm GitHub Actions is green on that exact commit.
3. Confirm `CHANGELOG.md` contains the final `v0.3.0` date and scope.
4. Create and push the annotated tag:

```bash
git tag -a v0.3.0 -m "v0.3.0"
git push origin v0.3.0
```

5. Create a normal GitHub Release, not a pre-release, using the release notes
   above.
