package cluster

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRegistryBindLookup(t *testing.T) {
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	registry := NewMemoryRegistry(MemoryRegistryConfig{
		TTL: 30 * time.Second,
		Now: func() time.Time {
			return now
		},
	})

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

func TestMemoryRegistryUnbindRequiresMatchingSessionID(t *testing.T) {
	registry := NewMemoryRegistry(MemoryRegistryConfig{})
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

func TestMemoryRegistryLookupExpiresEntries(t *testing.T) {
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	registry := NewMemoryRegistry(MemoryRegistryConfig{
		TTL: 10 * time.Second,
		Now: func() time.Time {
			return now
		},
	})
	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(11 * time.Second)
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || ok {
		t.Fatalf("Lookup() after expiry = ok:%v err:%v, want not found", ok, err)
	}
}

func TestMemoryRegistryListFiltersRoutes(t *testing.T) {
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	registry := NewMemoryRegistry(MemoryRegistryConfig{
		TTL: time.Minute,
		Now: func() time.Time {
			return now
		},
	})

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
		if err := registry.Bind(context.Background(), entry); err != nil {
			t.Fatalf("Bind(%s) error = %v", entry.SessionID, err)
		}
	}

	listed, err := registry.List(context.Background(), RouteListFilter{ClientID: "client-1", Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 2 || len(listed.Routes) != 1 {
		t.Fatalf("List() = %+v, want total=2 one route", listed)
	}
	if listed.Routes[0].DeviceID != "device-1" || listed.Routes[0].SessionID != "session-3" {
		t.Fatalf("first route = %+v, want sorted client-1/device-1", listed.Routes[0])
	}

	bySession, err := registry.List(context.Background(), RouteListFilter{SessionID: "session-2"})
	if err != nil {
		t.Fatalf("List(session) error = %v", err)
	}
	if bySession.Total != 1 || len(bySession.Routes) != 1 || bySession.Routes[0].ClientID != "client-2" {
		t.Fatalf("List(session) = %+v, want client-2 route", bySession)
	}
}

func TestMemoryRegistryListExpiresEntries(t *testing.T) {
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	registry := NewMemoryRegistry(MemoryRegistryConfig{
		TTL: 10 * time.Second,
		Now: func() time.Time {
			return now
		},
	})
	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(11 * time.Second)
	listed, err := registry.List(context.Background(), RouteListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listed.Total != 0 || len(listed.Routes) != 0 {
		t.Fatalf("List() after expiry = %+v, want empty", listed)
	}
}

func TestMemoryRegistryTouchRefreshesTTL(t *testing.T) {
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	registry := NewMemoryRegistry(MemoryRegistryConfig{
		TTL: 10 * time.Second,
		Now: func() time.Time {
			return now
		},
	})
	entry := testRouteEntry("session-1")
	if err := registry.Bind(context.Background(), entry); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now = now.Add(9 * time.Second)
	touched := entry
	touched.InternalAddr = "http://gateway-a:18082"
	if err := registry.Touch(context.Background(), touched); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}

	now = now.Add(9 * time.Second)
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || !ok {
		t.Fatalf("Lookup() after refreshed ttl = ok:%v err:%v, want found", ok, err)
	}

	now = now.Add(2 * time.Second)
	if _, ok, err := registry.Lookup(context.Background(), entry.Key()); err != nil || ok {
		t.Fatalf("Lookup() after refreshed ttl expiry = ok:%v err:%v, want not found", ok, err)
	}
}

func TestMemoryRegistryTouchRejectsSessionMismatch(t *testing.T) {
	registry := NewMemoryRegistry(MemoryRegistryConfig{})
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

func TestMemoryRegistryCloseRejectsOperations(t *testing.T) {
	registry := NewMemoryRegistry(MemoryRegistryConfig{})
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

func testRouteEntry(sessionID string) RouteEntry {
	return RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    sessionID,
		GatewayNode:  "gateway-a",
		InternalAddr: "http://gateway-a:18080",
		TokenID:      "token-1",
	}
}
