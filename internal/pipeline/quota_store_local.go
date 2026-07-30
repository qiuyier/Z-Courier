package pipeline

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
)

type LocalQuotaStoreConfig struct {
	MaxKeys int
	IdleTTL time.Duration
	Now     func() time.Time
	Runtime *TrafficPolicyRuntime
}

type LocalQuotaStore struct {
	maxKeys int
	idleTTL time.Duration
	now     func() time.Time

	mu      sync.Mutex
	buckets map[localQuotaKey]*localQuotaBucket
	lru     *list.List
	runtime *TrafficPolicyRuntime
}

type localQuotaKey struct {
	policyName string
	keyScope   string
	keyValue   string
}

type localQuotaBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
	element    *list.Element
}

func NewLocalQuotaStore(config LocalQuotaStoreConfig) *LocalQuotaStore {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	store := &LocalQuotaStore{
		maxKeys: config.MaxKeys,
		idleTTL: config.IdleTTL,
		now:     now,
		buckets: make(map[localQuotaKey]*localQuotaBucket),
		lru:     list.New(),
		runtime: config.Runtime,
	}
	metrics.SetTrafficPolicyLocalKeyLimit(config.MaxKeys)
	metrics.SetTrafficPolicyLocalKeys(0)
	store.runtime.setLocalKeyState(0, config.MaxKeys)
	return store
}

func (s *LocalQuotaStore) Admit(ctx context.Context, request QuotaRequest) (QuotaDecision, error) {
	if s == nil {
		return QuotaDecisionAdmissionUnavailable, fmt.Errorf("traffic policy local quota store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return QuotaDecisionAdmissionUnavailable, err
	}
	if s.maxKeys <= 0 {
		return QuotaDecisionAdmissionUnavailable, fmt.Errorf("traffic policy local quota max keys must be greater than zero")
	}
	if s.idleTTL <= 0 {
		return QuotaDecisionAdmissionUnavailable, fmt.Errorf("traffic policy local quota idle TTL must be greater than zero")
	}
	if err := validateQuotaRequest(request); err != nil {
		return QuotaDecisionAdmissionUnavailable, err
	}

	now := s.now()
	key := localQuotaKey{
		policyName: request.PolicyName,
		keyScope:   request.KeyScope,
		keyValue:   request.KeyValue,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		count := len(s.buckets)
		metrics.SetTrafficPolicyLocalKeys(count)
		s.runtime.setLocalKeyState(count, s.maxKeys)
	}()

	s.removeIdleBuckets(now)
	bucket, exists := s.buckets[key]
	if !exists {
		if len(s.buckets) >= s.maxKeys {
			return QuotaDecisionOverloaded, nil
		}
		bucket = &localQuotaBucket{
			tokens:     float64(request.TokenBucket.Capacity),
			lastRefill: now,
			lastSeen:   now,
		}
		bucket.element = s.lru.PushFront(key)
		s.buckets[key] = bucket
	} else {
		s.refill(bucket, request.TokenBucket, now)
		bucket.lastSeen = now
		s.lru.MoveToFront(bucket.element)
	}

	if bucket.tokens < 1 {
		return QuotaDecisionRateLimited, nil
	}
	bucket.tokens--
	return QuotaDecisionAllowed, nil
}

func (s *LocalQuotaStore) refill(bucket *localQuotaBucket, config TokenBucketConfig, now time.Time) {
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed <= 0 {
		return
	}

	refill := float64(elapsed) / float64(config.RefillInterval) * float64(config.RefillTokens)
	bucket.tokens = min(float64(config.Capacity), bucket.tokens+refill)
	bucket.lastRefill = now
}

func (s *LocalQuotaStore) removeIdleBuckets(now time.Time) {
	for {
		element := s.lru.Back()
		if element == nil {
			return
		}
		key := element.Value.(localQuotaKey)
		bucket := s.buckets[key]
		if now.Sub(bucket.lastSeen) < s.idleTTL {
			return
		}
		delete(s.buckets, key)
		s.lru.Remove(element)
	}
}

func (s *LocalQuotaStore) bucketCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buckets)
}
