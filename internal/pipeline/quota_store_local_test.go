package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalQuotaStoreBurstAndRefill(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	store := NewLocalQuotaStore(LocalQuotaStoreConfig{
		MaxKeys: 10,
		IdleTTL: time.Minute,
		Now:     func() time.Time { return now },
	})
	request := localQuotaRequest("standard", "client-a", TokenBucketConfig{
		Capacity:       2,
		RefillTokens:   2,
		RefillInterval: time.Second,
	})

	assertQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertQuotaDecision(t, store, request, QuotaDecisionRateLimited)

	now = now.Add(500 * time.Millisecond)
	assertQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertQuotaDecision(t, store, request, QuotaDecisionRateLimited)
}

func TestLocalQuotaStoreBoundsKeysAndExpiresIdleBuckets(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	store := NewLocalQuotaStore(LocalQuotaStoreConfig{
		MaxKeys: 1,
		IdleTTL: time.Second,
		Now:     func() time.Time { return now },
	})
	bucket := TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	}

	assertQuotaDecision(t, store, localQuotaRequest("standard", "client-a", bucket), QuotaDecisionAllowed)
	assertQuotaDecision(t, store, localQuotaRequest("standard", "client-b", bucket), QuotaDecisionOverloaded)
	if got := store.bucketCount(); got != 1 {
		t.Fatalf("bucketCount() = %d, want 1", got)
	}

	now = now.Add(time.Second)
	assertQuotaDecision(t, store, localQuotaRequest("standard", "client-b", bucket), QuotaDecisionAllowed)
	if got := store.bucketCount(); got != 1 {
		t.Fatalf("bucketCount() after eviction = %d, want 1", got)
	}
}

func TestLocalQuotaStorePartitionsPolicyAndScope(t *testing.T) {
	store := NewLocalQuotaStore(LocalQuotaStoreConfig{
		MaxKeys: 3,
		IdleTTL: time.Minute,
	})
	bucket := TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	}

	assertQuotaDecision(t, store, localQuotaRequest("standard", "client-a", bucket), QuotaDecisionAllowed)
	assertQuotaDecision(t, store, localQuotaRequest("priority", "client-a", bucket), QuotaDecisionAllowed)
	request := localQuotaRequest("standard", "client-a", bucket)
	request.KeyScope = "future_scope"
	assertQuotaDecision(t, store, request, QuotaDecisionAllowed)
	if got := store.bucketCount(); got != 3 {
		t.Fatalf("bucketCount() = %d, want 3", got)
	}
}

func TestLocalQuotaStoreConcurrentAdmission(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	store := NewLocalQuotaStore(LocalQuotaStoreConfig{
		MaxKeys: 10,
		IdleTTL: time.Minute,
		Now:     func() time.Time { return now },
	})
	request := localQuotaRequest("standard", "client-a", TokenBucketConfig{
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	})

	var accepted atomic.Int64
	var waitGroup sync.WaitGroup
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			decision, err := store.Admit(context.Background(), request)
			if err == nil && decision == QuotaDecisionAllowed {
				accepted.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := accepted.Load(); got != 10 {
		t.Fatalf("accepted = %d, want 10", got)
	}
}

func TestLocalQuotaStoreUnavailableRequests(t *testing.T) {
	bucket := TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	}
	request := localQuotaRequest("standard", "client-a", bucket)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewLocalQuotaStore(LocalQuotaStoreConfig{MaxKeys: 1, IdleTTL: time.Minute})
	decision, err := store.Admit(ctx, request)
	if decision != QuotaDecisionAdmissionUnavailable || !errors.Is(err, context.Canceled) {
		t.Fatalf("Admit(canceled) = %q, %v", decision, err)
	}

	store = NewLocalQuotaStore(LocalQuotaStoreConfig{MaxKeys: 0, IdleTTL: time.Minute})
	decision, err = store.Admit(context.Background(), request)
	if decision != QuotaDecisionAdmissionUnavailable || err == nil {
		t.Fatalf("Admit(invalid config) = %q, %v", decision, err)
	}

	store = NewLocalQuotaStore(LocalQuotaStoreConfig{MaxKeys: 1, IdleTTL: time.Minute})
	request.KeyValue = ""
	decision, err = store.Admit(context.Background(), request)
	if decision != QuotaDecisionAdmissionUnavailable || err == nil {
		t.Fatalf("Admit(invalid request) = %q, %v", decision, err)
	}
}

func localQuotaRequest(policyName, clientID string, bucket TokenBucketConfig) QuotaRequest {
	return QuotaRequest{
		PolicyName:  policyName,
		KeyScope:    TrafficPolicyKeyClientID,
		KeyValue:    clientID,
		TokenBucket: bucket,
	}
}

func assertQuotaDecision(
	t *testing.T,
	store QuotaStore,
	request QuotaRequest,
	want QuotaDecision,
) {
	t.Helper()
	got, err := store.Admit(context.Background(), request)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if got != want {
		t.Fatalf("Admit() = %q, want %q", got, want)
	}
}
