package httpforwarder

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/router"
)

type DiscoveryRefreshResult string

const (
	DiscoveryRefreshSuccess DiscoveryRefreshResult = "success"
	DiscoveryRefreshError   DiscoveryRefreshResult = "error"
	DiscoveryRefreshEmpty   DiscoveryRefreshResult = "empty"
)

type EndpointSelectionResult string

const (
	EndpointSelectionSelected      EndpointSelectionResult = "selected"
	EndpointSelectionResolverError EndpointSelectionResult = "resolver_error"
	EndpointSelectionNoAvailable   EndpointSelectionResult = "no_available"
)

type ForwardObservationResult string

const (
	ForwardObservationSuccess ForwardObservationResult = "success"
	ForwardObservationFailure ForwardObservationResult = "failure"
)

type Observer interface {
	ObserveDiscoveryRefresh(DiscoveryRefreshResult, time.Duration)
	SetResolvedEndpoints(int)
	RecordEndpointSelection(EndpointSelectionResult)
	RecordCooldownSkipped(int)
	SetUnhealthyEndpoints(int)
	RecordEndpointFailure(router.FailureClass)
	ObserveForward(int, ForwardObservationResult, router.FailoverDecision)
}
