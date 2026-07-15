# V14 Roadmap

V14 is the planning track for the next public milestone after `v0.13.0`. Its
target SemVer version is `v0.14.0`, not `v14.0.0`.

`v0.13.0` added reliable signed HTTP publication for terminal delivery events.
The gateway now has the transport and retry behavior required to integrate
with deployments that do not operate NSQ. V14 hardens the network boundaries
around that feature and the existing administration and client entry points.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.14.0` until it is implemented, documented, tested, and included in the
release acceptance path.

## Product Direction

V14 is the production transport-security milestone. It provides a practical
edge deployment model without turning Z-Courier into a certificate authority,
an ingress controller, or a general-purpose service mesh.

The intended production boundaries are:

```text
TLS client
    -> edge proxy TCP listener
    -> private gateway TCP listener

browser
    -> edge proxy HTTPS listener
    -> selected Console and browser-session paths
    -> private gateway internal HTTP listener

gateway terminal publisher
    -> HTTPS or mTLS
    -> webhook receiver
```

HMAC remains required where it already protects request identity and replay
resistance. TLS encrypts and authenticates the transport; it does not replace
message identity, receiver idempotency, or the existing HMAC contract.

V14 should focus on:

- Adding custom CA and optional client-certificate support to the signed
  terminal HTTP publisher.
- Adding first-class TLS connection options to the public Go and PHP clients
  so they can connect to a TLS-terminating edge proxy.
- Providing reviewed Nginx and Caddy production templates with a narrow route
  allowlist for the browser console.
- Documenting and testing zero-downtime HMAC key and TLS certificate rotation.
- Mounting certificates and private keys through Compose and Helm secret files
  without copying secret bytes into ConfigMaps, logs, diagnostics, or the
  console.

## Non-Goals

V14 does not target:

- Operating an ACME server, issuing certificates, or managing a public PKI.
- Replacing Kubernetes Ingress, a cloud load balancer, or a service mesh.
- Adding an `insecure_skip_verify` option. Local plain HTTP remains available
  only through the existing explicit development opt-in.
- Exposing all `/internal/*` routes through a public reverse proxy.
- Removing HMAC from backend, peer, or terminal-webhook requests.
- Adding TLS directly to every gateway listener when a reviewed edge proxy can
  provide the boundary with less protocol and certificate-management code.
- Changing the packet envelope, MsgID semantics, delivery policy, or terminal
  outbox reliability model.

## Security Model

### Client TCP Boundary

Public clients connect to a TLS listener owned by an edge proxy or managed load
balancer. The proxy forwards raw TCP to the gateway only on a private network.
The gateway packet protocol remains unchanged inside the encrypted stream.

The Go and PHP SDKs must verify the proxy certificate by default. A custom CA
is allowed for private deployments. Disabling certificate verification is not
a supported production option.

### Console HTTP Boundary

The gateway internal HTTP listener remains private. The public HTTPS listener
for the Console forwards only the browser UI and the API paths required by the
Console session. Machine endpoints such as backend push, peer push, metrics,
health, and readiness are denied by default at the public proxy.

The proxy must preserve the original host and scheme, set bounded request and
idle timeouts, and emit security headers suitable for a browser administration
surface. `admin_console.session.cookie_secure` remains enabled behind HTTPS.

### Terminal Webhook Boundary

The HTTP terminal publisher continues to sign the exact request bytes using
`ZCOURIER-HMAC-SHA256`. Standard HTTPS validates the receiver with system roots
or an optional custom CA. mTLS additionally presents a gateway client
certificate so the receiver can authenticate the transport peer before HMAC
verification.

The receiver must still verify the HMAC key ID, timestamp, nonce, signature,
and stable `event_id`. A valid client certificate alone is not sufficient to
accept an event.

## Target Configuration

### Terminal Publisher TLS

The TLS block is optional and additive. Omitting it preserves the `v0.13.0`
behavior and uses the host system root pool:

```yaml
downlink:
  terminal:
    publisher:
      type: http
      http:
        url: https://terminal-events.example.internal/v1/z-courier
        timeout: 5s
        hmac:
          key_id: terminal-2026-08
          secret: ${ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET}
        tls:
          ca_file: /run/secrets/terminal-webhook/ca.crt
          client_cert_file: /run/secrets/terminal-webhook/tls.crt
          client_key_file: /run/secrets/terminal-webhook/tls.key
          server_name: terminal-events.example.internal
```

Rules:

- `ca_file` is optional. When set, its PEM certificates are added to a root
  pool used only by this publisher.
- `client_cert_file` and `client_key_file` must be configured together.
- `server_name` is optional and overrides certificate-name verification only
  when private DNS or a proxy target requires it.
- TLS 1.2 is the minimum accepted protocol version.
- Files are loaded and validated at startup. Errors identify the field or file
  path but never include key or certificate contents.
- Redirects remain disabled and only `2xx` responses are successful.

### Public Client TLS

The Go and PHP SDK configuration should expose equivalent behavior:

- TLS is opt-in so existing private-network TCP deployments stay compatible.
- Certificate verification is enabled whenever TLS is enabled.
- System roots are the default; a custom CA file is optional.
- An explicit server name is supported for private load balancers.
- Client certificates are not required for the first client-TCP increment;
  they can be added later without changing the wire packet format.

## Key And Certificate Rotation

V14 documents one repeatable rotation procedure for every HMAC boundary:

1. Add the new key ID and secret to the verifier's accepted key ring.
2. Confirm both old and new key IDs verify in a pre-production probe.
3. Roll signers to the new active key ID without stopping all nodes at once.
4. Observe authentication rejects, terminal publication failures, and peer
   failures for at least one maximum replay window plus deployment interval.
5. Remove the old key only after no old-key traffic remains.

The roles differ by boundary:

- Backend-to-gateway rotation adds a key to `internal_http.auth.hmac.keys`,
  rolls backend signers, then removes the old gateway verification key.
- Gateway peer rotation adds the new key to every node's peer key ring before
  changing the active peer signer key during a rolling deployment.
- Terminal webhook rotation adds the new key to the receiver first, then rolls
  gateways with the new terminal `key_id` and secret. During a cluster rollout,
  the receiver intentionally accepts events signed by either key ID.

TLS certificate rotation follows the same overlap principle: trust the new CA
or certificate before rolling clients, keep old trust during the rollout, and
remove it only after old connections and pods have drained.

## Reverse Proxy References

### Nginx

The Nginx reference should cover:

- Raw TCP TLS termination for the public client port using the stream module.
- HTTPS termination for the Console with TLS 1.2 or later.
- A narrow Console API allowlist and explicit denial of unrelated internal
  machine endpoints.
- Forwarded host/scheme headers, request-size limits, timeouts, HSTS, and
  browser security headers.
- Optional mTLS on a separate private listener for selected machine callers;
  this listener must not share the public Console route policy.

### Caddy

The standard Caddy reference should cover the HTTPS Console boundary and
automatic certificate management. Standard Caddy does not provide raw TCP
proxying, so the reference must state that client TCP requires a cloud load
balancer, Nginx stream, HAProxy, Envoy, or a separately reviewed Caddy L4
plugin.

Templates must use placeholder hostnames and secret paths, remain disabled by
default, and pass syntax or container-start validation in CI.

## Workstreams

### V14.1 TLS Configuration And HTTP Publisher

- Add the optional terminal HTTP TLS configuration and strict validation.
- Build a dedicated `http.Transport` with a cloned TLS configuration.
- Load custom roots and optional client key pairs once at startup.
- Preserve timeout, cancellation, no-redirect, exact-body signing, and `2xx`
  response behavior.
- Add tests for trusted and untrusted roots, name mismatch, missing key pair,
  valid mTLS, rejected client certificate, timeout, and shutdown.

Acceptance criteria:

- Existing `none`, `nsq`, and ordinary HTTPS configurations remain compatible.
- A private-CA server is accepted only when the configured CA verifies it.
- An mTLS receiver observes the configured client certificate and a valid HMAC
  signature on the same request.
- No private-key bytes appear in errors, logs, metrics, diagnostics, or admin
  responses.

### V14.2 Public Client TLS

- Add Go SDK TLS options while preserving the injectable dialer contract.
- Add equivalent PHP SDK TLS stream-context options.
- Cover system CA, custom CA, server-name verification, handshake timeout,
  reconnect, and authentication after TLS establishment.
- Add a TLS edge listener to SDK E2E without changing packet fixtures.

Acceptance criteria:

- Existing plaintext clients remain compatible by default.
- TLS clients reject an unknown CA and a mismatched server name.
- Go and PHP clients reconnect through the TLS proxy and complete bind,
  upstream, downlink, and ACK paths.

### V14.3 Reverse Proxy And Deployment Templates

- Add Nginx and Caddy references with generated local test certificates.
- Add Compose examples for Console HTTPS and Nginx client TCP TLS.
- Add Helm values and secret-volume wiring for terminal webhook CA, client
  certificate, and private key files.
- Document equivalents for Kubernetes Ingress or managed load balancers rather
  than shipping controller-specific templates in the first increment.
- Add static checks and smoke tests to CI and the release checker.

Acceptance criteria:

- Console login and core read/mutation browser flows pass through HTTPS.
- Public proxy requests to backend push, peer push, metrics, and health routes
  are denied unless a separate private listener explicitly allows them.
- Certificate and key material is sourced from files or Kubernetes Secrets,
  never from a ConfigMap or committed example value.

### V14.4 Rotation Runbook And Release Acceptance

- Publish English and Chinese HMAC and certificate rotation runbooks.
- Test a two-node terminal webhook key rotation where old and new gateway pods
  coexist and the receiver accepts both key IDs.
- Test certificate replacement with overlapping trust and a rolling restart.
- Add rollback steps and failure signals for every rotation phase.
- Include TLS/mTLS, proxy, Compose, Helm, SDK, and secret-leak checks in release
  acceptance.

Acceptance criteria:

- A documented rotation completes without dropping accepted terminal events or
  breaking peer communication.
- Rollback restores the previous active key or certificate without database or
  packet-protocol changes.
- Operators can identify failed TLS handshakes or HMAC verification errors
  without exposing secret material.

## Suggested Implementation Order

1. Implement terminal webhook custom CA and mTLS configuration with focused
   tests.
2. Add Compose and Helm secret-file mounts for the new TLS material.
3. Add Go then PHP client TLS support and SDK E2E through a TLS edge listener.
4. Add the reviewed Nginx and Caddy templates and browser/proxy smoke tests.
5. Add the bilingual rotation runbook, cluster rotation E2E, and release checks.

## Completion Criteria

`v0.14.0` is complete when:

- the terminal webhook publisher verifies private-CA receivers and can present
  a client certificate without weakening HMAC verification;
- public Go and PHP clients can verify and use a TLS-terminating gateway edge;
- reviewed Nginx and Caddy references protect the Console and deny unrelated
  internal endpoints by default;
- HMAC keys and certificates have tested, reversible, zero-downtime rotation
  procedures;
- Compose, Helm, CI, E2E, release acceptance, and English/Chinese operations
  documentation cover the supported security model; and
- existing plaintext private-network and ordinary HTTPS deployments remain
  backward-compatible.

## Known Boundaries

- Certificate issuance, revocation infrastructure, DNS, and external load
  balancer availability remain deployment responsibilities.
- An edge proxy can encrypt public traffic but does not make an untrusted
  private network safe; production network policy still matters.
- mTLS authenticates a certificate identity, while HMAC authenticates the
  request and protects replay-sensitive canonical bytes. Deployments should
  keep both where the protocol requires HMAC.
- A rolling rotation temporarily increases the accepted trust set. Operators
  must remove old keys and certificates after the observation window.
