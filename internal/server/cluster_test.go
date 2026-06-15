package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

func TestNewClusterRegistryCreatesMemoryRegistry(t *testing.T) {
	config := normalizeConfig(Config{
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Registry: ClusterRegistryConfig{
				Type: "memory",
			},
		},
	})

	registry, closer, err := newClusterRegistry(config)
	if err != nil {
		t.Fatalf("newClusterRegistry() error = %v", err)
	}
	if registry == nil {
		t.Fatal("newClusterRegistry() registry = nil, want memory registry")
	}
	if closer == nil {
		t.Fatal("newClusterRegistry() closer = nil, want closer")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close() error = %v", err)
	}
}

func TestNewClusterRegistryCreatesRedisRegistry(t *testing.T) {
	redisServer := miniredis.RunT(t)
	config := normalizeConfig(Config{
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Registry: ClusterRegistryConfig{
				Type: "redis",
				Redis: ClusterRedisConfig{
					Addr: redisServer.Addr(),
				},
			},
		},
	})

	registry, closer, err := newClusterRegistry(config)
	if err != nil {
		t.Fatalf("newClusterRegistry() error = %v", err)
	}
	if registry == nil {
		t.Fatal("newClusterRegistry() registry = nil, want redis registry")
	}
	if closer == nil {
		t.Fatal("newClusterRegistry() closer = nil, want closer")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close() error = %v", err)
	}
}

func TestClusterBindHandlerBindsRoute(t *testing.T) {
	now := time.Date(2026, 6, 9, 11, 0, 0, 0, time.UTC)
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: 30 * time.Second})
	handler := newClusterBindHandler(Config{
		GatewayNode: "gateway-a",
		Cluster: ClusterConfig{
			InternalAddr: "http://gateway-a:18080",
		},
	}, registry, zap.NewNop())

	request := &testZinxRequest{
		conn:  &testZinxConnection{connID: 7},
		msgID: protocol.MsgIDBind,
	}
	ctx := pipeline.NewContext(request, protocol.NewPacket(protocol.MsgIDBind, []byte("hello")), zap.NewNop())
	ctx.BindResult = &session.BindResult{
		Session: &session.Session{
			SessionID:   "session-1",
			ConnID:      7,
			ClientID:    "client-1",
			DeviceID:    "device-1",
			TokenID:     "token-1",
			GatewayNode: "gateway-a",
			LastSeenAt:  now,
		},
	}

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	got, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.SessionID != "session-1" || got.InternalAddr != "http://gateway-a:18080" || got.TokenID != "token-1" {
		t.Fatalf("route entry = %+v", got)
	}
}

func TestClusterBindHandlerRejectsBindFailure(t *testing.T) {
	handler := newClusterBindHandler(Config{
		GatewayNode: "gateway-a",
		Cluster: ClusterConfig{
			InternalAddr: "http://gateway-a:18080",
		},
	}, failingClusterRegistry{}, zap.NewNop())

	request := &testZinxRequest{
		conn:  &testZinxConnection{connID: 7},
		msgID: protocol.MsgIDBind,
	}
	ctx := pipeline.NewContext(request, protocol.NewPacket(protocol.MsgIDBind, []byte("hello")), zap.NewNop())
	ctx.BindResult = &session.BindResult{
		Session: &session.Session{
			SessionID:   "session-1",
			ConnID:      7,
			ClientID:    "client-1",
			DeviceID:    "device-1",
			TokenID:     "token-1",
			GatewayNode: "gateway-a",
		},
	}

	err := handler.Handle(ctx)
	if err == nil {
		t.Fatal("Handle() error = nil, want error")
	}
	code, _ := pipeline.AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("AckError() code = %s, want %s", code, protocol.AckRejected)
	}
}

func TestGatewayConnStopUnbindsClusterRoute(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{})
	bindSession(t, sessions, "session-1", 7)
	if err := registry.Bind(context.Background(), testServerRouteEntry("session-1")); err != nil {
		t.Fatalf("registry.Bind() error = %v", err)
	}

	gateway := &Gateway{
		sessions:        sessions,
		logger:          zap.NewNop(),
		clusterRegistry: registry,
	}
	gateway.onConnStop(&testZinxConnection{connID: 7})

	_, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if ok {
		t.Fatal("Lookup() ok = true, want route removed")
	}
}

func TestGatewayConnStopDoesNotRemoveNewerClusterRoute(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{})
	bindSession(t, sessions, "session-old", 7)
	if err := registry.Bind(context.Background(), testServerRouteEntry("session-new")); err != nil {
		t.Fatalf("registry.Bind() error = %v", err)
	}

	gateway := &Gateway{
		sessions:        sessions,
		logger:          zap.NewNop(),
		clusterRegistry: registry,
	}
	gateway.onConnStop(&testZinxConnection{connID: 7})

	got, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want newer route kept")
	}
	if got.SessionID != "session-new" {
		t.Fatalf("SessionID = %q, want session-new", got.SessionID)
	}
}

func TestGatewayUnbindAllClusterRoutes(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{})
	bindSession(t, sessions, "session-1", 7)
	if err := registry.Bind(context.Background(), testServerRouteEntry("session-1")); err != nil {
		t.Fatalf("registry.Bind() error = %v", err)
	}

	gateway := &Gateway{
		sessions:        sessions,
		logger:          zap.NewNop(),
		clusterRegistry: registry,
	}
	unbound := gateway.unbindAllClusterRoutes(context.Background())
	if unbound != 1 {
		t.Fatalf("unbound = %d, want 1", unbound)
	}

	_, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if ok {
		t.Fatal("Lookup() ok = true, want route removed")
	}
}

func TestGatewayUnbindAllClusterRoutesDoesNotRemoveNewerRoute(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{})
	bindSession(t, sessions, "session-old", 7)
	if err := registry.Bind(context.Background(), testServerRouteEntry("session-new")); err != nil {
		t.Fatalf("registry.Bind() error = %v", err)
	}

	gateway := &Gateway{
		sessions:        sessions,
		logger:          zap.NewNop(),
		clusterRegistry: registry,
	}
	unbound := gateway.unbindAllClusterRoutes(context.Background())
	if unbound != 1 {
		t.Fatalf("unbound = %d, want 1", unbound)
	}

	got, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want newer route kept")
	}
	if got.SessionID != "session-new" {
		t.Fatalf("SessionID = %q, want session-new", got.SessionID)
	}
}

func TestClusterRouteRefresherBindsMissingRoute(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: 30 * time.Second})
	bindSession(t, sessions, "session-1", 7)

	refresher := newClusterRouteRefresher(Config{
		GatewayNode: "gateway-a",
		Cluster: ClusterConfig{
			InternalAddr:         "http://gateway-a:18080",
			RouteRefreshInterval: time.Second,
			Registry: ClusterRegistryConfig{
				TTL: 30 * time.Second,
			},
		},
	}, registry, sessions, zap.NewNop())

	result := refresher.refresh(context.Background())
	if result.Scanned != 1 || result.Refreshed != 1 || result.Skipped != 0 || result.Failed != 0 {
		t.Fatalf("refresh result = %+v", result)
	}

	got, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want refreshed route")
	}
	if got.SessionID != "session-1" || got.InternalAddr != "http://gateway-a:18080" {
		t.Fatalf("route entry = %+v", got)
	}
}

func TestClusterRouteRefresherDoesNotOverwriteMismatchedRoute(t *testing.T) {
	sessions := session.NewManager()
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: 30 * time.Second})
	bindSession(t, sessions, "session-old", 7)
	if err := registry.Bind(context.Background(), testServerRouteEntry("session-new")); err != nil {
		t.Fatalf("registry.Bind() error = %v", err)
	}

	refresher := newClusterRouteRefresher(Config{
		GatewayNode: "gateway-a",
		Cluster: ClusterConfig{
			InternalAddr:         "http://gateway-a:18080",
			RouteRefreshInterval: time.Second,
			Registry: ClusterRegistryConfig{
				TTL: 30 * time.Second,
			},
		},
	}, registry, sessions, zap.NewNop())

	result := refresher.refresh(context.Background())
	if result.Scanned != 1 || result.Refreshed != 0 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("refresh result = %+v", result)
	}

	got, ok, err := registry.Lookup(context.Background(), cluster.RouteKey{
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want newer route kept")
	}
	if got.SessionID != "session-new" {
		t.Fatalf("SessionID = %q, want session-new", got.SessionID)
	}
}

func TestClusterRouteRefreshInterval(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "default", ttl: 0, want: 10 * time.Second},
		{name: "third", ttl: 30 * time.Second, want: 10 * time.Second},
		{name: "minimum", ttl: time.Second, want: 500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clusterRouteRefreshInterval(tt.ttl); got != tt.want {
				t.Fatalf("clusterRouteRefreshInterval(%v) = %v, want %v", tt.ttl, got, tt.want)
			}
		})
	}
}

func bindSession(t *testing.T, sessions *session.Manager, sessionID string, connID uint64) {
	t.Helper()

	_, err := sessions.Bind(session.BindInput{
		SessionID:   sessionID,
		ConnID:      connID,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		TokenID:     "token-1",
		GatewayNode: "gateway-a",
	})
	if err != nil {
		t.Fatalf("sessions.Bind() error = %v", err)
	}
}

func testServerRouteEntry(sessionID string) cluster.RouteEntry {
	return cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    sessionID,
		GatewayNode:  "gateway-a",
		InternalAddr: "http://gateway-a:18080",
		TokenID:      "token-1",
	}
}

type failingClusterRegistry struct{}

func (failingClusterRegistry) Bind(context.Context, cluster.RouteEntry) error {
	return errors.New("bind failed")
}

func (failingClusterRegistry) Unbind(context.Context, cluster.RouteKey, string) error {
	return nil
}

func (failingClusterRegistry) Lookup(context.Context, cluster.RouteKey) (cluster.RouteEntry, bool, error) {
	return cluster.RouteEntry{}, false, nil
}

func (failingClusterRegistry) Touch(context.Context, cluster.RouteEntry) error {
	return nil
}

func (failingClusterRegistry) Close() error {
	return nil
}
