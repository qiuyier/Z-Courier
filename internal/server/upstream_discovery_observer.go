package server

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/router"
)

type upstreamDiscoveryObserver struct {
	routeName     string
	discoveryType string
	runtime       *upstreamDiscoveryRuntime
	metrics       *UpstreamRuntime
}

func newUpstreamDiscoveryObserver(routeName, discoveryType string, runtime *UpstreamRuntime) *upstreamDiscoveryObserver {
	var discoveryRuntime *upstreamDiscoveryRuntime
	if runtime != nil {
		discoveryRuntime = runtime.ensureDiscovery(routeName, discoveryType)
	}
	return &upstreamDiscoveryObserver{
		routeName:     routeName,
		discoveryType: discoveryType,
		runtime:       discoveryRuntime,
		metrics:       runtime,
	}
}

func (o *upstreamDiscoveryObserver) ObserveDiscoveryRefresh(result httpforwarder.DiscoveryRefreshResult, duration time.Duration) {
	o.runtime.recordRefresh(string(result), duration)
	o.metrics.recordActiveMetrics(func() {
		metrics.RecordUpstreamDiscoveryRefresh(o.routeName, o.discoveryType, string(result), duration)
	})
}

func (o *upstreamDiscoveryObserver) SetResolvedEndpoints(count int) {
	o.runtime.setResolvedEndpoints(count)
	o.metrics.recordActiveMetrics(func() {
		metrics.SetUpstreamDiscoveryResolvedEndpoints(o.routeName, o.discoveryType, count)
	})
}

func (o *upstreamDiscoveryObserver) RecordEndpointSelection(result httpforwarder.EndpointSelectionResult) {
	o.runtime.recordSelection(string(result))
	o.metrics.recordActiveMetrics(func() {
		metrics.RecordUpstreamEndpointSelection(o.routeName, o.discoveryType, string(result))
	})
}

func (o *upstreamDiscoveryObserver) RecordCooldownSkipped(count int) {
	o.runtime.recordCooldownSkipped(count)
	o.metrics.recordActiveMetrics(func() {
		metrics.RecordUpstreamEndpointCooldownSkipped(o.routeName, o.discoveryType, count)
	})
}

func (o *upstreamDiscoveryObserver) SetUnhealthyEndpoints(count int) {
	o.runtime.setUnhealthyEndpoints(count)
	o.metrics.recordActiveMetrics(func() {
		metrics.SetUpstreamEndpointUnhealthy(o.routeName, o.discoveryType, count)
	})
}

func (o *upstreamDiscoveryObserver) RecordEndpointFailure(failureClass router.FailureClass) {
	o.runtime.recordEndpointFailure(string(failureClass))
	o.metrics.recordActiveMetrics(func() {
		metrics.RecordUpstreamEndpointFailure(o.routeName, o.discoveryType, string(failureClass))
	})
}

func (o *upstreamDiscoveryObserver) ObserveForward(attempts int, result httpforwarder.ForwardObservationResult, decision router.FailoverDecision) {
	o.runtime.recordForward(attempts, string(result), string(decision))
	o.metrics.recordActiveMetrics(func() {
		metrics.ObserveUpstreamDiscoveryAttempts(o.routeName, o.discoveryType, string(result), attempts)
		if decision != "" {
			metrics.RecordUpstreamFailoverDecision(o.routeName, o.discoveryType, string(decision))
		}
	})
}
