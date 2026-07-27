package server

import (
	"sync"
	"time"
)

type upstreamDiscoveryRuntime struct {
	mu sync.RWMutex

	discoveryType string
	now           func() time.Time

	resolvedEndpoints        int
	unhealthyEndpoints       int
	lastRefreshResult        string
	lastRefreshDuration      time.Duration
	lastRefreshAt            time.Time
	lastSelectionResult      string
	lastSelectionAt          time.Time
	cooldownSkippedTotal     uint64
	lastCooldownSkippedAt    time.Time
	lastEndpointFailureClass string
	lastEndpointFailureAt    time.Time
	lastForwardResult        string
	lastForwardAttempts      int
	lastForwardAt            time.Time
	lastFailoverDecision     string
	lastFailoverDecisionAt   time.Time
}

func newUpstreamDiscoveryRuntime(discoveryType string, now func() time.Time) *upstreamDiscoveryRuntime {
	if now == nil {
		now = time.Now
	}
	return &upstreamDiscoveryRuntime{
		discoveryType: discoveryType,
		now:           now,
	}
}

func (r *upstreamDiscoveryRuntime) setResolvedEndpoints(count int) {
	if r == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	r.mu.Lock()
	r.resolvedEndpoints = count
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) recordRefresh(result string, duration time.Duration) {
	if r == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	now := r.now()
	r.mu.Lock()
	r.lastRefreshResult = result
	r.lastRefreshDuration = duration
	r.lastRefreshAt = now
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) recordSelection(result string) {
	if r == nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	r.lastSelectionResult = result
	r.lastSelectionAt = now
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) recordCooldownSkipped(count int) {
	if r == nil || count <= 0 {
		return
	}
	now := r.now()
	r.mu.Lock()
	r.cooldownSkippedTotal += uint64(count)
	r.lastCooldownSkippedAt = now
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) setUnhealthyEndpoints(count int) {
	if r == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	r.mu.Lock()
	r.unhealthyEndpoints = count
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) recordEndpointFailure(failureClass string) {
	if r == nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	r.lastEndpointFailureClass = failureClass
	r.lastEndpointFailureAt = now
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) recordForward(attempts int, result, decision string) {
	if r == nil {
		return
	}
	if attempts < 0 {
		attempts = 0
	}
	now := r.now()
	r.mu.Lock()
	r.lastForwardAttempts = attempts
	r.lastForwardResult = result
	r.lastForwardAt = now
	if decision != "" {
		r.lastFailoverDecision = decision
		r.lastFailoverDecisionAt = now
	}
	r.mu.Unlock()
}

func (r *upstreamDiscoveryRuntime) snapshot() upstreamDiscoveryRuntimeSnapshot {
	if r == nil {
		return upstreamDiscoveryRuntimeSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return upstreamDiscoveryRuntimeSnapshot{
		Type:                     r.discoveryType,
		ResolvedEndpoints:        r.resolvedEndpoints,
		UnhealthyEndpoints:       r.unhealthyEndpoints,
		LastRefreshResult:        r.lastRefreshResult,
		LastRefreshDuration:      r.lastRefreshDuration,
		LastRefreshAt:            r.lastRefreshAt,
		LastSelectionResult:      r.lastSelectionResult,
		LastSelectionAt:          r.lastSelectionAt,
		CooldownSkippedTotal:     r.cooldownSkippedTotal,
		LastCooldownSkippedAt:    r.lastCooldownSkippedAt,
		LastEndpointFailureClass: r.lastEndpointFailureClass,
		LastEndpointFailureAt:    r.lastEndpointFailureAt,
		LastForwardResult:        r.lastForwardResult,
		LastForwardAttempts:      r.lastForwardAttempts,
		LastForwardAt:            r.lastForwardAt,
		LastFailoverDecision:     r.lastFailoverDecision,
		LastFailoverDecisionAt:   r.lastFailoverDecisionAt,
	}
}

type upstreamDiscoveryRuntimeSnapshot struct {
	Type                     string
	ResolvedEndpoints        int
	UnhealthyEndpoints       int
	LastRefreshResult        string
	LastRefreshDuration      time.Duration
	LastRefreshAt            time.Time
	LastSelectionResult      string
	LastSelectionAt          time.Time
	CooldownSkippedTotal     uint64
	LastCooldownSkippedAt    time.Time
	LastEndpointFailureClass string
	LastEndpointFailureAt    time.Time
	LastForwardResult        string
	LastForwardAttempts      int
	LastForwardAt            time.Time
	LastFailoverDecision     string
	LastFailoverDecisionAt   time.Time
}
