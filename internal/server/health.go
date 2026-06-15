package server

import (
	"net/http"
	"sync/atomic"
)

type gatewayHealth struct {
	draining atomic.Bool
}

func (h *gatewayHealth) Ready() bool {
	return h == nil || !h.draining.Load()
}

func (h *gatewayHealth) BeginDrain() {
	if h == nil {
		return
	}

	h.draining.Store(true)
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
