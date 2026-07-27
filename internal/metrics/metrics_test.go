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
