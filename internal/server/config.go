package server

import (
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/session"
)

type Config struct {
	RouteMsgIDs []uint32
	Verifier    auth.Verifier
	Sessions    *session.Manager
	GatewayNode string
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
		Sessions:    session.NewManager(),
		GatewayNode: "local",
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

	return config
}
