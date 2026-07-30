package pipeline

import (
	"sync"
	"testing"
	"time"
)

func TestTrafficPolicyRuntimeTracksBoundedOutcomes(t *testing.T) {
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	runtime := newTrafficPolicyRuntime(TrafficPoliciesConfig{
		Mode:    TrafficPolicyModeLocal,
		MaxKeys: 20,
		Policies: []TrafficPolicy{
			{Name: "standard"},
			{Name: "priority"},
		},
	}, func() time.Time { return now })

	runtime.recordNoMatch()
	runtime.recordSelection("standard")
	runtime.recordSelection("tenant-controlled-unknown-policy")
	runtime.recordDecision("standard", QuotaDecisionAllowed)
	now = now.Add(time.Second)
	runtime.recordDecision("standard", QuotaDecisionRateLimited)
	runtime.recordDecision("tenant-controlled-unknown-policy", QuotaDecisionAllowed)
	runtime.setLocalKeyState(7, 20)

	snapshot := runtime.Snapshot()
	if snapshot.Mode != TrafficPolicyModeLocal ||
		snapshot.NoMatchTotal != 1 ||
		snapshot.LocalKeys != 7 ||
		snapshot.LocalKeyLimit != 20 {
		t.Fatalf("runtime snapshot = %+v", snapshot)
	}
	if snapshot.Decisions.Allowed != 1 ||
		snapshot.Decisions.RateLimited != 1 ||
		snapshot.Decisions.Overloaded != 0 ||
		snapshot.Decisions.AdmissionUnavailable != 0 {
		t.Fatalf("decision totals = %+v", snapshot.Decisions)
	}
	if snapshot.LastResult != QuotaDecisionRateLimited ||
		snapshot.LastState != TrafficPolicyBucketStateDepleted ||
		!snapshot.LastDecisionAt.Equal(now) ||
		!snapshot.LastSuccessAt.Equal(now) ||
		!snapshot.LastUnavailableAt.IsZero() {
		t.Fatalf("last decision = %+v", snapshot)
	}
	if len(snapshot.Policies) != 2 ||
		snapshot.Policies[0].Name != "priority" ||
		snapshot.Policies[1].Name != "standard" {
		t.Fatalf("policy snapshots = %+v, want sorted configured policies", snapshot.Policies)
	}
	standard := snapshot.Policies[1]
	if standard.SelectionTotal != 1 ||
		standard.Decisions.Allowed != 1 ||
		standard.Decisions.RateLimited != 1 ||
		standard.LastResult != QuotaDecisionRateLimited ||
		standard.LastState != TrafficPolicyBucketStateDepleted ||
		!standard.LastDecisionAt.Equal(now) {
		t.Fatalf("standard policy snapshot = %+v", standard)
	}
}

func TestTrafficPolicyRuntimeNormalizesUnavailableAndReturnsCopies(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	runtime := newTrafficPolicyRuntime(TrafficPoliciesConfig{
		Mode:     TrafficPolicyModeRedis,
		Policies: []TrafficPolicy{{Name: "shared"}},
	}, func() time.Time { return now })

	runtime.recordSelection("shared")
	runtime.recordDecision("shared", QuotaDecision("tenant-controlled-result"))

	first := runtime.Snapshot()
	if first.LastResult != QuotaDecisionAdmissionUnavailable ||
		first.LastState != TrafficPolicyBucketStateStoreUnavailable ||
		first.Decisions.AdmissionUnavailable != 1 ||
		!first.LastUnavailableAt.Equal(now) ||
		!first.LastSuccessAt.IsZero() {
		t.Fatalf("unavailable snapshot = %+v", first)
	}
	first.Policies[0].Name = "mutated"
	second := runtime.Snapshot()
	if second.Policies[0].Name != "shared" {
		t.Fatalf("snapshot mutation changed runtime policy name to %q", second.Policies[0].Name)
	}
}

func TestTrafficPolicyRuntimeConcurrentRecording(t *testing.T) {
	runtime := newTrafficPolicyRuntime(TrafficPoliciesConfig{
		Mode:     TrafficPolicyModeRedis,
		Policies: []TrafficPolicy{{Name: "shared"}},
	}, nil)

	const workers = 64
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for index := range workers {
		go func(index int) {
			defer waitGroup.Done()
			runtime.recordSelection("shared")
			runtime.recordDecision("shared", QuotaDecisionAllowed)
			runtime.setLocalKeyState(index, workers)
			_ = runtime.Snapshot()
		}(index)
	}
	waitGroup.Wait()

	snapshot := runtime.Snapshot()
	if snapshot.Policies[0].SelectionTotal != workers ||
		snapshot.Decisions.Allowed != workers {
		t.Fatalf("concurrent snapshot = %+v", snapshot)
	}
	if snapshot.LocalKeys != 0 || snapshot.LocalKeyLimit != 0 {
		t.Fatalf("redis runtime accepted local key state: %+v", snapshot)
	}
}
