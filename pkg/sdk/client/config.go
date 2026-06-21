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
	defaultWriteTimeout          = 5 * time.Second
	defaultAckTimeout            = 5 * time.Second
	defaultMaxPendingBeforeReady = 128
	defaultInboundBuffer         = 128
	defaultDownlinkDedupCapacity = 10_000
	defaultReconnectInitialDelay = 250 * time.Millisecond
	defaultReconnectMaxDelay     = 30 * time.Second
	defaultReconnectMultiplier   = 2.0
	defaultReconnectJitter       = 0.2
)

// TokenProvider returns the credential used for the next AUTH/BIND attempt.
// It may refresh an expiring token and must honor context cancellation.
type TokenProvider func(context.Context) (string, error)

// Dialer opens a network connection. *net.Dialer satisfies this interface.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// ReconnectConfig enables bounded exponential backoff after an established
// connection is lost. MaxAttempts counts reconnect attempts; zero means no
// attempt limit.
type ReconnectConfig struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	Jitter       float64
	MaxAttempts  int
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
	WriteTimeout          time.Duration
	AckTimeout            time.Duration
	MaxFramePayloadSize   uint32
	MaxBodySize           uint32
	MaxPendingBeforeReady int
	InboundBuffer         int

	// DownlinkHandler consumes non-ACK gateway packets. When it is configured,
	// Receive is disabled so a packet cannot be consumed by two APIs.
	DownlinkHandler DownlinkHandler
	// ManualDownlinkAck prevents successful handlers from automatically sending
	// a delivered ACK. Applications can call AcknowledgeDownlink after durable
	// business processing completes.
	ManualDownlinkAck bool
	// DownlinkDedupCapacity bounds handled MessageIDs retained in memory.
	DownlinkDedupCapacity int
	// OnDownlinkError observes handler and automatic ACK failures. It must return
	// promptly and must not panic.
	OnDownlinkError func(error)

	// Reconnect enables automatic reconnect when non-nil.
	Reconnect *ReconnectConfig
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
	writeTimeout          time.Duration
	ackTimeout            time.Duration
	maxFramePayloadSize   uint32
	maxBodySize           uint32
	maxPendingBeforeReady int
	inboundBuffer         int
	downlinkHandler       DownlinkHandler
	manualDownlinkAck     bool
	downlinkDedupCapacity int
	onDownlinkError       func(error)
	reconnect             normalizedReconnectConfig
}

type normalizedReconnectConfig struct {
	enabled      bool
	initialDelay time.Duration
	maxDelay     time.Duration
	multiplier   float64
	jitter       float64
	maxAttempts  int
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
	if config.WriteTimeout < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: write timeout cannot be negative", ErrInvalidConfig)
	}
	if config.AckTimeout < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: ACK timeout cannot be negative", ErrInvalidConfig)
	}
	if config.MaxPendingBeforeReady < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: max pending before ready cannot be negative", ErrInvalidConfig)
	}
	if config.InboundBuffer < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: inbound buffer cannot be negative", ErrInvalidConfig)
	}
	if config.DownlinkDedupCapacity < 0 {
		return normalizedConfig{}, fmt.Errorf("%w: downlink dedup capacity cannot be negative", ErrInvalidConfig)
	}
	reconnect, err := normalizeReconnectConfig(config.Reconnect)
	if err != nil {
		return normalizedConfig{}, err
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
	writeTimeout := config.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = defaultWriteTimeout
	}
	ackTimeout := config.AckTimeout
	if ackTimeout == 0 {
		ackTimeout = defaultAckTimeout
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
	inboundBuffer := config.InboundBuffer
	if inboundBuffer == 0 {
		inboundBuffer = defaultInboundBuffer
	}
	downlinkDedupCapacity := config.DownlinkDedupCapacity
	if downlinkDedupCapacity == 0 {
		downlinkDedupCapacity = defaultDownlinkDedupCapacity
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
		writeTimeout:          writeTimeout,
		ackTimeout:            ackTimeout,
		maxFramePayloadSize:   maxFramePayloadSize,
		maxBodySize:           maxBodySize,
		maxPendingBeforeReady: maxPendingBeforeReady,
		inboundBuffer:         inboundBuffer,
		downlinkHandler:       config.DownlinkHandler,
		manualDownlinkAck:     config.ManualDownlinkAck,
		downlinkDedupCapacity: downlinkDedupCapacity,
		onDownlinkError:       config.OnDownlinkError,
		reconnect:             reconnect,
	}, nil
}

func normalizeReconnectConfig(config *ReconnectConfig) (normalizedReconnectConfig, error) {
	if config == nil {
		return normalizedReconnectConfig{}, nil
	}
	if config.InitialDelay < 0 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect initial delay cannot be negative", ErrInvalidConfig)
	}
	if config.MaxDelay < 0 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect max delay cannot be negative", ErrInvalidConfig)
	}
	if config.Multiplier < 0 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect multiplier cannot be negative", ErrInvalidConfig)
	}
	if config.Jitter < 0 || config.Jitter > 1 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect jitter must be between 0 and 1", ErrInvalidConfig)
	}
	if config.MaxAttempts < 0 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect max attempts cannot be negative", ErrInvalidConfig)
	}

	initialDelay := config.InitialDelay
	if initialDelay == 0 {
		initialDelay = defaultReconnectInitialDelay
	}
	maxDelay := config.MaxDelay
	if maxDelay == 0 {
		maxDelay = defaultReconnectMaxDelay
	}
	if maxDelay < initialDelay {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect max delay cannot be less than initial delay", ErrInvalidConfig)
	}
	multiplier := config.Multiplier
	if multiplier == 0 {
		multiplier = defaultReconnectMultiplier
	}
	if multiplier < 1 {
		return normalizedReconnectConfig{}, fmt.Errorf("%w: reconnect multiplier must be at least 1", ErrInvalidConfig)
	}
	jitter := config.Jitter
	if jitter == 0 {
		jitter = defaultReconnectJitter
	}

	return normalizedReconnectConfig{
		enabled:      true,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
		multiplier:   multiplier,
		jitter:       jitter,
		maxAttempts:  config.MaxAttempts,
	}, nil
}
