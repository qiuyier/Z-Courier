package server

import (
	"time"

	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"github.com/qiuyier/Z-Courier/internal/tlsconfig"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const (
	InternalHTTPAuthModeToken = "token"
	InternalHTTPAuthModeHMAC  = "hmac"
)

type Config struct {
	RouteMsgIDs                []uint32
	Verifier                   auth.Verifier
	Sessions                   *session.Manager
	GatewayNode                string
	Cluster                    ClusterConfig
	OnlineRegistry             cluster.OnlineRegistry
	DisableInternalHTTP        bool
	InternalHTTPAddr           string
	InternalToken              string
	InternalHTTPAuth           InternalHTTPAuthConfig
	InternalMaxRequestBodySize int64
	InternalPushMaxInFlight    int
	AdminConsole               AdminConsoleConfig
	AdminSessions              *adminSessionManager
	AdminAudit                 adminaudit.Trail
	AdminAuditStorage          AdminAuditStorageConfig
	UpstreamRoutes             []UpstreamRouteConfig
	UpstreamRuntime            *UpstreamRuntime
	Pipeline                   pipeline.Config
	DownlinkStore              downlink.Store
	DownlinkStorage            DownlinkStorageConfig
	DownlinkDelivery           DownlinkDeliveryConfig
	DownlinkPolicies           []downlink.DeliveryPolicyRule
	DownlinkCapacity           downlink.QueueCapacity
	DownlinkTerminal           DownlinkTerminalConfig
	DownlinkRetention          DownlinkRetentionConfig
}

type InternalHTTPAuthConfig struct {
	Mode string
	HMAC InternalHTTPHMACConfig
}

type InternalHTTPHMACConfig struct {
	Keys            map[string][]byte
	MaxClockSkew    time.Duration
	NonceTTL        time.Duration
	MaxNonceEntries int
}

type AdminConsoleConfig struct {
	Enabled    bool
	Path       string
	AssetsDir  string
	Monitoring AdminConsoleMonitoringConfig
	Session    AdminConsoleSessionConfig
}

type AdminConsoleMonitoringConfig struct {
	PrometheusURL string
	GrafanaURL    string
	DashboardURL  string
}

type AdminConsoleSessionConfig struct {
	Enabled        bool
	TTL            time.Duration
	CookieName     string
	CookieSecure   bool
	CookieSameSite string
	Role           string
	Store          AdminSessionStoreConfig
}

type AdminSessionStoreConfig struct {
	Type  string
	Redis AdminSessionRedisConfig
}

type AdminSessionRedisConfig struct {
	Addr             string
	Username         string
	Password         string
	DB               int
	KeyPrefix        string
	DialTimeout      time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	OperationTimeout time.Duration
}

type AdminAuditStorageConfig struct {
	Type     string
	Capacity int
	Postgres AdminAuditPostgresConfig
}

type AdminAuditPostgresConfig struct {
	DSN              string
	AutoMigrate      bool
	AutoMigrateSet   bool
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	OperationTimeout time.Duration
}

type UpstreamRouteConfig struct {
	Name        string
	MsgIDMin    uint32
	MsgIDMax    uint32
	MaxInFlight int
	HTTP        *HTTPUpstreamConfig
	NSQ         *NSQUpstreamConfig
}

type HTTPUpstreamConfig struct {
	URL       string
	Path      string
	Token     string
	Timeout   time.Duration
	Discovery HTTPUpstreamDiscoveryConfig
	Failover  HTTPUpstreamFailoverConfig
}

type HTTPUpstreamDiscoveryConfig struct {
	Type            string
	Endpoints       []string
	Scheme          string
	Hostname        string
	Port            int
	RefreshInterval time.Duration
}

type HTTPUpstreamFailoverConfig struct {
	Enabled           bool
	MaxAttempts       int
	UnhealthyCooldown time.Duration
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
	RetryJitter    time.Duration
	AckTimeout     time.Duration
	RetryLease     time.Duration
	MaxAttempts    int
	ScanLimit      int
	BindFlushLimit int
	RetryFairness  downlink.RetryFairness
}

type DownlinkTerminalConfig struct {
	PublisherType     string
	NSQ               NSQUpstreamConfig
	HTTP              DownlinkTerminalHTTPConfig
	RetryInterval     time.Duration
	RetryDelay        time.Duration
	RetryJitter       time.Duration
	BackoffMultiplier float64
	MaxRetryDelay     time.Duration
	RetryLease        time.Duration
	ScanLimit         int
}

type DownlinkTerminalHTTPConfig struct {
	URL               string
	Timeout           time.Duration
	HMACKeyID         string
	HMACSecret        []byte
	AllowInsecureHTTP bool
	TLS               tlsconfig.Files
}

type DownlinkRetentionConfig struct {
	DeliveredTTL    time.Duration
	FailedTTL       time.Duration
	DiscardedTTL    time.Duration
	CleanupInterval time.Duration
	CleanupLimit    int
}

type ClusterConfig struct {
	Enabled              bool
	InternalAddr         string
	RouteRefreshInterval time.Duration
	Registry             ClusterRegistryConfig
	Peer                 ClusterPeerConfig
}

type ClusterRegistryConfig struct {
	Type  string
	TTL   time.Duration
	Redis ClusterRedisConfig
}

type ClusterRedisConfig struct {
	Addr         string
	Username     string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type ClusterPeerConfig struct {
	Token   string
	Timeout time.Duration
	Auth    ClusterPeerAuthConfig
}

const (
	ClusterPeerAuthModeToken = "token"
	ClusterPeerAuthModeHMAC  = "hmac"
)

type ClusterPeerAuthConfig struct {
	Mode string
	HMAC ClusterPeerHMACConfig
}

type ClusterPeerHMACConfig struct {
	KeyID           string
	Keys            map[string][]byte
	MaxClockSkew    time.Duration
	NonceTTL        time.Duration
	MaxNonceEntries int
}

func DefaultConfig() Config {
	return Config{
		RouteMsgIDs: []uint32{protocol.MsgIDBind},
		Verifier: auth.NewObservedVerifier(auth.NewStaticTokenVerifier(map[string]auth.Principal{
			"dev-token": {
				ClientID: "dev-client",
				TokenID:  "dev-token",
				Scopes:   []string{"gateway:dev"},
			},
		})),
		Sessions:         session.NewManager(),
		GatewayNode:      "local",
		Cluster:          DefaultClusterConfig(),
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "dev-internal-token",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeToken,
			HMAC: InternalHTTPHMACConfig{
				MaxClockSkew:    signing.DefaultMaxClockSkew,
				NonceTTL:        signing.DefaultNonceTTL,
				MaxNonceEntries: signing.DefaultMaxNonceEntries,
			},
		},
		InternalMaxRequestBodySize: 10 << 20,
		InternalPushMaxInFlight:    1000,
		AdminConsole: AdminConsoleConfig{
			Path:      "/console/",
			AssetsDir: "web/admin/dist",
			Session: AdminConsoleSessionConfig{
				TTL:            8 * time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleAdmin,
				Store: AdminSessionStoreConfig{
					Type: "memory",
					Redis: AdminSessionRedisConfig{
						KeyPrefix:        defaultAdminSessionRedisKeyPrefix,
						DialTimeout:      time.Second,
						ReadTimeout:      time.Second,
						WriteTimeout:     time.Second,
						OperationTimeout: 2 * time.Second,
					},
				},
			},
		},
		AdminAuditStorage: AdminAuditStorageConfig{
			Type:     "memory",
			Capacity: adminaudit.DefaultCapacity,
			Postgres: AdminAuditPostgresConfig{
				AutoMigrate:      true,
				AutoMigrateSet:   true,
				OperationTimeout: 2 * time.Second,
			},
		},
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
			RetryJitter:    0,
			AckTimeout:     30 * time.Second,
			RetryLease:     30 * time.Second,
			MaxAttempts:    5,
			ScanLimit:      100,
			BindFlushLimit: 100,
			RetryFairness: downlink.RetryFairness{
				CandidateMultiplier: downlink.DefaultRetryFairnessCandidateMultiplier,
			},
		},
		DownlinkTerminal: DownlinkTerminalConfig{
			PublisherType: downlink.TerminalPublisherNone,
			HTTP: DownlinkTerminalHTTPConfig{
				Timeout: 5 * time.Second,
			},
			RetryInterval:     5 * time.Second,
			RetryDelay:        30 * time.Second,
			RetryJitter:       0,
			BackoffMultiplier: 2,
			MaxRetryDelay:     5 * time.Minute,
			RetryLease:        30 * time.Second,
			ScanLimit:         100,
		},
		DownlinkRetention: DownlinkRetentionConfig{
			DeliveredTTL:    24 * time.Hour,
			FailedTTL:       7 * 24 * time.Hour,
			DiscardedTTL:    7 * 24 * time.Hour,
			CleanupInterval: time.Hour,
			CleanupLimit:    1000,
		},
	}
}

func DefaultClusterConfig() ClusterConfig {
	ttl := 30 * time.Second

	return ClusterConfig{
		RouteRefreshInterval: clusterRouteRefreshInterval(ttl),
		Registry: ClusterRegistryConfig{
			Type: "memory",
			TTL:  ttl,
			Redis: ClusterRedisConfig{
				KeyPrefix:    "zcourier",
				DialTimeout:  time.Second,
				ReadTimeout:  time.Second,
				WriteTimeout: time.Second,
			},
		},
		Peer: ClusterPeerConfig{
			Timeout: 2 * time.Second,
			Auth: ClusterPeerAuthConfig{
				Mode: ClusterPeerAuthModeToken,
				HMAC: ClusterPeerHMACConfig{
					MaxClockSkew:    signing.DefaultMaxClockSkew,
					NonceTTL:        signing.DefaultNonceTTL,
					MaxNonceEntries: signing.DefaultMaxNonceEntries,
				},
			},
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
	if config.Cluster.Registry.Type == "" {
		config.Cluster.Registry.Type = defaults.Cluster.Registry.Type
	}
	if config.Cluster.Registry.TTL <= 0 {
		config.Cluster.Registry.TTL = defaults.Cluster.Registry.TTL
	}
	if config.Cluster.Registry.Redis.KeyPrefix == "" {
		config.Cluster.Registry.Redis.KeyPrefix = defaults.Cluster.Registry.Redis.KeyPrefix
	}
	if config.Cluster.Registry.Redis.DialTimeout <= 0 {
		config.Cluster.Registry.Redis.DialTimeout = defaults.Cluster.Registry.Redis.DialTimeout
	}
	if config.Cluster.Registry.Redis.ReadTimeout <= 0 {
		config.Cluster.Registry.Redis.ReadTimeout = defaults.Cluster.Registry.Redis.ReadTimeout
	}
	if config.Cluster.Registry.Redis.WriteTimeout <= 0 {
		config.Cluster.Registry.Redis.WriteTimeout = defaults.Cluster.Registry.Redis.WriteTimeout
	}
	if config.Cluster.RouteRefreshInterval <= 0 {
		config.Cluster.RouteRefreshInterval = clusterRouteRefreshInterval(config.Cluster.Registry.TTL)
	}
	if config.Cluster.Peer.Timeout <= 0 {
		config.Cluster.Peer.Timeout = defaults.Cluster.Peer.Timeout
	}
	if config.Cluster.Peer.Auth.Mode == "" {
		config.Cluster.Peer.Auth.Mode = defaults.Cluster.Peer.Auth.Mode
	}
	if config.Cluster.Peer.Auth.Mode == ClusterPeerAuthModeHMAC {
		config.Cluster.Peer.Token = ""
	}
	if config.Cluster.Peer.Auth.HMAC.MaxClockSkew <= 0 {
		config.Cluster.Peer.Auth.HMAC.MaxClockSkew = defaults.Cluster.Peer.Auth.HMAC.MaxClockSkew
	}
	if config.Cluster.Peer.Auth.HMAC.NonceTTL <= 0 {
		config.Cluster.Peer.Auth.HMAC.NonceTTL = defaults.Cluster.Peer.Auth.HMAC.NonceTTL
	}
	if config.Cluster.Peer.Auth.HMAC.MaxNonceEntries <= 0 {
		config.Cluster.Peer.Auth.HMAC.MaxNonceEntries = defaults.Cluster.Peer.Auth.HMAC.MaxNonceEntries
	}
	if config.InternalHTTPAddr == "" {
		config.InternalHTTPAddr = defaults.InternalHTTPAddr
	}
	if config.InternalHTTPAuth.Mode == "" {
		config.InternalHTTPAuth.Mode = defaults.InternalHTTPAuth.Mode
	}
	if config.InternalHTTPAuth.Mode == InternalHTTPAuthModeHMAC {
		config.InternalToken = ""
	} else if config.InternalToken == "" {
		config.InternalToken = defaults.InternalToken
	}
	if config.InternalHTTPAuth.HMAC.MaxClockSkew <= 0 {
		config.InternalHTTPAuth.HMAC.MaxClockSkew = defaults.InternalHTTPAuth.HMAC.MaxClockSkew
	}
	if config.InternalHTTPAuth.HMAC.NonceTTL <= 0 {
		config.InternalHTTPAuth.HMAC.NonceTTL = defaults.InternalHTTPAuth.HMAC.NonceTTL
	}
	if config.InternalHTTPAuth.HMAC.MaxNonceEntries <= 0 {
		config.InternalHTTPAuth.HMAC.MaxNonceEntries = defaults.InternalHTTPAuth.HMAC.MaxNonceEntries
	}
	if config.InternalMaxRequestBodySize == 0 {
		config.InternalMaxRequestBodySize = defaults.InternalMaxRequestBodySize
	}
	if config.InternalPushMaxInFlight <= 0 {
		config.InternalPushMaxInFlight = defaults.InternalPushMaxInFlight
	}
	if config.AdminConsole.Path == "" {
		config.AdminConsole.Path = defaults.AdminConsole.Path
	}
	if config.AdminConsole.AssetsDir == "" {
		config.AdminConsole.AssetsDir = defaults.AdminConsole.AssetsDir
	}
	if config.AdminConsole.Session.TTL <= 0 {
		config.AdminConsole.Session.TTL = defaults.AdminConsole.Session.TTL
	}
	if config.AdminConsole.Session.CookieName == "" {
		config.AdminConsole.Session.CookieName = defaults.AdminConsole.Session.CookieName
	}
	if config.AdminConsole.Session.CookieSameSite == "" {
		config.AdminConsole.Session.CookieSameSite = defaults.AdminConsole.Session.CookieSameSite
	}
	config.AdminConsole.Session.Role = normalizeAdminRole(config.AdminConsole.Session.Role)
	if config.AdminConsole.Session.Store.Type == "" {
		config.AdminConsole.Session.Store.Type = defaults.AdminConsole.Session.Store.Type
	}
	if config.AdminConsole.Session.Store.Redis.KeyPrefix == "" {
		config.AdminConsole.Session.Store.Redis.KeyPrefix = defaults.AdminConsole.Session.Store.Redis.KeyPrefix
	}
	if config.AdminConsole.Session.Store.Redis.DialTimeout <= 0 {
		config.AdminConsole.Session.Store.Redis.DialTimeout = defaults.AdminConsole.Session.Store.Redis.DialTimeout
	}
	if config.AdminConsole.Session.Store.Redis.ReadTimeout <= 0 {
		config.AdminConsole.Session.Store.Redis.ReadTimeout = defaults.AdminConsole.Session.Store.Redis.ReadTimeout
	}
	if config.AdminConsole.Session.Store.Redis.WriteTimeout <= 0 {
		config.AdminConsole.Session.Store.Redis.WriteTimeout = defaults.AdminConsole.Session.Store.Redis.WriteTimeout
	}
	if config.AdminConsole.Session.Store.Redis.OperationTimeout <= 0 {
		config.AdminConsole.Session.Store.Redis.OperationTimeout = defaults.AdminConsole.Session.Store.Redis.OperationTimeout
	}
	if config.AdminAuditStorage.Type == "" {
		config.AdminAuditStorage.Type = defaults.AdminAuditStorage.Type
	}
	if config.AdminAuditStorage.Capacity <= 0 {
		config.AdminAuditStorage.Capacity = defaults.AdminAuditStorage.Capacity
	}
	if !config.AdminAuditStorage.Postgres.AutoMigrateSet {
		config.AdminAuditStorage.Postgres.AutoMigrate = defaults.AdminAuditStorage.Postgres.AutoMigrate
		config.AdminAuditStorage.Postgres.AutoMigrateSet = defaults.AdminAuditStorage.Postgres.AutoMigrateSet
	}
	if config.AdminAuditStorage.Postgres.OperationTimeout <= 0 {
		config.AdminAuditStorage.Postgres.OperationTimeout = defaults.AdminAuditStorage.Postgres.OperationTimeout
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
	if config.DownlinkDelivery.RetryJitter < 0 {
		config.DownlinkDelivery.RetryJitter = 0
	}
	if config.DownlinkDelivery.AckTimeout <= 0 {
		config.DownlinkDelivery.AckTimeout = defaults.DownlinkDelivery.AckTimeout
	}
	if config.DownlinkDelivery.RetryLease <= 0 {
		config.DownlinkDelivery.RetryLease = defaults.DownlinkDelivery.RetryLease
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
	if config.DownlinkTerminal.PublisherType == "" {
		config.DownlinkTerminal.PublisherType = defaults.DownlinkTerminal.PublisherType
	}
	if config.DownlinkTerminal.RetryInterval <= 0 {
		config.DownlinkTerminal.RetryInterval = defaults.DownlinkTerminal.RetryInterval
	}
	if config.DownlinkTerminal.RetryDelay <= 0 {
		config.DownlinkTerminal.RetryDelay = defaults.DownlinkTerminal.RetryDelay
	}
	if config.DownlinkTerminal.RetryJitter < 0 {
		config.DownlinkTerminal.RetryJitter = 0
	}
	if config.DownlinkTerminal.BackoffMultiplier < 1 {
		config.DownlinkTerminal.BackoffMultiplier = defaults.DownlinkTerminal.BackoffMultiplier
	}
	if config.DownlinkTerminal.MaxRetryDelay <= 0 {
		config.DownlinkTerminal.MaxRetryDelay = defaults.DownlinkTerminal.MaxRetryDelay
	}
	if config.DownlinkTerminal.RetryLease <= 0 {
		config.DownlinkTerminal.RetryLease = defaults.DownlinkTerminal.RetryLease
	}
	if config.DownlinkTerminal.ScanLimit <= 0 {
		config.DownlinkTerminal.ScanLimit = defaults.DownlinkTerminal.ScanLimit
	}
	if config.DownlinkRetention.DeliveredTTL <= 0 {
		config.DownlinkRetention.DeliveredTTL = defaults.DownlinkRetention.DeliveredTTL
	}
	if config.DownlinkRetention.FailedTTL <= 0 {
		config.DownlinkRetention.FailedTTL = defaults.DownlinkRetention.FailedTTL
	}
	if config.DownlinkRetention.DiscardedTTL <= 0 {
		config.DownlinkRetention.DiscardedTTL = defaults.DownlinkRetention.DiscardedTTL
	}
	if config.DownlinkRetention.CleanupInterval <= 0 {
		config.DownlinkRetention.CleanupInterval = defaults.DownlinkRetention.CleanupInterval
	}
	if config.DownlinkRetention.CleanupLimit <= 0 {
		config.DownlinkRetention.CleanupLimit = defaults.DownlinkRetention.CleanupLimit
	}

	return config
}
