package server

import (
	"log/slog"
	"os"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
)

type Gateway struct {
	server ziface.IServer
}

func New(config Config, logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	if len(config.RouteMsgIDs) == 0 {
		config = DefaultConfig()
	}

	zServer := znet.NewServer()
	router := NewIngressRouter(logger)

	for _, msgID := range config.RouteMsgIDs {
		zServer.AddRouter(msgID, router)
	}

	return &Gateway{server: zServer}
}

func (g *Gateway) Serve() {
	g.server.Serve()
}
