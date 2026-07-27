package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/nsqforwarder"
	"github.com/qiuyier/Z-Courier/internal/capacity"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func newUpstreamEngine(config Config) (*router.Engine, error) {
	if len(config.UpstreamRoutes) == 0 {
		return nil, nil
	}

	routes := make([]router.Route, 0, len(config.UpstreamRoutes))
	for _, routeConfig := range config.UpstreamRoutes {
		forwarder, err := newRouteForwarder(routeConfig)
		if err != nil {
			_ = router.NewEngine(routes).Close()
			return nil, err
		}
		if forwarder == nil {
			continue
		}
		targetType := routeTargetType(routeConfig)
		if routeConfig.HTTP != nil && config.UpstreamRuntime != nil {
			forwarder = newDependencyTrackingForwarder(
				routeConfig.Name,
				targetType,
				config.UpstreamRuntime.ensureRoute(routeConfig.Name, targetType),
				forwarder,
			)
		}
		if routeConfig.MaxInFlight > 0 {
			forwarder = newCapacityForwarder(routeConfig.Name, targetType, routeConfig.MaxInFlight, forwarder)
		}

		routes = append(routes, router.Route{
			Name:      routeConfig.Name,
			MsgIDMin:  routeConfig.MsgIDMin,
			MsgIDMax:  routeConfig.MsgIDMax,
			Forwarder: forwarder,
		})
	}
	if len(routes) == 0 {
		return nil, nil
	}

	return router.NewEngine(routes), nil
}

func newRouteForwarder(routeConfig UpstreamRouteConfig) (router.Forwarder, error) {
	if routeConfig.HTTP != nil {
		switch routeConfig.HTTP.Discovery.Type {
		case "":
			return httpforwarder.New(httpforwarder.Config{
				URL:     routeConfig.HTTP.URL,
				Token:   routeConfig.HTTP.Token,
				Timeout: routeConfig.HTTP.Timeout,
			}), nil
		case "static":
			resolver, err := httpforwarder.NewStaticResolver(routeConfig.HTTP.Discovery.Endpoints)
			if err != nil {
				return nil, fmt.Errorf("upstream route %q: %w", routeConfig.Name, err)
			}
			return newDiscoveredHTTPForwarder(routeConfig, resolver, "", "")
		case "dns":
			discovery := routeConfig.HTTP.Discovery
			resolver, err := httpforwarder.NewDNSResolver(httpforwarder.DNSResolverConfig{
				Scheme:          discovery.Scheme,
				Hostname:        discovery.Hostname,
				Port:            discovery.Port,
				Path:            routeConfig.HTTP.Path,
				RefreshInterval: discovery.RefreshInterval,
				LookupTimeout:   discovery.LookupTimeout,
				Lookup:          discovery.Lookup,
			})
			if err != nil {
				return nil, fmt.Errorf("upstream route %q: %w", routeConfig.Name, err)
			}
			requestHost := net.JoinHostPort(discovery.Hostname, strconv.Itoa(discovery.Port))
			return newDiscoveredHTTPForwarder(routeConfig, resolver, requestHost, discovery.Hostname)
		default:
			return nil, fmt.Errorf("upstream route %q: unsupported HTTP discovery type %q", routeConfig.Name, routeConfig.HTTP.Discovery.Type)
		}
	}

	if routeConfig.NSQ != nil {
		forwarder, err := nsqforwarder.New(nsqforwarder.Config{
			Address:       routeConfig.NSQ.Address,
			Addresses:     routeConfig.NSQ.Addresses,
			Topic:         routeConfig.NSQ.Topic,
			AuthSecret:    routeConfig.NSQ.AuthSecret,
			DialTimeout:   routeConfig.NSQ.DialTimeout,
			ReadTimeout:   routeConfig.NSQ.ReadTimeout,
			WriteTimeout:  routeConfig.NSQ.WriteTimeout,
			PublishMode:   routeConfig.NSQ.PublishMode,
			RetryAttempts: routeConfig.NSQ.RetryAttempts,
		})
		if err != nil {
			return nil, fmt.Errorf("upstream route %q: %w", routeConfig.Name, err)
		}
		return forwarder, nil
	}

	return nil, nil
}

func newDiscoveredHTTPForwarder(routeConfig UpstreamRouteConfig, resolver httpforwarder.Resolver, requestHost, serverName string) (router.Forwarder, error) {
	maxAttempts := 1
	var unhealthyCooldown time.Duration
	if routeConfig.HTTP.Failover.Enabled {
		maxAttempts = routeConfig.HTTP.Failover.MaxAttempts
		unhealthyCooldown = routeConfig.HTTP.Failover.UnhealthyCooldown
	}
	forwarder, err := httpforwarder.NewDiscovered(httpforwarder.DiscoveryConfig{
		Resolver:          resolver,
		Token:             routeConfig.HTTP.Token,
		Timeout:           routeConfig.HTTP.Timeout,
		MaxAttempts:       maxAttempts,
		UnhealthyCooldown: unhealthyCooldown,
		RequestHost:       requestHost,
		ServerName:        serverName,
	})
	if err != nil {
		_ = resolver.Close()
		return nil, fmt.Errorf("upstream route %q: %w", routeConfig.Name, err)
	}
	return forwarder, nil
}

func routeTargetType(routeConfig UpstreamRouteConfig) string {
	switch {
	case routeConfig.HTTP != nil:
		return "http"
	case routeConfig.NSQ != nil:
		return "nsq"
	default:
		return "unknown"
	}
}

type capacityForwarder struct {
	routeName  string
	targetType string
	limiter    *capacity.Limiter
	next       router.Forwarder
}

func newCapacityForwarder(routeName, targetType string, limit int, next router.Forwarder) router.Forwarder {
	return &capacityForwarder{
		routeName:  routeName,
		targetType: targetType,
		limiter:    capacity.NewLimiter(limit),
		next:       next,
	}
}

func (f *capacityForwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if f == nil || f.next == nil {
		return nil, router.ErrRouteNotFound
	}
	if !f.limiter.TryAcquire() {
		metrics.RecordUpstreamOverloadRejected(f.routeName, f.targetType)
		return &router.ForwardResult{
			RouteName:  f.routeName,
			TargetType: f.targetType,
			Status:     resilience.ReasonOverloaded,
		}, router.ErrOverloaded
	}

	metrics.AddUpstreamInFlight(f.routeName, f.targetType, 1)
	defer func() {
		f.limiter.Release()
		metrics.AddUpstreamInFlight(f.routeName, f.targetType, -1)
	}()

	return f.next.Forward(ctx, packet)
}

func (f *capacityForwarder) Close() error {
	closer, ok := f.next.(interface{ Close() error })
	if !ok {
		return nil
	}

	return closer.Close()
}

type dependencyTrackingForwarder struct {
	routeName  string
	targetType string
	tracker    dependencyTracker
	next       router.Forwarder
}

type dependencyTracker interface {
	MarkSuccess() resilience.DependencySnapshot
	MarkFailure(string) resilience.DependencySnapshot
}

func newDependencyTrackingForwarder(routeName, targetType string, tracker dependencyTracker, next router.Forwarder) router.Forwarder {
	metrics.SetUpstreamRouteDegraded(routeName, targetType, false)
	return &dependencyTrackingForwarder{
		routeName:  routeName,
		targetType: targetType,
		tracker:    tracker,
		next:       next,
	}
}

func (f *dependencyTrackingForwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if f == nil || f.next == nil {
		return nil, router.ErrRouteNotFound
	}

	result, err := f.next.Forward(ctx, packet)
	if err != nil {
		f.markFailure(safeUpstreamFailureReason(result, err))
		return result, err
	}

	f.markSuccess()
	return result, nil
}

func (f *dependencyTrackingForwarder) markSuccess() {
	if f.tracker != nil {
		f.tracker.MarkSuccess()
	}
	metrics.SetUpstreamRouteDegraded(f.routeName, f.targetType, false)
}

func (f *dependencyTrackingForwarder) markFailure(reason string) {
	degraded := true
	if f.tracker != nil {
		snapshot := f.tracker.MarkFailure(reason)
		degraded = snapshot.Status != resilience.DependencyStatusHealthy
	}
	metrics.SetUpstreamRouteDegraded(f.routeName, f.targetType, degraded)
}

func (f *dependencyTrackingForwarder) Close() error {
	closer, ok := f.next.(interface{ Close() error })
	if !ok {
		return nil
	}

	return closer.Close()
}

func safeUpstreamFailureReason(result *router.ForwardResult, err error) string {
	if result != nil && result.StatusCode > 0 {
		return "http_status_" + strconv.Itoa(result.StatusCode)
	}
	var forwardErr *router.ForwardError
	if errors.As(err, &forwardErr) && forwardErr != nil && forwardErr.Class != "" {
		return string(forwardErr.Class)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "request_failed"
}
