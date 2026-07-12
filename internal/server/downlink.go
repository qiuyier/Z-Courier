package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

func newDownlinkService(
	config Config,
	connManager ziface.IConnManager,
	registry cluster.OnlineRegistry,
	policies *downlink.DeliveryPolicySet,
) (*downlink.Service, io.Closer, error) {
	if config.DisableInternalHTTP || config.InternalHTTPAddr == "" {
		return nil, nil, nil
	}

	var peerDispatcher downlink.PeerDispatcher
	if registry != nil {
		peerConfig := downlink.HTTPPeerDispatcherConfig{
			Token:               config.Cluster.Peer.Token,
			Timeout:             config.Cluster.Peer.Timeout,
			MaxResponseBodySize: config.InternalMaxRequestBodySize,
		}
		if config.Cluster.Peer.Auth.Mode == ClusterPeerAuthModeHMAC {
			secret := config.Cluster.Peer.Auth.HMAC.Keys[config.Cluster.Peer.Auth.HMAC.KeyID]
			peerConfig.Token = ""
			peerConfig.HMAC = &signing.SignerConfig{
				KeyID:  config.Cluster.Peer.Auth.HMAC.KeyID,
				Secret: secret,
			}
		}
		var err error
		peerDispatcher, err = downlink.NewHTTPPeerDispatcher(peerConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("cluster peer dispatcher: %w", err)
		}
	}

	store, closer, err := newDownlinkStore(config)
	if err != nil {
		return nil, nil, err
	}
	publisher, publisherCloser, err := newTerminalPublisher(config)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, err
	}
	if publisher != nil {
		if _, ok := store.(downlink.TerminalStore); !ok {
			if publisherCloser != nil {
				_ = publisherCloser.Close()
			}
			if closer != nil {
				_ = closer.Close()
			}
			return nil, nil, fmt.Errorf("downlink terminal publisher requires a terminal-capable store")
		}
	}

	options := make([]downlink.ServiceOption, 0, 3)
	if store != nil {
		options = append(options, downlink.WithStore(store))
	}
	options = append(options,
		downlink.WithRetryDelay(config.DownlinkDelivery.RetryDelay),
		downlink.WithRetryJitter(config.DownlinkDelivery.RetryJitter),
		downlink.WithAckTimeout(config.DownlinkDelivery.AckTimeout),
		downlink.WithRetryClaim(config.GatewayNode, config.DownlinkDelivery.RetryLease),
		downlink.WithMaxAttempts(config.DownlinkDelivery.MaxAttempts),
		downlink.WithDeliveryPolicies(policies),
		downlink.WithTerminalPublishing(downlink.TerminalPublishingConfig{
			Publisher:         publisher,
			GatewayNode:       config.GatewayNode,
			RetryDelay:        config.DownlinkTerminal.RetryDelay,
			RetryJitter:       config.DownlinkTerminal.RetryJitter,
			BackoffMultiplier: config.DownlinkTerminal.BackoffMultiplier,
			MaxRetryDelay:     config.DownlinkTerminal.MaxRetryDelay,
			ClaimOwner:        config.GatewayNode,
			ClaimLease:        config.DownlinkTerminal.RetryLease,
		}),
	)
	if registry != nil {
		options = append(options, downlink.WithClusterDelivery(downlink.ClusterDeliveryConfig{
			GatewayNode:    config.GatewayNode,
			Registry:       registry,
			PeerDispatcher: peerDispatcher,
		}))
	}

	return downlink.NewService(config.Sessions, zinxConnectionFinder{connManager: connManager}, options...), downlinkResourceGroup{
		publisherCloser,
		closer,
	}, nil
}

type downlinkResourceGroup []io.Closer

func (group downlinkResourceGroup) Close() error {
	var joined error
	for _, closer := range group {
		if closer == nil {
			continue
		}
		joined = errors.Join(joined, closer.Close())
	}
	return joined
}

func newDownlinkPolicySet(config Config) (*downlink.DeliveryPolicySet, error) {
	defaultPolicy := downlink.DeliveryPolicy{
		Name:              downlink.DefaultDeliveryPolicyName,
		MaxAttempts:       config.DownlinkDelivery.MaxAttempts,
		AckTimeout:        config.DownlinkDelivery.AckTimeout,
		InitialRetryDelay: config.DownlinkDelivery.RetryDelay,
		BackoffMultiplier: 1,
		MaxRetryDelay:     config.DownlinkDelivery.RetryDelay,
		RetryJitter:       config.DownlinkDelivery.RetryJitter,
	}
	set, err := downlink.NewDeliveryPolicySet(defaultPolicy, config.DownlinkPolicies)
	if err != nil {
		return nil, fmt.Errorf("downlink delivery policies: %w", err)
	}
	return set, nil
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
