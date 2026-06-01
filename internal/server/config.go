package server

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
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
	Address      string
	Topic        string
	AuthSecret   string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
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

	return config
}
