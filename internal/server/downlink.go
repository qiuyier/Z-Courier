package server

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

func newDownlinkService(config Config, connManager ziface.IConnManager, registry cluster.OnlineRegistry) (*downlink.Service, io.Closer, error) {
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" {
		return nil, nil, nil
	}

	store, closer, err := newDownlinkStore(config)
	if err != nil {
		return nil, nil, err
	}

	options := make([]downlink.ServiceOption, 0, 3)
	if store != nil {
		options = append(options, downlink.WithStore(store))
	}
	options = append(options,
		downlink.WithRetryDelay(config.DownlinkDelivery.RetryDelay),
		downlink.WithAckTimeout(config.DownlinkDelivery.AckTimeout),
		downlink.WithRetryClaim(config.GatewayNode, config.DownlinkDelivery.RetryLease),
		downlink.WithMaxAttempts(config.DownlinkDelivery.MaxAttempts),
	)
	if registry != nil {
		options = append(options, downlink.WithClusterDelivery(downlink.ClusterDeliveryConfig{
			GatewayNode: config.GatewayNode,
			Registry:    registry,
			PeerDispatcher: downlink.NewHTTPPeerDispatcher(downlink.HTTPPeerDispatcherConfig{
				Token:               config.Cluster.Peer.Token,
				Timeout:             config.Cluster.Peer.Timeout,
				MaxResponseBodySize: config.InternalMaxRequestBodySize,
			}),
		}))
	}

	return downlink.NewService(config.Sessions, zinxConnectionFinder{connManager: connManager}, options...), closer, nil
}

func newDownlinkStore(config Config) (downlink.Store, io.Closer, error) {
	if config.DownlinkStore != nil {
		closer, _ := config.DownlinkStore.(io.Closer)
		return config.DownlinkStore, closer, nil
	}

	switch strings.ToLower(strings.TrimSpace(config.DownlinkStorage.Type)) {
	case "", "memory":
		store := downlink.NewMemoryStore()
		return store, store, nil
	case "none", "disabled":
		return nil, nil, nil
	case "postgres":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		store, err := downlink.NewPostgresStore(ctx, downlink.PostgresStoreConfig{
			DSN:             config.DownlinkStorage.Postgres.DSN,
			AutoMigrate:     config.DownlinkStorage.Postgres.AutoMigrate,
			MaxOpenConns:    config.DownlinkStorage.Postgres.MaxOpenConns,
			MaxIdleConns:    config.DownlinkStorage.Postgres.MaxIdleConns,
			ConnMaxLifetime: config.DownlinkStorage.Postgres.ConnMaxLifetime,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("downlink postgres store: %w", err)
		}

		return store, store, nil
	default:
		return nil, nil, fmt.Errorf("unsupported downlink storage type %q", config.DownlinkStorage.Type)
	}
}
