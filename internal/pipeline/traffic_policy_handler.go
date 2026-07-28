package pipeline

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type TrafficPolicyHandler struct {
	mode     string
	maxKeys  int
	idleTTL  time.Duration
	selector *TrafficPolicySelector
	now      func() time.Time

	mu      sync.Mutex
	buckets map[trafficBucketKey]*trafficBucket
	lru     *list.List
}

type trafficBucketKey struct {
	policy   string
	clientID string
}

type trafficBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
	element    *list.Element
}

func NewTrafficPolicyHandler(config TrafficPoliciesConfig) *TrafficPolicyHandler {
	if !config.Enabled {
		return nil
	}
	return &TrafficPolicyHandler{
		mode:     config.Mode,
		maxKeys:  config.MaxKeys,
		idleTTL:  config.IdleTTL,
		selector: NewTrafficPolicySelector(config),
		now:      time.Now,
		buckets:  make(map[trafficBucketKey]*trafficBucket),
		lru:      list.New(),
	}
}

func (h *TrafficPolicyHandler) Handle(ctx *Context) error {
	if h == nil {
		return nil
	}
	if h.mode != TrafficPolicyModeLocal || h.maxKeys <= 0 || h.idleTTL <= 0 {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policies are misconfigured"))
	}
	if ctx == nil || ctx.Packet == nil {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policy packet is missing"))
	}
	if ctx.Principal == nil || ctx.Principal.ClientID == "" {
		return Reject(protocol.AckUnauthorized, fmt.Errorf("traffic policy requires an authenticated client"))
	}

	policy, selected := h.selector.Select(ctx.Principal.ClientID, ctx.Packet.MsgID)
	if !selected {
		return nil
	}
	if policy.Key != TrafficPolicyKeyClientID ||
		policy.TokenBucket.Capacity <= 0 ||
		policy.TokenBucket.RefillTokens <= 0 ||
		policy.TokenBucket.RefillInterval <= 0 {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policy %q is misconfigured", policy.Name))
	}

	reason := h.admit(
		trafficBucketKey{policy: policy.Name, clientID: ctx.Principal.ClientID},
		policy.TokenBucket,
		h.now(),
	)
	if reason == "" {
		return nil
	}

	metrics.RecordRateLimitRejected(ctx.Packet.MsgID)
	switch reason {
	case resilience.ReasonOverloaded:
		return RejectWithReason(
			protocol.AckRejected,
			reason,
			fmt.Errorf("traffic policy %q local key capacity is exhausted", policy.Name),
		)
	default:
		return RejectWithReason(
			protocol.AckRejected,
			resilience.ReasonRateLimited,
			fmt.Errorf("traffic policy %q rate limit exceeded", policy.Name),
		)
	}
}

func (h *TrafficPolicyHandler) admit(key trafficBucketKey, config TokenBucketConfig, now time.Time) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.removeIdleBuckets(now)
	bucket, exists := h.buckets[key]
	if !exists {
		if len(h.buckets) >= h.maxKeys {
			return resilience.ReasonOverloaded
		}
		bucket = &trafficBucket{
			tokens:     float64(config.Capacity),
			lastRefill: now,
			lastSeen:   now,
		}
		bucket.element = h.lru.PushFront(key)
		h.buckets[key] = bucket
	} else {
		h.refill(bucket, config, now)
		bucket.lastSeen = now
		h.lru.MoveToFront(bucket.element)
	}

	if bucket.tokens < 1 {
		return resilience.ReasonRateLimited
	}
	bucket.tokens--
	return ""
}

func (h *TrafficPolicyHandler) refill(bucket *trafficBucket, config TokenBucketConfig, now time.Time) {
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed <= 0 {
		return
	}

	refill := float64(elapsed) / float64(config.RefillInterval) * float64(config.RefillTokens)
	bucket.tokens = min(float64(config.Capacity), bucket.tokens+refill)
	bucket.lastRefill = now
}

func (h *TrafficPolicyHandler) removeIdleBuckets(now time.Time) {
	for {
		element := h.lru.Back()
		if element == nil {
			return
		}
		key := element.Value.(trafficBucketKey)
		bucket := h.buckets[key]
		if now.Sub(bucket.lastSeen) < h.idleTTL {
			return
		}
		delete(h.buckets, key)
		h.lru.Remove(element)
	}
}

func (h *TrafficPolicyHandler) bucketCount() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.buckets)
}
