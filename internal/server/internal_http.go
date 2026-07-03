package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/capacity"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

func newInternalHTTPServer(config Config, logger *zap.Logger, service *downlink.Service, health *gatewayHealth, registry cluster.OnlineRegistry, runtime *gatewayRuntime) (*http.Server, error) {
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" || service == nil {
		return nil, nil
	}
	switch config.InternalHTTPAuth.Mode {
	case InternalHTTPAuthModeToken, InternalHTTPAuthModeHMAC:
	default:
		return nil, fmt.Errorf("unsupported internal HTTP auth mode %q", config.InternalHTTPAuth.Mode)
	}

	mux := http.NewServeMux()
	handlerConfig := config
	if config.InternalHTTPAuth.Mode == InternalHTTPAuthModeHMAC {
		handlerConfig.InternalToken = ""
	}
	adminSessions := newAdminSessionManager(config.AdminConsole.Session)
	adminSessionConfig := newAdminSessionHTTPConfig(handlerConfig, adminSessions)
	withConsoleSession := func(handler http.Handler) http.Handler {
		return withAdminSessionAuth(handler, adminSessions, adminSessionConfig.sessionConfig, handlerConfig.InternalToken)
	}
	withConsolePermission := func(handler http.Handler, permission string) http.Handler {
		return withConsoleSession(withAdminPermission(handler, permission, logger))
	}
	pushLimiter := capacity.NewLimiter(config.InternalPushMaxInFlight)
	mux.Handle("/healthz", newHealthHandler())
	mux.Handle("/readyz", newReadyHandler(health))
	mux.Handle("/internal/push", downlink.NewHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		PushLimiter:        pushLimiter,
		Logger:             logger,
	}))
	mux.Handle("/internal/push/batch", downlink.NewBatchHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		PushLimiter:        pushLimiter,
		Logger:             logger,
	}))
	mux.Handle("/internal/message/status", withConsolePermission(downlink.NewStatusHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		Logger:             logger,
	}), adminPermissionRead))
	mux.Handle("/internal/messages", withConsolePermission(downlink.NewMessageListHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		Logger:             logger,
	}), adminPermissionRead))
	mux.Handle("/internal/message/requeue", withConsolePermission(downlink.NewRequeueHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		Logger:             logger,
	}), adminPermissionMessageRepair))
	mux.Handle("/internal/message/discard", withConsolePermission(downlink.NewDiscardHandler(downlink.HandlerConfig{
		Service:            service,
		InternalToken:      handlerConfig.InternalToken,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		GatewayNode:        config.GatewayNode,
		Logger:             logger,
	}), adminPermissionMessageRepair))
	if adminSessions != nil {
		mux.Handle(adminSessionLoginPath, newAdminSessionLoginHandler(adminSessionConfig))
		mux.Handle(adminSessionMePath, newAdminSessionMeHandler(adminSessionConfig))
		mux.Handle(adminSessionLogoutPath, newAdminSessionLogoutHandler(adminSessionConfig))
	}
	mux.Handle("/internal/admin/overview", withConsolePermission(newAdminOverviewHandler(handlerConfig, health, registry), adminPermissionRead))
	mux.Handle("/internal/admin/routes", withConsolePermission(newAdminRoutesHandler(handlerConfig), adminPermissionRead))
	mux.Handle("/internal/admin/diagnostics", withConsolePermission(newAdminDiagnosticsHandler(handlerConfig, health, registry, runtime, service.HasStore()), adminPermissionRead))
	mux.Handle("/internal/admin/check", withConsolePermission(newAdminCheckHandler(handlerConfig, service, registry), adminPermissionRead))
	mux.Handle("/internal/admin/diagnose", withConsolePermission(newAdminDiagnoseHandler(handlerConfig, health, registry, runtime, service), adminPermissionRead))
	mux.Handle("/internal/debug/route", withConsolePermission(newDebugRouteHandler(handlerConfig, registry), adminPermissionRead))
	mux.Handle("/internal/debug/sessions", withConsolePermission(newDebugSessionsHandler(handlerConfig), adminPermissionRead))
	mux.Handle("/internal/debug/session/disconnect", withConsolePermission(newDebugSessionDisconnectHandler(handlerConfig, service.ConnectionFinder(), logger), adminPermissionSessionDisconnect))
	if config.AdminConsole.Enabled {
		mux.Handle(config.AdminConsole.Path, newAdminConsoleHandler(config.AdminConsole))
	}
	if config.Cluster.Enabled {
		switch config.Cluster.Peer.Auth.Mode {
		case ClusterPeerAuthModeToken, ClusterPeerAuthModeHMAC:
		default:
			return nil, fmt.Errorf("unsupported cluster peer auth mode %q", config.Cluster.Peer.Auth.Mode)
		}
		peerToken := config.Cluster.Peer.Token
		if config.Cluster.Peer.Auth.Mode == ClusterPeerAuthModeHMAC {
			peerToken = ""
		}
		var peerHandler http.Handler = downlink.NewPeerHandler(downlink.PeerHandlerConfig{
			Service:            service,
			GatewayNode:        config.GatewayNode,
			PeerToken:          peerToken,
			MaxRequestBodySize: config.InternalMaxRequestBodySize,
			Logger:             logger,
		})
		if config.Cluster.Peer.Auth.Mode == ClusterPeerAuthModeHMAC {
			var err error
			peerHandler, err = newPeerHMACHandler(peerHandler, config.Cluster.Peer.Auth.HMAC, config.InternalMaxRequestBodySize, logger)
			if err != nil {
				return nil, fmt.Errorf("cluster peer HMAC: %w", err)
			}
		}
		mux.Handle(downlink.PeerPushPath, peerHandler)
	}
	mux.Handle("/metrics", metrics.Handler())

	var handler http.Handler = mux
	if config.InternalHTTPAuth.Mode == InternalHTTPAuthModeHMAC {
		var err error
		hmacHandler, err := newInternalHMACHandler(mux, config, logger)
		if err != nil {
			return nil, fmt.Errorf("internal HTTP HMAC: %w", err)
		}
		handler = newAdminSessionHMACBypassHandler(mux, hmacHandler, adminSessions, adminSessionConfig.sessionConfig)
	}

	return &http.Server{
		Addr:              config.InternalHTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
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
