package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
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
	InternalHTTP InternalHTTPConfig `yaml:"internal_http"`
	Upstream     UpstreamConfig     `yaml:"upstream"`
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

type UpstreamConfig struct {
	Routes []UpstreamRouteConfig `yaml:"routes"`
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
