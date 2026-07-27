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
	}
}

func (o *upstreamDiscoveryObserver) ObserveDiscoveryRefresh(result httpforwarder.DiscoveryRefreshResult, duration time.Duration) {
	metrics.RecordUpstreamDiscoveryRefresh(o.routeName, o.discoveryType, string(result), duration)
	o.runtime.recordRefresh(string(result), duration)
}

func (o *upstreamDiscoveryObserver) SetResolvedEndpoints(count int) {
	metrics.SetUpstreamDiscoveryResolvedEndpoints(o.routeName, o.discoveryType, count)
	o.runtime.setResolvedEndpoints(count)
}

func (o *upstreamDiscoveryObserver) RecordEndpointSelection(result httpforwarder.EndpointSelectionResult) {
	metrics.RecordUpstreamEndpointSelection(o.routeName, o.discoveryType, string(result))
	o.runtime.recordSelection(string(result))
}

func (o *upstreamDiscoveryObserver) RecordCooldownSkipped(count int) {
	metrics.RecordUpstreamEndpointCooldownSkipped(o.routeName, o.discoveryType, count)
	o.runtime.recordCooldownSkipped(count)
}

func (o *upstreamDiscoveryObserver) SetUnhealthyEndpoints(count int) {
	metrics.SetUpstreamEndpointUnhealthy(o.routeName, o.discoveryType, count)
	o.runtime.setUnhealthyEndpoints(count)
}

func (o *upstreamDiscoveryObserver) RecordEndpointFailure(failureClass router.FailureClass) {
	metrics.RecordUpstreamEndpointFailure(o.routeName, o.discoveryType, string(failureClass))
	o.runtime.recordEndpointFailure(string(failureClass))
}

func (o *upstreamDiscoveryObserver) ObserveForward(attempts int, result httpforwarder.ForwardObservationResult, decision router.FailoverDecision) {
	metrics.ObserveUpstreamDiscoveryAttempts(o.routeName, o.discoveryType, string(result), attempts)
	if decision != "" {
		metrics.RecordUpstreamFailoverDecision(o.routeName, o.discoveryType, string(decision))
	}
	o.runtime.recordForward(attempts, string(result), string(decision))
}
