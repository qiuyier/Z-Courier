package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"go.uber.org/zap"
)

func newInternalHTTPServer(config Config, logger *zap.Logger, connManager ziface.IConnManager) *http.Server {
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" {
		return nil
	}

	service := downlink.NewService(config.Sessions, zinxConnectionFinder{connManager: connManager})
	mux := http.NewServeMux()
	mux.Handle("/internal/push", downlink.NewHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))

	return &http.Server{
		Addr:              config.InternalHTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

type zinxConnectionFinder struct {
	connManager ziface.IConnManager
}

func (f zinxConnectionFinder) Get(connID uint64) (downlink.Connection, error) {
	if f.connManager == nil {
		return nil, fmt.Errorf("zinx connection manager is nil")
	}

	return f.connManager.Get(connID)
}
