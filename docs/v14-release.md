# V14 Release Readiness Guide

This guide is the release acceptance source of truth for the V14 security and
edge-delivery work. V14 does not change the Z-Courier client packet format,
PostgreSQL schema, Redis route keys, or NSQ topics. It adds opt-in TLS/mTLS
deployment references, HMAC overlap support, and operational verification.

## Compatibility And Rollback

Plaintext private-network deployments remain supported. Existing HTTPS
deployments remain supported. TLS terminates at the selected edge proxy or
platform listener; the gateway packet protocol remains unchanged behind it.

HMAC and certificate rollback is configuration-only:

- restore the previous active HMAC signer and keep both key IDs accepted while
  the fleet rolls back;
- restore the previous certificate and trust bundle one edge instance at a
  time;
- do not delete downlink rows, Redis routes, NSQ messages, or database objects
  to address a signing or TLS incident.

Use the detailed procedure in [rotation-runbook.md](rotation-runbook.md).

## Release Acceptance Matrix

Run checks against the exact commit intended for the tag.

| Area | Required evidence | Command or workflow |
| --- | --- | --- |
| Source | Clean worktree, expected commit, no tracked runtime key/certificate material | `git status --short`, `git log -1 --oneline`, `bash scripts/secret_boundary_check.sh` |
| Fast validation | Go tests, race tests, vet, PHP SDK, admin build, configs, shell syntax | `bash scripts/release_check.sh` |
| HMAC overlap | Helm renders active plus previous verification keys without secret bytes in ConfigMaps | `bash scripts/helm_hmac_rotation_check.sh` |
| Terminal webhook | Private CA, mTLS client certificate, HMAC signing, and retry behavior | `bash scripts/e2e.sh` and `bash scripts/compose_terminal_webhook_tls_check.sh` |
| Cluster rotation | Old/new terminal webhook key rollout, peer HMAC, shared storage, route delivery | `bash scripts/e2e_cluster.sh` |
| Edge policy | Nginx/Caddy templates, secret mount boundaries, Console allowlist, private mTLS routes | `bash scripts/edge_proxy_check.sh` |
| Edge runtime | Browser Console HTTPS, Go SDK TCP TLS, Caddy HTTPS, and private mTLS listener | `bash scripts/edge_proxy_smoke.sh` |
| Certificate rotation | Old/new server and client CA overlap, trust retirement, and rollback after Nginx reload | `bash scripts/certificate_rotation_smoke.sh` |
| Compose and production | Rendered Compose references plus single-node and cluster production smoke | `docker compose ... config`, `bash scripts/production_smoke.sh`, `bash scripts/production_cluster_smoke.sh` |
| Helm and Kubernetes | Lint, package, HMAC and terminal TLS rendering, optional kind smoke/E2E | `bash scripts/k8s_helm_smoke.sh`, `bash scripts/k8s_helm_e2e.sh` |
| CI | Validate, E2E, image, Helm/package, release workflows green on tag commit | GitHub Actions run summary |

The full local command is:

```bash
ZCOURIER_RELEASE_COMPOSER_DOCKER_IMAGE=dnmp8-php82 \
ZCOURIER_RELEASE_RUN_DOCKER=1 \
ZCOURIER_RELEASE_RUN_SLOW=1 \
ZCOURIER_RELEASE_RUN_K8S=1 \
bash scripts/release_check.sh
```

Omit `ZCOURIER_RELEASE_RUN_K8S=1` only when kind/Helm acceptance is intentionally
deferred and record that gap in the release evidence.

## Security Evidence

Attach only non-sensitive evidence to the release record:

- gateway image and Helm chart versions, commit SHA, and workflow URLs;
- Secret or certificate-manager version identifiers, active key IDs, and
  certificate serial numbers;
- readiness and rollout timestamps;
- metrics snapshots for signature results, peer delivery, terminal publication,
  TLS/edge errors, and connection health;
- rotation start, retirement, and rollback decision timestamps.

Do not attach secret values, private keys, complete signed request headers,
client tokens, or PEM content.

## Release Decision

Release V14 only when the matrix is complete, CI is green on the exact tag
commit, normal traffic and security metrics are stable through the agreed
observation window, and the previous signer/certificate material remains
recoverable through the approved secret or certificate manager.
