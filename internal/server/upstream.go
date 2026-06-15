package server

import (
	"context"
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/nsqforwarder"
	"github.com/qiuyier/Z-Courier/internal/capacity"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
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
		return httpforwarder.New(httpforwarder.Config{
			URL:     routeConfig.HTTP.URL,
			Token:   routeConfig.HTTP.Token,
			Timeout: routeConfig.HTTP.Timeout,
		}), nil
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
			Status:     "overloaded",
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
