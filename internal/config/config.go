package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/server"
	"gopkg.in/yaml.v3"
)

const (
	DefaultPath = "configs/z-courier.yaml"
	EnvPathKey  = "ZCOURIER_CONFIG"
)

type File struct {
	GatewayNode string   `yaml:"gateway_node"`
	RouteMsgIDs []uint32 `yaml:"route_msg_ids"`

	Auth         AuthConfig         `yaml:"auth"`
	Cluster      ClusterConfig      `yaml:"cluster"`
	InternalHTTP InternalHTTPConfig `yaml:"internal_http"`
	Downlink     DownlinkConfig     `yaml:"downlink"`
	Upstream     UpstreamConfig     `yaml:"upstream"`
	Pipeline     PipelineConfig     `yaml:"pipeline"`
}

type AuthConfig struct {
	StaticTokens map[string]StaticTokenConfig `yaml:"static_tokens"`
}

type StaticTokenConfig struct {
	ClientID string   `yaml:"client_id"`
	TokenID  string   `yaml:"token_id"`
	Subject  string   `yaml:"subject"`
	Scopes   []string `yaml:"scopes"`
}

type InternalHTTPConfig struct {
	Enabled            *bool   `yaml:"enabled"`
	Addr               *string `yaml:"addr"`
	Token              *string `yaml:"token"`
	MaxRequestBodySize *int64  `yaml:"max_request_body_size"`
}

type ClusterConfig struct {
	Enabled              bool                  `yaml:"enabled"`
	InternalAddr         string                `yaml:"internal_addr"`
	RouteRefreshInterval string                `yaml:"route_refresh_interval"`
	Registry             ClusterRegistryConfig `yaml:"registry"`
	Peer                 ClusterPeerConfig     `yaml:"peer"`
}

type ClusterRegistryConfig struct {
	Type  string             `yaml:"type"`
	TTL   string             `yaml:"ttl"`
	Redis ClusterRedisConfig `yaml:"redis"`
}

type ClusterRedisConfig struct {
	Addr         string `yaml:"addr"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	DB           int    `yaml:"db"`
	KeyPrefix    string `yaml:"key_prefix"`
	DialTimeout  string `yaml:"dial_timeout"`
	ReadTimeout  string `yaml:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout"`
}

type ClusterPeerConfig struct {
	Token   string `yaml:"token"`
	Timeout string `yaml:"timeout"`
}

type UpstreamConfig struct {
	Routes []UpstreamRouteConfig `yaml:"routes"`
}

type DownlinkConfig struct {
	Storage  DownlinkStorageConfig  `yaml:"storage"`
	Delivery DownlinkDeliveryConfig `yaml:"delivery"`
}

type DownlinkStorageConfig struct {
	Type     string                 `yaml:"type"`
	Postgres DownlinkPostgresConfig `yaml:"postgres"`
}

type DownlinkPostgresConfig struct {
	DSN             string `yaml:"dsn"`
	AutoMigrate     *bool  `yaml:"auto_migrate"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
}

type DownlinkDeliveryConfig struct {
	RetryInterval  string `yaml:"retry_interval"`
	RetryDelay     string `yaml:"retry_delay"`
	AckTimeout     string `yaml:"ack_timeout"`
	RetryLease     string `yaml:"retry_lease"`
	MaxAttempts    int    `yaml:"max_attempts"`
	ScanLimit      int    `yaml:"scan_limit"`
	BindFlushLimit int    `yaml:"bind_flush_limit"`
}

type PipelineConfig struct {
	Allowlist PolicyListConfig `yaml:"allowlist"`
	Blocklist PolicyListConfig `yaml:"blocklist"`
	RateLimit RateLimitConfig  `yaml:"rate_limit"`
}

type PolicyListConfig struct {
	ClientIDs []string `yaml:"client_ids"`
	MsgIDs    []uint32 `yaml:"msg_ids"`
}

type RateLimitConfig struct {
	Enabled     bool   `yaml:"enabled"`
	MaxRequests int    `yaml:"max_requests"`
	Window      string `yaml:"window"`
}

type UpstreamRouteConfig struct {
	Name     string       `yaml:"name"`
	Enabled  *bool        `yaml:"enabled"`
	MsgIDMin uint32       `yaml:"msg_id_min"`
	MsgIDMax uint32       `yaml:"msg_id_max"`
	Target   TargetConfig `yaml:"target"`
}

type TargetConfig struct {
	Type          string   `yaml:"type"`
	URL           string   `yaml:"url"`
	Token         string   `yaml:"token"`
	Timeout       string   `yaml:"timeout"`
	Addr          string   `yaml:"addr"`
	NSQDAddrs     []string `yaml:"nsqd_addrs"`
	Topic         string   `yaml:"topic"`
	AuthSecret    string   `yaml:"auth_secret"`
	DialTimeout   string   `yaml:"dial_timeout"`
	ReadTimeout   string   `yaml:"read_timeout"`
	WriteTimeout  string   `yaml:"write_timeout"`
	PublishMode   string   `yaml:"publish_mode"`
	RetryAttempts int      `yaml:"retry_attempts"`
}

func ResolvePath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue := os.Getenv(EnvPathKey); envValue != "" {
		return envValue
	}

	return DefaultPath
}

func LoadServerConfig(path string) (server.Config, error) {
	fileConfig, err := Load(path)
	if err != nil {
		return server.Config{}, err
	}

	return fileConfig.ToServerConfig()
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var fileConfig File
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fileConfig); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	return &fileConfig, nil
}

func (c *File) ToServerConfig() (server.Config, error) {
	out := server.DefaultConfig()

	if c.GatewayNode != "" {
		out.GatewayNode = c.GatewayNode
	}
	if len(c.RouteMsgIDs) > 0 {
		out.RouteMsgIDs = append([]uint32(nil), c.RouteMsgIDs...)
	}
	if c.Auth.StaticTokens != nil {
		out.Verifier = auth.NewStaticTokenVerifier(toPrincipals(c.Auth.StaticTokens))
	}

	applyInternalHTTPConfig(&out, c.InternalHTTP)
	if err := applyClusterConfig(&out, c.Cluster); err != nil {
		return server.Config{}, err
	}
	if err := applyDownlinkConfig(&out, c.Downlink); err != nil {
		return server.Config{}, err
	}
	pipelineConfig, err := toPipelineConfig(c.Pipeline)
	if err != nil {
		return server.Config{}, err
	}
	out.Pipeline = pipelineConfig

	routes, err := toUpstreamRoutes(c.Upstream.Routes)
	if err != nil {
		return server.Config{}, err
	}
	out.UpstreamRoutes = routes

	return out, nil
}

func toPrincipals(tokens map[string]StaticTokenConfig) map[string]auth.Principal {
	principals := make(map[string]auth.Principal, len(tokens))
	for token, tokenConfig := range tokens {
		tokenID := tokenConfig.TokenID
		if tokenID == "" {
			tokenID = token
		}

		principals[token] = auth.Principal{
			ClientID: tokenConfig.ClientID,
			TokenID:  tokenID,
			Subject:  tokenConfig.Subject,
			Scopes:   append([]string(nil), tokenConfig.Scopes...),
		}
	}

	return principals
}

func applyInternalHTTPConfig(out *server.Config, config InternalHTTPConfig) {
	if config.Enabled != nil {
		out.DisableInternalHTTP = !*config.Enabled
	}
	if config.Addr != nil {
		out.InternalHTTPAddr = *config.Addr
	}
	if config.Token != nil {
		out.InternalToken = *config.Token
	}
	if config.MaxRequestBodySize != nil {
		out.InternalMaxRequestBodySize = *config.MaxRequestBodySize
	}
}

func applyClusterConfig(out *server.Config, config ClusterConfig) error {
	out.Cluster.Enabled = config.Enabled
	if config.InternalAddr != "" {
		out.Cluster.InternalAddr = config.InternalAddr
	}

	registryType := strings.ToLower(strings.TrimSpace(config.Registry.Type))
	if registryType != "" {
		switch registryType {
		case "memory", "redis":
			out.Cluster.Registry.Type = registryType
		default:
			return fmt.Errorf("config: unsupported cluster registry type %q", config.Registry.Type)
		}
	}

	ttl, err := parseOptionalPositiveDuration(config.Registry.TTL)
	if err != nil {
		return fmt.Errorf("config: cluster registry ttl: %w", err)
	}
	if ttl > 0 {
		out.Cluster.Registry.TTL = ttl
	}

	routeRefreshInterval, err := parseOptionalPositiveDuration(config.RouteRefreshInterval)
	if err != nil {
		return fmt.Errorf("config: cluster route_refresh_interval: %w", err)
	}
	if routeRefreshInterval > 0 {
		out.Cluster.RouteRefreshInterval = routeRefreshInterval
	} else if config.Registry.TTL != "" {
		out.Cluster.RouteRefreshInterval = server.DefaultClusterRouteRefreshInterval(out.Cluster.Registry.TTL)
	}

	redis := config.Registry.Redis
	if redis.DB < 0 {
		return fmt.Errorf("config: cluster registry redis db must be greater than or equal to 0")
	}
	if redis.Addr != "" {
		out.Cluster.Registry.Redis.Addr = redis.Addr
	}
	if redis.Username != "" {
		out.Cluster.Registry.Redis.Username = redis.Username
	}
	if redis.Password != "" {
		out.Cluster.Registry.Redis.Password = redis.Password
	}
	if redis.KeyPrefix != "" {
		out.Cluster.Registry.Redis.KeyPrefix = redis.KeyPrefix
	}
	out.Cluster.Registry.Redis.DB = redis.DB

	dialTimeout, err := parseOptionalPositiveDuration(redis.DialTimeout)
	if err != nil {
		return fmt.Errorf("config: cluster registry redis dial_timeout: %w", err)
	}
	readTimeout, err := parseOptionalPositiveDuration(redis.ReadTimeout)
	if err != nil {
		return fmt.Errorf("config: cluster registry redis read_timeout: %w", err)
	}
	writeTimeout, err := parseOptionalPositiveDuration(redis.WriteTimeout)
	if err != nil {
		return fmt.Errorf("config: cluster registry redis write_timeout: %w", err)
	}
	if dialTimeout > 0 {
		out.Cluster.Registry.Redis.DialTimeout = dialTimeout
	}
	if readTimeout > 0 {
		out.Cluster.Registry.Redis.ReadTimeout = readTimeout
	}
	if writeTimeout > 0 {
		out.Cluster.Registry.Redis.WriteTimeout = writeTimeout
	}

	if config.Peer.Token != "" {
		out.Cluster.Peer.Token = config.Peer.Token
	}
	peerTimeout, err := parseOptionalPositiveDuration(config.Peer.Timeout)
	if err != nil {
		return fmt.Errorf("config: cluster peer timeout: %w", err)
	}
	if peerTimeout > 0 {
		out.Cluster.Peer.Timeout = peerTimeout
	}

	if out.Cluster.Enabled && out.Cluster.InternalAddr == "" {
		return fmt.Errorf("config: cluster internal_addr is required when cluster is enabled")
	}
	if out.Cluster.Enabled && out.Cluster.Registry.Type == "redis" && out.Cluster.Registry.Redis.Addr == "" {
		return fmt.Errorf("config: cluster redis addr is required when redis registry is enabled")
	}

	return nil
}

func applyDownlinkConfig(out *server.Config, config DownlinkConfig) error {
	if config.Storage.Type != "" {
		storageType := strings.ToLower(strings.TrimSpace(config.Storage.Type))
		switch storageType {
		case "memory", "postgres", "none", "disabled":
			out.DownlinkStorage.Type = storageType
		default:
			return fmt.Errorf("config: unsupported downlink storage type %q", config.Storage.Type)
		}
	}

	postgres := config.Storage.Postgres
	if postgres.DSN != "" {
		out.DownlinkStorage.Postgres.DSN = postgres.DSN
	}
	if postgres.AutoMigrate != nil {
		out.DownlinkStorage.Postgres.AutoMigrate = *postgres.AutoMigrate
		out.DownlinkStorage.Postgres.AutoMigrateSet = true
	}
	if postgres.MaxOpenConns < 0 {
		return fmt.Errorf("config: downlink postgres max_open_conns must be greater than or equal to 0")
	}
	if postgres.MaxIdleConns < 0 {
		return fmt.Errorf("config: downlink postgres max_idle_conns must be greater than or equal to 0")
	}
	out.DownlinkStorage.Postgres.MaxOpenConns = postgres.MaxOpenConns
	out.DownlinkStorage.Postgres.MaxIdleConns = postgres.MaxIdleConns

	connMaxLifetime, err := parseOptionalDuration(postgres.ConnMaxLifetime)
	if err != nil {
		return fmt.Errorf("config: downlink postgres conn_max_lifetime: %w", err)
	}
	out.DownlinkStorage.Postgres.ConnMaxLifetime = connMaxLifetime

	if out.DownlinkStorage.Type == "postgres" && out.DownlinkStorage.Postgres.DSN == "" {
		return fmt.Errorf("config: downlink postgres dsn is required")
	}

	delivery := config.Delivery
	retryInterval, err := parseOptionalDuration(delivery.RetryInterval)
	if err != nil {
		return fmt.Errorf("config: downlink delivery retry_interval: %w", err)
	}
	retryDelay, err := parseOptionalDuration(delivery.RetryDelay)
	if err != nil {
		return fmt.Errorf("config: downlink delivery retry_delay: %w", err)
	}
	ackTimeout, err := parseOptionalPositiveDuration(delivery.AckTimeout)
	if err != nil {
		return fmt.Errorf("config: downlink delivery ack_timeout: %w", err)
	}
	retryLease, err := parseOptionalPositiveDuration(delivery.RetryLease)
	if err != nil {
		return fmt.Errorf("config: downlink delivery retry_lease: %w", err)
	}
	if retryInterval > 0 {
		out.DownlinkDelivery.RetryInterval = retryInterval
	}
	if retryDelay > 0 {
		out.DownlinkDelivery.RetryDelay = retryDelay
	}
	if ackTimeout > 0 {
		out.DownlinkDelivery.AckTimeout = ackTimeout
	}
	if retryLease > 0 {
		out.DownlinkDelivery.RetryLease = retryLease
	}
	if delivery.MaxAttempts < 0 {
		return fmt.Errorf("config: downlink delivery max_attempts must be greater than or equal to 0")
	}
	if delivery.ScanLimit < 0 {
		return fmt.Errorf("config: downlink delivery scan_limit must be greater than or equal to 0")
	}
	if delivery.BindFlushLimit < 0 {
		return fmt.Errorf("config: downlink delivery bind_flush_limit must be greater than or equal to 0")
	}
	if delivery.MaxAttempts > 0 {
		out.DownlinkDelivery.MaxAttempts = delivery.MaxAttempts
	}
	if delivery.ScanLimit > 0 {
		out.DownlinkDelivery.ScanLimit = delivery.ScanLimit
	}
	if delivery.BindFlushLimit > 0 {
		out.DownlinkDelivery.BindFlushLimit = delivery.BindFlushLimit
	}

	return nil
}

func toUpstreamRoutes(routes []UpstreamRouteConfig) ([]server.UpstreamRouteConfig, error) {
	out := make([]server.UpstreamRouteConfig, 0, len(routes))
	for _, route := range routes {
		if route.Enabled != nil && !*route.Enabled {
			continue
		}
		if err := validateMsgIDRange(route); err != nil {
			return nil, err
		}

		targetType := route.Target.Type
		if targetType == "" {
			targetType = "http"
		}

		switch targetType {
		case "http":
			httpConfig, err := toHTTPUpstreamConfig(route)
			if err != nil {
				return nil, err
			}

			out = append(out, server.UpstreamRouteConfig{
				Name:     route.Name,
				MsgIDMin: route.MsgIDMin,
				MsgIDMax: route.MsgIDMax,
				HTTP:     httpConfig,
			})
		case "nsq":
			nsqConfig, err := toNSQUpstreamConfig(route)
			if err != nil {
				return nil, err
			}

			out = append(out, server.UpstreamRouteConfig{
				Name:     route.Name,
				MsgIDMin: route.MsgIDMin,
				MsgIDMax: route.MsgIDMax,
				NSQ:      nsqConfig,
			})
		default:
			return nil, fmt.Errorf("config: unsupported upstream target type %q for route %q", targetType, route.Name)
		}
	}

	return out, nil
}

func validateMsgIDRange(route UpstreamRouteConfig) error {
	if route.MsgIDMin == 0 {
		return fmt.Errorf("config: route %q msg_id_min must be greater than 0", route.Name)
	}
	if route.MsgIDMax != 0 && route.MsgIDMax < route.MsgIDMin {
		return fmt.Errorf("config: route %q msg_id_max must be greater than or equal to msg_id_min", route.Name)
	}

	return nil
}

func toPipelineConfig(config PipelineConfig) (pipeline.Config, error) {
	window, err := parseOptionalDuration(config.RateLimit.Window)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("config: pipeline rate_limit window: %w", err)
	}
	if config.RateLimit.Enabled {
		if config.RateLimit.MaxRequests <= 0 {
			return pipeline.Config{}, fmt.Errorf("config: pipeline rate_limit max_requests must be greater than 0")
		}
		if window <= 0 {
			return pipeline.Config{}, fmt.Errorf("config: pipeline rate_limit window must be greater than 0")
		}
	}

	return pipeline.Config{
		Policy: pipeline.PolicyConfig{
			AllowClientIDs: append([]string(nil), config.Allowlist.ClientIDs...),
			BlockClientIDs: append([]string(nil), config.Blocklist.ClientIDs...),
			AllowMsgIDs:    append([]uint32(nil), config.Allowlist.MsgIDs...),
			BlockMsgIDs:    append([]uint32(nil), config.Blocklist.MsgIDs...),
		},
		RateLimit: pipeline.RateLimitConfig{
			Enabled:     config.RateLimit.Enabled,
			MaxRequests: config.RateLimit.MaxRequests,
			Window:      window,
		},
	}, nil
}

func toHTTPUpstreamConfig(route UpstreamRouteConfig) (*server.HTTPUpstreamConfig, error) {
	if route.Target.URL == "" {
		return nil, fmt.Errorf("config: route %q http target url is required", route.Name)
	}

	timeout, err := parseOptionalDuration(route.Target.Timeout)
	if err != nil {
		return nil, fmt.Errorf("config: route %q target timeout: %w", route.Name, err)
	}

	return &server.HTTPUpstreamConfig{
		URL:     route.Target.URL,
		Token:   route.Target.Token,
		Timeout: timeout,
	}, nil
}

func toNSQUpstreamConfig(route UpstreamRouteConfig) (*server.NSQUpstreamConfig, error) {
	addresses, err := normalizeNSQDAddrs(route.Target)
	if err != nil {
		return nil, fmt.Errorf("config: route %q nsq target: %w", route.Name, err)
	}
	if route.Target.Topic == "" {
		return nil, fmt.Errorf("config: route %q nsq target topic is required", route.Name)
	}
	if route.Target.PublishMode != "" && route.Target.PublishMode != "round_robin" {
		return nil, fmt.Errorf("config: route %q nsq target unsupported publish_mode %q", route.Name, route.Target.PublishMode)
	}
	if route.Target.RetryAttempts < 0 {
		return nil, fmt.Errorf("config: route %q nsq target retry_attempts must be greater than or equal to 0", route.Name)
	}
	if route.Target.Timeout != "" {
		return nil, fmt.Errorf("config: route %q nsq target uses write_timeout/read_timeout/dial_timeout instead of timeout", route.Name)
	}

	dialTimeout, err := parseOptionalDuration(route.Target.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("config: route %q target dial_timeout: %w", route.Name, err)
	}
	readTimeout, err := parseOptionalDuration(route.Target.ReadTimeout)
	if err != nil {
		return nil, fmt.Errorf("config: route %q target read_timeout: %w", route.Name, err)
	}
	writeTimeout, err := parseOptionalDuration(route.Target.WriteTimeout)
	if err != nil {
		return nil, fmt.Errorf("config: route %q target write_timeout: %w", route.Name, err)
	}

	return &server.NSQUpstreamConfig{
		Address:       firstAddress(addresses),
		Addresses:     addresses,
		Topic:         route.Target.Topic,
		AuthSecret:    route.Target.AuthSecret,
		DialTimeout:   dialTimeout,
		ReadTimeout:   readTimeout,
		WriteTimeout:  writeTimeout,
		PublishMode:   route.Target.PublishMode,
		RetryAttempts: route.Target.RetryAttempts,
	}, nil
}

func normalizeNSQDAddrs(target TargetConfig) ([]string, error) {
	raw := target.NSQDAddrs
	if len(raw) == 0 && target.Addr != "" {
		raw = []string{target.Addr}
	}

	seen := make(map[string]struct{}, len(raw))
	addresses := make([]string, 0, len(raw))
	for _, address := range raw {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("addr or nsqd_addrs is required")
	}

	return addresses, nil
}

func firstAddress(addresses []string) string {
	if len(addresses) == 0 {
		return ""
	}

	return addresses[0]
}

func parseOptionalDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}

	return time.ParseDuration(raw)
}

func parseOptionalPositiveDuration(raw string) (time.Duration, error) {
	duration, err := parseOptionalDuration(raw)
	if err != nil {
		return 0, err
	}
	if raw != "" && duration <= 0 {
		return 0, fmt.Errorf("must be greater than 0")
	}
	return duration, nil
}
