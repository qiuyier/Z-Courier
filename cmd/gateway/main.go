package main

import (
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

	gateway := server.New(server.DefaultConfig(), logger)

	logger.Info("starting z-courier gateway")
	gateway.Serve()
}
