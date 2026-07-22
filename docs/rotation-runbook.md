# Key And Certificate Rotation Runbook

This runbook rotates Z-Courier HMAC keys and edge TLS/mTLS certificates without
changing the packet protocol, PostgreSQL schema, Redis state, or NSQ topics.
Perform one boundary at a time. Do not combine a key rotation with an unrelated
gateway upgrade.

## Before You Start

1. Record the current deployment revision, active key IDs, certificate serials,
   Secret versions, and rollback revision.
2. Generate new HMAC secrets with at least 32 random bytes. Generate
   certificates through the deployment PKI or certificate manager; never use
   `scripts/generate_edge_test_certs.sh` outside disposable local testing.
3. Verify every target gateway is ready and the normal control-plane metrics
   are stable. Keep the old secrets and certificate material available only in
   the approved secret manager until the observation window ends.
4. In staging, run the relevant acceptance checks:

   ```bash
   bash scripts/e2e_cluster.sh
   bash scripts/certificate_rotation_smoke.sh
   bash scripts/helm_hmac_rotation_check.sh
   ```

5. Prepare an explicit rollback change before changing production. A rollback
   restores previous secret references or certificate files; it does not roll
   back the database or resend accepted packets.

Never put HMAC secrets, private keys, or CA private keys in Helm values,
ConfigMaps, browser code, logs, diagnostics, tickets, or shell history.

## HMAC Rotation

Z-Courier uses the same timestamped signing protocol at three independent
boundaries:

| Boundary | Gateway verifier configuration | Active outbound signer |
| --- | --- | --- |
| Backend to gateway | `internal_http.auth.hmac.keys` | Backend SDK or backend signer |
| Gateway peer push | `cluster.peer.auth.hmac.keys` | `cluster.peer.auth.hmac.key_id` |
| Terminal HTTP webhook | Receiver-owned keyring | `downlink.terminal.publisher.http.hmac.key_id` |

`keys` is an accepted verification keyring. The peer `key_id` selects exactly
one outbound signing key. A terminal webhook receiver is external to the
gateway, so it must independently accept old and new key IDs during the rollout.

### 1. Stage The New Key

Add the new key ID and secret to every verifier before any signer changes.

- For backend-to-gateway and peer HMAC, deploy all gateway pods with both old
  and new entries in their keyrings.
- For terminal webhooks, deploy the receiver with both keys first.
- For Helm, keep the new active secret in the primary `secretEnv`, put the old
  verification key in `additionalKeys`, and source both values from an existing
  Kubernetes Secret. See
  [values-hmac-rotation.yaml](../deploy/helm/z-courier/examples/values-hmac-rotation.yaml).

Do not remove the old key at this point. Confirm all pods loaded the intended
configuration using readiness checks and deployment status, without printing
secret values.

### 2. Roll Signers

Switch signers only after all verifiers accept the new ID.

1. Roll backend SDK or backend signer instances to the new internal HTTP key.
2. For peer HMAC, change `cluster.peer.auth.hmac.key_id` to the new ID while
   retaining both entries in `keys`, then roll gateway pods one at a time.
3. For terminal webhooks, roll gateway pods one at a time with the new terminal
   `key_id` and secret. Keep the receiver keyring dual-key until old pods have
   drained and their terminal retry leases have expired.

For each pod, wait for readiness before continuing. Do not drain all cluster
nodes at once. Existing TCP connections may reconnect during a gateway drain;
new AUTH/BIND traffic must continue to work.

### 3. Observe The Overlap Window

Watch these signals throughout the rollout:

| Signal | Expected | Investigate when |
| --- | --- | --- |
| `z_courier_internal_http_signature_total` | `success` continues | `invalid_signature`, `expired`, `replay`, or `auth_unavailable` rises |
| `z_courier_cluster_peer_signature_total` | `success` continues | a peer push receives signature rejection |
| Terminal webhook receiver | accepted events from both key IDs | 401/403, repeated delivery attempts, or missing terminal events |
| `z_courier_downlink_terminal_publish_total` and retry metrics | normal publication progress | terminal events remain pending or publication failures rise |
| `/readyz`, route lookup, peer push | healthy per node | a rolled pod is not ready or routes point at a drained node |

Key IDs may appear in receiver audit records if the receiver deliberately stores
them, but Z-Courier does not use them as Prometheus labels. Never include secret
bytes in diagnostics.

### 4. Retire The Old Key

Wait for at least the maximum request lifetime, `max_clock_skew`, nonce TTL,
load-balancer drain period, and terminal retry lease. Also wait until all old
gateway pods and backend signer instances are gone.

Then remove the old key from verifiers and remove its external Secret reference.
For Helm, remove the matching `additionalKeys` item and `extraEnv` entry in a
follow-up deployment. Keep secret-manager recovery history according to the
organization retention policy.

### HMAC Rollback

If signature failures begin after a signer rollout:

1. Restore the prior active signer key ID and secret reference.
2. Keep both keys accepted while the rollback rolls through the fleet.
3. Verify signed internal requests, peer pushes, and terminal publication.
4. Do not remove either key until the new stable observation window completes.

Restoring the old active key is protocol-compatible and does not require a data
migration. Do not delete queued downlink messages to "clear" a signing issue.

## TLS And mTLS Certificate Rotation

TLS terminates at the reviewed Nginx, Caddy, load balancer, or platform ingress
edge. Gateway packets remain unchanged behind that edge. For mTLS machine
listeners, server certificate trust and client certificate trust are separate
sets and must overlap independently.

### 1. Stage Trust

Before replacing a server certificate:

1. Put the new issuing CA in every client trust bundle alongside the old CA.
2. If mTLS client certificates also rotate, put the new client CA in the edge
   `client-ca.crt` trust bundle alongside the old client CA.
3. Mount only `tls.crt`, `tls.key`, and the public `client-ca.crt` bundle into
   the proxy. Keep CA private keys outside the proxy container or pod.
4. Reload or roll the edge only after the dual trust bundle is present.

For Kubernetes, update an externally managed Secret or certificate-manager
reference and perform a controlled rolling reload. Do not place PEM contents in
chart values or ConfigMaps.

### 2. Replace Certificates Gradually

Replace the server certificate and private key one edge instance at a time.

- Nginx can perform a controlled `nginx -s reload` after atomically updating
  mounted certificate files.
- Load balancers and ingress controllers should use their provider's rolling
  certificate update mechanism.
- Keep at least one healthy old or new edge instance available during every
  update. Preserve configured TCP drain and idle timeouts for long-lived client
  connections.

Validate a fresh TLS handshake with the dual trust bundle after each instance.
For mTLS, validate both an old and a new client certificate during the overlap.
Existing client connections may finish or reconnect; the Go SDK creates a fresh
TLS handshake when it reconnects, so recreate clients after changing local CA
files.

### 3. Retire Old Trust

After old server certificates, old client certificates, and their active
connections have drained:

1. Remove the old CA from client trust bundles.
2. Remove the old client CA from the mTLS listener trust bundle.
3. Reload or roll edges gradually again.
4. Verify a new certificate client succeeds and an old-certificate client is
   rejected.

### Certificate Rollback

If new certificate validation, SNI, or mTLS verification fails:

1. Restore the previous server certificate and key reference.
2. Restore the previous client CA bundle if the failure is mTLS-related.
3. Reload or roll one edge at a time and verify fresh old-trust handshakes.
4. Keep the dual trust bundle until the incident is resolved and a new rotation
   plan is approved.

Common failure signals are TLS handshake errors at the proxy, client
`unknown authority` errors, Nginx `400` responses caused by client certificate
verification, rising edge 5xx responses, or a loss of new AUTH/BIND sessions.

## Evidence And Exit Criteria

Attach the following to the change record without attaching secrets or private
keys:

- Secret or certificate-manager version identifiers and active key IDs.
- Deployment revisions, pod readiness, and edge reload or rollout timestamps.
- A short metrics snapshot covering signature results, peer push, terminal
  publication, and connection health before, during, and after the change.
- Output or CI links for the relevant smoke tests.
- The exact retirement time for old key IDs and old certificate trust.

The rotation is complete only when the configured observation window has passed,
all active signers and certificates use the new material, old trust has been
removed, normal traffic is stable, and the rollback material remains recoverable
through the approved secret or certificate manager.
