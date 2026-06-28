package pipeline

import (
	"fmt"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type RateLimitHandler struct {
	enabled     bool
	maxRequests int
	window      time.Duration
	now         func() time.Time

	mu      sync.Mutex
	buckets map[string]rateBucket
}

type rateBucket struct {
	windowStart time.Time
	count       int
}

func NewRateLimitHandler(config RateLimitConfig) *RateLimitHandler {
	return &RateLimitHandler{
		enabled:     config.Enabled,
		maxRequests: config.MaxRequests,
		window:      config.Window,
		now:         time.Now,
		buckets:     make(map[string]rateBucket),
	}
}

func (h *RateLimitHandler) Handle(ctx *Context) error {
	if h == nil || !h.enabled {
		return nil
	}
	if h.maxRequests <= 0 || h.window <= 0 {
		return Reject(protocol.AckRejected, fmt.Errorf("rate limit is misconfigured"))
	}

	key := rateLimitKey(ctx)
	now := h.now()

	h.mu.Lock()
	defer h.mu.Unlock()

	bucket := h.buckets[key]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= h.window {
		bucket = rateBucket{windowStart: now}
	}
	bucket.count++
	h.buckets[key] = bucket

	if bucket.count > h.maxRequests {
		if ctx.Packet != nil {
			metrics.RecordRateLimitRejected(ctx.Packet.MsgID)
		}
		return RejectWithReason(protocol.AckRejected, resilience.ReasonRateLimited, fmt.Errorf("rate limit exceeded for %s", key))
	}

	return nil
}

func rateLimitKey(ctx *Context) string {
	if ctx.Principal != nil && ctx.Principal.ClientID != "" {
		return "client:" + ctx.Principal.ClientID
	}
	if ctx.Packet != nil && ctx.Packet.ClientID != "" {
		return "client:" + ctx.Packet.ClientID
	}

	return fmt.Sprintf("conn:%d", ctx.ConnID())
}
