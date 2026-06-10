package server

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type clusterBindHandler struct {
	registry     cluster.OnlineRegistry
	gatewayNode  string
	internalAddr string
	logger       *zap.Logger
}

func newClusterRegistry(config Config) (cluster.OnlineRegistry, io.Closer, error) {
	if !config.Cluster.Enabled {
		return nil, nil, nil
	}
	if config.Cluster.InternalAddr == "" {
		return nil, nil, fmt.Errorf("cluster internal addr is required when cluster is enabled")
	}
	if config.OnlineRegistry != nil {
		return config.OnlineRegistry, nil, nil
	}

	switch strings.ToLower(strings.TrimSpace(config.Cluster.Registry.Type)) {
	case "", "memory":
		registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{
			TTL: config.Cluster.Registry.TTL,
		})
		return registry, registry, nil
	case "redis":
		registry, err := cluster.NewRedisRegistry(cluster.RedisRegistryConfig{
			Addr:         config.Cluster.Registry.Redis.Addr,
			Username:     config.Cluster.Registry.Redis.Username,
			Password:     config.Cluster.Registry.Redis.Password,
			DB:           config.Cluster.Registry.Redis.DB,
			KeyPrefix:    config.Cluster.Registry.Redis.KeyPrefix,
			TTL:          config.Cluster.Registry.TTL,
			DialTimeout:  config.Cluster.Registry.Redis.DialTimeout,
			ReadTimeout:  config.Cluster.Registry.Redis.ReadTimeout,
			WriteTimeout: config.Cluster.Registry.Redis.WriteTimeout,
		})
		if err != nil {
			return nil, nil, err
		}

		pingTimeout := config.Cluster.Registry.Redis.DialTimeout
		if pingTimeout <= 0 {
			pingTimeout = time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		defer cancel()
		if err := registry.Ping(ctx); err != nil {
			_ = registry.Close()
			return nil, nil, fmt.Errorf("cluster redis registry ping: %w", err)
		}

		return registry, registry, nil
	default:
		return nil, nil, fmt.Errorf("unsupported cluster registry type %q", config.Cluster.Registry.Type)
	}
}

func newClusterBindHandler(config Config, registry cluster.OnlineRegistry, logger *zap.Logger) pipeline.Handler {
	if registry == nil {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &clusterBindHandler{
		registry:     registry,
		gatewayNode:  config.GatewayNode,
		internalAddr: config.Cluster.InternalAddr,
		logger:       logger,
	}
}

func (h *clusterBindHandler) Handle(ctx *pipeline.Context) error {
	if h == nil || h.registry == nil || ctx == nil || ctx.BindResult == nil || ctx.BindResult.Session == nil {
		return nil
	}

	entry := routeEntryFromSession(ctx.BindResult.Session, h.internalAddr)
	if entry.GatewayNode == "" {
		entry.GatewayNode = h.gatewayNode
	}
	if err := h.registry.Bind(ctx.Context(), entry); err != nil {
		h.logger.Warn(
			"failed to bind cluster route",
			zap.String("session_id", entry.SessionID),
			zap.String("client_id", entry.ClientID),
			zap.String("device_id", entry.DeviceID),
			zap.String("gateway_node", entry.GatewayNode),
			zap.Error(err),
		)
		return pipeline.Reject(protocol.AckRejected, err)
	}

	h.logger.Debug(
		"bound cluster route",
		zap.String("session_id", entry.SessionID),
		zap.String("client_id", entry.ClientID),
		zap.String("device_id", entry.DeviceID),
		zap.String("gateway_node", entry.GatewayNode),
		zap.String("internal_addr", entry.InternalAddr),
	)
	return nil
}

func routeEntryFromSession(session *session.Session, internalAddr string) cluster.RouteEntry {
	if session == nil {
		return cluster.RouteEntry{}
	}

	return cluster.RouteEntry{
		ClientID:     session.ClientID,
		DeviceID:     session.DeviceID,
		SessionID:    session.SessionID,
		GatewayNode:  session.GatewayNode,
		InternalAddr: internalAddr,
		TokenID:      session.TokenID,
		UpdatedAt:    session.LastSeenAt,
	}
}

func unbindClusterRoute(
	ctx context.Context,
	registry cluster.OnlineRegistry,
	session *session.Session,
	logger *zap.Logger,
) {
	if registry == nil || session == nil {
		return
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	key := cluster.RouteKey{
		ClientID: session.ClientID,
		DeviceID: session.DeviceID,
	}
	if err := registry.Unbind(ctx, key, session.SessionID); err != nil {
		logger.Warn(
			"failed to unbind cluster route",
			zap.String("session_id", session.SessionID),
			zap.String("client_id", session.ClientID),
			zap.String("device_id", session.DeviceID),
			zap.String("gateway_node", session.GatewayNode),
			zap.Error(err),
		)
	}
}
