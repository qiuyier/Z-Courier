# V3 Authentication And Integration Design

V3 is the third internal development phase of Z-Courier. It is expected to
produce the `v0.3.x` release line; it does not imply a SemVer `v3.0.0` release.

The V2 release candidate proved reliable single-node and cluster delivery. V3
focuses on the next integration blocker for open-source users: replacing local
static tokens without modifying gateway core code, while keeping gateway and
backend identity semantics consistent.

## Problem Statement

The current authentication path already has a useful abstraction:

```go
type Verifier interface {
    Verify(ctx context.Context, token string) (*Principal, error)
}
```

The ingress pipeline depends on this interface, but file configuration always
constructs `StaticTokenVerifier`. This is appropriate for tests and examples,
not for production integration.

V3 must let an operator select a verifier through configuration and must define
one stable principal contract that every verifier returns.

## Goals

- Preserve `auth.Verifier` as the gateway-facing extension point.
- Keep static tokens working for local development and existing configs.
- Add remote HTTP token verification for monolithic and custom backends.
- Add local JWT verification with issuer, audience, algorithm, and JWKS checks.
- Give invalid credentials and unavailable auth providers different error
  semantics.
- Add bounded authentication caching without storing raw tokens as cache keys.
- Expose low-cardinality auth metrics without leaking token or client data.
- Provide a minimal Go SDK for protocol and backend integration after verifier
  behavior is stable.
- Cover success, rejection, timeout, outage, cache, and key-rotation paths.

## Non-Goals

- Z-Courier will not issue login tokens or become an identity provider.
- V3 will not implement a full user, role, or permission management system.
- V3 will not add a web admin console.
- V3 will not add every upstream adapter at once.
- V3 will not claim exactly-once delivery.
- V3 will not make multi-tenancy mandatory.

## Existing Components To Keep

The current flow should remain recognizable:

```text
Packet decode
-> auth.Verifier.Verify
-> auth.Principal
-> policy and rate limit
-> session bind
-> route forwarding or downlink ACK handling
```

Existing ownership remains:

```text
internal/auth          verifier implementations and principal contract
internal/pipeline      auth invocation and packet rejection mapping
internal/config        YAML parsing and verifier construction
internal/metrics       auth counters, latency, cache, and in-flight metrics
internal/server        verifier injection only
```

The packet format and reserved MsgIDs do not change in V3 authentication work.

## Principal Contract

The current fields remain the base contract:

```go
type Principal struct {
    ClientID  string
    TokenID   string
    Subject   string
    Scopes    []string
    ExpiresAt time.Time
}
```

V3.2 adds expiry metadata for safe positive-cache lifetime. V3.3 may add issuer
metadata for JWT/JWKS verification:

```go
Issuer string
```

Rules:

- `ClientID` is required and is authoritative for session binding.
- `TokenID` should use JWT `jti` or a backend-provided stable token identifier.
- `Subject` represents the authenticated subject when available.
- `Scopes` use exact strings and are cloned at ownership boundaries.
- `ExpiresAt` limits positive cache lifetime when present.
- Raw token values must never be copied into `Principal`.

The packet `ClientID` remains a claim supplied by the client. A mismatch may be
logged as metadata, but the verified principal always wins.

## Error Contract

V3 needs typed verifier errors so invalid credentials are not confused with a
temporary auth dependency failure:

```text
invalid_token          malformed, unknown, bad signature, bad audience
expired_token          valid structure but expired
forbidden              verified but missing required gateway permission
provider_timeout       remote verifier timed out
provider_unavailable   remote verifier or JWKS source unavailable
misconfigured          invalid local verifier configuration
```

Recommended packet behavior:

```text
invalid_token / expired_token / forbidden -> unauthorized
provider_timeout / provider_unavailable   -> auth_unavailable (retryable)
misconfigured                             -> rejected and readiness failure
```

`auth_unavailable` would be a backward-compatible new ACK code. Older clients
can still treat unknown non-success codes as rejection, while newer clients can
retry with backoff.

## Configuration

### Compatibility

Existing configuration remains valid:

```yaml
auth:
  static_tokens:
    dev-token:
      client_id: dev-client
      token_id: dev-token
```

When `auth.type` is omitted and `static_tokens` is present, the gateway selects
the static verifier.

### Static Provider

```yaml
auth:
  type: static
  static_tokens:
    dev-token:
      client_id: dev-client
      token_id: dev-token
      scopes: [gateway:connect]
```

### Remote HTTP Provider

```yaml
auth:
  type: http
  http:
    url: http://backend:8080/internal/auth/verify
    internal_token: replace-with-a-shared-secret
    timeout: 2s
    max_in_flight: 500
  cache:
    enabled: true
    max_entries: 10000
    positive_ttl: 30s
    negative_ttl: 3s
```

The initial HTTP contract is:

```text
POST /internal/auth/verify
Authorization: Bearer <client-token>
X-ZCourier-Internal-Token: <gateway-to-backend-token>
```

Successful response:

```json
{
  "client_id": "client-1",
  "token_id": "token-1",
  "subject": "user-1",
  "scopes": ["gateway:connect"],
  "expires_at": "2026-06-18T12:00:00Z"
}
```

Status mapping:

```text
200       verified principal
401/403   invalid or forbidden token
429       provider temporarily unavailable
5xx       provider temporarily unavailable
timeout   provider timeout
```

### JWT/JWKS Provider

```yaml
auth:
  type: jwt
  jwt:
    issuer: https://identity.example.com
    audience: z-courier
    jwks_url: https://identity.example.com/.well-known/jwks.json
    algorithms: [RS256, ES256]
    client_id_claim: client_id
    token_id_claim: jti
    scopes_claim: scope
    clock_skew: 30s
    refresh_interval: 5m
  cache:
    enabled: true
    max_entries: 10000
    positive_ttl: 30s
    negative_ttl: 3s
```

JWT verification must validate issuer, audience, expiry, not-before time, and
an explicit algorithm allowlist. It must not trust the token header to choose
an unrestricted algorithm.

## Verifier Construction

`internal/config` should create exactly one verifier from `auth.type`:

```text
static -> StaticTokenVerifier
http   -> HTTPVerifier
jwt    -> JWTVerifier
```

Optional decorators wrap the selected provider:

```text
provider verifier
-> bounded cache
-> metrics
-> auth.Verifier exposed to pipeline
```

The pipeline should not know which provider is active.

Configuration validation must reject:

- an unknown provider type
- multiple conflicting provider configs
- HTTP auth without a URL
- JWT auth without issuer, audience, JWKS URL, or algorithm allowlist
- negative or unbounded cache settings

## Cache Rules

- Cache keys use a SHA-256 digest of the raw token, never the token itself.
- Positive entries expire at the earlier of configured TTL and token expiry.
- Negative caching is short and only applies to deterministic invalid-token
  results.
- Provider timeout and unavailable errors are never negative-cached.
- Cache size is bounded and eviction behavior is deterministic enough to test.
- Cache metrics must not include token, subject, or client labels.

Revocation-sensitive deployments can disable the cache or use short TTLs. A
future revocation feed may evict entries by `TokenID`, but it is not required
for the first V3 implementation.

## Metrics And Logging

Proposed metrics:

```text
z_courier_auth_verify_total{provider,result}
z_courier_auth_verify_duration_seconds{provider,result}
z_courier_auth_inflight{provider}
z_courier_auth_cache_total{provider,result}
z_courier_auth_jwks_refresh_total{result}
z_courier_auth_jwks_refresh_duration_seconds{result}
```

Allowed result labels are bounded values such as:

```text
success
invalid
expired
forbidden
timeout
unavailable
misconfigured
cache_hit
cache_miss
```

Logs may include provider, MsgID, claimed client ID, device ID, message ID, and
trace ID. They must never include the raw token, Authorization header, or a
token digest.

## Readiness And Failure Behavior

- Invalid credentials fail closed and do not bind a session.
- A temporary provider outage rejects new authentication attempts with a
  retryable ACK; existing bound sessions remain active.
- Startup configuration errors fail gateway construction.
- A JWT verifier may start only after initial key material is available unless
  explicitly configured with a safe cached-key policy.
- Repeated HTTP/JWKS failures should affect auth-provider readiness detail, not
  silently switch to an insecure verifier.

Circuit breaking is optional for the first HTTP verifier implementation. The
mandatory protections are timeout, bounded in-flight work, bounded cache, and
metrics.

## Minimal Go SDK

SDK work begins after verifier semantics are covered by E2E tests. Proposed
public packages:

```text
pkg/sdk/protocol   stable packet types, codec, reserved MsgIDs, ACK types
pkg/sdk/backend    internal push, batch, status, requeue, discard client
pkg/sdk/client     AUTH/BIND, send, receive, and downlink ACK helper
```

The first SDK milestone should include `protocol` and `backend`. A high-level
TCP client can follow once reconnect and concurrency behavior are specified.

SDK rules:

- Do not expose Zinx interfaces in the public API.
- Accept `context.Context` for network operations.
- Keep transport configuration explicit.
- Return typed errors for protocol and HTTP failures.
- Keep business payloads as `[]byte`.

## Implementation Milestones

### V3.1 Auth Foundation

Status: implemented.

- Add `auth.type` and provider-specific config structures.
- Preserve old `static_tokens` configuration compatibility.
- Add typed auth errors and pipeline ACK mapping.
- Add verifier metrics wrapper.
- Add config and compatibility tests.
- Reserve `http` and `jwt` configuration with fail-fast validation until their
  implementations land in V3.2 and V3.3.

Exit criteria: existing V2 E2E and load-test smoke remain unchanged and green.

### V3.2 Remote HTTP Verification

Status: implemented.

- Implement `HTTPVerifier` with timeout and response validation.
- Add max-in-flight protection.
- Add bounded positive and negative caching.
- Add a development auth endpoint or test server.
- Add HTTP auth integration and outage tests.

Exit criteria: a backend can own token semantics while the gateway binds the
returned `ClientID` without core-code changes.

### V3.3 JWT/JWKS Verification

- Select a maintained JWT/JWK library after a focused dependency review.
- Implement issuer, audience, time, and algorithm checks.
- Add JWKS refresh, key rotation, and stale-key tests.
- Add JWT verifier metrics and documentation.

Exit criteria: the gateway verifies signed tokens locally and survives normal
JWKS key rotation without restart.

### V3.4 Go Integration SDK

- Publish protocol and backend SDK packages.
- Add SDK examples and compatibility tests.
- Decide the high-level client reconnect contract before implementing it.

Exit criteria: a Go backend can push and inspect messages without hand-writing
HTTP requests, and protocol users do not need to duplicate the binary codec.

### V3.5 Internal Request Hardening

- Add optional timestamped HMAC signatures for internal HTTP requests.
- Reject expired timestamps and replayed nonces.
- Document deployment-level TLS or mTLS termination.
- Add security metrics without high-cardinality labels.

Exit criteria: shared-token mode remains available for simple deployments, and
operators can opt into replay-resistant signed internal requests.

## V3 Completion Criteria

V3 is complete when:

- Static, HTTP, and JWT verifier modes are configuration-selectable.
- Gateway and backend share one documented principal contract.
- Provider outages are distinguishable from invalid client credentials.
- Auth cache, timeout, in-flight, metrics, and secret-handling rules are tested.
- HTTP and JWT auth paths have automated integration coverage.
- Existing single-node, cluster, retry, and load-test checks remain green.
- The first Go SDK packages are documented and tested.
- A third-party project can integrate without modifying gateway core code.

## Recommended First Task

Start with V3.1 only. It creates the provider configuration and typed error
foundation while keeping `StaticTokenVerifier` as the active implementation.
That gives the HTTP and JWT implementations a stable place to plug in without
mixing several risk areas in the first change.
