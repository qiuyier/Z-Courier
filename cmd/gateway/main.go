package main

import (
	"os"
	"time"

	"github.com/qiuyier/Z-Courier/internal/server"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	config := server.DefaultConfig()
	configureDevUpstreamRoute(&config)

	gateway := server.New(config, logger)

	logger.Info("starting z-courier gateway")
	gateway.Serve()
}

func configureDevUpstreamRoute(config *server.Config) {
	upstreamURL := os.Getenv("ZCOURIER_UPSTREAM_HTTP_URL")
	if upstreamURL == "" {
		return
	}

	config.UpstreamRoutes = []server.UpstreamRouteConfig{
		{
			Name:     "dev-http-upstream",
			MsgIDMin: 1000,
			MsgIDMax: 1999,
			HTTP: &server.HTTPUpstreamConfig{
				URL:     upstreamURL,
				Token:   os.Getenv("ZCOURIER_UPSTREAM_TOKEN"),
				Timeout: 5 * time.Second,
			},
		},
	}
}
