package httpforwarder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultDNSLookupTimeout = 5 * time.Second

type HostLookup interface {
	LookupHost(context.Context, string) ([]string, error)
}

type HostLookupFunc func(context.Context, string) ([]string, error)

func (f HostLookupFunc) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return f(ctx, hostname)
}

type DNSResolverConfig struct {
	Scheme          string
	Hostname        string
	Port            int
	Path            string
	RefreshInterval time.Duration
	LookupTimeout   time.Duration
	Lookup          HostLookup
}

type DNSResolver struct {
	scheme          string
	hostname        string
	port            int
	path            string
	refreshInterval time.Duration
	lookupTimeout   time.Duration
	lookup          HostLookup

	mu        sync.RWMutex
	endpoints []string
	lastErr   error

	refreshMu    sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	initialOnce  sync.Once
	initialReady chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
}

func NewDNSResolver(config DNSResolverConfig) (*DNSResolver, error) {
	scheme := strings.ToLower(strings.TrimSpace(config.Scheme))
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("http forwarder: dns resolver scheme must be http or https")
	}
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(config.Hostname)), ".")
	if hostname == "" || strings.ContainsAny(hostname, "/:@?#") {
		return nil, fmt.Errorf("http forwarder: dns resolver hostname is invalid")
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf("http forwarder: dns resolver port must be between 1 and 65535")
	}
	pathValue := strings.TrimSpace(config.Path)
	if pathValue == "" {
		pathValue = "/"
	}
	parsedPath, err := url.ParseRequestURI(pathValue)
	if err != nil || !strings.HasPrefix(pathValue, "/") || strings.HasPrefix(pathValue, "//") ||
		parsedPath.RawQuery != "" || parsedPath.Fragment != "" {
		return nil, fmt.Errorf("http forwarder: dns resolver path must be absolute without query or fragment")
	}
	if config.RefreshInterval <= 0 {
		return nil, fmt.Errorf("http forwarder: dns resolver refresh interval must be greater than 0")
	}
	lookupTimeout := config.LookupTimeout
	if lookupTimeout <= 0 {
		lookupTimeout = defaultDNSLookupTimeout
	}
	lookup := config.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver
	}

	ctx, cancel := context.WithCancel(context.Background())
	resolver := &DNSResolver{
		scheme:          scheme,
		hostname:        hostname,
		port:            config.Port,
		path:            pathValue,
		refreshInterval: config.RefreshInterval,
		lookupTimeout:   lookupTimeout,
		lookup:          lookup,
		ctx:             ctx,
		cancel:          cancel,
		initialReady:    make(chan struct{}),
		done:            make(chan struct{}),
	}
	go resolver.run()
	return resolver, nil
}

func (r *DNSResolver) Resolve(ctx context.Context) (EndpointSnapshot, error) {
	if r == nil {
		return EndpointSnapshot{}, ErrNoAvailableEndpoint
	}
	if err := ctx.Err(); err != nil {
		return EndpointSnapshot{}, err
	}
	select {
	case <-r.initialReady:
	case <-ctx.Done():
		return EndpointSnapshot{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return EndpointSnapshot{}, err
	}

	r.mu.RLock()
	endpoints := append([]string(nil), r.endpoints...)
	lastErr := r.lastErr
	r.mu.RUnlock()
	if len(endpoints) > 0 {
		return EndpointSnapshot{Endpoints: endpoints}, nil
	}
	if lastErr != nil {
		return EndpointSnapshot{}, errors.Join(ErrNoAvailableEndpoint, lastErr)
	}
	return EndpointSnapshot{}, ErrNoAvailableEndpoint
}

func (r *DNSResolver) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.cancel()
		<-r.done
	})
	return nil
}

func (r *DNSResolver) run() {
	defer close(r.done)

	_ = r.refresh(r.ctx)
	r.initialOnce.Do(func() {
		close(r.initialReady)
	})

	timer := time.NewTimer(r.refreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			_ = r.refresh(r.ctx)
			timer.Reset(r.refreshInterval)
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *DNSResolver) refresh(parent context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	ctx, cancel := context.WithTimeout(parent, r.lookupTimeout)
	defer cancel()
	addresses, err := r.lookup.LookupHost(ctx, r.hostname)
	if err != nil {
		wrapped := fmt.Errorf("http forwarder: dns lookup failed: %w", err)
		r.recordFailure(wrapped)
		return wrapped
	}

	endpoints := r.buildEndpoints(addresses)
	if len(endpoints) == 0 {
		err := fmt.Errorf("http forwarder: dns lookup returned no usable addresses")
		r.recordFailure(err)
		return err
	}

	r.mu.Lock()
	r.endpoints = endpoints
	r.lastErr = nil
	r.mu.Unlock()
	return nil
}

func (r *DNSResolver) recordFailure(err error) {
	r.mu.Lock()
	r.lastErr = err
	r.mu.Unlock()
}

func (r *DNSResolver) buildEndpoints(addresses []string) []string {
	seen := make(map[string]struct{}, len(addresses))
	endpoints := make([]string, 0, len(addresses))
	for _, address := range addresses {
		ip := net.ParseIP(strings.TrimSpace(address))
		if ip == nil {
			continue
		}
		endpoint := r.scheme + "://" + net.JoinHostPort(ip.String(), strconv.Itoa(r.port)) + r.path
		if _, exists := seen[endpoint]; exists {
			continue
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	return endpoints
}
