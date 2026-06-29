package server

import (
	"net/http"
	"sync/atomic"
	"time"
)

type gatewayHealth struct {
	draining             atomic.Bool
	drainStartedUnixNano atomic.Int64
}

func (h *gatewayHealth) Ready() bool {
	return h == nil || !h.draining.Load()
}

func (h *gatewayHealth) BeginDrain() {
	h.BeginDrainAt(time.Now())
}

func (h *gatewayHealth) BeginDrainAt(now time.Time) {
	if h == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}

	h.draining.Store(true)
	h.drainStartedUnixNano.CompareAndSwap(0, now.UnixNano())
}

func (h *gatewayHealth) DrainingSince() (time.Time, bool) {
	if h == nil || !h.draining.Load() {
		return time.Time{}, false
	}
	raw := h.drainStartedUnixNano.Load()
	if raw == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, raw), true
}

func (h *gatewayHealth) DrainDuration(now time.Time) time.Duration {
	startedAt, ok := h.DrainingSince()
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

func newHealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

func newReadyHandler(health *gatewayHealth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !health.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("draining\n"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
}
