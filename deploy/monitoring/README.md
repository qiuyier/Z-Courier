# Z-Courier Monitoring

This directory contains a local Prometheus + Grafana stack for Z-Courier
development and demos.

## What Runs

- Prometheus scrapes Z-Courier metrics from the gateway internal HTTP server.
- Grafana loads the Prometheus data source and the `Z-Courier Overview`
  dashboard automatically.
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

Example PromQL queries:

```promql
sum(z_courier_sessions_online)
z_courier_sessions_online
sum(z_courier_clients_online)
sum by (result) (rate(z_courier_ingress_packets_total[1m]))
sum by (route, target_type, result) (rate(z_courier_upstream_forward_total[1m]))
histogram_quantile(0.95, sum by (le, route, target_type) (rate(z_courier_upstream_forward_duration_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_push_total[1m]))
sum by (result) (rate(z_courier_downlink_ack_total[1m]))
histogram_quantile(0.95, sum by (le, msg_id) (rate(z_courier_downlink_ack_latency_seconds_bucket[5m])))
sum by (result) (rate(z_courier_downlink_requeue_total[1m]))
sum by (result) (rate(z_courier_downlink_discard_total[1m]))
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
```

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

Stop containers but keep Prometheus and Grafana data:

```bash
docker compose -f deploy/monitoring/docker-compose.yml down
```

Remove containers and local monitoring volumes:

```bash
docker compose -f deploy/monitoring/docker-compose.yml down -v
```
