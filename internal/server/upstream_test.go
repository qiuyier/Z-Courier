package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func TestNewRouteForwarderRunsStaticDiscoveryFailover(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var received atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer healthy.Close()
	unavailableURL := unavailable.URL
	unavailable.Close()

	forwarder, err := newRouteForwarder(UpstreamRouteConfig{
		Name: "orders",
		HTTP: &HTTPUpstreamConfig{
			Discovery: HTTPUpstreamDiscoveryConfig{
				Type:      "static",
				Endpoints: []string{unavailableURL, healthy.URL},
			},
			Failover: HTTPUpstreamFailoverConfig{
				Enabled:           true,
				MaxAttempts:       2,
				UnhealthyCooldown: time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("newRouteForwarder() error = %v", err)
	}
	defer func() {
		if closer, ok := forwarder.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("hello")))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result == nil || result.StatusCode != http.StatusNoContent || received.Load() != 1 {
		t.Fatalf("result = %+v, received = %d", result, received.Load())
	}
}

func TestNewRouteForwarderRunsDNSDiscovery(t *testing.T) {
	requestHost := make(chan string, 1)
	requestPath := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost <- r.Host
		requestPath <- r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	address := backend.Listener.Addr().(*net.TCPAddr)

	forwarder, err := newRouteForwarder(UpstreamRouteConfig{
		Name: "orders",
		HTTP: &HTTPUpstreamConfig{
			Path: "/gateway/upstream",
			Discovery: HTTPUpstreamDiscoveryConfig{
				Type:            "dns",
				Scheme:          "http",
				Hostname:        "orders.internal",
				Port:            address.Port,
				RefreshInterval: time.Hour,
				LookupTimeout:   time.Second,
				Lookup: dnsHostLookupFunc(func(context.Context, string) ([]string, error) {
					return []string{address.IP.String()}, nil
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("newRouteForwarder() error = %v", err)
	}
	defer func() {
		if closer, ok := forwarder.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("hello")))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result == nil || result.StatusCode != http.StatusNoContent {
		t.Fatalf("result = %+v", result)
	}
	if got := <-requestHost; got != net.JoinHostPort("orders.internal", strconv.Itoa(address.Port)) {
		t.Fatalf("request host = %q", got)
	}
	if got := <-requestPath; got != "/gateway/upstream" {
		t.Fatalf("request path = %q", got)
	}
}

func TestPrimaryHTTPUpstreamURLUsesStaticEndpoint(t *testing.T) {
	config := &HTTPUpstreamConfig{
		Discovery: HTTPUpstreamDiscoveryConfig{
			Type:      "static",
			Endpoints: []string{"https://orders-a.internal/gateway/upstream", "https://orders-b.internal/gateway/upstream"},
		},
	}
	if got := primaryHTTPUpstreamURL(config); got != config.Discovery.Endpoints[0] {
		t.Fatalf("primaryHTTPUpstreamURL() = %q", got)
	}
}

func TestPrimaryHTTPUpstreamURLBuildsDNSLogicalURL(t *testing.T) {
	config := &HTTPUpstreamConfig{
		Path: "/gateway/upstream",
		Discovery: HTTPUpstreamDiscoveryConfig{
			Type:     "dns",
			Scheme:   "https",
			Hostname: "orders.internal",
			Port:     8443,
		},
	}
	if got := primaryHTTPUpstreamURL(config); got != "https://orders.internal:8443/gateway/upstream" {
		t.Fatalf("primaryHTTPUpstreamURL() = %q", got)
	}
}

func TestCheckHTTPUpstreamReportsPartialStaticFailure(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer healthy.Close()
	unavailableURL := unavailable.URL
	unavailable.Close()

	handler := &adminCheckHandler{httpClient: &http.Client{Timeout: time.Second}}
	result := handler.checkHTTPUpstream(context.Background(), UpstreamRouteConfig{
		Name: "orders",
		HTTP: &HTTPUpstreamConfig{
			Discovery: HTTPUpstreamDiscoveryConfig{
				Type:      "static",
				Endpoints: []string{unavailableURL, healthy.URL},
			},
		},
	})
	if result.Status != adminCheckStatusDegraded || result.Error != "1/2 http upstream endpoints passed" {
		t.Fatalf("check result = %+v", result)
	}
}

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

type dnsHostLookupFunc func(context.Context, string) ([]string, error)

func (f dnsHostLookupFunc) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return f(ctx, hostname)
}

func (f blockingForwarder) Forward(context.Context, *protocol.Packet) (*router.ForwardResult, error) {
	f.entered <- struct{}{}
	<-f.release
	return &router.ForwardResult{TargetType: "http", Status: "ok", StatusCode: 200}, nil
}
