package server

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/router"
)

type upstreamDiscoveryMetricsObserver struct {
	routeName     string
	discoveryType string
}

func newUpstreamDiscoveryMetricsObserver(routeName, discoveryType string) *upstreamDiscoveryMetricsObserver {
	return &upstreamDiscoveryMetricsObserver{
		routeName:     routeName,
		discoveryType: discoveryType,
	}
}

func (o *upstreamDiscoveryMetricsObserver) ObserveDiscoveryRefresh(result httpforwarder.DiscoveryRefreshResult, duration time.Duration) {
	metrics.RecordUpstreamDiscoveryRefresh(o.routeName, o.discoveryType, string(result), duration)
}

func (o *upstreamDiscoveryMetricsObserver) SetResolvedEndpoints(count int) {
	metrics.SetUpstreamDiscoveryResolvedEndpoints(o.routeName, o.discoveryType, count)
}

func (o *upstreamDiscoveryMetricsObserver) RecordEndpointSelection(result httpforwarder.EndpointSelectionResult) {
	metrics.RecordUpstreamEndpointSelection(o.routeName, o.discoveryType, string(result))
}

func (o *upstreamDiscoveryMetricsObserver) RecordCooldownSkipped(count int) {
	metrics.RecordUpstreamEndpointCooldownSkipped(o.routeName, o.discoveryType, count)
}

func (o *upstreamDiscoveryMetricsObserver) SetUnhealthyEndpoints(count int) {
	metrics.SetUpstreamEndpointUnhealthy(o.routeName, o.discoveryType, count)
}

func (o *upstreamDiscoveryMetricsObserver) RecordEndpointFailure(failureClass router.FailureClass) {
	metrics.RecordUpstreamEndpointFailure(o.routeName, o.discoveryType, string(failureClass))
}

func (o *upstreamDiscoveryMetricsObserver) ObserveForward(attempts int, result httpforwarder.ForwardObservationResult, decision router.FailoverDecision) {
	metrics.ObserveUpstreamDiscoveryAttempts(o.routeName, o.discoveryType, string(result), attempts)
	if decision != "" {
		metrics.RecordUpstreamFailoverDecision(o.routeName, o.discoveryType, string(decision))
	}
}
