# Internal HTTP HMAC Signing

Z-Courier can authenticate backend-to-gateway and gateway-to-gateway internal
HTTP requests with a timestamped HMAC-SHA256 signature. HMAC mode detects body,
path, query, and method tampering and rejects a valid signed request when its
nonce is replayed.

HMAC authenticates requests but does not encrypt them. Production deployments
must still use TLS, mTLS, or a trusted encrypted service-mesh connection.

## Headers

Every signed request carries:

```text
X-ZCourier-Key-ID: backend-2026-01
X-ZCourier-Timestamp: 1780000000
X-ZCourier-Nonce: <unpadded Base64URL containing 16-64 random bytes>
X-ZCourier-Signature: <unpadded Base64URL HMAC-SHA256>
```

The timestamp is Unix time in seconds. Key IDs contain 1-128 visible ASCII
characters. HMAC secrets must contain at least 32 bytes.

## Canonical String

The UTF-8 canonical string contains seven newline-separated fields and no
trailing newline:

```text
ZCOURIER-HMAC-SHA256
<timestamp header>
<nonce header>
<uppercase HTTP method>
<escaped path or />
<canonical query>
<lowercase hexadecimal SHA-256 body digest>
```

Canonical query construction:

1. Parse query keys and values as UTF-8 form data, where `+` represents space.
2. Percent-encode every UTF-8 byte except `A-Z a-z 0-9 - . _ ~`.
3. Encode spaces as `%20` and use uppercase hexadecimal digits.
4. Sort pairs by encoded key and then encoded value.
5. Join each pair as `key=value` with `&`; an absent query is an empty line.

Example input:

```text
POST /internal/push?b=two&a=1&a=0
timestamp: 1780000000
nonce: MDEyMzQ1Njc4OWFiY2RlZg
body: hello
```

Canonical value:

```text
ZCOURIER-HMAC-SHA256
1780000000
MDEyMzQ1Njc4OWFiY2RlZg
POST
/internal/push
a=0&a=1&b=two
2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
```

Compute HMAC-SHA256 over these exact bytes using the selected secret, then
encode the 32-byte result with unpadded Base64URL.

## Replay Protection

The gateway accepts timestamps only within `max_clock_skew`. After a signature
is cryptographically valid, its `key_id + nonce` pair is atomically stored until
the configured nonce expiry. Reuse returns `401`. The in-memory nonce store is
bounded; when full after expired entries are removed, verification fails closed
with `503 auth_unavailable`.

The current store is local to one gateway process. When one public backend
address load-balances signed requests across multiple gateway nodes, use
session affinity or separate node addresses until a shared nonce store is
implemented. The signature still covers each request, but replay detection is
not cluster-global in this first version.

## Key Rotation

The gateway accepts multiple key IDs. Rotate without downtime:

1. Add the new key to the gateway and deploy.
2. Switch backend SDK clients or the gateway peer `key_id` to the new key ID.
3. Wait longer than the maximum request lifetime and deployment overlap.
4. Remove the old key from the gateway.

Do not put secrets in Git. Inject them through a secret manager or deployment
environment when producing the final configuration file.

## Protected Paths

Backend HMAC mode protects backend-facing `/internal/*` APIs, including push,
message administration, admin overview/diagnostics/check/routes, and debug
routes. Cluster peer HMAC mode independently protects
`POST /internal/cluster/push`. Backend and peer authentication use separate key
rings and nonce stores even though they share this wire protocol. `/healthz`,
`/readyz`, and `/metrics` remain unsigned for probes and Prometheus.

## Observability

Backend verification exports
`z_courier_internal_http_signature_total{result=...}`. Peer verification exports
`z_courier_cluster_peer_signature_total{result=...}`. Results use a bounded set
such as `success`, `replay`, `expired`, and `invalid_signature`; secrets, key IDs,
and caller identifiers are never metric labels. The provisioned Grafana
dashboard displays both request rates.
