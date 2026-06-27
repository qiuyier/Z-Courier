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
	checkConfigFlag := flag.Bool("check-config", false, "validate z-courier config and exit without starting the gateway")
	flag.Parse()

	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	configPath := appconfig.ResolvePath(*configPathFlag)
	fileConfig, err := appconfig.Load(configPath)
	if err != nil {
		logger.Fatal("failed to load z-courier config", zap.String("path", configPath), zap.Error(err))
	}
	if *checkConfigFlag {
		report, err := fileConfig.Validate()
		if err != nil {
			logger.Fatal("z-courier config validation failed", zap.String("path", configPath), zap.Error(err))
		}
		for _, warning := range report.Warnings {
			logger.Warn("z-courier config validation warning", zap.String("path", configPath), zap.String("warning", warning))
		}
		logger.Info("z-courier config validation passed", zap.String("path", configPath), zap.Int("warnings", len(report.Warnings)))
		return
	}

	config, err := fileConfig.ToServerConfig()
	if err != nil {
		logger.Fatal("failed to build z-courier config", zap.String("path", configPath), zap.Error(err))
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
