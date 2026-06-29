package resilience

import (
	"testing"
	"time"
)

func TestDependencyTrackerTransitions(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	tracker := NewDependencyTracker(DependencyTrackerConfig{
		Name:                 "http_upstream:chat",
		DegradedThreshold:    2,
		UnavailableThreshold: 4,
		Now:                  func() time.Time { return now },
	})

	if got := tracker.Snapshot(); got.Status != DependencyStatusHealthy || got.ConsecutiveFailures != 0 {
		t.Fatalf("initial snapshot = %+v, want healthy", got)
	}

	if got := tracker.MarkFailure("request_failed"); got.Status != DependencyStatusHealthy || got.ConsecutiveFailures != 1 || got.LastReason != "request_failed" {
		t.Fatalf("first failure snapshot = %+v, want healthy with one failure", got)
	}
	if got := tracker.MarkFailure("request_failed"); got.Status != DependencyStatusDegraded || got.ConsecutiveFailures != 2 {
		t.Fatalf("second failure snapshot = %+v, want degraded", got)
	}
	tracker.MarkFailure("request_failed")
	if got := tracker.MarkFailure("request_failed"); got.Status != DependencyStatusUnavailable || got.ConsecutiveFailures != 4 {
		t.Fatalf("fourth failure snapshot = %+v, want unavailable", got)
	}

	if got := tracker.MarkSuccess(); got.Status != DependencyStatusHealthy || got.ConsecutiveFailures != 0 || got.LastReason != "" {
		t.Fatalf("success snapshot = %+v, want recovered healthy", got)
	}
}
