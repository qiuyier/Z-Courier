package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

func newInternalHTTPServer(config Config, logger *zap.Logger, service *downlink.Service) *http.Server {
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" || service == nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle("/internal/push", downlink.NewHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	mux.Handle("/internal/push/batch", downlink.NewBatchHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	mux.Handle("/internal/message/status", downlink.NewStatusHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	mux.Handle("/internal/messages", downlink.NewMessageListHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	mux.Handle("/internal/message/requeue", downlink.NewRequeueHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	mux.Handle("/internal/message/discard", downlink.NewDiscardHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      config.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		Logger:             logger,
	}))
	if config.Cluster.Enabled {
		mux.Handle(downlink.PeerPushPath, downlink.NewPeerHandler(downlink.PeerHandlerConfig{
			Service:            service,
			GatewayNode:        config.GatewayNode,
			PeerToken:          config.Cluster.Peer.Token,
			MaxRequestBodySize: config.InternalMaxRequestBodySize,
			Logger:             logger,
		}))
	}
	mux.Handle("/metrics", metrics.Handler())

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
