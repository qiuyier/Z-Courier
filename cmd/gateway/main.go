package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"
	"time"

	appconfig "github.com/qiuyier/Z-Courier/internal/config"
	"github.com/qiuyier/Z-Courier/internal/server"
	"go.uber.org/zap"
)

func main() {
	configPathFlag := flag.String("config", "", "path to z-courier config file")
	flag.Parse()

	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	configPath := appconfig.ResolvePath(*configPathFlag)
	config, err := appconfig.LoadServerConfig(configPath)
	if err != nil {
		logger.Fatal("failed to load z-courier config", zap.String("path", configPath), zap.Error(err))
	}

	gateway, err := server.New(config, logger)
	if err != nil {
		logger.Fatal("failed to create z-courier gateway", zap.Error(err))
	}

	logger.Info("starting z-courier gateway", zap.String("config", configPath))
	gateway.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gateway.Shutdown(shutdownCtx); err != nil {
		logger.Warn("z-courier gateway shutdown completed with errors", zap.Error(err))
	}
}
