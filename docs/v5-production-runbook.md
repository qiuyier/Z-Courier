# V5 Production Runbook

This runbook is the operator path for a production or production-like
Z-Courier deployment. It assumes the gateway is already configured with private
internal HTTP, durable downlink storage, monitoring, and either token or HMAC
authentication for internal APIs.

Use this together with:

- [Production Image](../deploy/production/README.md)
- [Production Cluster Reference](../deploy/production-cluster/README.md)
- [Monitoring](../deploy/monitoring/README.md)
- [V5 Admin Ops](v5-admin-ops.md)
- [Internal HTTP Signing](internal-http-signing.md)

## Operator Environment

Set these locally before running `cmd/admin`:

```bash
export ZCOURIER_ADMIN_INTERNAL_URL=http://127.0.0.1:18182
export ZCOURIER_ADMIN_AUTH=token
export ZCOURIER_ADMIN_INTERNAL_TOKEN=dev-internal-token
```

For HMAC-protected production internal HTTP:

```bash
export ZCOURIER_ADMIN_INTERNAL_URL=http://gateway-a.internal:18080
export ZCOURIER_ADMIN_AUTH=hmac
export ZCOURIER_ADMIN_HMAC_KEY_ID=backend-1
export ZCOURIER_ADMIN_HMAC_SECRET="$ZCOURIER_INTERNAL_HMAC_SECRET"
```

Do not expose internal HTTP to the public internet. Backend services, operator
hosts, Prometheus, and peer gateways should reach it through a private network,
private load balancer, VPN, or service mesh.

## First Five Minutes

Start every incident with the same small checklist:

1. Check process liveness:

   ```bash
   curl -fsS "$ZCOURIER_ADMIN_INTERNAL_URL/healthz"
   ```

2. Check traffic readiness:

   ```bash
   curl -fsS "$ZCOURIER_ADMIN_INTERNAL_URL/readyz"
   ```

   `200` means the gateway is ready for traffic. `503` means it is draining,
   starting, or unhealthy for load-balancer traffic.

3. Check gateway overview:

   ```bash
   go run ./cmd/admin overview
   ```

4. Actively check configured dependencies:

   ```bash
   go run ./cmd/admin check
   ```

5. Collect a diagnosis bundle:

   ```bash
   go run ./cmd/admin diagnose \
     -output reports/diagnose/gateway-a.json
   ```

6. Check route configuration:

   ```bash
   go run ./cmd/admin routes
   ```

7. Check Prometheus target health:

   ```text
   Prometheus -> Status -> Targets
   ```

If the gateway is unreachable, collect container logs and dependency status
before changing message state.

## Normal Health Signals

| Area | Healthy Signal |
| --- | --- |
| Process | `/healthz` returns `200` |
| Readiness | `/readyz` returns `200` outside drain/shutdown |
| Admin overview | `readiness.ready=true` and dependencies are available |
| Prometheus | gateway target is `UP` |
| Drain | `z_courier_gateway_readiness{status="ready"} == 1` outside shutdown |
| Sessions | `z_courier_sessions_online` matches expected live connections |
| Clients | `z_courier_clients_online` matches unique online ClientIDs |
| Downlink | `downlink_push_total{result="sent"}` or `queued` increases as expected |
| ACK | `downlink_ack_total{result="delivered"}` increases for ACK-required messages |
| Retry | retry scans run without sustained failed claim or store errors |
| Cluster | peer push success increases when cross-node delivery is expected |

## Prometheus Alerts

The bundled alert rules live at
`deploy/monitoring/prometheus/rules/z-courier-alerts.yml`. Local and production
Compose Prometheus configs load them automatically. Kubernetes users with
Prometheus Operator can start from
`deploy/helm/z-courier/examples/prometheusrule.yaml`.

Default thresholds are examples. Tune rates and `for` windows to production
traffic before paging humans.

The local monitoring stack includes an Alertmanager example at
`deploy/monitoring/alertmanager/alertmanager.yml`. Its default receivers are
local no-op receivers, so production deployments must add real notification
routing before relying on paging. Receiver examples for webhook, SMTP email,
and Slack live under `deploy/monitoring/alertmanager/examples/`.

| Alert | Probable Source | First Action |
| --- | --- | --- |
| `ZCourierGatewayTargetDown` | Gateway down, internal HTTP unreachable, scrape config wrong | Check `/readyz`, container status, and Prometheus targets |
| `ZCourierIngressRejectedSpike` | Bad AUTH/BIND packets, invalid protocol, rate limiting | Run `cmd/admin diagnose`; inspect ingress rejects and auth metrics |
| `ZCourierAuthFailureRatioHigh` | Auth backend/JWT issue or client token rollout problem | Check auth provider status and rejected bind logs |
| `ZCourierHMACSignatureFailures` | Wrong key ID, secret mismatch, clock skew, replay | Check internal and peer HMAC config on both caller and gateway |
| `ZCourierUpstreamFailureRatioHigh` | Business backend, NSQ, network, or route config problem | Run `cmd/admin routes` and `cmd/admin check`; inspect upstream logs |
| `ZCourierUpstreamOverloadRejects` | Upstream capacity limit reached | Check backend latency, route `max_in_flight`, and load-test traffic |
| `ZCourierInternalHTTPOverloadRejects` | Backend push/admin pressure exceeds gateway limit | Check internal push rate and `internal_http.max_in_flight` |
| `ZCourierDownlinkPushFailures` | Missing route, peer push issue, storage/retry problem | Run `cmd/admin route` for affected client/device |
| `ZCourierDownlinkACKLatencyHigh` | Slow clients, network trouble, retry pressure | Check client connection stability and peer push latency |
| `ZCourierRetryWorkerFailures` | PostgreSQL or retry claim problem | Check Postgres health and gateway retry worker logs |
| `ZCourierClusterPeerPushFailureRatioHigh` | Peer gateway unreachable or peer auth mismatch | Check peer internal HTTP, HMAC keys, and NetworkPolicy |
| `ZCourierClusterStaleRoutesDetected` | Redis TTL/refresh issue or unbind cleanup problem | Inspect Redis route key and gateway disconnect logs |
| `ZCourierJWKSRefreshFailures` | JWKS endpoint unreachable or malformed key set | Check JWKS endpoint status, response size, and key format |

## Admin Command Reference

Inspect one gateway node:

```bash
go run ./cmd/admin overview
```

Actively check runtime dependencies:

```bash
go run ./cmd/admin check
```

Collect a safe JSON diagnosis bundle:

```bash
go run ./cmd/admin diagnose \
  -output reports/diagnose/gateway-a.json
```

Inspect upstream route ranges and targets:

```bash
go run ./cmd/admin routes
```

Find where a client/device would receive downlink:

```bash
go run ./cmd/admin route \
  -client-id client-1 \
  -device-id device-1
```

List local sessions on the queried gateway:

```bash
go run ./cmd/admin sessions \
  -client-id client-1 \
  -device-id device-1
```

List failed reliable downlink messages:

```bash
go run ./cmd/admin messages \
  -status failed \
  -limit 100
```

Inspect one stored message:

```bash
go run ./cmd/admin message \
  -message-id message-1
```

Requeue one failed message:

```bash
go run ./cmd/admin requeue \
  -message-id message-1 \
  -confirm
```

Discard one failed message after manual handling:

```bash
go run ./cmd/admin discard \
  -message-id message-1 \
  -reason "handled manually after backend confirmation" \
  -confirm
```

`requeue` and `discard` intentionally require explicit confirmation and operate
on one message at a time.

## Common Incident Paths

### Gateway Is Not Ready

Symptoms:

- `/healthz` fails.
- `/readyz` returns `503`.
- Prometheus target is down.

Checks:

```bash
docker compose -f deploy/production/docker-compose.yml ps
docker logs <gateway-container>
go run ./cmd/admin overview
```

Look for:

- config load errors
- missing `ZINX_CONFIG_FILE_PATH`
- PostgreSQL, Redis, NSQ, or auth backend connection errors
- readiness drain during graceful shutdown

Do not requeue messages until the gateway is ready or you understand why it is
draining.

### Client Cannot Bind

Symptoms:

- TCP connection opens then closes.
- AUTH/BIND ACK is rejected.
- Online sessions stay at `0`.

Checks:

```bash
go run ./cmd/admin overview
```

PromQL:

```promql
sum by (result) (rate(z_courier_ingress_rejected_total[1m]))
sum by (provider, result) (rate(z_courier_auth_verify_total[1m]))
sum by (provider, result) (rate(z_courier_auth_cache_total[1m]))
```

Look for:

- token belongs to a different `client_id`
- auth backend timeout or HTTP error
- JWT/JWKS key id mismatch
- allowlist/blocklist rejection
- rate-limit rejection

### Client Is Online But Downlink Does Not Arrive

First locate the client:

```bash
go run ./cmd/admin route \
  -client-id client-1 \
  -device-id device-1
```

If `local_session_found=true`, query that gateway's sessions:

```bash
go run ./cmd/admin sessions \
  -internal-url http://gateway-node.internal:18080 \
  -client-id client-1
```

If `cluster_route_found=true` but the local session is false, the receiving
gateway should peer-push to the route's `internal_addr`.

PromQL:

```promql
sum by (result) (rate(z_courier_downlink_push_total[1m]))
sum by (target_node, result) (rate(z_courier_cluster_peer_push_total[1m]))
histogram_quantile(0.95, sum by (le, target_node) (rate(z_courier_cluster_peer_push_duration_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_ack_total[1m]))
```

Look for:

- stale Redis route
- peer HMAC failure
- target gateway not reachable on private internal HTTP
- client connected with a different `device_id`
- message was queued because the client disconnected between route lookup and
  push

### Failed Downlink Messages

List failures:

```bash
go run ./cmd/admin messages \
  -status failed \
  -limit 100
```

Inspect one message:

```bash
go run ./cmd/admin message \
  -message-id message-1
```

Decide:

- If the failure was transient and the message is still business-valid,
  `requeue`.
- If the backend already handled it, the user no longer needs it, or the body is
  stale, `discard` with a clear reason.

PromQL:

```promql
sum by (result) (rate(z_courier_downlink_retry_messages_total[1m]))
sum by (owner, result) (rate(z_courier_downlink_retry_claim_messages_total[1m]))
histogram_quantile(0.95, sum by (le) (rate(z_courier_downlink_retry_scan_duration_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_requeue_total[1m]))
sum by (result) (rate(z_courier_downlink_discard_total[1m]))
```

Watch logs for:

```text
admin message action audit
```

Important fields:

- `action`
- `result`
- `http_status`
- `gateway_node`
- `message_id`
- `reason`
- `message_status`
- `auth_mode`
- `auth_key_id`

The audit log must not include internal tokens or HMAC secrets.

### Retry Burst Tuning

If many clients reconnect or a dependency recovers at the same time, failed
downlink messages can become due together. Keep `retry_delay` as the minimum
backoff and use `retry_jitter` to spread the next attempt across a small random
window.

Recommended starting point:

- `retry_delay`: `30s`
- `retry_jitter`: `5s`
- `retry_interval`: `5s`

Use `retry_jitter: 0s` for deterministic local tests. In production, prefer a
jitter window around 10-25% of `retry_delay` unless the downstream dependency
needs a wider recovery window.

### Upstream Is Not Forwarding

Check route ranges:

```bash
go run ./cmd/admin routes
```

PromQL:

```promql
sum by (route, target_type, result) (rate(z_courier_upstream_forward_total[1m]))
histogram_quantile(0.95, sum by (le, route, target_type) (rate(z_courier_upstream_forward_duration_seconds_bucket[5m])))
sum by (route, target_type) (z_courier_upstream_inflight)
sum by (route, target_type) (rate(z_courier_upstream_overload_rejected_total[1m]))
sum by (route, target_type) (z_courier_upstream_route_degraded)
```

Look for:

- MsgID outside all configured route ranges
- HTTP upstream returning non-2xx
- HTTP upstream route state `degraded` or `unavailable` in `cmd/admin diagnostics`
- NSQ address or topic mismatch
- route disabled in YAML
- in-flight limiter saturation

### PostgreSQL Store Errors

Symptoms:

- downlink messages cannot be queued
- retry worker logs store errors
- admin message queries fail with `store_not_configured` or store errors

Checks:

```bash
go run ./cmd/admin overview
go run ./cmd/admin messages -status failed -limit 10
```

Look for:

- DSN or password mismatch
- database not reachable from gateway network
- missing schema migration
- PostgreSQL connection saturation
- clock skew affecting retry scheduling

Do not discard messages just because a store is temporarily unavailable.

### Redis Route Issues

Symptoms:

- `route` returns no cluster route for an online remote client.
- peer push never starts.
- cross-node downlink queues unexpectedly.

Checks:

```bash
go run ./cmd/admin route \
  -internal-url http://gateway-a.internal:18080 \
  -client-id client-1 \
  -device-id device-1

go run ./cmd/admin route \
  -internal-url http://gateway-b.internal:18080 \
  -client-id client-1 \
  -device-id device-1
```

PromQL:

```promql
sum by (result) (rate(z_courier_cluster_registry_lookup_total[1m]))
sum by (reason) (rate(z_courier_cluster_stale_routes_total[1m]))
```

Look for:

- gateway nodes using different Redis key prefixes
- route TTL too short for the bind refresh behavior
- Redis credentials or network mismatch
- `internal_addr` set to loopback instead of a peer-reachable address

### Internal HTTP HMAC Failures

Symptoms:

- admin/backend requests return `401`.
- Prometheus shows internal signature failures.

PromQL:

```promql
sum by (result) (rate(z_courier_internal_http_signature_total[1m]))
sum by (result) (rate(z_courier_cluster_peer_signature_total[1m]))
```

Look for:

- wrong `key_id`
- different secret on caller and gateway
- request body changed after signing
- timestamp outside allowed clock skew
- replayed nonce
- backend HMAC key confused with peer HMAC key

Use separate key rings for backend-to-gateway internal HTTP and gateway-to-gateway
peer push.

### Capacity Or Rate-Limit Rejection

Symptoms:

- requests return `429`
- in-flight metrics stay high
- client ingress is rejected during bursts
- client ACKs have `code="rejected"` with `reason="rate_limited"` or
  `reason="overloaded"`

PromQL:

```promql
sum by (path) (z_courier_internal_http_inflight)
sum by (path) (rate(z_courier_internal_http_overload_rejected_total[1m]))
sum by (route, target_type) (z_courier_upstream_inflight)
sum by (route, target_type) (rate(z_courier_upstream_overload_rejected_total[1m]))
sum(rate(z_courier_rate_limit_rejected_total[1m]))
```

Check whether the bottleneck is:

- client ingress policy
- internal downlink push capacity
- upstream route capacity
- backend/MQ latency
- retry worker competing with live traffic

Raise limits only after confirming dependencies can absorb the traffic.

## Graceful Shutdown

Before planned restart:

1. Remove the node from external load balancer traffic or stop sending new
   client connections to it.
2. Confirm `/readyz` returns `503` after drain begins.
3. Watch online sessions and cluster route cleanup.
4. Start the replacement node and verify `/readyz` returns `200`.
5. Confirm Prometheus target is `UP`.

PromQL:

```promql
sum by (instance) (z_courier_sessions_online)
sum by (instance) (z_courier_clients_online)
sum by (instance, status) (z_courier_gateway_readiness)
sum by (reason) (rate(z_courier_cluster_stale_routes_total[1m]))
```

If a node stays draining for more than the expected rollout window, run:

```bash
go run ./cmd/admin diagnostics \
  -internal-url "$ZCOURIER_ADMIN_INTERNAL_URL"
```

Check `readiness.draining_since`, `readiness.drain_duration`, remaining online
sessions, and cluster route cleanup logs.

## Load-Test And Baseline Review

Use load tests for release confidence and regression diagnosis, not as a
real-time production repair tool.

Smoke:

```bash
bash scripts/loadtest_smoke.sh
```

Manual larger run:

```bash
go run ./cmd/loadtest \
  -mode downlink \
  -port 9902 \
  -internal-url http://127.0.0.1:18182 \
  -internal-token dev-internal-token \
  -clients 100 \
  -rate 100 \
  -duration 60s \
  -output reports/loadtest-manual/downlink.json
```

Compare against a matching baseline:

```bash
go run ./cmd/loadcompare \
  -base reports/baseline/loadtest-manual/downlink.json \
  -current reports/loadtest-manual/downlink.json \
  -output reports/loadtest-manual/compare.md
```

Baseline comparisons should remain informational unless the release process
explicitly promotes them to a hard gate.

## What To Collect For A Bug Report

Collect:

- gateway version or git commit
- config with secrets removed
- `cmd/admin overview`
- `cmd/admin check`
- `cmd/admin diagnose`
- `cmd/admin routes`
- relevant `cmd/admin route`, `sessions`, `message`, or `messages` output
- Prometheus target state
- PromQL screenshots or query results for the affected path
- gateway logs around the incident window
- `admin message action audit` entries for any requeue/discard action
- whether the deployment is single-node or clustered

Never paste internal tokens, HMAC secrets, JWT private keys, PostgreSQL
passwords, Redis passwords, or full message bodies unless the issue is in a
private trusted channel and the body is safe to disclose.
