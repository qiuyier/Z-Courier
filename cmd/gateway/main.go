package main

import (
	"log/slog"
	"os"

	"github.com/qiuyier/Z-Courier/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	gateway := server.New(server.DefaultConfig(), logger)

	logger.Info("starting z-courier gateway")
	gateway.Serve()
}
