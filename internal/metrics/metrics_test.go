package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestUpstreamDiscoveryMetrics(t *testing.T) {
	const (
		route         = "metrics-test-discovery-route"
		discoveryType = "dns"
	)

	RecordUpstreamDiscoveryRefresh(route, discoveryType, "success", 25*time.Millisecond)
	if got := gatheredScalar(t, "z_courier_upstream_discovery_refresh_total", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
		"result":         "success",
	}); got != 1 {
		t.Fatalf("discovery refresh counter = %v, want 1", got)
	}

	SetUpstreamDiscoveryResolvedEndpoints(route, discoveryType, 3)
	if got := gatheredScalar(t, "z_courier_upstream_discovery_resolved_endpoints", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
	}); got != 3 {
		t.Fatalf("resolved endpoints gauge = %v, want 3", got)
	}
	SetUpstreamDiscoveryResolvedEndpoints(route, discoveryType, -1)
	if got := gatheredScalar(t, "z_courier_upstream_discovery_resolved_endpoints", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
	}); got != 0 {
		t.Fatalf("negative resolved endpoints gauge = %v, want 0", got)
	}

	RecordUpstreamEndpointSelection(route, discoveryType, "selected")
	if got := gatheredScalar(t, "z_courier_upstream_endpoint_selection_total", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
		"result":         "selected",
	}); got != 1 {
		t.Fatalf("endpoint selection counter = %v, want 1", got)
	}

	RecordUpstreamEndpointCooldownSkipped(route, discoveryType, 3)
	RecordUpstreamEndpointCooldownSkipped(route, discoveryType, 0)
	if got := gatheredScalar(t, "z_courier_upstream_endpoint_cooldown_skipped_total", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
	}); got != 3 {
		t.Fatalf("cooldown skipped counter = %v, want 3", got)
	}

	SetUpstreamEndpointUnhealthy(route, discoveryType, 2)
	if got := gatheredScalar(t, "z_courier_upstream_endpoint_unhealthy", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
	}); got != 2 {
		t.Fatalf("unhealthy endpoints gauge = %v, want 2", got)
	}

	RecordUpstreamEndpointFailure(route, discoveryType, "transport")
	if got := gatheredScalar(t, "z_courier_upstream_endpoint_failure_total", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
		"failure_class":  "transport",
	}); got != 1 {
		t.Fatalf("endpoint failure counter = %v, want 1", got)
	}

	ObserveUpstreamDiscoveryAttempts(route, discoveryType, "success", 2)
	count, sum := gatheredHistogram(t, "z_courier_upstream_discovery_attempts", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
		"result":         "success",
	})
	if count != 1 || sum != 2 {
		t.Fatalf("discovery attempts histogram count=%d sum=%v, want count=1 sum=2", count, sum)
	}

	RecordUpstreamFailoverDecision(route, discoveryType, "succeeded")
	if got := gatheredScalar(t, "z_courier_upstream_failover_total", map[string]string{
		"route":          route,
		"discovery_type": discoveryType,
		"decision":       "succeeded",
	}); got != 1 {
		t.Fatalf("failover counter = %v, want 1", got)
	}
}

func TestTrafficPolicyMetrics(t *testing.T) {
	const policy = "metrics-test-traffic-policy"

	RecordTrafficPolicySelection("redis", policy, "selected")
	if got := gatheredScalar(t, "z_courier_traffic_policy_selection_total", map[string]string{
		"mode":   "redis",
		"policy": policy,
		"result": "selected",
	}); got != 1 {
		t.Fatalf("traffic policy selection counter = %v, want 1", got)
	}

	RecordTrafficPolicyQuotaStore(
		"redis",
		policy,
		"client_id",
		"rate_limited",
		25*time.Millisecond,
	)
	quotaLabels := map[string]string{
		"mode":      "redis",
		"policy":    policy,
		"key_scope": "client_id",
		"result":    "rate_limited",
	}
	if got := gatheredScalar(t, "z_courier_traffic_policy_quota_store_total", quotaLabels); got != 1 {
		t.Fatalf("traffic policy quota store counter = %v, want 1", got)
	}
	count, sum := gatheredHistogram(
		t,
		"z_courier_traffic_policy_quota_store_duration_seconds",
		quotaLabels,
	)
	if count != 1 || sum != 0.025 {
		t.Fatalf("traffic policy quota duration count=%d sum=%v, want count=1 sum=0.025", count, sum)
	}

	SetTrafficPolicyLocalKeyLimit(100)
	SetTrafficPolicyLocalKeys(7)
	if got := gatheredScalar(t, "z_courier_traffic_policy_local_key_limit", map[string]string{
		"mode": "local",
	}); got != 100 {
		t.Fatalf("traffic policy local key limit = %v, want 100", got)
	}
	if got := gatheredScalar(t, "z_courier_traffic_policy_local_keys", map[string]string{
		"mode": "local",
	}); got != 7 {
		t.Fatalf("traffic policy local keys = %v, want 7", got)
	}
}

func TestTrafficPolicyMetricsBoundDynamicLabels(t *testing.T) {
	const policy = "metrics-test-bounded-policy"

	RecordTrafficPolicySelection("tenant-controlled-mode", "", "tenant-controlled-result")
	if got := gatheredScalar(t, "z_courier_traffic_policy_selection_total", map[string]string{
		"mode":   "unknown",
		"policy": "none",
		"result": "unknown",
	}); got != 1 {
		t.Fatalf("bounded traffic policy selection counter = %v, want 1", got)
	}

	RecordTrafficPolicyQuotaStore(
		"tenant-controlled-mode",
		policy,
		"tenant-controlled-scope",
		"tenant-controlled-error",
		-1,
	)
	if got := gatheredScalar(t, "z_courier_traffic_policy_quota_store_total", map[string]string{
		"mode":      "unknown",
		"policy":    policy,
		"key_scope": "unknown",
		"result":    "unknown",
	}); got != 1 {
		t.Fatalf("bounded traffic policy quota store counter = %v, want 1", got)
	}
}

func TestRouteReloadMetricsUseBoundedLabelsAndTrackGeneration(t *testing.T) {
	completedAt := time.Unix(1_800_000_000, 0)
	RecordRouteReload("tenant-trigger", "tenant-result", 25*time.Millisecond, completedAt)
	if got := gatheredScalar(t, "z_courier_route_reload_total", map[string]string{
		"trigger": "unknown",
		"result":  "unknown",
	}); got != 1 {
		t.Fatalf("bounded route reload counter = %v, want 1", got)
	}
	count, sum := gatheredHistogram(t, "z_courier_route_reload_duration_seconds", map[string]string{
		"result": "unknown",
	})
	if count != 1 || sum != 0.025 {
		t.Fatalf("route reload duration count=%d sum=%v, want count=1 sum=0.025", count, sum)
	}

	RecordRouteReload("admin_api", "reloaded", time.Millisecond, completedAt)
	if got := gatheredScalar(t, "z_courier_route_reload_last_success_timestamp_seconds", nil); got != float64(completedAt.Unix()) {
		t.Fatalf("last route reload success timestamp = %v, want %d", got, completedAt.Unix())
	}

	SetRouteGeneration(7)
	SetRouteRetiringGenerations(1)
	if got := gatheredScalar(t, "z_courier_route_generation", nil); got != 7 {
		t.Fatalf("route generation = %v, want 7", got)
	}
	if got := gatheredScalar(t, "z_courier_route_retiring_generations", nil); got != 1 {
		t.Fatalf("retiring generations = %v, want 1", got)
	}

	ObserveRouteRetirementDuration(50 * time.Millisecond)
	count, sum = gatheredHistogram(t, "z_courier_route_retirement_duration_seconds", nil)
	if count != 1 || sum != 0.05 {
		t.Fatalf("route retirement duration count=%d sum=%v, want count=1 sum=0.05", count, sum)
	}

	SetRouteRetirement(completedAt, 30*time.Second)
	if got := gatheredScalar(t, "z_courier_route_retirement_started_timestamp_seconds", nil); got != float64(completedAt.Unix()) {
		t.Fatalf("route retirement started timestamp = %v, want %d", got, completedAt.Unix())
	}
	if got := gatheredScalar(t, "z_courier_route_retirement_timeout_seconds", nil); got != 30 {
		t.Fatalf("route retirement timeout = %v, want 30", got)
	}
	ClearRouteRetirement()
	if got := gatheredScalar(t, "z_courier_route_retirement_started_timestamp_seconds", nil); got != 0 {
		t.Fatalf("cleared route retirement started timestamp = %v, want 0", got)
	}
}

func TestDeleteUpstreamMutableMetricsRemovesGaugeSeries(t *testing.T) {
	const (
		route         = "metrics-cleanup-route"
		targetType    = "http"
		discoveryType = "dns"
	)
	AddUpstreamInFlight(route, targetType, 1)
	SetUpstreamRouteDegraded(route, targetType, true)
	SetUpstreamDiscoveryResolvedEndpoints(route, discoveryType, 2)
	SetUpstreamEndpointUnhealthy(route, discoveryType, 1)

	DeleteUpstreamRouteMutableMetrics(route, targetType)
	DeleteUpstreamDiscoveryMutableMetrics(route, discoveryType)

	checks := []struct {
		name   string
		labels map[string]string
	}{
		{name: "z_courier_upstream_inflight", labels: map[string]string{"route": route, "target_type": targetType}},
		{name: "z_courier_upstream_route_degraded", labels: map[string]string{"route": route, "target_type": targetType}},
		{name: "z_courier_upstream_discovery_resolved_endpoints", labels: map[string]string{"route": route, "discovery_type": discoveryType}},
		{name: "z_courier_upstream_endpoint_unhealthy", labels: map[string]string{"route": route, "discovery_type": discoveryType}},
	}
	for _, check := range checks {
		if gatheredMetricExists(t, check.name, check.labels) {
			t.Fatalf("mutable metric %s with labels %v still exists", check.name, check.labels)
		}
	}
}

func gatheredScalar(t *testing.T, metricName string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			matches := 0
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] == label.GetValue() {
					matches++
				}
			}
			if matches != len(labels) || len(metric.GetLabel()) != len(labels) {
				continue
			}
			switch {
			case metric.GetCounter() != nil:
				return metric.GetCounter().GetValue()
			case metric.GetGauge() != nil:
				return metric.GetGauge().GetValue()
			case metric.GetUntyped() != nil:
				return metric.GetUntyped().GetValue()
			default:
				t.Fatalf("%s is not a scalar metric", metricName)
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", metricName, labels)
	return 0
}

func gatheredMetricExists(t *testing.T, metricName string, labels map[string]string) bool {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			matches := 0
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] == label.GetValue() {
					matches++
				}
			}
			if matches == len(labels) && len(metric.GetLabel()) == len(labels) {
				return true
			}
		}
	}
	return false
}

func gatheredHistogram(t *testing.T, metricName string, labels map[string]string) (uint64, float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			matches := 0
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] == label.GetValue() {
					matches++
				}
			}
			if matches != len(labels) || len(metric.GetLabel()) != len(labels) {
				continue
			}
			histogram := metric.GetHistogram()
			if histogram == nil {
				t.Fatalf("%s is not a histogram", metricName)
			}
			return histogram.GetSampleCount(), histogram.GetSampleSum()
		}
	}
	t.Fatalf("histogram %s with labels %v not found", metricName, labels)
	return 0, 0
}
