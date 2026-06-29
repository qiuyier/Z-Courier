package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/server"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
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
	Type         string                       `yaml:"type"`
	StaticTokens map[string]StaticTokenConfig `yaml:"static_tokens"`
	HTTP         HTTPAuthConfig               `yaml:"http"`
	JWT          JWTAuthConfig                `yaml:"jwt"`
	Cache        AuthCacheConfig              `yaml:"cache"`
}

type StaticTokenConfig struct {
	ClientID string   `yaml:"client_id"`
	TokenID  string   `yaml:"token_id"`
	Subject  string   `yaml:"subject"`
	Scopes   []string `yaml:"scopes"`
}

type HTTPAuthConfig struct {
	URL           string `yaml:"url"`
	InternalToken string `yaml:"internal_token"`
	Timeout       string `yaml:"timeout"`
	MaxInFlight   int    `yaml:"max_in_flight"`
}

type AuthCacheConfig struct {
	Enabled     bool   `yaml:"enabled"`
	MaxEntries  int    `yaml:"max_entries"`
	PositiveTTL string `yaml:"positive_ttl"`
	NegativeTTL string `yaml:"negative_ttl"`
}

type JWTAuthConfig struct {
	Issuer              string   `yaml:"issuer"`
	Audience            string   `yaml:"audience"`
	JWKSURL             string   `yaml:"jwks_url"`
	Algorithms          []string `yaml:"algorithms"`
	ClientIDClaim       string   `yaml:"client_id_claim"`
	TokenIDClaim        string   `yaml:"token_id_claim"`
	ScopesClaim         string   `yaml:"scopes_claim"`
	ClockSkew           string   `yaml:"clock_skew"`
	RefreshInterval     string   `yaml:"refresh_interval"`
	FetchTimeout        string   `yaml:"fetch_timeout"`
	MaxResponseBodySize int64    `yaml:"max_response_body_size"`
}

type InternalHTTPConfig struct {
	Enabled            *bool                  `yaml:"enabled"`
	Addr               *string                `yaml:"addr"`
	Token              *string                `yaml:"token"`
	Auth               InternalHTTPAuthConfig `yaml:"auth"`
	MaxRequestBodySize *int64                 `yaml:"max_request_body_size"`
	MaxInFlight        int                    `yaml:"max_in_flight"`
}

type InternalHTTPAuthConfig struct {
	Mode string                 `yaml:"mode"`
	HMAC InternalHTTPHMACConfig `yaml:"hmac"`
}

type InternalHTTPHMACConfig struct {
	Keys            map[string]string `yaml:"keys"`
	MaxClockSkew    string            `yaml:"max_clock_skew"`
	NonceTTL        string            `yaml:"nonce_ttl"`
	MaxNonceEntries int               `yaml:"max_nonce_entries"`
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
	Token   string                `yaml:"token"`
	Timeout string                `yaml:"timeout"`
	Auth    ClusterPeerAuthConfig `yaml:"auth"`
}

type ClusterPeerAuthConfig struct {
	Mode string                `yaml:"mode"`
	HMAC ClusterPeerHMACConfig `yaml:"hmac"`
}

type ClusterPeerHMACConfig struct {
	KeyID           string            `yaml:"key_id"`
	Keys            map[string]string `yaml:"keys"`
	MaxClockSkew    string            `yaml:"max_clock_skew"`
	NonceTTL        string            `yaml:"nonce_ttl"`
	MaxNonceEntries int               `yaml:"max_nonce_entries"`
}

type UpstreamConfig struct {
	Routes []UpstreamRouteConfig `yaml:"routes"`
}

type DownlinkConfig struct {
	Storage   DownlinkStorageConfig   `yaml:"storage"`
	Delivery  DownlinkDeliveryConfig  `yaml:"delivery"`
	Retention DownlinkRetentionConfig `yaml:"retention"`
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
	RetryJitter    string `yaml:"retry_jitter"`
	AckTimeout     string `yaml:"ack_timeout"`
	RetryLease     string `yaml:"retry_lease"`
	MaxAttempts    int    `yaml:"max_attempts"`
	ScanLimit      int    `yaml:"scan_limit"`
	BindFlushLimit int    `yaml:"bind_flush_limit"`
}

type DownlinkRetentionConfig struct {
	DeliveredTTL    string `yaml:"delivered_ttl"`
	FailedTTL       string `yaml:"failed_ttl"`
	DiscardedTTL    string `yaml:"discarded_ttl"`
	CleanupInterval string `yaml:"cleanup_interval"`
	CleanupLimit    int    `yaml:"cleanup_limit"`
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
	MaxInFlight   int      `yaml:"max_in_flight"`
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
	data, err = expandEnvPlaceholders(data)
	if err != nil {
		return nil, fmt.Errorf("config: expand env %s: %w", path, err)
	}

	var fileConfig File
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fileConfig); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	return &fileConfig, nil
}

func expandEnvPlaceholders(data []byte) ([]byte, error) {
	text := string(data)
	if !strings.Contains(text, "${") {
		return data, nil
	}

	var builder strings.Builder
	builder.Grow(len(text))

	missing := make(map[string]struct{})
	offset := 0
	for {
		start := strings.Index(text[offset:], "${")
		if start < 0 {
			builder.WriteString(text[offset:])
			break
		}
		start += offset
		end := strings.IndexByte(text[start+2:], '}')
		if end < 0 {
			return nil, fmt.Errorf("unterminated environment placeholder at byte %d", start)
		}
		end += start + 2

		name := text[start+2 : end]
		if !validEnvPlaceholderName(name) {
			return nil, fmt.Errorf("invalid environment placeholder %q", name)
		}

		builder.WriteString(text[offset:start])
		if value, ok := os.LookupEnv(name); ok {
			builder.WriteString(value)
		} else {
			missing[name] = struct{}{}
		}
		offset = end + 1
	}

	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("missing environment variables: %s", strings.Join(names, ", "))
	}

	return []byte(builder.String()), nil
}

func validEnvPlaceholderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		if char == '_' || ('A' <= char && char <= 'Z') || ('a' <= char && char <= 'z') {
			continue
		}
		if index > 0 && '0' <= char && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func (c *File) ToServerConfig() (server.Config, error) {
	if _, err := c.Validate(); err != nil {
		return server.Config{}, err
	}

	out := server.DefaultConfig()

	if c.GatewayNode != "" {
		out.GatewayNode = c.GatewayNode
	}
	if len(c.RouteMsgIDs) > 0 {
		out.RouteMsgIDs = append([]uint32(nil), c.RouteMsgIDs...)
	}
	if err := applyInternalHTTPConfig(&out, c.InternalHTTP); err != nil {
		return server.Config{}, err
	}
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

	// JWT verifier construction performs the initial JWKS fetch and starts its
	// refresh worker, so keep it after every other fallible conversion.
	verifier, configured, err := toAuthVerifier(c.Auth)
	if err != nil {
		return server.Config{}, err
	}
	if configured {
		out.Verifier = verifier
	}

	return out, nil
}

func toAuthVerifier(config AuthConfig) (auth.Verifier, bool, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Type))
	if provider == "" {
		hasHTTP := isHTTPAuthConfigSet(config.HTTP)
		hasJWT := isJWTAuthConfigSet(config.JWT)
		if config.StaticTokens == nil && !hasHTTP && !hasJWT {
			return nil, false, nil
		}
		if config.StaticTokens == nil || hasHTTP || hasJWT {
			return nil, false, fmt.Errorf("%w: auth.type is required when using a non-static provider", auth.ErrMisconfigured)
		}
		provider = auth.ProviderStatic
	}

	if err := validateAuthProviderConfig(provider, config); err != nil {
		return nil, false, err
	}
	cacheConfig, cacheEnabled, err := toAuthCacheConfig(config.Cache)
	if err != nil {
		return nil, false, err
	}

	var verifier auth.Verifier
	switch provider {
	case auth.ProviderStatic:
		if config.StaticTokens == nil {
			return nil, false, fmt.Errorf("%w: static provider requires static_tokens", auth.ErrMisconfigured)
		}
		verifier = auth.NewStaticTokenVerifier(toPrincipals(config.StaticTokens))
	case auth.ProviderHTTP:
		if strings.TrimSpace(config.HTTP.URL) == "" {
			return nil, false, fmt.Errorf("%w: http provider requires auth.http.url", auth.ErrMisconfigured)
		}
		timeout, err := parseOptionalPositiveDuration(config.HTTP.Timeout)
		if err != nil {
			return nil, false, fmt.Errorf("%w: invalid auth.http.timeout: %v", auth.ErrMisconfigured, err)
		}
		verifier, err = auth.NewHTTPVerifier(auth.HTTPVerifierConfig{
			URL:           config.HTTP.URL,
			InternalToken: config.HTTP.InternalToken,
			Timeout:       timeout,
			MaxInFlight:   config.HTTP.MaxInFlight,
		})
		if err != nil {
			return nil, false, err
		}
	case auth.ProviderJWT:
		if strings.TrimSpace(config.JWT.Issuer) == "" ||
			strings.TrimSpace(config.JWT.Audience) == "" ||
			strings.TrimSpace(config.JWT.JWKSURL) == "" ||
			len(config.JWT.Algorithms) == 0 {
			return nil, false, fmt.Errorf("%w: jwt provider requires issuer, audience, jwks_url, and algorithms", auth.ErrMisconfigured)
		}
		clockSkew, err := parseOptionalNonNegativeDuration(config.JWT.ClockSkew)
		if err != nil {
			return nil, false, fmt.Errorf("%w: invalid auth.jwt.clock_skew: %v", auth.ErrMisconfigured, err)
		}
		refreshInterval, err := parseOptionalPositiveDuration(config.JWT.RefreshInterval)
		if err != nil {
			return nil, false, fmt.Errorf("%w: invalid auth.jwt.refresh_interval: %v", auth.ErrMisconfigured, err)
		}
		fetchTimeout, err := parseOptionalPositiveDuration(config.JWT.FetchTimeout)
		if err != nil {
			return nil, false, fmt.Errorf("%w: invalid auth.jwt.fetch_timeout: %v", auth.ErrMisconfigured, err)
		}
		verifier, err = auth.NewJWTVerifier(auth.JWTVerifierConfig{
			Issuer:              config.JWT.Issuer,
			Audience:            config.JWT.Audience,
			JWKSURL:             config.JWT.JWKSURL,
			Algorithms:          config.JWT.Algorithms,
			ClientIDClaim:       config.JWT.ClientIDClaim,
			TokenIDClaim:        config.JWT.TokenIDClaim,
			ScopesClaim:         config.JWT.ScopesClaim,
			ClockSkew:           clockSkew,
			RefreshInterval:     refreshInterval,
			FetchTimeout:        fetchTimeout,
			MaxResponseBodySize: config.JWT.MaxResponseBodySize,
		})
		if err != nil {
			return nil, false, err
		}
	default:
		return nil, false, fmt.Errorf("%w: unsupported auth provider %q", auth.ErrMisconfigured, provider)
	}

	if cacheEnabled {
		cachedVerifier, err := auth.NewCachedVerifier(verifier, cacheConfig)
		if err != nil {
			if closer, ok := verifier.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			return nil, false, err
		}
		verifier = cachedVerifier
	}

	return auth.NewObservedVerifier(verifier), true, nil
}

func validateAuthProviderConfig(provider string, config AuthConfig) error {
	hasStatic := config.StaticTokens != nil
	hasHTTP := isHTTPAuthConfigSet(config.HTTP)
	hasJWT := isJWTAuthConfigSet(config.JWT)

	conflict := false
	switch provider {
	case auth.ProviderStatic:
		conflict = hasHTTP || hasJWT
	case auth.ProviderHTTP:
		conflict = hasStatic || hasJWT
	case auth.ProviderJWT:
		conflict = hasStatic || hasHTTP
	}
	if conflict {
		return fmt.Errorf("%w: auth provider %q has conflicting provider configuration", auth.ErrMisconfigured, provider)
	}
	return nil
}

func isHTTPAuthConfigSet(config HTTPAuthConfig) bool {
	return config.URL != "" || config.InternalToken != "" || config.Timeout != "" || config.MaxInFlight != 0
}

func isJWTAuthConfigSet(config JWTAuthConfig) bool {
	return config.Issuer != "" || config.Audience != "" || config.JWKSURL != "" || len(config.Algorithms) > 0 ||
		config.ClientIDClaim != "" || config.TokenIDClaim != "" || config.ScopesClaim != "" ||
		config.ClockSkew != "" || config.RefreshInterval != "" || config.FetchTimeout != "" ||
		config.MaxResponseBodySize != 0
}

func toAuthCacheConfig(config AuthCacheConfig) (auth.CacheConfig, bool, error) {
	if config.MaxEntries < 0 {
		return auth.CacheConfig{}, false, fmt.Errorf("%w: auth.cache.max_entries must not be negative", auth.ErrMisconfigured)
	}
	positiveTTL, err := parseOptionalPositiveDuration(config.PositiveTTL)
	if err != nil {
		return auth.CacheConfig{}, false, fmt.Errorf("%w: invalid auth.cache.positive_ttl: %v", auth.ErrMisconfigured, err)
	}
	negativeTTL, err := parseOptionalPositiveDuration(config.NegativeTTL)
	if err != nil {
		return auth.CacheConfig{}, false, fmt.Errorf("%w: invalid auth.cache.negative_ttl: %v", auth.ErrMisconfigured, err)
	}
	if !config.Enabled {
		return auth.CacheConfig{}, false, nil
	}

	maxEntries := config.MaxEntries
	if maxEntries == 0 {
		maxEntries = 10000
	}
	if positiveTTL == 0 {
		positiveTTL = 30 * time.Second
	}
	if negativeTTL == 0 {
		negativeTTL = 3 * time.Second
	}
	return auth.CacheConfig{
		MaxEntries:  maxEntries,
		PositiveTTL: positiveTTL,
		NegativeTTL: negativeTTL,
	}, true, nil
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

func applyInternalHTTPConfig(out *server.Config, config InternalHTTPConfig) error {
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
	if config.MaxInFlight < 0 {
		return fmt.Errorf("config: internal_http max_in_flight must be greater than or equal to 0")
	}
	if config.MaxInFlight > 0 {
		out.InternalPushMaxInFlight = config.MaxInFlight
	}

	mode := strings.ToLower(strings.TrimSpace(config.Auth.Mode))
	if mode == "" {
		if internalHTTPHMACConfigSet(config.Auth.HMAC) {
			return fmt.Errorf("config: internal_http.auth.mode is required when HMAC settings are present")
		}
		mode = server.InternalHTTPAuthModeToken
	}
	switch mode {
	case server.InternalHTTPAuthModeToken:
		if internalHTTPHMACConfigSet(config.Auth.HMAC) {
			return fmt.Errorf("config: internal_http.auth.hmac conflicts with token mode")
		}
		out.InternalHTTPAuth.Mode = mode
	case server.InternalHTTPAuthModeHMAC:
		if config.Token != nil && *config.Token != "" {
			return fmt.Errorf("config: internal_http.token conflicts with HMAC mode")
		}
		hmacConfig, err := toInternalHTTPHMACConfig(config.Auth.HMAC)
		if err != nil {
			return err
		}
		out.InternalToken = ""
		out.InternalHTTPAuth = server.InternalHTTPAuthConfig{Mode: mode, HMAC: hmacConfig}
	default:
		return fmt.Errorf("config: unsupported internal_http.auth.mode %q", config.Auth.Mode)
	}

	return nil
}

func toInternalHTTPHMACConfig(config InternalHTTPHMACConfig) (server.InternalHTTPHMACConfig, error) {
	maxClockSkew, err := parseOptionalPositiveDuration(config.MaxClockSkew)
	if err != nil {
		return server.InternalHTTPHMACConfig{}, fmt.Errorf("config: internal_http.auth.hmac.max_clock_skew: %w", err)
	}
	if maxClockSkew == 0 {
		maxClockSkew = signing.DefaultMaxClockSkew
	}
	nonceTTL, err := parseOptionalPositiveDuration(config.NonceTTL)
	if err != nil {
		return server.InternalHTTPHMACConfig{}, fmt.Errorf("config: internal_http.auth.hmac.nonce_ttl: %w", err)
	}
	if nonceTTL == 0 {
		nonceTTL = signing.DefaultNonceTTL
	}
	if config.MaxNonceEntries < 0 {
		return server.InternalHTTPHMACConfig{}, fmt.Errorf("config: internal_http.auth.hmac.max_nonce_entries must not be negative")
	}
	maxNonceEntries := config.MaxNonceEntries
	if maxNonceEntries == 0 {
		maxNonceEntries = signing.DefaultMaxNonceEntries
	}

	keys := make(map[string][]byte, len(config.Keys))
	for keyID, secret := range config.Keys {
		keys[keyID] = []byte(secret)
	}
	verifierConfig := signing.VerifierConfig{
		Keys:            keys,
		MaxClockSkew:    maxClockSkew,
		NonceTTL:        nonceTTL,
		MaxNonceEntries: maxNonceEntries,
	}
	if _, err := signing.NewVerifier(verifierConfig); err != nil {
		return server.InternalHTTPHMACConfig{}, fmt.Errorf("config: internal_http.auth.hmac: %w", err)
	}
	return server.InternalHTTPHMACConfig{
		Keys:            keys,
		MaxClockSkew:    maxClockSkew,
		NonceTTL:        nonceTTL,
		MaxNonceEntries: maxNonceEntries,
	}, nil
}

func internalHTTPHMACConfigSet(config InternalHTTPHMACConfig) bool {
	return len(config.Keys) > 0 || config.MaxClockSkew != "" || config.NonceTTL != "" || config.MaxNonceEntries != 0
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

	if err := applyClusterPeerAuthConfig(&out.Cluster.Peer, config.Peer); err != nil {
		return err
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

func applyClusterPeerAuthConfig(out *server.ClusterPeerConfig, config ClusterPeerConfig) error {
	mode := strings.ToLower(strings.TrimSpace(config.Auth.Mode))
	if mode == "" {
		if clusterPeerHMACConfigSet(config.Auth.HMAC) {
			return fmt.Errorf("config: cluster.peer.auth.mode is required when HMAC settings are present")
		}
		mode = server.ClusterPeerAuthModeToken
	}

	switch mode {
	case server.ClusterPeerAuthModeToken:
		if clusterPeerHMACConfigSet(config.Auth.HMAC) {
			return fmt.Errorf("config: cluster.peer.auth.hmac conflicts with token mode")
		}
		out.Auth.Mode = mode
		if config.Token != "" {
			out.Token = config.Token
		}
	case server.ClusterPeerAuthModeHMAC:
		if config.Token != "" {
			return fmt.Errorf("config: cluster.peer.token conflicts with HMAC mode")
		}
		hmacConfig, err := toClusterPeerHMACConfig(config.Auth.HMAC)
		if err != nil {
			return err
		}
		out.Token = ""
		out.Auth = server.ClusterPeerAuthConfig{Mode: mode, HMAC: hmacConfig}
	default:
		return fmt.Errorf("config: unsupported cluster.peer.auth.mode %q", config.Auth.Mode)
	}
	return nil
}

func toClusterPeerHMACConfig(config ClusterPeerHMACConfig) (server.ClusterPeerHMACConfig, error) {
	maxClockSkew, err := parseOptionalPositiveDuration(config.MaxClockSkew)
	if err != nil {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac.max_clock_skew: %w", err)
	}
	if maxClockSkew == 0 {
		maxClockSkew = signing.DefaultMaxClockSkew
	}
	nonceTTL, err := parseOptionalPositiveDuration(config.NonceTTL)
	if err != nil {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac.nonce_ttl: %w", err)
	}
	if nonceTTL == 0 {
		nonceTTL = signing.DefaultNonceTTL
	}
	if config.MaxNonceEntries < 0 {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac.max_nonce_entries must not be negative")
	}
	maxNonceEntries := config.MaxNonceEntries
	if maxNonceEntries == 0 {
		maxNonceEntries = signing.DefaultMaxNonceEntries
	}

	keys := make(map[string][]byte, len(config.Keys))
	for keyID, secret := range config.Keys {
		keys[keyID] = []byte(secret)
	}
	verifierConfig := signing.VerifierConfig{
		Keys:            keys,
		MaxClockSkew:    maxClockSkew,
		NonceTTL:        nonceTTL,
		MaxNonceEntries: maxNonceEntries,
	}
	if _, err := signing.NewVerifier(verifierConfig); err != nil {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac: %w", err)
	}
	secret, ok := keys[config.KeyID]
	if !ok {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac.key_id %q is not present in keys", config.KeyID)
	}
	if _, err := signing.NewSigner(signing.SignerConfig{KeyID: config.KeyID, Secret: secret}); err != nil {
		return server.ClusterPeerHMACConfig{}, fmt.Errorf("config: cluster.peer.auth.hmac signer: %w", err)
	}

	return server.ClusterPeerHMACConfig{
		KeyID:           config.KeyID,
		Keys:            keys,
		MaxClockSkew:    maxClockSkew,
		NonceTTL:        nonceTTL,
		MaxNonceEntries: maxNonceEntries,
	}, nil
}

func clusterPeerHMACConfigSet(config ClusterPeerHMACConfig) bool {
	return config.KeyID != "" || len(config.Keys) > 0 || config.MaxClockSkew != "" || config.NonceTTL != "" || config.MaxNonceEntries != 0
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
	retryJitter, err := parseOptionalDuration(delivery.RetryJitter)
	if err != nil {
		return fmt.Errorf("config: downlink delivery retry_jitter: %w", err)
	}
	if retryJitter < 0 {
		return fmt.Errorf("config: downlink delivery retry_jitter must be greater than or equal to 0")
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
	out.DownlinkDelivery.RetryJitter = retryJitter
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

	retention := config.Retention
	deliveredTTL, err := parseOptionalPositiveDuration(retention.DeliveredTTL)
	if err != nil {
		return fmt.Errorf("config: downlink retention delivered_ttl: %w", err)
	}
	failedTTL, err := parseOptionalPositiveDuration(retention.FailedTTL)
	if err != nil {
		return fmt.Errorf("config: downlink retention failed_ttl: %w", err)
	}
	discardedTTL, err := parseOptionalPositiveDuration(retention.DiscardedTTL)
	if err != nil {
		return fmt.Errorf("config: downlink retention discarded_ttl: %w", err)
	}
	cleanupInterval, err := parseOptionalPositiveDuration(retention.CleanupInterval)
	if err != nil {
		return fmt.Errorf("config: downlink retention cleanup_interval: %w", err)
	}
	if deliveredTTL > 0 {
		out.DownlinkRetention.DeliveredTTL = deliveredTTL
	}
	if failedTTL > 0 {
		out.DownlinkRetention.FailedTTL = failedTTL
	}
	if discardedTTL > 0 {
		out.DownlinkRetention.DiscardedTTL = discardedTTL
	}
	if cleanupInterval > 0 {
		out.DownlinkRetention.CleanupInterval = cleanupInterval
	}
	if retention.CleanupLimit < 0 {
		return fmt.Errorf("config: downlink retention cleanup_limit must be greater than or equal to 0")
	}
	if retention.CleanupLimit > 0 {
		out.DownlinkRetention.CleanupLimit = retention.CleanupLimit
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
				Name:        route.Name,
				MsgIDMin:    route.MsgIDMin,
				MsgIDMax:    route.MsgIDMax,
				MaxInFlight: route.Target.MaxInFlight,
				HTTP:        httpConfig,
			})
		case "nsq":
			nsqConfig, err := toNSQUpstreamConfig(route)
			if err != nil {
				return nil, err
			}

			out = append(out, server.UpstreamRouteConfig{
				Name:        route.Name,
				MsgIDMin:    route.MsgIDMin,
				MsgIDMax:    route.MsgIDMax,
				MaxInFlight: route.Target.MaxInFlight,
				NSQ:         nsqConfig,
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
	if route.Target.MaxInFlight < 0 {
		return fmt.Errorf("config: route %q target max_in_flight must be greater than or equal to 0", route.Name)
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

func parseOptionalNonNegativeDuration(raw string) (time.Duration, error) {
	duration, err := parseOptionalDuration(raw)
	if err != nil {
		return 0, err
	}
	if duration < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return duration, nil
}
