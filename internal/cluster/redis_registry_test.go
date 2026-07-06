package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisRegistryBindLookup(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	registry := newTestRedisRegistry(t, server, 30*time.Second, &now)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	got, ok, err := registry.Lookup(context.Background(), entry.Key())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.SessionID != entry.SessionID || got.GatewayNode != entry.GatewayNode {
		t.Fatalf("Lookup() = %+v, want session %q gateway %q", got, entry.SessionID, entry.GatewayNode)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}
	if !got.ExpiresAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, now.Add(30*time.Second))
	}
}

func TestRedisRegistryLookupAcrossInstances(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	gatewayA := newTestRedisRegistry(t, server, 30*time.Second, &now)
	defer gatewayA.Close()
	gatewayB := newTestRedisRegistry(t, server, 30*time.Second, &now)
	defer gatewayB.Close()

	entry := testRouteEntry("session-1")
	if err := gatewayA.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	got, ok, err := gatewayB.Lookup(context.Background(), entry.Key())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.SessionID != entry.SessionID || got.InternalAddr != entry.InternalAddr {
		t.Fatalf("Lookup() = %+v", got)
	}
}

func TestRedisRegistryListAcrossInstances(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	gatewayA := newTestRedisRegistry(t, server, 30*time.Second, &now)
	defer gatewayA.Close()
	gatewayB := newTestRedisRegistry(t, server, 30*time.Second, &now)
	defer gatewayB.Close()

	entries := []RouteEntry{
		{
			ClientID:     "client-2",
			DeviceID:     "device-1",
			SessionID:    "session-2",
			GatewayNode:  "gateway-b",
			InternalAddr: "http://gateway-b:18080",
			TokenID:      "token-2",
		},
		{
			ClientID:     "client-1",
			DeviceID:     "device-2",
			SessionID:    "session-1",
			GatewayNode:  "gateway-a",
			InternalAddr: "http://gateway-a:18080",
			TokenID:      "token-1",
		},
		{
			ClientID:     "client-1",
			DeviceID:     "device-1",
			SessionID:    "session-3",
			GatewayNode:  "gateway-a",
			InternalAddr: "http://gateway-a:18080",
			TokenID:      "token-3",
		},
	}
	for _, entry := range entries {
		if err := gatewayA.Bind(context.Background(), entry); err != nil {
			t.Fatalf("Bind(%s) error = %v", entry.SessionID, err)
		}
	}

	listed, err := gatewayB.List(context.Background(), RouteListFilter{ClientID: "client-1", Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 2 || len(listed.Routes) != 1 {
		t.Fatalf("List() = %+v, want total=2 one route", listed)
	}
	if listed.Routes[0].DeviceID != "device-1" || listed.Routes[0].SessionID != "session-3" {
		t.Fatalf("first route = %+v, want sorted client-1/device-1", listed.Routes[0])
	}

	byDevice, err := gatewayB.List(context.Background(), RouteListFilter{ClientID: "client-1", DeviceID: "device-2"})
	if err != nil {
		t.Fatalf("List(exact) error = %v", err)
	}
	if byDevice.Total != 1 || len(byDevice.Routes) != 1 || byDevice.Routes[0].SessionID != "session-1" {
		t.Fatalf("List(exact) = %+v, want session-1", byDevice)
	}
}

func TestRedisRegistryListExpiresEntries(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	registry := newTestRedisRegistry(t, server, 10*time.Second, &now)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(11 * time.Second)
	server.FastForward(11 * time.Second)
	listed, err := registry.List(context.Background(), RouteListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 0 || len(listed.Routes) != 0 {
		t.Fatalf("List() after expiry = %+v, want empty", listed)
	}
}

func TestRedisRegistryUnbindRequiresMatchingSessionID(t *testing.T) {
	server := miniredis.RunT(t)
	registry := newTestRedisRegistry(t, server, 30*time.Second, nil)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	if err := registry.Unbind(context.Background(), entry.Key(), "stale-session"); err != nil {
		t.Fatalf("Unbind(stale) error = %v", err)
	}
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || !ok {
		t.Fatalf("Lookup() after stale unbind = ok:%v err:%v, want still bound", ok, err)
	}

	if err := registry.Unbind(context.Background(), entry.Key(), entry.SessionID); err != nil {
		t.Fatalf("Unbind(current) error = %v", err)
	}
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || ok {
		t.Fatalf("Lookup() after current unbind = ok:%v err:%v, want not found", ok, err)
	}
}

func TestRedisRegistryLookupExpiresEntries(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	registry := newTestRedisRegistry(t, server, 10*time.Second, &now)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(11 * time.Second)
	server.FastForward(11 * time.Second)
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || ok {
		t.Fatalf("Lookup() after expiry = ok:%v err:%v, want not found", ok, err)
	}
}

func TestRedisRegistryTouchRefreshesTTL(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	registry := newTestRedisRegistry(t, server, 10*time.Second, &now)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(9 * time.Second)
	server.FastForward(9 * time.Second)
	touched := entry
	touched.InternalAddr = "http://gateway-a:18082"
	if err := registry.Touch(context.Background(), touched); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}

	now = now.Add(9 * time.Second)
	server.FastForward(9 * time.Second)
	if got, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || !ok {
		t.Fatalf("Lookup() after refreshed ttl = ok:%v err:%v, want found", ok, err)
	} else if got.InternalAddr != "http://gateway-a:18082" {
		t.Fatalf("InternalAddr = %q, want refreshed addr", got.InternalAddr)
	}

	now = now.Add(2 * time.Second)
	server.FastForward(2 * time.Second)
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || ok {
		t.Fatalf("Lookup() after refreshed ttl expiry = ok:%v err:%v, want not found", ok, err)
	}
}

func TestRedisRegistryTouchRejectsSessionMismatch(t *testing.T) {
	server := miniredis.RunT(t)
	registry := newTestRedisRegistry(t, server, 30*time.Second, nil)
	defer registry.Close()

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	stale := entry
	stale.SessionID = "session-2"
	if err := registry.Touch(context.Background(), stale); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("Touch() error = %v, want %v", err, ErrSessionMismatch)
	}

	got, ok, err := registry.Lookup(context.Background(), entry.Key())
	if err != nil || !ok {
		t.Fatalf("Lookup() after failed touch = ok:%v err:%v, want found", ok, err)
	}
	if got.SessionID != entry.SessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, entry.SessionID)
	}
}

func TestRedisRegistryCloseRejectsOperations(t *testing.T) {
	server := miniredis.RunT(t)
	registry := newTestRedisRegistry(t, server, 30*time.Second, nil)
	if err := registry.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); !errors.Is(err, ErrClosed) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrClosed)
	}
	if _, _, err := registry.Lookup(context.Background(), entry.Key()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Lookup() error = %v, want %v", err, ErrClosed)
	}
}

func newTestRedisRegistry(t *testing.T, server *miniredis.Miniredis, ttl time.Duration, now *time.Time) *RedisRegistry {
	t.Helper()

	config := RedisRegistryConfig{
		Addr:         server.Addr(),
		KeyPrefix:    "test-zcourier",
		TTL:          ttl,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	}
	if now != nil {
		config.Now = func() time.Time {
			return *now
		}
	}

	registry, err := NewRedisRegistry(config)
	if err != nil {
		t.Fatalf("NewRedisRegistry() error = %v", err)
	}
	if err := registry.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	return registry
}
