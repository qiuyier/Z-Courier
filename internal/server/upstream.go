package server

import (
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/nsqforwarder"
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
			Address:      routeConfig.NSQ.Address,
			Topic:        routeConfig.NSQ.Topic,
			AuthSecret:   routeConfig.NSQ.AuthSecret,
			DialTimeout:  routeConfig.NSQ.DialTimeout,
			ReadTimeout:  routeConfig.NSQ.ReadTimeout,
			WriteTimeout: routeConfig.NSQ.WriteTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("upstream route %q: %w", routeConfig.Name, err)
		}
		return forwarder, nil
	}

	return nil, nil
}
