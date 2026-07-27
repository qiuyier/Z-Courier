package httpforwarder

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrNoAvailableEndpoint = errors.New("http forwarder: no available endpoint")

type EndpointSnapshot struct {
	Endpoints []string
}

type Resolver interface {
	Resolve(context.Context) (EndpointSnapshot, error)
	Close() error
}

type StaticResolver struct {
	endpoints []string
	observer  Observer
}

func NewStaticResolver(endpoints []string) (*StaticResolver, error) {
	return NewStaticResolverWithObserver(endpoints, nil)
}

func NewStaticResolverWithObserver(endpoints []string, observer Observer) (*StaticResolver, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("http forwarder: static resolver requires endpoints")
	}

	normalized := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for index, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("http forwarder: static endpoint %d must be an absolute http or https URL", index+1)
		}
		if _, exists := seen[endpoint]; exists {
			return nil, fmt.Errorf("http forwarder: duplicate static endpoint at index %d", index+1)
		}
		seen[endpoint] = struct{}{}
		normalized = append(normalized, endpoint)
	}

	resolver := &StaticResolver{endpoints: normalized, observer: observer}
	if observer != nil {
		observer.SetResolvedEndpoints(len(normalized))
	}
	return resolver, nil
}

func (r *StaticResolver) Resolve(ctx context.Context) (EndpointSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return EndpointSnapshot{}, err
	}
	if r == nil || len(r.endpoints) == 0 {
		return EndpointSnapshot{}, ErrNoAvailableEndpoint
	}

	return EndpointSnapshot{Endpoints: append([]string(nil), r.endpoints...)}, nil
}

func (r *StaticResolver) Close() error {
	if r != nil && r.observer != nil {
		r.observer.SetResolvedEndpoints(0)
	}
	return nil
}

type endpointSelector struct {
	resolver Resolver
	cooldown time.Duration
	now      func() time.Time
	observer Observer

	mu             sync.Mutex
	next           int
	unhealthyUntil map[string]time.Time
}

func newEndpointSelector(resolver Resolver, cooldown time.Duration, now func() time.Time, observer Observer) *endpointSelector {
	if now == nil {
		now = time.Now
	}
	selector := &endpointSelector{
		resolver:       resolver,
		cooldown:       cooldown,
		now:            now,
		observer:       observer,
		unhealthyUntil: make(map[string]time.Time),
	}
	if observer != nil {
		observer.SetUnhealthyEndpoints(0)
	}
	return selector
}

func (s *endpointSelector) Select(ctx context.Context, excluded map[string]struct{}) (string, error) {
	if s == nil || s.resolver == nil {
		if s != nil && s.observer != nil {
			s.observer.RecordEndpointSelection(EndpointSelectionResolverError)
		}
		return "", ErrNoAvailableEndpoint
	}

	snapshot, err := s.resolver.Resolve(ctx)
	if err != nil {
		if s.observer != nil {
			s.observer.RecordEndpointSelection(EndpointSelectionResolverError)
		}
		return "", err
	}
	if len(snapshot.Endpoints) == 0 {
		if s.observer != nil {
			s.observer.RecordEndpointSelection(EndpointSelectionNoAvailable)
		}
		return "", ErrNoAvailableEndpoint
	}

	now := s.now()
	s.mu.Lock()

	active := make(map[string]struct{}, len(snapshot.Endpoints))
	for _, endpoint := range snapshot.Endpoints {
		active[endpoint] = struct{}{}
	}
	for endpoint, until := range s.unhealthyUntil {
		_, stillActive := active[endpoint]
		if !stillActive || !now.Before(until) {
			delete(s.unhealthyUntil, endpoint)
		}
	}

	start := s.next % len(snapshot.Endpoints)
	cooldownSkipped := 0
	selected := ""
	for offset := range len(snapshot.Endpoints) {
		index := (start + offset) % len(snapshot.Endpoints)
		endpoint := snapshot.Endpoints[index]
		if _, skip := excluded[endpoint]; skip {
			continue
		}
		if until, unhealthy := s.unhealthyUntil[endpoint]; unhealthy && now.Before(until) {
			cooldownSkipped++
			continue
		}

		s.next = (index + 1) % len(snapshot.Endpoints)
		selected = endpoint
		break
	}
	if s.observer != nil {
		s.observer.SetUnhealthyEndpoints(len(s.unhealthyUntil))
	}
	s.mu.Unlock()

	if s.observer != nil {
		s.observer.RecordCooldownSkipped(cooldownSkipped)
		if selected != "" {
			s.observer.RecordEndpointSelection(EndpointSelectionSelected)
		} else {
			s.observer.RecordEndpointSelection(EndpointSelectionNoAvailable)
		}
	}
	if selected != "" {
		return selected, nil
	}
	return "", ErrNoAvailableEndpoint
}

func (s *endpointSelector) MarkFailure(endpoint string) {
	if s == nil || endpoint == "" || s.cooldown <= 0 {
		return
	}

	s.mu.Lock()
	s.unhealthyUntil[endpoint] = s.now().Add(s.cooldown)
	if s.observer != nil {
		s.observer.SetUnhealthyEndpoints(len(s.unhealthyUntil))
	}
	s.mu.Unlock()
}

func (s *endpointSelector) MarkSuccess(endpoint string) {
	if s == nil || endpoint == "" {
		return
	}

	s.mu.Lock()
	delete(s.unhealthyUntil, endpoint)
	if s.observer != nil {
		s.observer.SetUnhealthyEndpoints(len(s.unhealthyUntil))
	}
	s.mu.Unlock()
}

func (s *endpointSelector) Close() error {
	if s == nil || s.resolver == nil {
		return nil
	}
	s.mu.Lock()
	clear(s.unhealthyUntil)
	if s.observer != nil {
		s.observer.SetUnhealthyEndpoints(0)
	}
	s.mu.Unlock()
	return s.resolver.Close()
}
