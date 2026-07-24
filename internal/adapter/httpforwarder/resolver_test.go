package httpforwarder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaticResolverReturnsImmutableSnapshot(t *testing.T) {
	source := []string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	}
	resolver, err := NewStaticResolver(source)
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	source[0] = "http://mutated.local"

	first, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	first.Endpoints[0] = "http://mutated-again.local"

	second, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() second error = %v", err)
	}
	if second.Endpoints[0] != "http://backend-a.local/gateway/upstream" {
		t.Fatalf("second snapshot = %v, want immutable endpoint list", second.Endpoints)
	}
}

func TestStaticResolverRejectsInvalidEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []string
	}{
		{name: "empty", endpoints: nil},
		{name: "relative", endpoints: []string{"/gateway/upstream"}},
		{name: "credentials", endpoints: []string{"http://user:password@backend.local/gateway/upstream"}},
		{name: "duplicate", endpoints: []string{"http://backend.local", "http://backend.local"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewStaticResolver(test.endpoints); err == nil {
				t.Fatal("NewStaticResolver() error = nil, want error")
			}
		})
	}
}

func TestEndpointSelectorRoundRobinCooldownAndRecovery(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local",
		"http://backend-b.local",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	selector := newEndpointSelector(resolver, 10*time.Second, func() time.Time {
		return now
	})

	first, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() first error = %v", err)
	}
	if first != "http://backend-a.local" {
		t.Fatalf("first endpoint = %q", first)
	}
	selector.MarkFailure(first)

	second, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() second error = %v", err)
	}
	if second != "http://backend-b.local" {
		t.Fatalf("second endpoint = %q", second)
	}

	third, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() third error = %v", err)
	}
	if third != "http://backend-b.local" {
		t.Fatalf("third endpoint = %q, want healthy backend-b", third)
	}

	now = now.Add(11 * time.Second)
	recovered, err := selector.Select(context.Background(), nil)
	if err != nil {
		t.Fatalf("Select() recovered error = %v", err)
	}
	if recovered != "http://backend-a.local" {
		t.Fatalf("recovered endpoint = %q, want backend-a", recovered)
	}
}

func TestEndpointSelectorReturnsNoEndpointWhenAllCandidatesUnavailable(t *testing.T) {
	resolver, err := NewStaticResolver([]string{"http://backend-a.local"})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	selector := newEndpointSelector(resolver, time.Minute, nil)
	selector.MarkFailure("http://backend-a.local")

	_, err = selector.Select(context.Background(), nil)
	if !errors.Is(err, ErrNoAvailableEndpoint) {
		t.Fatalf("Select() error = %v, want %v", err, ErrNoAvailableEndpoint)
	}
}

func TestEndpointSelectorConcurrentRoundRobin(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local",
		"http://backend-b.local",
		"http://backend-c.local",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	selector := newEndpointSelector(resolver, 0, nil)

	var counts [3]atomic.Int64
	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, 102)
	for range 102 {
		waitGroup.Go(func() {
			endpoint, err := selector.Select(context.Background(), nil)
			if err != nil {
				errorsChannel <- err
				return
			}
			switch endpoint {
			case "http://backend-a.local":
				counts[0].Add(1)
			case "http://backend-b.local":
				counts[1].Add(1)
			case "http://backend-c.local":
				counts[2].Add(1)
			default:
				errorsChannel <- errors.New("unexpected endpoint")
			}
		})
	}
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("Select() error = %v", err)
	}
	for index := range counts {
		if got := counts[index].Load(); got != 34 {
			t.Fatalf("endpoint %d selections = %d, want 34", index, got)
		}
	}
}
