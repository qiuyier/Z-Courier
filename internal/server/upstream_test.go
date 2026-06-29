package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func TestCapacityForwarderRejectsWhenFull(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	forwarder := newCapacityForwarder("chat", "http", 1, blockingForwarder{
		entered: entered,
		release: release,
	})

	done := make(chan error, 1)
	go func() {
		_, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("first")))
		done <- err
	}()

	<-entered
	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("second")))
	if !errors.Is(err, router.ErrOverloaded) {
		t.Fatalf("Forward() error = %v, want %v", err, router.ErrOverloaded)
	}
	if result == nil || result.RouteName != "chat" || result.TargetType != "http" || result.Status != resilience.ReasonOverloaded {
		t.Fatalf("result = %+v, want overloaded chat/http", result)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first Forward() error = %v", err)
	}
}

func TestHTTPUpstreamRouteTracksDegradedState(t *testing.T) {
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer upstream.Close()

	routes := []UpstreamRouteConfig{{
		Name:     "http-route",
		MsgIDMin: 1001,
		MsgIDMax: 1999,
		HTTP: &HTTPUpstreamConfig{
			URL: upstream.URL,
		},
	}}
	runtime := newUpstreamRuntime(routes)
	engine, err := newUpstreamEngine(Config{
		UpstreamRoutes:  routes,
		UpstreamRuntime: runtime,
	})
	if err != nil {
		t.Fatalf("newUpstreamEngine() error = %v", err)
	}

	for range 3 {
		_, err := engine.Forward(context.Background(), protocol.NewPacket(1001, []byte("hello")))
		if err == nil {
			t.Fatal("Forward() error = nil, want upstream failure")
		}
	}
	snapshot, ok := runtime.snapshot("http-route")
	if !ok {
		t.Fatal("runtime snapshot not found")
	}
	if snapshot.Snapshot.Status != resilience.DependencyStatusDegraded || snapshot.Snapshot.ConsecutiveFailures != 3 || snapshot.Snapshot.LastReason != "http_status_502" {
		t.Fatalf("snapshot = %+v, want degraded with http_status_502", snapshot.Snapshot)
	}

	healthy.Store(true)
	if _, err := engine.Forward(context.Background(), protocol.NewPacket(1001, []byte("hello"))); err != nil {
		t.Fatalf("Forward() after recovery error = %v", err)
	}
	snapshot, ok = runtime.snapshot("http-route")
	if !ok {
		t.Fatal("runtime snapshot after recovery not found")
	}
	if snapshot.Snapshot.Status != resilience.DependencyStatusHealthy || snapshot.Snapshot.ConsecutiveFailures != 0 || snapshot.Snapshot.LastReason != "" {
		t.Fatalf("snapshot after recovery = %+v, want healthy reset", snapshot.Snapshot)
	}
}

type blockingForwarder struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f blockingForwarder) Forward(context.Context, *protocol.Packet) (*router.ForwardResult, error) {
	f.entered <- struct{}{}
	<-f.release
	return &router.ForwardResult{TargetType: "http", Status: "ok", StatusCode: 200}, nil
}
