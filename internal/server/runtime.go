package server

import (
	"sync/atomic"
	"time"
)

type gatewayRuntime struct {
	startedUnixNano atomic.Int64
}

func newGatewayRuntime() *gatewayRuntime {
	return &gatewayRuntime{}
}

func (r *gatewayRuntime) MarkStarted(now time.Time) {
	if r == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	r.startedUnixNano.CompareAndSwap(0, now.UnixNano())
}

func (r *gatewayRuntime) StartedAt() (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	raw := r.startedUnixNano.Load()
	if raw == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, raw), true
}

func (r *gatewayRuntime) Uptime(now time.Time) time.Duration {
	startedAt, ok := r.StartedAt()
	if !ok {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(startedAt) {
		return 0
	}
	return now.Sub(startedAt)
}
