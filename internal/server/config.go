package server

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/session"
)

type Config struct {
	RouteMsgIDs                []uint32
	Verifier                   auth.Verifier
	Sessions                   *session.Manager
	GatewayNode                string
	DisableInternalHTTP        bool
	InternalHTTPAddr           string
	InternalToken              string
	InternalMaxRequestBodySize int64
	UpstreamRoutes             []UpstreamRouteConfig
	Pipeline                   pipeline.Config
	DownlinkStore              downlink.Store
	DownlinkStorage            DownlinkStorageConfig
	DownlinkDelivery           DownlinkDeliveryConfig
}

type UpstreamRouteConfig struct {
	Name     string
	MsgIDMin uint32
	MsgIDMax uint32
	HTTP     *HTTPUpstreamConfig
	NSQ      *NSQUpstreamConfig
}

type HTTPUpstreamConfig struct {
	URL     string
	Token   string
	Timeout time.Duration
}

type NSQUpstreamConfig struct {
	Address       string
	Addresses     []string
	Topic         string
	AuthSecret    string
	DialTimeout   time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	PublishMode   string
	RetryAttempts int
}

type DownlinkStorageConfig struct {
	Type     string
	Postgres DownlinkPostgresConfig
}

type DownlinkPostgresConfig struct {
	DSN             string
	AutoMigrate     bool
	AutoMigrateSet  bool
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type DownlinkDeliveryConfig struct {
	RetryInterval  time.Duration
	RetryDelay     time.Duration
	MaxAttempts    int
	ScanLimit      int
	BindFlushLimit int
}

func DefaultConfig() Config {
	return Config{
		RouteMsgIDs: []uint32{1000},
		Verifier: auth.NewStaticTokenVerifier(map[string]auth.Principal{
			"dev-token": {
				ClientID: "dev-client",
				TokenID:  "dev-token",
				Scopes:   []string{"gateway:dev"},
			},
		}),
		Sessions:                   session.NewManager(),
		GatewayNode:                "local",
		InternalHTTPAddr:           "127.0.0.1:18080",
		InternalToken:              "dev-internal-token",
		InternalMaxRequestBodySize: 10 << 20,
		DownlinkStorage: DownlinkStorageConfig{
			Type: "memory",
			Postgres: DownlinkPostgresConfig{
				AutoMigrate:    true,
				AutoMigrateSet: true,
			},
		},
		DownlinkDelivery: DownlinkDeliveryConfig{
			RetryInterval:  5 * time.Second,
			RetryDelay:     30 * time.Second,
			MaxAttempts:    5,
			ScanLimit:      100,
			BindFlushLimit: 100,
		},
	}
}

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()

	if len(config.RouteMsgIDs) == 0 {
		config.RouteMsgIDs = defaults.RouteMsgIDs
	}
	if config.Verifier == nil {
		config.Verifier = defaults.Verifier
	}
	if config.Sessions == nil {
		config.Sessions = defaults.Sessions
	}
	if config.GatewayNode == "" {
		config.GatewayNode = defaults.GatewayNode
	}
	if config.InternalHTTPAddr == "" {
		config.InternalHTTPAddr = defaults.InternalHTTPAddr
	}
	if config.InternalToken == "" {
		config.InternalToken = defaults.InternalToken
	}
	if config.InternalMaxRequestBodySize == 0 {
		config.InternalMaxRequestBodySize = defaults.InternalMaxRequestBodySize
	}
	if config.DownlinkStorage.Type == "" {
		config.DownlinkStorage.Type = defaults.DownlinkStorage.Type
	}
	if !config.DownlinkStorage.Postgres.AutoMigrateSet {
		config.DownlinkStorage.Postgres.AutoMigrate = defaults.DownlinkStorage.Postgres.AutoMigrate
		config.DownlinkStorage.Postgres.AutoMigrateSet = defaults.DownlinkStorage.Postgres.AutoMigrateSet
	}
	if config.DownlinkDelivery.RetryInterval <= 0 {
		config.DownlinkDelivery.RetryInterval = defaults.DownlinkDelivery.RetryInterval
	}
	if config.DownlinkDelivery.RetryDelay <= 0 {
		config.DownlinkDelivery.RetryDelay = defaults.DownlinkDelivery.RetryDelay
	}
	if config.DownlinkDelivery.MaxAttempts <= 0 {
		config.DownlinkDelivery.MaxAttempts = defaults.DownlinkDelivery.MaxAttempts
	}
	if config.DownlinkDelivery.ScanLimit <= 0 {
		config.DownlinkDelivery.ScanLimit = defaults.DownlinkDelivery.ScanLimit
	}
	if config.DownlinkDelivery.BindFlushLimit <= 0 {
		config.DownlinkDelivery.BindFlushLimit = defaults.DownlinkDelivery.BindFlushLimit
	}

	return config
}
