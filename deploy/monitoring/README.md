# Z-Courier Monitoring

This directory contains a local Prometheus + Alertmanager + Grafana stack for
Z-Courier development and demos.

## What Runs

- Prometheus scrapes Z-Courier metrics from the gateway internal HTTP server
  and loads the bundled Z-Courier recording and alert rules.
- Alertmanager receives firing Prometheus alerts and groups them with a local
  no-op receiver that is safe for demos.
- Grafana loads the Prometheus data source, `Z-Courier Overview`, and
  `Z-Courier Production Signals` dashboards automatically.
- node-exporter exposes host/container node metrics. It is useful for CPU,
  memory, disk, and network visibility, but it is not required for Z-Courier
  application metrics.

## Start Z-Courier

Start the gateway from the repository root:

```bash
go run ./cmd/gateway -config configs/z-courier.yaml
```

The default internal HTTP address is `127.0.0.1:18080`, and metrics are exposed
at:

```bash
curl http://127.0.0.1:18080/metrics
```

## Start Monitoring

Start the monitoring stack from the repository root:

```bash
docker compose -f deploy/monitoring/docker-compose.yml up -d
```

Open Prometheus:

```text
http://127.0.0.1:9090
```

Check `Status -> Targets`. The `z-courier` target should be `UP`.
Check `Status -> Rules` to review the bundled Z-Courier recording rules and
alert rules.

Open Alertmanager:

```text
http://127.0.0.1:9093
```

Firing alerts from Prometheus should appear here after their `for` windows pass.

Example PromQL queries:

```promql
sum(z_courier_sessions_online)
z_courier_sessions_online
sum(z_courier_clients_online)
sum by (status) (z_courier_gateway_readiness)
sum by (result) (rate(z_courier_ingress_packets_total[1m]))
sum by (route, target_type, result) (rate(z_courier_upstream_forward_total[1m]))
histogram_quantile(0.95, sum by (le, route, target_type) (rate(z_courier_upstream_forward_duration_seconds_bucket[5m])))
sum by (route, target_type) (z_courier_upstream_inflight)
sum by (route, target_type) (rate(z_courier_upstream_overload_rejected_total[1m]))
sum by (instance, mode, policy, result) (rate(z_courier_traffic_policy_selection_total[5m]))
sum by (instance, mode, policy, key_scope, result) (rate(z_courier_traffic_policy_quota_store_total[5m]))
histogram_quantile(0.99, sum by (instance, mode, policy, key_scope, result, le) (rate(z_courier_traffic_policy_quota_store_duration_seconds_bucket[5m])))
100 * max by (instance) (z_courier_traffic_policy_local_keys{mode="local"}) / clamp_min(max by (instance) (z_courier_traffic_policy_local_key_limit{mode="local"}), 1)
max by (instance) (z_courier_route_generation)
sum by (instance, trigger, result) (rate(z_courier_route_reload_total[5m]))
z_courier:route_reload:p95_seconds
z_courier:route_retirement_age_seconds
max by (instance) (z_courier_route_retirement_timeout_seconds)
sum by (route, target_type) (z_courier_upstream_route_degraded)
sum by (route, discovery_type, result) (rate(z_courier_upstream_discovery_refresh_total[5m]))
histogram_quantile(0.95, sum by (le, route, discovery_type, result) (rate(z_courier_upstream_discovery_refresh_duration_seconds_bucket[5m])))
max by (route, discovery_type) (z_courier_upstream_discovery_resolved_endpoints)
sum by (route, discovery_type, result) (rate(z_courier_upstream_endpoint_selection_total[1m]))
sum by (route, discovery_type) (rate(z_courier_upstream_endpoint_cooldown_skipped_total[1m]))
max by (route, discovery_type) (z_courier_upstream_endpoint_unhealthy)
sum by (route, discovery_type, failure_class) (rate(z_courier_upstream_endpoint_failure_total[1m]))
histogram_quantile(0.95, sum by (le, route, discovery_type, result) (rate(z_courier_upstream_discovery_attempts_bucket[5m])))
sum by (route, discovery_type, decision) (rate(z_courier_upstream_failover_total[1m]))
sum by (path) (z_courier_internal_http_inflight)
sum by (path) (rate(z_courier_internal_http_overload_rejected_total[1m]))
sum by (result) (rate(z_courier_downlink_push_total[1m]))
sum by (result) (rate(z_courier_downlink_ack_total[1m]))
histogram_quantile(0.95, sum by (le, msg_id) (rate(z_courier_downlink_ack_latency_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_requeue_total[1m]))
sum by (result) (rate(z_courier_downlink_discard_total[1m]))
sum by (action, result) (rate(z_courier_admin_action_total[1m]))
sum by (status, result) (rate(z_courier_downlink_cleanup_total[1m]))
sum by (status) (rate(z_courier_downlink_cleanup_deleted_total[1m]))
histogram_quantile(0.95, sum by (le) (rate(z_courier_downlink_cleanup_duration_seconds_bucket[5m])))
sum by (result) (rate(z_courier_cluster_registry_lookup_total[1m]))
sum by (target_node, result) (rate(z_courier_cluster_peer_push_total[1m]))
histogram_quantile(0.95, sum by (le, target_node) (rate(z_courier_cluster_peer_push_duration_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_retry_messages_total[1m]))
sum by (owner, result) (rate(z_courier_downlink_retry_claim_messages_total[1m]))
histogram_quantile(0.95, sum by (le) (rate(z_courier_downlink_retry_scan_duration_seconds_bucket[5m])))
sum by (reason) (rate(z_courier_cluster_stale_routes_total[1m]))
```

`z_courier_sessions_online` and `z_courier_clients_online` are emitted per
gateway instance. Use `sum(...)` for the cluster total, or the raw metric with
the `instance` label to inspect per-node distribution.

Discovery gauges and cooldown state are also emitted per gateway process.
Use the Prometheus `instance` label when comparing independently resolved DNS
snapshots or unhealthy state across gateway nodes. The discovery metrics never
use endpoint addresses, hostnames, internal URLs, raw errors, tokens, or
message identifiers as labels; route names and all result labels are bounded.

Traffic-policy local-key gauges are also process-local. Redis decision counters
still retain the gateway `instance` that observed each decision, while quota is
shared by Redis. Policy names come only from validated static configuration;
ClientID, DeviceID, token, Redis key, body, and raw error are never metric
labels.

Open Grafana:

```text
http://127.0.0.1:3000
```

Use the local credentials:

```text
admin / admin
```

The dashboard is provisioned under:

```text
Dashboards -> Z-Courier -> Z-Courier Overview
Dashboards -> Z-Courier -> Z-Courier Production Signals
```

`Z-Courier Overview` is the raw operations dashboard. `Z-Courier Production
Signals` focuses on alert-oriented signals: target health, firing alerts, auth
failure ratio, overload rejects, upstream failures, downlink push or ACK
problems, retry worker failures, stale routes, HMAC failures, JWKS refresh
failures, peer push latency, discovery endpoint availability, and active
discovery/failover problems.

The Overview dashboard includes discovery endpoint counts, DNS refresh
outcomes and latency, endpoint selection and cooldown skips, forward attempt
quantiles, classified endpoint failures, and terminal failover decisions. The
Production Signals dashboard keeps the smaller incident view: resolved versus
unhealthy endpoints and only refresh, selection, endpoint, or failover problem
rates.

Both dashboards also include traffic-policy panels. Overview shows selection
outcomes, every quota decision, Store p95/p99 latency, and local live keys
against their configured limit. Production Signals focuses on policy
rejections, Store p99 latency, local-key utilization, and no-match traffic.

Both dashboards include route-control panels. Overview shows the active and
retiring generations, age since the last successful activation, reload
outcomes, p95 latency, and retirement age against the configured timeout.
Production Signals focuses on failed operations, cross-gateway generation
differences, and retirement timeout safety.

## Alert Rules

Prometheus loads:

```text
deploy/monitoring/prometheus/rules/z-courier-alerts.yml
```

The rules are example production defaults. They are intentionally conservative
enough for staging and demos, but production teams should tune thresholds and
`for` windows to their traffic patterns.

The rule file includes:

- recording rules for common 5-minute rates and latency percentiles
- target-down alerts
- ingress rejection spike alerts
- auth and HMAC failure alerts
- upstream failure and overload alerts
- Redis traffic-policy Store unavailability, sustained local-key capacity,
  overload, and high rate-limited-ratio alerts
- readiness-gated empty-discovery and actively unavailable-endpoint alerts
- downlink push, ACK latency, retry, and stale-route alerts
- peer push and JWKS refresh failure alerts
- route reload failure, slow retirement, and sustained mixed-generation alerts

Alert annotations link to the production runbook for first-response actions.
`scripts/promtool_check.sh` also runs the behavior cases in
`deploy/monitoring/prometheus/tests/z-courier-alerts.test.yml`, including the
readiness and active-selection gates used by the discovery alerts.

The bundled traffic-policy defaults intentionally distinguish normal shaping
from incidents:

- `admission_unavailable` in Redis mode is critical after 2 minutes because
  fail-closed admission is rejecting traffic.
- local-key utilization at or above 80% is a warning after 10 minutes.
- sustained `overloaded` decisions are a warning after 5 minutes.
- `rate_limited` is a warning only when it exceeds 20% of one policy's
  decisions, decision traffic is above 1/sec, and both conditions persist for
  10 minutes.

Tune these examples against production baselines before routing them to paging.
See the
[traffic-policy runbook](../../docs/v5-production-runbook.md#traffic-policy-admission)
for canary, Redis outage, tuning, and rollback guidance.

## Alertmanager

The bundled Alertmanager config is:

```text
deploy/monitoring/alertmanager/alertmanager.yml
```

The default receivers are intentionally local no-op receivers. They let
Prometheus send firing alerts to Alertmanager without sending external messages.
Before using this setup for production paging, add your real notification
receiver, such as a webhook, email, Slack, PagerDuty, or a platform-specific
bridge for Feishu, DingTalk, or WeCom.

Example receiver configs are available under:

```text
deploy/monitoring/alertmanager/examples/
```

- `webhook.yml` is the most flexible option. Use it to forward Alertmanager
  payloads to your own bridge for Feishu, DingTalk, WeCom, PagerDuty, or an
  internal incident platform.
- `email.yml` shows SMTP-based email routing.
- `slack.yml` shows Slack incoming-webhook routing.

To try an example locally, replace the placeholder values and either copy the
example over `deploy/monitoring/alertmanager/alertmanager.yml` or change the
Alertmanager volume in the Compose file to point at the example file.

The local monitoring stack exposes Alertmanager at `http://127.0.0.1:9093`.
The full local dependency stack in `deploy/local/docker-compose.yml` exposes the
same Alertmanager config at `http://127.0.0.1:19093`.

## Gateway Target

Prometheus is configured to scrape:

```text
host.docker.internal:18080
```

That address is correct when Z-Courier runs directly on the host and Prometheus
runs in Docker Compose.

If Z-Courier later runs inside the same Docker Compose network, replace the
target in `prometheus/prometheus.yml` with the gateway service name, for example:

```yaml
targets:
  - gateway:18080
```

## Stop

Stop containers but keep Prometheus, Alertmanager, and Grafana data:

```bash
docker compose -f deploy/monitoring/docker-compose.yml down
```

Remove containers and local monitoring volumes:

```bash
docker compose -f deploy/monitoring/docker-compose.yml down -v
```
