package httpforwarder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDNSResolverBuildsImmutableIPv4AndIPv6Snapshot(t *testing.T) {
	lookup := &mutableHostLookup{
		addresses: []string{"2001:db8::1", "127.0.0.1", "127.0.0.1", "not-an-ip"},
	}
	resolver := newTestDNSResolver(t, lookup, time.Hour)
	defer resolver.Close()

	first, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []string{
		"http://127.0.0.1:8080/gateway/upstream",
		"http://[2001:db8::1]:8080/gateway/upstream",
	}
	if !equalStrings(first.Endpoints, want) {
		t.Fatalf("endpoints = %v, want %v", first.Endpoints, want)
	}
	first.Endpoints[0] = "http://mutated.local"

	second, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() second error = %v", err)
	}
	if !equalStrings(second.Endpoints, want) {
		t.Fatalf("second endpoints = %v, want immutable %v", second.Endpoints, want)
	}
}

func TestDNSResolverRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config DNSResolverConfig
	}{
		{name: "scheme", config: DNSResolverConfig{Scheme: "ftp", Hostname: "backend.internal", Port: 8080, Path: "/", RefreshInterval: time.Second}},
		{name: "hostname", config: DNSResolverConfig{Scheme: "http", Port: 8080, Path: "/", RefreshInterval: time.Second}},
		{name: "port", config: DNSResolverConfig{Scheme: "http", Hostname: "backend.internal", Path: "/", RefreshInterval: time.Second}},
		{name: "path", config: DNSResolverConfig{Scheme: "http", Hostname: "backend.internal", Port: 8080, Path: "/gateway?secret=value", RefreshInterval: time.Second}},
		{name: "refresh", config: DNSResolverConfig{Scheme: "http", Hostname: "backend.internal", Port: 8080, Path: "/"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewDNSResolver(test.config); err == nil {
				t.Fatal("NewDNSResolver() error = nil, want error")
			}
		})
	}
}

func TestDNSResolverKeepsLastKnownGoodAndReplacesSnapshot(t *testing.T) {
	lookup := &mutableHostLookup{addresses: []string{"127.0.0.1"}}
	resolver := newTestDNSResolver(t, lookup, time.Hour)
	defer resolver.Close()

	initial, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() initial error = %v", err)
	}

	lookup.Set(nil, errors.New("temporary dns failure"))
	if err := resolver.refresh(context.Background()); err == nil {
		t.Fatal("refresh() error = nil, want lookup failure")
	}
	lastKnownGood, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() after failure error = %v", err)
	}
	if !equalStrings(lastKnownGood.Endpoints, initial.Endpoints) {
		t.Fatalf("last-known-good = %v, want %v", lastKnownGood.Endpoints, initial.Endpoints)
	}

	lookup.Set([]string{"127.0.0.2"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() replacement error = %v", err)
	}
	replaced, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() replacement error = %v", err)
	}
	want := []string{"http://127.0.0.2:8080/gateway/upstream"}
	if !equalStrings(replaced.Endpoints, want) {
		t.Fatalf("replacement endpoints = %v, want %v", replaced.Endpoints, want)
	}
}

func TestDNSResolverReturnsClearErrorUntilFirstSuccessfulLookup(t *testing.T) {
	lookup := &mutableHostLookup{err: errors.New("dns unavailable")}
	resolver := newTestDNSResolver(t, lookup, time.Hour)
	defer resolver.Close()

	_, err := resolver.Resolve(context.Background())
	if !errors.Is(err, ErrNoAvailableEndpoint) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrNoAvailableEndpoint)
	}

	lookup.Set([]string{"127.0.0.3"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() recovery error = %v", err)
	}
	snapshot, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() recovery error = %v", err)
	}
	if len(snapshot.Endpoints) != 1 || snapshot.Endpoints[0] != "http://127.0.0.3:8080/gateway/upstream" {
		t.Fatalf("recovered snapshot = %v", snapshot.Endpoints)
	}
}

func TestDNSResolverResolveHonorsCanceledContextAfterInitialLookup(t *testing.T) {
	resolver := newTestDNSResolver(t, &mutableHostLookup{addresses: []string{"127.0.0.1"}}, time.Hour)
	defer resolver.Close()
	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve() initial error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want %v", err, context.Canceled)
	}
}

func TestDNSResolverRetiredEndpointDropsCooldownState(t *testing.T) {
	lookup := &mutableHostLookup{addresses: []string{"127.0.0.1"}}
	resolver := newTestDNSResolver(t, lookup, time.Hour)
	defer resolver.Close()
	selector := newEndpointSelector(resolver, time.Hour, nil)

	first, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() first error = %v", err)
	}
	selector.MarkFailure(first)

	lookup.Set([]string{"127.0.0.2"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() second endpoint error = %v", err)
	}
	if _, err := selector.Select(context.Background(), nil); err != nil {
		t.Fatalf("Select() second endpoint error = %v", err)
	}

	lookup.Set([]string{"127.0.0.1"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() restored endpoint error = %v", err)
	}
	restored, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() restored endpoint error = %v", err)
	}
	if restored != first {
		t.Fatalf("restored endpoint = %q, want %q", restored, first)
	}
}

func TestDNSResolverRefreshesOnInterval(t *testing.T) {
	var calls atomic.Int64
	secondLookup := make(chan struct{})
	var secondOnce sync.Once
	lookup := HostLookupFunc(func(context.Context, string) ([]string, error) {
		if calls.Add(1) == 1 {
			return []string{"127.0.0.1"}, nil
		}
		secondOnce.Do(func() {
			close(secondLookup)
		})
		return []string{"127.0.0.2"}, nil
	})
	resolver := newTestDNSResolver(t, lookup, 5*time.Millisecond)
	defer resolver.Close()

	initial, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() initial error = %v", err)
	}
	if initial.Endpoints[0] != "http://127.0.0.1:8080/gateway/upstream" {
		t.Fatalf("initial endpoint = %v", initial.Endpoints)
	}

	select {
	case <-secondLookup:
	case <-time.After(time.Second):
		t.Fatal("periodic DNS refresh did not run")
	}
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, err := resolver.Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve() refreshed error = %v", err)
		}
		if len(snapshot.Endpoints) == 1 && snapshot.Endpoints[0] == "http://127.0.0.2:8080/gateway/upstream" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refreshed endpoint = %v, want 127.0.0.2", snapshot.Endpoints)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDNSResolverCloseCancelsLookup(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	lookup := HostLookupFunc(func(ctx context.Context, _ string) ([]string, error) {
		startedOnce.Do(func() {
			close(started)
		})
		<-ctx.Done()
		return nil, ctx.Err()
	})
	resolver := newTestDNSResolver(t, lookup, time.Hour)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial DNS lookup did not start")
	}
	closed := make(chan struct{})
	go func() {
		_ = resolver.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close() did not cancel DNS lookup")
	}
}

func newTestDNSResolver(t *testing.T, lookup HostLookup, refreshInterval time.Duration) *DNSResolver {
	t.Helper()
	resolver, err := NewDNSResolver(DNSResolverConfig{
		Scheme:          "http",
		Hostname:        "backend.internal",
		Port:            8080,
		Path:            "/gateway/upstream",
		RefreshInterval: refreshInterval,
		LookupTimeout:   time.Second,
		Lookup:          lookup,
	})
	if err != nil {
		t.Fatalf("NewDNSResolver() error = %v", err)
	}
	return resolver
}

type mutableHostLookup struct {
	mu        sync.RWMutex
	addresses []string
	err       error
}

func (l *mutableHostLookup) LookupHost(ctx context.Context, _ string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.addresses...), l.err
}

func (l *mutableHostLookup) Set(addresses []string, err error) {
	l.mu.Lock()
	l.addresses = append([]string(nil), addresses...)
	l.err = err
	l.mu.Unlock()
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
