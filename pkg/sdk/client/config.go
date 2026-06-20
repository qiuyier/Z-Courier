package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const (
	defaultConnectTimeout        = 5 * time.Second
	defaultBindTimeout           = 5 * time.Second
	defaultMaxPendingBeforeReady = 128
)

// TokenProvider returns the credential used for the next AUTH/BIND attempt.
// It may refresh an expiring token and must honor context cancellation.
type TokenProvider func(context.Context) (string, error)

// Dialer opens a network connection. *net.Dialer satisfies this interface.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Config configures a Client.
type Config struct {
	Address  string
	Network  string
	ClientID string
	DeviceID string

	// Configure exactly one of Token or TokenProvider.
	Token         string
	TokenProvider TokenProvider

	Dialer                Dialer
	ConnectTimeout        time.Duration
	BindTimeout           time.Duration
	MaxFramePayloadSize   uint32
	MaxBodySize           uint32
	MaxPendingBeforeReady int
}

type normalizedConfig struct {
	address               string
	network               string
	clientID              string
	deviceID              string
	tokenProvider         TokenProvider
	dialer                Dialer
	connectTimeout        time.Duration
	bindTimeout           time.Duration
	maxFramePayloadSize   uint32
	maxBodySize           uint32
	maxPendingBeforeReady int
}

func normalizeConfig(config Config) (normalizedConfig, error) {
	if strings.TrimSpace(config.Address) == "" {
		return normalizedConfig{}, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(config.ClientID) == "" {
		return normalizedConfig{}, fmt.Errorf("%w: client ID is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(config.DeviceID) == "" {
		return normalizedConfig{}, fmt.Errorf("%w: device ID is required", ErrInvalidConfig)
	}
	if config.Token != "" && config.TokenProvider != nil {
		return normalizedConfig{}, fmt.Errorf("%w: token and token provider are mutually exclusive", ErrInvalidConfig)
	}
	if config.Token == "" && config.TokenProvider == nil {
		return normalizedConfig{}, fmt.Errorf("%w: token or token provider is required", ErrInvalidConfig)
	}
	if config.ConnectTimeout < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: connect timeout cannot be negative", ErrInvalidConfig)
	}
	if config.BindTimeout < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: bind timeout cannot be negative", ErrInvalidConfig)
	}
	if config.MaxPendingBeforeReady < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: max pending before ready cannot be negative", ErrInvalidConfig)
	}

	network := config.Network
	if network == "" {
		network = "tcp"
	}
	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	connectTimeout := config.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultConnectTimeout
	}
	bindTimeout := config.BindTimeout
	if bindTimeout == 0 {
		bindTimeout = defaultBindTimeout
	}
	maxFramePayloadSize := config.MaxFramePayloadSize
	if maxFramePayloadSize == 0 {
		maxFramePayloadSize = DefaultMaxFramePayloadSize
	}
	maxBodySize := config.MaxBodySize
	if maxBodySize == 0 {
		maxBodySize = protocol.DefaultMaxBodySize
	}
	maxPendingBeforeReady := config.MaxPendingBeforeReady
	if maxPendingBeforeReady == 0 {
		maxPendingBeforeReady = defaultMaxPendingBeforeReady
	}

	tokenProvider := config.TokenProvider
	if tokenProvider == nil {
		token := config.Token
		tokenProvider = func(context.Context) (string, error) {
			return token, nil
		}
	}

	return normalizedConfig{
		address:               config.Address,
		network:               network,
		clientID:              config.ClientID,
		deviceID:              config.DeviceID,
		tokenProvider:         tokenProvider,
		dialer:                dialer,
		connectTimeout:        connectTimeout,
		bindTimeout:           bindTimeout,
		maxFramePayloadSize:   maxFramePayloadSize,
		maxBodySize:           maxBodySize,
		maxPendingBeforeReady: maxPendingBeforeReady,
	}, nil
}
