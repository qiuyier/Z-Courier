package httpforwarder

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func TestDNSResolverObserverTracksRefreshAndLastKnownGood(t *testing.T) {
	lookup := &mutableHostLookup{addresses: []string{"127.0.0.1"}}
	observer := &recordingObserver{}
	resolver, err := NewDNSResolver(DNSResolverConfig{
		Scheme:          "http",
		Hostname:        "backend.internal",
		Port:            8080,
		Path:            "/gateway/upstream",
		RefreshInterval: time.Hour,
		LookupTimeout:   time.Second,
		Lookup:          lookup,
		Observer:        observer,
	})
	if err != nil {
		t.Fatalf("NewDNSResolver() error = %v", err)
	}
	t.Cleanup(func() {
		_ = resolver.Close()
	})

	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve() initial error = %v", err)
	}
	if got := observer.lastResolvedEndpoints(); got != 1 {
		t.Fatalf("resolved endpoints after initial refresh = %d, want 1", got)
	}

	lookup.Set(nil, errors.New("temporary dns failure"))
	if err := resolver.refresh(context.Background()); err == nil {
		t.Fatal("refresh() error = nil, want lookup failure")
	}
	if got := observer.lastResolvedEndpoints(); got != 1 {
		t.Fatalf("resolved endpoints after failed refresh = %d, want last-known-good 1", got)
	}

	lookup.Set([]string{"not-an-ip"}, nil)
	if err := resolver.refresh(context.Background()); err == nil {
		t.Fatal("refresh() error = nil, want empty usable result")
	}

	lookup.Set([]string{"127.0.0.2", "127.0.0.3"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() recovery error = %v", err)
	}
	if got := observer.lastResolvedEndpoints(); got != 2 {
		t.Fatalf("resolved endpoints after recovery = %d, want 2", got)
	}

	wantRefreshes := []DiscoveryRefreshResult{
		DiscoveryRefreshSuccess,
		DiscoveryRefreshError,
		DiscoveryRefreshEmpty,
		DiscoveryRefreshSuccess,
	}
	if got := observer.refreshResults(); !equalRefreshResults(got, wantRefreshes) {
		t.Fatalf("refresh results = %v, want %v", got, wantRefreshes)
	}

	if err := resolver.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := observer.lastResolvedEndpoints(); got != 0 {
		t.Fatalf("resolved endpoints after close = %d, want 0", got)
	}
}

func TestEndpointSelectorObserverTracksCooldownState(t *testing.T) {
	observer := &recordingObserver{}
	resolver, err := NewStaticResolverWithObserver([]string{
		"http://backend-a.local",
		"http://backend-b.local",
	}, observer)
	if err != nil {
		t.Fatalf("NewStaticResolverWithObserver() error = %v", err)
	}
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	selector := newEndpointSelector(resolver, 10*time.Second, func() time.Time {
		return now
	}, observer)
	t.Cleanup(func() {
		_ = selector.Close()
	})

	first, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() first error = %v", err)
	}
	selector.MarkFailure(first)
	if got := observer.lastUnhealthyEndpoints(); got != 1 {
		t.Fatalf("unhealthy endpoints after failure = %d, want 1", got)
	}
	for range 2 {
		if _, err := selector.Select(context.Background(), nil); err != nil {
			t.Fatalf("Select() during cooldown error = %v", err)
		}
	}
	if got := observer.cooldownSkippedCount(); got != 1 {
		t.Fatalf("cooldown skipped = %d, want 1", got)
	}

	now = now.Add(11 * time.Second)
	if _, err := selector.Select(context.Background(), nil); err != nil {
		t.Fatalf("Select() after cooldown error = %v", err)
	}
	if got := observer.lastUnhealthyEndpoints(); got != 0 {
		t.Fatalf("unhealthy endpoints after cooldown = %d, want 0", got)
	}
	if got := observer.selectionCount(EndpointSelectionSelected); got != 4 {
		t.Fatalf("selected observations = %d, want 4", got)
	}
}

func TestDiscoveredForwarderObserverTracksSuccessfulFailover(t *testing.T) {
	observer := &recordingObserver{}
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:          resolver,
		MaxAttempts:       2,
		UnhealthyCooldown: time.Minute,
		Observer:          observer,
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "backend-a.local" {
				return nil, errors.New("connection refused")
			}
			return response(http.StatusNoContent, ""), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}
	t.Cleanup(func() {
		_ = forwarder.Close()
	})

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	if got := observer.failureCount(router.FailureClassTransport); got != 1 {
		t.Fatalf("transport failures = %d, want 1", got)
	}
	forwardObservations := observer.forwardObservations()
	if len(forwardObservations) != 1 {
		t.Fatalf("forward observations = %v, want one", forwardObservations)
	}
	got := forwardObservations[0]
	if got.attempts != 2 || got.result != ForwardObservationSuccess ||
		got.decision != router.FailoverDecisionSucceeded {
		t.Fatalf("forward observation = %+v", got)
	}
}

type refreshObservation struct {
	result   DiscoveryRefreshResult
	duration time.Duration
}

type forwardObservation struct {
	attempts int
	result   ForwardObservationResult
	decision router.FailoverDecision
}

type recordingObserver struct {
	mu sync.Mutex

	refreshes         []refreshObservation
	resolvedEndpoints []int
	selections        []EndpointSelectionResult
	cooldownSkipped   int
	unhealthy         []int
	failures          []router.FailureClass
	forwards          []forwardObservation
}

func (o *recordingObserver) ObserveDiscoveryRefresh(result DiscoveryRefreshResult, duration time.Duration) {
	o.mu.Lock()
	o.refreshes = append(o.refreshes, refreshObservation{result: result, duration: duration})
	o.mu.Unlock()
}

func (o *recordingObserver) SetResolvedEndpoints(count int) {
	o.mu.Lock()
	o.resolvedEndpoints = append(o.resolvedEndpoints, count)
	o.mu.Unlock()
}

func (o *recordingObserver) RecordEndpointSelection(result EndpointSelectionResult) {
	o.mu.Lock()
	o.selections = append(o.selections, result)
	o.mu.Unlock()
}

func (o *recordingObserver) RecordCooldownSkipped(count int) {
	o.mu.Lock()
	o.cooldownSkipped += count
	o.mu.Unlock()
}

func (o *recordingObserver) SetUnhealthyEndpoints(count int) {
	o.mu.Lock()
	o.unhealthy = append(o.unhealthy, count)
	o.mu.Unlock()
}

func (o *recordingObserver) RecordEndpointFailure(failureClass router.FailureClass) {
	o.mu.Lock()
	o.failures = append(o.failures, failureClass)
	o.mu.Unlock()
}

func (o *recordingObserver) ObserveForward(attempts int, result ForwardObservationResult, decision router.FailoverDecision) {
	o.mu.Lock()
	o.forwards = append(o.forwards, forwardObservation{
		attempts: attempts,
		result:   result,
		decision: decision,
	})
	o.mu.Unlock()
}

func (o *recordingObserver) lastResolvedEndpoints() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.resolvedEndpoints) == 0 {
		return -1
	}
	return o.resolvedEndpoints[len(o.resolvedEndpoints)-1]
}

func (o *recordingObserver) refreshResults() []DiscoveryRefreshResult {
	o.mu.Lock()
	defer o.mu.Unlock()
	results := make([]DiscoveryRefreshResult, 0, len(o.refreshes))
	for _, refresh := range o.refreshes {
		results = append(results, refresh.result)
	}
	return results
}

func (o *recordingObserver) lastUnhealthyEndpoints() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.unhealthy) == 0 {
		return -1
	}
	return o.unhealthy[len(o.unhealthy)-1]
}

func (o *recordingObserver) cooldownSkippedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cooldownSkipped
}

func (o *recordingObserver) selectionCount(result EndpointSelectionResult) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	for _, selection := range o.selections {
		if selection == result {
			count++
		}
	}
	return count
}

func (o *recordingObserver) failureCount(failureClass router.FailureClass) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	for _, failure := range o.failures {
		if failure == failureClass {
			count++
		}
	}
	return count
}

func (o *recordingObserver) forwardObservations() []forwardObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]forwardObservation(nil), o.forwards...)
}

func equalRefreshResults(left, right []DiscoveryRefreshResult) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
