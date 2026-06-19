package config

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/qiuyier/Z-Courier/internal/auth"
)

func TestResolvePath(t *testing.T) {
	t.Setenv(EnvPathKey, "")

	if got := ResolvePath("custom.yaml"); got != "custom.yaml" {
		t.Fatalf("ResolvePath(flag) = %q, want custom.yaml", got)
	}

	t.Setenv(EnvPathKey, "env.yaml")
	if got := ResolvePath(""); got != "env.yaml" {
		t.Fatalf("ResolvePath(env) = %q, want env.yaml", got)
	}

	t.Setenv(EnvPathKey, "")
	if got := ResolvePath(""); got != DefaultPath {
		t.Fatalf("ResolvePath(default) = %q, want %q", got, DefaultPath)
	}
}

func TestLoadServerConfig(t *testing.T) {
	path := writeConfig(t, `
gateway_node: node-a
route_msg_ids:
  - 1000
  - 1001
auth:
  static_tokens:
    token-a:
      client_id: client-a
      scopes:
        - gateway:test
internal_http:
  enabled: false
  addr: 127.0.0.1:19080
  token: internal-a
  max_request_body_size: 12345
  max_in_flight: 321
cluster:
  enabled: true
  internal_addr: http://gateway-a:18082
  route_refresh_interval: 15s
  registry:
    type: redis
    ttl: 45s
    redis:
      addr: 127.0.0.1:6379
      username: redis-user
      password: redis-pass
      db: 2
      key_prefix: test-zcourier
      dial_timeout: 500ms
      read_timeout: 700ms
      write_timeout: 900ms
  peer:
    token: cluster-token
    timeout: 1500ms
downlink:
  storage:
    type: postgres
    postgres:
      dsn: postgres://user:pass@127.0.0.1:5432/z_courier?sslmode=disable
      auto_migrate: false
      max_open_conns: 9
      max_idle_conns: 4
      conn_max_lifetime: 30m
  delivery:
    retry_interval: 7s
    retry_delay: 11s
    ack_timeout: 12s
    retry_lease: 13s
    max_attempts: 6
    scan_limit: 77
    bind_flush_limit: 88
  retention:
    delivered_ttl: 24h
    failed_ttl: 48h
    discarded_ttl: 72h
    cleanup_interval: 9m
    cleanup_limit: 123
pipeline:
  allowlist:
    client_ids:
      - client-a
    msg_ids:
      - 1000
  blocklist:
    client_ids:
      - blocked-client
    msg_ids:
      - 9999
  rate_limit:
    enabled: true
    max_requests: 3
    window: 2s
upstream:
  routes:
    - name: disabled
      enabled: false
      msg_id_min: 1000
      msg_id_max: 1999
      target:
        type: http
        url: http://disabled.local
    - name: enabled
      enabled: true
      msg_id_min: 2000
      msg_id_max: 2999
      target:
        type: http
        url: http://backend.local/gateway/upstream
        token: upstream-a
        timeout: 3s
        max_in_flight: 111
    - name: nsq
      enabled: true
      msg_id_min: 3000
      msg_id_max: 3999
      target:
        type: nsq
        nsqd_addrs:
          - 127.0.0.1:4150
          - 127.0.0.1:4151
        topic: message_events
        auth_secret: nsq-secret
        dial_timeout: 1s
        read_timeout: 60s
        write_timeout: 2s
        publish_mode: round_robin
        retry_attempts: 1
        max_in_flight: 222
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	if config.GatewayNode != "node-a" {
		t.Fatalf("GatewayNode = %q, want node-a", config.GatewayNode)
	}
	if len(config.RouteMsgIDs) != 2 || config.RouteMsgIDs[1] != 1001 {
		t.Fatalf("RouteMsgIDs = %v, want [1000 1001]", config.RouteMsgIDs)
	}
	if !config.DisableInternalHTTP {
		t.Fatal("DisableInternalHTTP = false, want true")
	}
	if config.InternalHTTPAddr != "127.0.0.1:19080" {
		t.Fatalf("InternalHTTPAddr = %q, want 127.0.0.1:19080", config.InternalHTTPAddr)
	}
	if config.InternalToken != "internal-a" {
		t.Fatalf("InternalToken = %q, want internal-a", config.InternalToken)
	}
	if config.InternalHTTPAuth.Mode != "token" {
		t.Fatalf("InternalHTTPAuth Mode = %q, want token", config.InternalHTTPAuth.Mode)
	}
	if config.InternalMaxRequestBodySize != 12345 {
		t.Fatalf("InternalMaxRequestBodySize = %d, want 12345", config.InternalMaxRequestBodySize)
	}
	if config.InternalPushMaxInFlight != 321 {
		t.Fatalf("InternalPushMaxInFlight = %d, want 321", config.InternalPushMaxInFlight)
	}
	if !config.Cluster.Enabled {
		t.Fatal("Cluster Enabled = false, want true")
	}
	if config.Cluster.InternalAddr != "http://gateway-a:18082" {
		t.Fatalf("Cluster InternalAddr = %q, want http://gateway-a:18082", config.Cluster.InternalAddr)
	}
	if config.Cluster.RouteRefreshInterval != 15*time.Second {
		t.Fatalf("Cluster RouteRefreshInterval = %v, want 15s", config.Cluster.RouteRefreshInterval)
	}
	if config.Cluster.Registry.Type != "redis" {
		t.Fatalf("Cluster Registry Type = %q, want redis", config.Cluster.Registry.Type)
	}
	if config.Cluster.Registry.TTL != 45*time.Second {
		t.Fatalf("Cluster Registry TTL = %v, want 45s", config.Cluster.Registry.TTL)
	}
	if config.Cluster.Registry.Redis.Addr != "127.0.0.1:6379" {
		t.Fatalf("Cluster Redis Addr = %q", config.Cluster.Registry.Redis.Addr)
	}
	if config.Cluster.Registry.Redis.Username != "redis-user" {
		t.Fatalf("Cluster Redis Username = %q", config.Cluster.Registry.Redis.Username)
	}
	if config.Cluster.Registry.Redis.Password != "redis-pass" {
		t.Fatalf("Cluster Redis Password = %q", config.Cluster.Registry.Redis.Password)
	}
	if config.Cluster.Registry.Redis.DB != 2 {
		t.Fatalf("Cluster Redis DB = %d, want 2", config.Cluster.Registry.Redis.DB)
	}
	if config.Cluster.Registry.Redis.KeyPrefix != "test-zcourier" {
		t.Fatalf("Cluster Redis KeyPrefix = %q", config.Cluster.Registry.Redis.KeyPrefix)
	}
	if config.Cluster.Registry.Redis.DialTimeout != 500*time.Millisecond {
		t.Fatalf("Cluster Redis DialTimeout = %v, want 500ms", config.Cluster.Registry.Redis.DialTimeout)
	}
	if config.Cluster.Registry.Redis.ReadTimeout != 700*time.Millisecond {
		t.Fatalf("Cluster Redis ReadTimeout = %v, want 700ms", config.Cluster.Registry.Redis.ReadTimeout)
	}
	if config.Cluster.Registry.Redis.WriteTimeout != 900*time.Millisecond {
		t.Fatalf("Cluster Redis WriteTimeout = %v, want 900ms", config.Cluster.Registry.Redis.WriteTimeout)
	}
	if config.Cluster.Peer.Token != "cluster-token" {
		t.Fatalf("Cluster Peer Token = %q", config.Cluster.Peer.Token)
	}
	if config.Cluster.Peer.Auth.Mode != "token" {
		t.Fatalf("Cluster Peer Auth Mode = %q, want token", config.Cluster.Peer.Auth.Mode)
	}
	if config.Cluster.Peer.Timeout != 1500*time.Millisecond {
		t.Fatalf("Cluster Peer Timeout = %v, want 1500ms", config.Cluster.Peer.Timeout)
	}
	if config.DownlinkStorage.Type != "postgres" {
		t.Fatalf("DownlinkStorage Type = %q, want postgres", config.DownlinkStorage.Type)
	}
	if config.DownlinkStorage.Postgres.DSN != "postgres://user:pass@127.0.0.1:5432/z_courier?sslmode=disable" {
		t.Fatalf("DownlinkStorage Postgres DSN = %q", config.DownlinkStorage.Postgres.DSN)
	}
	if config.DownlinkStorage.Postgres.AutoMigrate {
		t.Fatal("DownlinkStorage Postgres AutoMigrate = true, want false")
	}
	if !config.DownlinkStorage.Postgres.AutoMigrateSet {
		t.Fatal("DownlinkStorage Postgres AutoMigrateSet = false, want true")
	}
	if config.DownlinkStorage.Postgres.MaxOpenConns != 9 {
		t.Fatalf("DownlinkStorage Postgres MaxOpenConns = %d, want 9", config.DownlinkStorage.Postgres.MaxOpenConns)
	}
	if config.DownlinkStorage.Postgres.MaxIdleConns != 4 {
		t.Fatalf("DownlinkStorage Postgres MaxIdleConns = %d, want 4", config.DownlinkStorage.Postgres.MaxIdleConns)
	}
	if config.DownlinkStorage.Postgres.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("DownlinkStorage Postgres ConnMaxLifetime = %v, want 30m", config.DownlinkStorage.Postgres.ConnMaxLifetime)
	}
	if config.DownlinkDelivery.RetryInterval != 7*time.Second {
		t.Fatalf("DownlinkDelivery RetryInterval = %v, want 7s", config.DownlinkDelivery.RetryInterval)
	}
	if config.DownlinkDelivery.RetryDelay != 11*time.Second {
		t.Fatalf("DownlinkDelivery RetryDelay = %v, want 11s", config.DownlinkDelivery.RetryDelay)
	}
	if config.DownlinkDelivery.AckTimeout != 12*time.Second {
		t.Fatalf("DownlinkDelivery AckTimeout = %v, want 12s", config.DownlinkDelivery.AckTimeout)
	}
	if config.DownlinkDelivery.RetryLease != 13*time.Second {
		t.Fatalf("DownlinkDelivery RetryLease = %v, want 13s", config.DownlinkDelivery.RetryLease)
	}
	if config.DownlinkDelivery.MaxAttempts != 6 {
		t.Fatalf("DownlinkDelivery MaxAttempts = %d, want 6", config.DownlinkDelivery.MaxAttempts)
	}
	if config.DownlinkDelivery.ScanLimit != 77 {
		t.Fatalf("DownlinkDelivery ScanLimit = %d, want 77", config.DownlinkDelivery.ScanLimit)
	}
	if config.DownlinkDelivery.BindFlushLimit != 88 {
		t.Fatalf("DownlinkDelivery BindFlushLimit = %d, want 88", config.DownlinkDelivery.BindFlushLimit)
	}
	if config.DownlinkRetention.DeliveredTTL != 24*time.Hour {
		t.Fatalf("DownlinkRetention DeliveredTTL = %v, want 24h", config.DownlinkRetention.DeliveredTTL)
	}
	if config.DownlinkRetention.FailedTTL != 48*time.Hour {
		t.Fatalf("DownlinkRetention FailedTTL = %v, want 48h", config.DownlinkRetention.FailedTTL)
	}
	if config.DownlinkRetention.DiscardedTTL != 72*time.Hour {
		t.Fatalf("DownlinkRetention DiscardedTTL = %v, want 72h", config.DownlinkRetention.DiscardedTTL)
	}
	if config.DownlinkRetention.CleanupInterval != 9*time.Minute {
		t.Fatalf("DownlinkRetention CleanupInterval = %v, want 9m", config.DownlinkRetention.CleanupInterval)
	}
	if config.DownlinkRetention.CleanupLimit != 123 {
		t.Fatalf("DownlinkRetention CleanupLimit = %d, want 123", config.DownlinkRetention.CleanupLimit)
	}
	if len(config.Pipeline.Policy.AllowClientIDs) != 1 || config.Pipeline.Policy.AllowClientIDs[0] != "client-a" {
		t.Fatalf("Pipeline AllowClientIDs = %v, want [client-a]", config.Pipeline.Policy.AllowClientIDs)
	}
	if len(config.Pipeline.Policy.BlockClientIDs) != 1 || config.Pipeline.Policy.BlockClientIDs[0] != "blocked-client" {
		t.Fatalf("Pipeline BlockClientIDs = %v, want [blocked-client]", config.Pipeline.Policy.BlockClientIDs)
	}
	if len(config.Pipeline.Policy.AllowMsgIDs) != 1 || config.Pipeline.Policy.AllowMsgIDs[0] != 1000 {
		t.Fatalf("Pipeline AllowMsgIDs = %v, want [1000]", config.Pipeline.Policy.AllowMsgIDs)
	}
	if len(config.Pipeline.Policy.BlockMsgIDs) != 1 || config.Pipeline.Policy.BlockMsgIDs[0] != 9999 {
		t.Fatalf("Pipeline BlockMsgIDs = %v, want [9999]", config.Pipeline.Policy.BlockMsgIDs)
	}
	if !config.Pipeline.RateLimit.Enabled {
		t.Fatal("Pipeline RateLimit Enabled = false, want true")
	}
	if config.Pipeline.RateLimit.MaxRequests != 3 {
		t.Fatalf("Pipeline RateLimit MaxRequests = %d, want 3", config.Pipeline.RateLimit.MaxRequests)
	}
	if config.Pipeline.RateLimit.Window != 2*time.Second {
		t.Fatalf("Pipeline RateLimit Window = %v, want 2s", config.Pipeline.RateLimit.Window)
	}
	if len(config.UpstreamRoutes) != 2 {
		t.Fatalf("UpstreamRoutes length = %d, want 2", len(config.UpstreamRoutes))
	}
	route := config.UpstreamRoutes[0]
	if route.Name != "enabled" || route.HTTP == nil {
		t.Fatalf("route = %+v, want enabled HTTP route", route)
	}
	if route.HTTP.URL != "http://backend.local/gateway/upstream" {
		t.Fatalf("route HTTP URL = %q", route.HTTP.URL)
	}
	if route.HTTP.Token != "upstream-a" {
		t.Fatalf("route HTTP Token = %q", route.HTTP.Token)
	}
	if route.HTTP.Timeout != 3*time.Second {
		t.Fatalf("route HTTP Timeout = %v, want 3s", route.HTTP.Timeout)
	}
	if route.MaxInFlight != 111 {
		t.Fatalf("route MaxInFlight = %d, want 111", route.MaxInFlight)
	}

	nsqRoute := config.UpstreamRoutes[1]
	if nsqRoute.Name != "nsq" || nsqRoute.NSQ == nil {
		t.Fatalf("nsq route = %+v, want NSQ route", nsqRoute)
	}
	if nsqRoute.NSQ.Address != "127.0.0.1:4150" {
		t.Fatalf("NSQ Address = %q", nsqRoute.NSQ.Address)
	}
	if len(nsqRoute.NSQ.Addresses) != 2 || nsqRoute.NSQ.Addresses[1] != "127.0.0.1:4151" {
		t.Fatalf("NSQ Addresses = %v, want [127.0.0.1:4150 127.0.0.1:4151]", nsqRoute.NSQ.Addresses)
	}
	if nsqRoute.NSQ.Topic != "message_events" {
		t.Fatalf("NSQ Topic = %q", nsqRoute.NSQ.Topic)
	}
	if nsqRoute.NSQ.AuthSecret != "nsq-secret" {
		t.Fatalf("NSQ AuthSecret = %q", nsqRoute.NSQ.AuthSecret)
	}
	if nsqRoute.NSQ.DialTimeout != time.Second {
		t.Fatalf("NSQ DialTimeout = %v, want 1s", nsqRoute.NSQ.DialTimeout)
	}
	if nsqRoute.NSQ.ReadTimeout != time.Minute {
		t.Fatalf("NSQ ReadTimeout = %v, want 60s", nsqRoute.NSQ.ReadTimeout)
	}
	if nsqRoute.NSQ.WriteTimeout != 2*time.Second {
		t.Fatalf("NSQ WriteTimeout = %v, want 2s", nsqRoute.NSQ.WriteTimeout)
	}
	if nsqRoute.NSQ.PublishMode != "round_robin" {
		t.Fatalf("NSQ PublishMode = %q, want round_robin", nsqRoute.NSQ.PublishMode)
	}
	if nsqRoute.NSQ.RetryAttempts != 1 {
		t.Fatalf("NSQ RetryAttempts = %d, want 1", nsqRoute.NSQ.RetryAttempts)
	}
	if nsqRoute.MaxInFlight != 222 {
		t.Fatalf("NSQ MaxInFlight = %d, want 222", nsqRoute.MaxInFlight)
	}
}

func TestLoadServerConfigClusterDefaults(t *testing.T) {
	path := writeConfig(t, `{}`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.Cluster.Enabled {
		t.Fatal("Cluster Enabled = true, want false")
	}
	if config.Cluster.Registry.Type != "memory" {
		t.Fatalf("Cluster Registry Type = %q, want memory", config.Cluster.Registry.Type)
	}
	if config.Cluster.Registry.TTL != 30*time.Second {
		t.Fatalf("Cluster Registry TTL = %v, want 30s", config.Cluster.Registry.TTL)
	}
	if config.Cluster.RouteRefreshInterval != 10*time.Second {
		t.Fatalf("Cluster RouteRefreshInterval = %v, want 10s", config.Cluster.RouteRefreshInterval)
	}
	if config.Cluster.Registry.Redis.KeyPrefix != "zcourier" {
		t.Fatalf("Cluster Redis KeyPrefix = %q, want zcourier", config.Cluster.Registry.Redis.KeyPrefix)
	}
	if config.Cluster.Registry.Redis.DialTimeout != time.Second {
		t.Fatalf("Cluster Redis DialTimeout = %v, want 1s", config.Cluster.Registry.Redis.DialTimeout)
	}
	if config.Cluster.Peer.Timeout != 2*time.Second {
		t.Fatalf("Cluster Peer Timeout = %v, want 2s", config.Cluster.Peer.Timeout)
	}
}

func TestLoadServerConfigDownlinkRetentionDefaults(t *testing.T) {
	path := writeConfig(t, `{}`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.DownlinkRetention.DeliveredTTL != 24*time.Hour {
		t.Fatalf("DownlinkRetention DeliveredTTL = %v, want 24h", config.DownlinkRetention.DeliveredTTL)
	}
	if config.DownlinkRetention.FailedTTL != 7*24*time.Hour {
		t.Fatalf("DownlinkRetention FailedTTL = %v, want 168h", config.DownlinkRetention.FailedTTL)
	}
	if config.DownlinkRetention.DiscardedTTL != 7*24*time.Hour {
		t.Fatalf("DownlinkRetention DiscardedTTL = %v, want 168h", config.DownlinkRetention.DiscardedTTL)
	}
	if config.DownlinkRetention.CleanupInterval != time.Hour {
		t.Fatalf("DownlinkRetention CleanupInterval = %v, want 1h", config.DownlinkRetention.CleanupInterval)
	}
	if config.DownlinkRetention.CleanupLimit != 1000 {
		t.Fatalf("DownlinkRetention CleanupLimit = %d, want 1000", config.DownlinkRetention.CleanupLimit)
	}
}

func TestLoadServerConfigClusterDerivesRouteRefreshIntervalFromTTL(t *testing.T) {
	path := writeConfig(t, `
cluster:
  registry:
    ttl: 45s
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.Cluster.RouteRefreshInterval != 15*time.Second {
		t.Fatalf("Cluster RouteRefreshInterval = %v, want 15s", config.Cluster.RouteRefreshInterval)
	}
}

func TestLoadServerConfigClusterRequiresInternalAddrWhenEnabled(t *testing.T) {
	path := writeConfig(t, `
cluster:
  enabled: true
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigClusterRejectsInvalidRegistryType(t *testing.T) {
	path := writeConfig(t, `
cluster:
  registry:
    type: consul
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigClusterRedisRequiresAddrWhenEnabled(t *testing.T) {
	path := writeConfig(t, `
cluster:
  enabled: true
  internal_addr: http://gateway-a:18082
  registry:
    type: redis
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigClusterRejectsInvalidTTL(t *testing.T) {
	path := writeConfig(t, `
cluster:
  registry:
    ttl: 0s
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigClusterRejectsInvalidRouteRefreshInterval(t *testing.T) {
	path := writeConfig(t, `
cluster:
  route_refresh_interval: 0s
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigClusterRejectsInvalidRedisDB(t *testing.T) {
	path := writeConfig(t, `
cluster:
  registry:
    redis:
      db: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDuration(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 1000
      target:
        type: http
        url: http://backend.local
        timeout: nope
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidInternalHTTPMaxInFlight(t *testing.T) {
	path := writeConfig(t, `
internal_http:
  max_in_flight: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInternalHTTPHMAC(t *testing.T) {
	path := writeConfig(t, `
internal_http:
  auth:
    mode: hmac
    hmac:
      keys:
        backend-1: 0123456789abcdef0123456789abcdef
        backend-previous: abcdef0123456789abcdef0123456789
      max_clock_skew: 20s
      nonce_ttl: 45s
      max_nonce_entries: 1234
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.InternalHTTPAuth.Mode != "hmac" || config.InternalToken != "" {
		t.Fatalf("internal HTTP auth = %+v token=%q, want HMAC without token", config.InternalHTTPAuth, config.InternalToken)
	}
	if len(config.InternalHTTPAuth.HMAC.Keys) != 2 || string(config.InternalHTTPAuth.HMAC.Keys["backend-1"]) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("HMAC keys = %v", config.InternalHTTPAuth.HMAC.Keys)
	}
	if config.InternalHTTPAuth.HMAC.MaxClockSkew != 20*time.Second || config.InternalHTTPAuth.HMAC.NonceTTL != 45*time.Second {
		t.Fatalf("HMAC durations = skew %v nonce TTL %v", config.InternalHTTPAuth.HMAC.MaxClockSkew, config.InternalHTTPAuth.HMAC.NonceTTL)
	}
	if config.InternalHTTPAuth.HMAC.MaxNonceEntries != 1234 {
		t.Fatalf("MaxNonceEntries = %d, want 1234", config.InternalHTTPAuth.HMAC.MaxNonceEntries)
	}
}

func TestLoadServerConfigRejectsInvalidInternalHTTPHMAC(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "unsupported mode", config: "internal_http:\n  auth:\n    mode: mtls\n"},
		{name: "missing keys", config: "internal_http:\n  auth:\n    mode: hmac\n"},
		{name: "token conflict", config: "internal_http:\n  token: secret\n  auth:\n    mode: hmac\n    hmac:\n      keys:\n        key: 0123456789abcdef0123456789abcdef\n"},
		{name: "short secret", config: "internal_http:\n  auth:\n    mode: hmac\n    hmac:\n      keys:\n        key: short\n"},
		{name: "short nonce TTL", config: "internal_http:\n  auth:\n    mode: hmac\n    hmac:\n      keys:\n        key: 0123456789abcdef0123456789abcdef\n      max_clock_skew: 30s\n      nonce_ttl: 30s\n"},
		{name: "mode required", config: "internal_http:\n  auth:\n    hmac:\n      keys:\n        key: 0123456789abcdef0123456789abcdef\n"},
		{name: "token mode conflict", config: "internal_http:\n  auth:\n    mode: token\n    hmac:\n      keys:\n        key: 0123456789abcdef0123456789abcdef\n"},
		{name: "negative entries", config: "internal_http:\n  auth:\n    mode: hmac\n    hmac:\n      keys:\n        key: 0123456789abcdef0123456789abcdef\n      max_nonce_entries: -1\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadServerConfig(writeConfig(t, test.config)); err == nil {
				t.Fatal("LoadServerConfig() error = nil, want error")
			}
		})
	}
}

func TestLoadServerConfigClusterPeerHMAC(t *testing.T) {
	path := writeConfig(t, `
cluster:
  peer:
    auth:
      mode: hmac
      hmac:
        key_id: gateway-current
        keys:
          gateway-current: cluster-peer-secret-0123456789abcdef
          gateway-previous: previous-peer-secret-0123456789abcdef
        max_clock_skew: 20s
        nonce_ttl: 45s
        max_nonce_entries: 2345
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	peer := config.Cluster.Peer
	if peer.Auth.Mode != "hmac" || peer.Token != "" {
		t.Fatalf("peer auth = %+v token=%q, want HMAC without token", peer.Auth, peer.Token)
	}
	if peer.Auth.HMAC.KeyID != "gateway-current" || len(peer.Auth.HMAC.Keys) != 2 {
		t.Fatalf("peer HMAC identity = %+v", peer.Auth.HMAC)
	}
	if peer.Auth.HMAC.MaxClockSkew != 20*time.Second || peer.Auth.HMAC.NonceTTL != 45*time.Second {
		t.Fatalf("peer HMAC durations = skew %v nonce TTL %v", peer.Auth.HMAC.MaxClockSkew, peer.Auth.HMAC.NonceTTL)
	}
	if peer.Auth.HMAC.MaxNonceEntries != 2345 {
		t.Fatalf("peer MaxNonceEntries = %d, want 2345", peer.Auth.HMAC.MaxNonceEntries)
	}
}

func TestLoadServerConfigRejectsInvalidClusterPeerHMAC(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "unsupported mode", config: "cluster:\n  peer:\n    auth:\n      mode: mtls\n"},
		{name: "missing keys", config: "cluster:\n  peer:\n    auth:\n      mode: hmac\n      hmac:\n        key_id: gateway\n"},
		{name: "missing signer key", config: "cluster:\n  peer:\n    auth:\n      mode: hmac\n      hmac:\n        key_id: missing\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n"},
		{name: "token conflict", config: "cluster:\n  peer:\n    token: secret\n    auth:\n      mode: hmac\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n"},
		{name: "short secret", config: "cluster:\n  peer:\n    auth:\n      mode: hmac\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: short\n"},
		{name: "short nonce TTL", config: "cluster:\n  peer:\n    auth:\n      mode: hmac\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n        max_clock_skew: 30s\n        nonce_ttl: 30s\n"},
		{name: "mode required", config: "cluster:\n  peer:\n    auth:\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n"},
		{name: "token mode conflict", config: "cluster:\n  peer:\n    auth:\n      mode: token\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n"},
		{name: "negative entries", config: "cluster:\n  peer:\n    auth:\n      mode: hmac\n      hmac:\n        key_id: gateway\n        keys:\n          gateway: cluster-peer-secret-0123456789abcdef\n        max_nonce_entries: -1\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadServerConfig(writeConfig(t, test.config)); err == nil {
				t.Fatal("LoadServerConfig() error = nil, want error")
			}
		})
	}
}

func TestLoadServerConfigInvalidRateLimit(t *testing.T) {
	path := writeConfig(t, `
pipeline:
  rate_limit:
    enabled: true
    max_requests: 0
    window: 1s
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidRateLimitWindow(t *testing.T) {
	path := writeConfig(t, `
pipeline:
  rate_limit:
    enabled: true
    max_requests: 1
    window: nope
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkStorage(t *testing.T) {
	path := writeConfig(t, `
downlink:
  storage:
    type: redis
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigPostgresRequiresDSN(t *testing.T) {
	path := writeConfig(t, `
downlink:
  storage:
    type: postgres
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidPostgresConnLifetime(t *testing.T) {
	path := writeConfig(t, `
downlink:
  storage:
    type: memory
    postgres:
      conn_max_lifetime: nope
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkRetryInterval(t *testing.T) {
	path := writeConfig(t, `
downlink:
  delivery:
    retry_interval: nope
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkRetryLease(t *testing.T) {
	path := writeConfig(t, `
downlink:
  delivery:
    retry_lease: 0s
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkDeliveryLimit(t *testing.T) {
	path := writeConfig(t, `
downlink:
  delivery:
    scan_limit: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkRetentionDuration(t *testing.T) {
	path := writeConfig(t, `
downlink:
  retention:
    cleanup_interval: nope
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidDownlinkRetentionLimit(t *testing.T) {
	path := writeConfig(t, `
downlink:
  retention:
    cleanup_limit: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidMsgIDRange(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 2000
      msg_id_max: 1000
      target:
        type: http
        url: http://backend.local
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigInvalidRouteMaxInFlight(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 1000
      target:
        type: http
        url: http://backend.local
        max_in_flight: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigNSQRequiresTopic(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 2000
      target:
        type: nsq
        addr: 127.0.0.1:4150
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigNSQSupportsLegacyAddr(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: nsq
      msg_id_min: 2000
      target:
        type: nsq
        addr: 127.0.0.1:4150
        topic: message_events
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if len(config.UpstreamRoutes) != 1 || config.UpstreamRoutes[0].NSQ == nil {
		t.Fatalf("UpstreamRoutes = %+v, want one NSQ route", config.UpstreamRoutes)
	}
	if len(config.UpstreamRoutes[0].NSQ.Addresses) != 1 || config.UpstreamRoutes[0].NSQ.Addresses[0] != "127.0.0.1:4150" {
		t.Fatalf("NSQ Addresses = %v", config.UpstreamRoutes[0].NSQ.Addresses)
	}
}

func TestLoadServerConfigNSQRequiresAddress(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 2000
      target:
        type: nsq
        topic: message_events
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigNSQRejectsInvalidRetryAttempts(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: broken
      msg_id_min: 2000
      target:
        type: nsq
        addr: 127.0.0.1:4150
        topic: message_events
        retry_attempts: -1
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigUnknownField(t *testing.T) {
	path := writeConfig(t, `
gateway_node: node-a
unknown_field: true
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want error")
	}
}

func TestLoadServerConfigExplicitStaticAuthProvider(t *testing.T) {
	path := writeConfig(t, `
auth:
  type: static
  static_tokens:
    token-a:
      client_id: client-a
      token_id: token-a
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if got := auth.ProviderName(config.Verifier); got != auth.ProviderStatic {
		t.Fatalf("auth provider = %q, want %q", got, auth.ProviderStatic)
	}
	principal, err := config.Verifier.Verify(context.Background(), "token-a")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.ClientID != "client-a" {
		t.Fatalf("principal client ID = %q, want client-a", principal.ClientID)
	}
}

func TestLoadServerConfigLegacyStaticAuthProvider(t *testing.T) {
	path := writeConfig(t, `
auth:
  static_tokens:
    token-a:
      client_id: client-a
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if got := auth.ProviderName(config.Verifier); got != auth.ProviderStatic {
		t.Fatalf("auth provider = %q, want %q", got, auth.ProviderStatic)
	}
}

func TestLoadServerConfigHTTPAuthProvider(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer token-a" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get(auth.InternalTokenHeader); got != "internal-secret" {
			t.Errorf("internal token = %q", got)
		}
		_, _ = w.Write([]byte(`{"client_id":"client-a","token_id":"token-a"}`))
	}))
	defer server.Close()

	path := writeConfig(t, fmt.Sprintf(`
auth:
  type: http
  http:
    url: %s
    internal_token: internal-secret
    timeout: 1s
    max_in_flight: 10
  cache:
    enabled: true
    max_entries: 100
    positive_ttl: 1m
    negative_ttl: 1s
`, server.URL))

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if got := auth.ProviderName(config.Verifier); got != auth.ProviderHTTP {
		t.Fatalf("auth provider = %q, want %q", got, auth.ProviderHTTP)
	}
	for range 2 {
		principal, verifyErr := config.Verifier.Verify(context.Background(), "token-a")
		if verifyErr != nil {
			t.Fatalf("Verify() error = %v", verifyErr)
		}
		if principal.ClientID != "client-a" {
			t.Fatalf("principal = %+v", principal)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("HTTP verification requests = %d, want 1", got)
	}
}

func TestLoadServerConfigJWTAuthProvider(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwks := configTestRSAJWKS(t, &privateKey.PublicKey, "config-key")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(jwks)
	}))
	defer server.Close()

	path := writeConfig(t, fmt.Sprintf(`
auth:
  type: jwt
  jwt:
    issuer: https://identity.example.test
    audience: z-courier
    jwks_url: %s
    algorithms: [RS256]
    client_id_claim: cid
    token_id_claim: tid
    scopes_claim: permissions
    clock_skew: 30s
    refresh_interval: 1h
    fetch_timeout: 1s
    max_response_body_size: 65536
  cache:
    enabled: true
    max_entries: 100
    positive_ttl: 1m
    negative_ttl: 1s
`, server.URL))

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if closer, ok := config.Verifier.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if got := auth.ProviderName(config.Verifier); got != auth.ProviderJWT {
		t.Fatalf("auth provider = %q, want %q", got, auth.ProviderJWT)
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, jwtlib.MapClaims{
		"iss": "https://identity.example.test", "aud": "z-courier",
		"exp": time.Now().Add(time.Hour).Unix(), "cid": "client-a",
		"tid": "token-a", "permissions": []string{"push", "status"},
	})
	token.Header["kid"] = "config-key"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	for range 2 {
		principal, verifyErr := config.Verifier.Verify(context.Background(), signed)
		if verifyErr != nil {
			t.Fatalf("Verify() error = %v", verifyErr)
		}
		if principal.ClientID != "client-a" || principal.TokenID != "token-a" || len(principal.Scopes) != 2 {
			t.Fatalf("principal = %+v", principal)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}
}

func TestLoadServerConfigRejectsInvalidAuthProvider(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{name: "unknown", config: "auth:\n  type: unknown\n", message: "unsupported auth provider"},
		{name: "static without tokens", config: "auth:\n  type: static\n", message: "requires static_tokens"},
		{name: "http without URL", config: "auth:\n  type: http\n", message: "requires auth.http.url"},
		{name: "http invalid URL", config: "auth:\n  type: http\n  http:\n    url: ftp://backend.local/verify\n", message: "absolute http or https URL"},
		{name: "http invalid timeout", config: "auth:\n  type: http\n  http:\n    url: http://backend.local/verify\n    timeout: 0s\n", message: "auth.http.timeout"},
		{name: "http invalid capacity", config: "auth:\n  type: http\n  http:\n    url: http://backend.local/verify\n    max_in_flight: -1\n", message: "max_in_flight"},
		{name: "http without type", config: "auth:\n  http:\n    url: http://backend.local/verify\n", message: "auth.type is required"},
		{name: "conflicting providers", config: "auth:\n  type: http\n  static_tokens: {}\n  http:\n    url: http://backend.local/verify\n", message: "conflicting provider configuration"},
		{name: "negative cache size", config: "auth:\n  type: static\n  static_tokens: {}\n  cache:\n    enabled: true\n    max_entries: -1\n", message: "cache.max_entries"},
		{name: "invalid cache ttl", config: "auth:\n  type: static\n  static_tokens: {}\n  cache:\n    enabled: true\n    positive_ttl: 0s\n", message: "cache.positive_ttl"},
		{name: "jwt incomplete", config: "auth:\n  type: jwt\n", message: "requires issuer"},
		{name: "jwt symmetric algorithm", config: "auth:\n  type: jwt\n  jwt:\n    issuer: https://issuer.local\n    audience: z-courier\n    jwks_url: https://issuer.local/.well-known/jwks.json\n    algorithms: [HS256]\n", message: "unsupported for JWKS"},
		{name: "jwt invalid clock skew", config: "auth:\n  type: jwt\n  jwt:\n    issuer: https://issuer.local\n    audience: z-courier\n    jwks_url: https://issuer.local/.well-known/jwks.json\n    algorithms: [RS256]\n    clock_skew: -1s\n", message: "clock_skew"},
		{name: "jwt invalid refresh", config: "auth:\n  type: jwt\n  jwt:\n    issuer: https://issuer.local\n    audience: z-courier\n    jwks_url: https://issuer.local/.well-known/jwks.json\n    algorithms: [RS256]\n    refresh_interval: 0s\n", message: "refresh_interval"},
		{name: "jwt invalid body size", config: "auth:\n  type: jwt\n  jwt:\n    issuer: https://issuer.local\n    audience: z-courier\n    jwks_url: https://issuer.local/.well-known/jwks.json\n    algorithms: [RS256]\n    max_response_body_size: -1\n", message: "max_response_body_size"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeConfig(t, test.config))
			if !errors.Is(err, auth.ErrMisconfigured) {
				t.Fatalf("LoadServerConfig() error = %v, want ErrMisconfigured", err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %q, want substring %q", err, test.message)
			}
		})
	}
}

func configTestRSAJWKS(t *testing.T, key *rsa.PublicKey, keyID string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "kid": keyID, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return body
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "z-courier.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
