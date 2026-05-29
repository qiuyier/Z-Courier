package server

import (
	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func newUpstreamEngine(config Config) *router.Engine {
	if len(config.UpstreamRoutes) == 0 {
		return nil
	}

	routes := make([]router.Route, 0, len(config.UpstreamRoutes))
	for _, routeConfig := range config.UpstreamRoutes {
		if routeConfig.HTTP == nil {
			continue
		}

		routes = append(routes, router.Route{
			Name:     routeConfig.Name,
			MsgIDMin: routeConfig.MsgIDMin,
			MsgIDMax: routeConfig.MsgIDMax,
			Forwarder: httpforwarder.New(httpforwarder.Config{
				URL:     routeConfig.HTTP.URL,
				Token:   routeConfig.HTTP.Token,
				Timeout: routeConfig.HTTP.Timeout,
			}),
		})
	}
	if len(routes) == 0 {
		return nil
	}

	return router.NewEngine(routes)
}
