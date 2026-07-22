# TLS Edge References

This directory contains the reviewed V14 edge-proxy references. They terminate
TLS outside the gateway and keep the gateway TCP and internal HTTP listeners on
a private network.

```text
Go/PHP SDK -- TLS --> Nginx stream -- private TCP --> gateway:8999

browser -- HTTPS --> Nginx or Caddy -- allowlisted HTTP --> gateway:18080

machine caller -- optional mTLS --> separate Nginx listener
               -- existing token/HMAC auth --> gateway:18080
```

TLS does not replace AUTH/BIND, admin sessions, CSRF protection, internal HMAC,
peer HMAC, or terminal-webhook HMAC. The edge proxy never receives a gateway
HMAC secret in these examples.

## Files

| Path | Purpose |
| --- | --- |
| `nginx/nginx.conf.template` | Single-node client TCP TLS and Console HTTPS |
| `nginx/nginx-cluster.conf.template` | Two-node TCP and Console load balancing |
| `nginx/nginx-mtls.conf.template` | Optional, separate private machine mTLS listener |
| `nginx/includes/console-locations.conf` | Exact public Console route allowlist |
| `nginx/includes/machine-locations.conf` | Exact private mTLS machine route allowlist |
| `caddy/Caddyfile` | Standard Caddy automatic HTTPS for the Console |
| `caddy/Caddyfile.local` | File-certificate variant used by local verification |
| `caddy/console.caddy` | Shared Caddy Console route policy |

The Compose references remain opt-in:

- `deploy/production/docker-compose.edge-nginx.yml`
- `deploy/production/docker-compose.edge-caddy.yml`
- `deploy/production/docker-compose.edge-caddy-local.yml`
- `deploy/production/docker-compose.edge-nginx-mtls.yml`
- equivalent Nginx and Caddy files under `deploy/production-cluster/`

## Public Route Policy

The Console HTTPS listener forwards only:

- `/console/*`
- the three admin-session endpoints
- exact admin, route, session, message, diagnostic, and guarded mutation paths
  used by `web/admin`

The fallback is an edge-generated `404`. In particular, the public listener
does not forward:

```text
/internal/push
/internal/push/batch
/internal/cluster/push
/metrics
/healthz
/readyz
```

Methods are also constrained: Console reads use `GET`, mutations use `POST`,
and static assets use `GET` or `HEAD`. The gateway still performs its own
method, permission, admin-session, CSRF, token, and HMAC checks.

## Disposable Test Certificates

Generate a seven-day private CA, server certificate, and mTLS client certificate
for local verification:

```bash
bash scripts/generate_edge_test_certs.sh deploy/production/secrets/edge
```

The generated layout keeps CA signing keys out of the proxy runtime mount:

```text
secrets/edge/
  issuer/  # test CA private keys; never mount this directory
  server/  # tls.crt, tls.key, optional client-ca.crt
  client/  # ca.crt, test client tls.crt and tls.key
```

The script refuses to overwrite a non-empty directory. Its certificates are
only for local tests. Production certificates must come from the deployment's
certificate manager or PKI, and the proxy should receive only `tls.crt`,
`tls.key`, and an optional client trust bundle. Never mount a CA private key.

## Nginx: TCP TLS And Console HTTPS

Set the edge values in `deploy/production/.env`. For the generated local
certificate use:

```text
ZCOURIER_EDGE_SERVER_NAME=edge-proxy.test
ZCOURIER_EDGE_TLS_DIR=./secrets/edge/server
ZCOURIER_EDGE_CLIENT_TLS_PORT=8999
ZCOURIER_EDGE_CONSOLE_HTTPS_PORT=8443
```

Start the single-node reference:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx.yml \
  up -d --build
```

The override enables the embedded Console and admin-session layer, removes the
gateway's plaintext host port, and publishes only Nginx's TLS ports. Inside the
Compose network Nginx still uses `gateway:8999` and `gateway:18080`.

A Go SDK client using the test CA can connect with:

```bash
ZCOURIER_CLIENT_TOKEN='<client-token>' go run ./examples/go-client \
  -address 127.0.0.1:8999 \
  -client-id '<client-id>' \
  -device-id '<device-id>' \
  -tls \
  -tls-ca-file deploy/production/secrets/edge/client/ca.crt \
  -tls-server-name edge-proxy.test
```

Use a trusted production certificate and DNS name for real clients. The
gateway packet format does not change inside TLS. The reference deliberately
does not enable PROXY protocol because Z-Courier does not currently parse a
PROXY protocol header.

For the two-node reference, generate the certificate under
`deploy/production-cluster/secrets/edge`, update that directory's `.env`, and
run:

```bash
docker compose \
  --env-file deploy/production-cluster/.env \
  -f deploy/production-cluster/docker-compose.yml \
  -f deploy/production-cluster/docker-compose.edge-nginx.yml \
  up -d --build
```

Nginx uses least-connections balancing for the two long-lived TCP upstreams.
Console requests can land on either gateway because the production cluster
configuration uses shared Redis admin sessions and PostgreSQL audit storage.

## Caddy: Console HTTPS

Standard Caddy is the Console HTTPS reference. It manages public certificates
automatically and persists ACME state in Compose volumes:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-caddy.yml \
  up -d --build
```

Before production use, point `ZCOURIER_EDGE_SERVER_NAME` at real DNS and map
`ZCOURIER_EDGE_CONSOLE_HTTPS_PORT=443`. The host must be reachable for the ACME
challenge allowed by your Caddy deployment.

To use the generated local certificate instead of ACME, add the local override:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-caddy.yml \
  -f deploy/production/docker-compose.edge-caddy-local.yml \
  up -d --build
```

Standard Caddy does not proxy arbitrary raw TCP. Client TCP TLS still requires
Nginx stream, HAProxy, Envoy, a managed layer-4 load balancer, or a separately
reviewed Caddy L4 build. The Caddy override binds the remaining plaintext
gateway host port to `127.0.0.1`; do not expose it as the public client entry.

## Console Authentication Behind HTTPS

The edge allows the Console session login path, but TLS termination is not
operator authentication.

- In token-mode private deployments, the browser can exchange the internal
  token for the existing short-lived HTTP-only admin session cookie.
- The production references keep `internal_http.auth.mode: hmac`. Browser
  JavaScript cannot safely hold or generate this signature. Put an
  identity-aware deployment service in front of the login path that authenticates
  the operator and signs only the upstream login request, or keep HMAC-only
  administration in `cmd/admin`.
- Do not place `ZCOURIER_INTERNAL_HMAC_SECRET` in Nginx, Caddy, browser
  JavaScript, a ConfigMap, or a public environment dump.

VPN, private ingress, or another operator access boundary is still recommended
even when the Console has HTTPS and a gateway admin session.

## Optional Private mTLS Listener

The optional Nginx mTLS listener is a separate service and route policy. It is
bound to `127.0.0.1:9443` by default:

```bash
docker compose \
  --env-file deploy/production/.env \
  -f deploy/production/docker-compose.yml \
  -f deploy/production/docker-compose.edge-nginx-mtls.yml \
  up -d edge-nginx-mtls
```

It accepts only the exact health, metrics, backend push, batch push, and peer
push paths in `machine-locations.conf`. A valid client certificate establishes
the transport identity, but backend and peer requests must still pass the
gateway's token or HMAC verification. Keep this listener on a private network;
change its bind address only after reviewing firewall and caller identity.

## Kubernetes And Managed Load Balancers

The Helm chart intentionally does not install an ingress controller or issue
edge certificates. Equivalent Kubernetes wiring should preserve these rules:

1. Put the chart's client Service behind a TCP-capable load balancer that
   terminates or passes verified TLS, then forwards raw TCP to port `8999`.
2. Keep the internal Service private. A Console ingress or gateway must use the
   same exact path allowlist as `console-locations.conf`, not a broad
   `/internal` prefix.
3. Keep `/internal/push`, peer push, metrics, health, and readiness on separate
   private routes or listeners. Do not combine them with the public Console
   policy.
4. Store edge certificates in Kubernetes Secrets or the platform certificate
   manager. Do not put PEM bytes in Helm values or ConfigMaps.
5. Use Redis-backed admin sessions when Console traffic can move between pods.
6. Set load-balancer idle timeout and drain behavior for long-lived TCP
   connections, and do not enable PROXY protocol unless the gateway gains
   explicit support for it.

Provider-specific Ingress, Gateway API, and load-balancer annotations are left
to the deployment because their TLS and TCP semantics differ.

## Verification

Run deterministic config and secret-boundary checks:

```bash
bash scripts/edge_proxy_check.sh
```

Run the full local smoke. It verifies Nginx browser login/read/mutation flows,
public route denial, Go SDK AUTH/BIND through Nginx TCP TLS, Caddy HTTPS login,
and the separate mTLS listener:

```bash
bash scripts/edge_proxy_smoke.sh
```

Both checks are included in CI and the release checker. Generated certificates
and private keys remain in temporary or gitignored directories.
