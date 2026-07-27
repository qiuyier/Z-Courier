package server

import (
	"sync"
	"testing"
	"time"
)

func TestUpstreamDiscoveryRuntimeSnapshot(t *testing.T) {
	now := time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC)
	runtime := newUpstreamDiscoveryRuntime("dns", func() time.Time {
		return now
	})

	runtime.setResolvedEndpoints(-1)
	runtime.setUnhealthyEndpoints(-1)
	runtime.recordRefresh("error", -time.Second)

	now = now.Add(time.Second)
	runtime.setResolvedEndpoints(2)
	runtime.setUnhealthyEndpoints(1)
	runtime.recordSelection("selected")
	runtime.recordCooldownSkipped(2)
	runtime.recordEndpointFailure("transport")
	runtime.recordForward(2, "success", "succeeded")
	failoverAt := now

	now = now.Add(time.Second)
	runtime.recordForward(1, "success", "")

	snapshot := runtime.snapshot()
	if snapshot.Type != "dns" || snapshot.ResolvedEndpoints != 2 || snapshot.UnhealthyEndpoints != 1 {
		t.Fatalf("endpoint snapshot = %+v", snapshot)
	}
	if snapshot.LastRefreshResult != "error" || snapshot.LastRefreshDuration != 0 ||
		snapshot.LastRefreshAt != now.Add(-2*time.Second) {
		t.Fatalf("refresh snapshot = %+v", snapshot)
	}
	if snapshot.LastSelectionResult != "selected" || snapshot.LastSelectionAt != failoverAt ||
		snapshot.CooldownSkippedTotal != 2 || snapshot.LastCooldownSkippedAt != failoverAt {
		t.Fatalf("selection snapshot = %+v", snapshot)
	}
	if snapshot.LastEndpointFailureClass != "transport" || snapshot.LastEndpointFailureAt != failoverAt {
		t.Fatalf("failure snapshot = %+v", snapshot)
	}
	if snapshot.LastForwardResult != "success" || snapshot.LastForwardAttempts != 1 ||
		snapshot.LastForwardAt != now {
		t.Fatalf("forward snapshot = %+v", snapshot)
	}
	if snapshot.LastFailoverDecision != "succeeded" || snapshot.LastFailoverDecisionAt != failoverAt {
		t.Fatalf("failover snapshot = %+v", snapshot)
	}
}

func TestUpstreamDiscoveryRuntimeConcurrentUpdatesAndSnapshots(t *testing.T) {
	runtime := newUpstreamDiscoveryRuntime("static", nil)

	var waitGroup sync.WaitGroup
	for index := range 100 {
		waitGroup.Go(func() {
			runtime.setResolvedEndpoints(index % 4)
			runtime.setUnhealthyEndpoints(index % 2)
			runtime.recordSelection("selected")
			runtime.recordCooldownSkipped(1)
			runtime.recordEndpointFailure("transport")
			runtime.recordForward(2, "success", "succeeded")
			_ = runtime.snapshot()
		})
	}
	waitGroup.Wait()

	snapshot := runtime.snapshot()
	if snapshot.Type != "static" || snapshot.CooldownSkippedTotal != 100 {
		t.Fatalf("snapshot = %+v, want static with 100 cooldown skips", snapshot)
	}
}

func TestUpstreamRuntimeInitialDiscoveryDiagnosticsOmitUnobservedEvents(t *testing.T) {
	runtime := newUpstreamRuntime([]UpstreamRouteConfig{{
		Name: "orders",
		HTTP: &HTTPUpstreamConfig{
			Discovery: HTTPUpstreamDiscoveryConfig{Type: "static"},
		},
	}})
	snapshot, ok := runtime.snapshot("orders")
	if !ok || snapshot.Discovery == nil {
		t.Fatalf("snapshot = %+v, want initialized discovery state", snapshot)
	}

	diagnostics := adminUpstreamRouteRuntimeFromSnapshot(snapshot)
	discovery := diagnostics.Discovery
	if discovery == nil || discovery.Type != "static" ||
		discovery.ResolvedEndpoints != 0 || discovery.UnhealthyEndpoints != 0 ||
		discovery.CooldownSkippedTotal != 0 {
		t.Fatalf("initial discovery diagnostics = %+v", discovery)
	}
	if discovery.LastRefreshAt != nil || discovery.LastSelectionAt != nil ||
		discovery.LastCooldownSkippedAt != nil || discovery.LastEndpointFailureAt != nil ||
		discovery.LastForwardAttempts != nil || discovery.LastForwardAt != nil ||
		discovery.LastFailoverAt != nil {
		t.Fatalf("initial discovery diagnostics contains unobserved timestamps: %+v", discovery)
	}

	runtime.ensureDiscovery("orders", "static").recordForward(0, "failure", "no_alternate")
	snapshot, _ = runtime.snapshot("orders")
	discovery = adminUpstreamRouteRuntimeFromSnapshot(snapshot).Discovery
	if discovery.LastForwardAttempts == nil || *discovery.LastForwardAttempts != 0 {
		t.Fatalf("zero-attempt forward diagnostics = %+v, want explicit 0", discovery)
	}
}
