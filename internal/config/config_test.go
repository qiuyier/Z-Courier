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

func TestLoadExpandsEnvironmentPlaceholders(t *testing.T) {
	t.Setenv("ZCOURIER_TEST_CLIENT_ID", "client-from-env")
	t.Setenv("ZCOURIER_TEST_TOKEN", "token-from-env")

	path := writeConfig(t, `
auth:
  static_tokens:
    ${ZCOURIER_TEST_TOKEN}:
      client_id: ${ZCOURIER_TEST_CLIENT_ID}
`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	principal := config.Auth.StaticTokens["token-from-env"]
	if principal.ClientID != "client-from-env" {
		t.Fatalf("expanded client_id = %q, want client-from-env", principal.ClientID)
	}
}

func TestLoadRejectsMissingEnvironmentPlaceholder(t *testing.T) {
	t.Setenv("ZCOURIER_MISSING_TEST", "")
	os.Unsetenv("ZCOURIER_MISSING_TEST")

	path := writeConfig(t, `
auth:
  static_tokens:
    token-a:
      client_id: ${ZCOURIER_MISSING_TEST}
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want missing environment error")
	}
	if !strings.Contains(err.Error(), "missing environment variables: ZCOURIER_MISSING_TEST") {
		t.Fatalf("Load() error = %q, want missing env message", err)
	}
}

func TestLoadRejectsMalformedEnvironmentPlaceholder(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name: "unterminated",
			config: `
auth:
  static_tokens:
    token-a:
      client_id: ${ZCOURIER_CLIENT_ID
`,
			message: "unterminated environment placeholder",
		},
		{
			name: "invalid name",
			config: `
auth:
  static_tokens:
    token-a:
      client_id: ${1INVALID}
`,
			message: "invalid environment placeholder",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, test.config))
			if err == nil {
				t.Fatal("Load() error = nil, want placeholder error")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load() error = %q, want substring %q", err, test.message)
			}
		})
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
admin_console:
  enabled: true
  path: /ops/
  assets_dir: web/admin/dist
  session:
    enabled: true
    ttl: 2h
    cookie_name: zcourier_ops_session
    cookie_secure: true
    cookie_same_site: strict
    role: operator
    store:
      type: redis
      redis:
        addr: 127.0.0.1:16379
        username: admin-session-user
        password: admin-session-pass
        db: 3
        key_prefix: zcourier:test:admin-session
        dial_timeout: 600ms
        read_timeout: 800ms
        write_timeout: 950ms
        operation_timeout: 1200ms
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
    retry_jitter: 3s
    ack_timeout: 12s
    retry_lease: 13s
    max_attempts: 6
    scan_limit: 77
    bind_flush_limit: 88
    retry_fairness:
      enabled: true
      candidate_multiplier: 6
  policies:
    - name: critical
      msg_id_min: 4000
      msg_id_max: 4099
      max_attempts: 9
      max_age: 10m
      ack_timeout: 4s
      retry_delay: 2s
      backoff_multiplier: 2
      max_retry_delay: 20s
      retry_jitter: 500ms
    - name: bulk
      msg_id_min: 5000
  terminal:
    publisher:
      type: nsq
      nsq:
        nsqd_addrs:
          - 127.0.0.1:4250
          - 127.0.0.1:4251
        topic: downlink_terminal_events
        auth_secret: terminal-secret
        dial_timeout: 2s
        read_timeout: 50s
        write_timeout: 3s
        publish_mode: round_robin
        retry_attempts: 1
    retry_interval: 8s
    retry_delay: 14s
    retry_jitter: 4s
    backoff_multiplier: 3
    max_retry_delay: 2m
    retry_lease: 16s
    scan_limit: 66
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
	if !config.AdminConsole.Enabled || config.AdminConsole.Path != "/ops/" {
		t.Fatalf("AdminConsole = %+v, want enabled /ops/", config.AdminConsole)
	}
	if !config.AdminConsole.Session.Enabled ||
		config.AdminConsole.Session.TTL != 2*time.Hour ||
		config.AdminConsole.Session.CookieName != "zcourier_ops_session" ||
		!config.AdminConsole.Session.CookieSecure ||
		config.AdminConsole.Session.CookieSameSite != "strict" ||
		config.AdminConsole.Session.Role != "operator" ||
		config.AdminConsole.Session.Store.Type != "redis" ||
		config.AdminConsole.Session.Store.Redis.Addr != "127.0.0.1:16379" ||
		config.AdminConsole.Session.Store.Redis.Username != "admin-session-user" ||
		config.AdminConsole.Session.Store.Redis.Password != "admin-session-pass" ||
		config.AdminConsole.Session.Store.Redis.DB != 3 ||
		config.AdminConsole.Session.Store.Redis.KeyPrefix != "zcourier:test:admin-session" ||
		config.AdminConsole.Session.Store.Redis.DialTimeout != 600*time.Millisecond ||
		config.AdminConsole.Session.Store.Redis.ReadTimeout != 800*time.Millisecond ||
		config.AdminConsole.Session.Store.Redis.WriteTimeout != 950*time.Millisecond ||
		config.AdminConsole.Session.Store.Redis.OperationTimeout != 1200*time.Millisecond {
		t.Fatalf("AdminConsole Session = %+v, want configured session", config.AdminConsole.Session)
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
	if config.DownlinkDelivery.RetryJitter != 3*time.Second {
		t.Fatalf("DownlinkDelivery RetryJitter = %v, want 3s", config.DownlinkDelivery.RetryJitter)
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
	if !config.DownlinkDelivery.RetryFairness.Enabled || config.DownlinkDelivery.RetryFairness.CandidateMultiplier != 6 {
		t.Fatalf("DownlinkDelivery RetryFairness = %+v", config.DownlinkDelivery.RetryFairness)
	}
	if len(config.DownlinkPolicies) != 2 {
		t.Fatalf("DownlinkPolicies length = %d, want 2", len(config.DownlinkPolicies))
	}
	critical := config.DownlinkPolicies[0]
	if critical.Policy.Name != "critical" || critical.MsgIDMin != 4000 || critical.MsgIDMax != 4099 {
		t.Fatalf("critical DownlinkPolicy = %+v", critical)
	}
	if critical.Policy.MaxAttempts != 9 ||
		critical.Policy.MaxAge != 10*time.Minute ||
		critical.Policy.AckTimeout != 4*time.Second ||
		critical.Policy.InitialRetryDelay != 2*time.Second ||
		critical.Policy.BackoffMultiplier != 2 ||
		critical.Policy.MaxRetryDelay != 20*time.Second ||
		critical.Policy.RetryJitter != 500*time.Millisecond {
		t.Fatalf("critical DownlinkPolicy settings = %+v", critical.Policy)
	}
	bulk := config.DownlinkPolicies[1]
	if bulk.Policy.Name != "bulk" || bulk.MsgIDMin != 5000 || bulk.MsgIDMax != 5000 {
		t.Fatalf("bulk DownlinkPolicy = %+v", bulk)
	}
	if bulk.Policy.MaxAttempts != 6 ||
		bulk.Policy.MaxAge != 0 ||
		bulk.Policy.AckTimeout != 12*time.Second ||
		bulk.Policy.InitialRetryDelay != 11*time.Second ||
		bulk.Policy.BackoffMultiplier != 1 ||
		bulk.Policy.MaxRetryDelay != 11*time.Second ||
		bulk.Policy.RetryJitter != 3*time.Second {
		t.Fatalf("bulk inherited DownlinkPolicy settings = %+v", bulk.Policy)
	}
	if config.DownlinkTerminal.PublisherType != "nsq" ||
		len(config.DownlinkTerminal.NSQ.Addresses) != 2 ||
		config.DownlinkTerminal.NSQ.Topic != "downlink_terminal_events" ||
		config.DownlinkTerminal.NSQ.AuthSecret != "terminal-secret" ||
		config.DownlinkTerminal.NSQ.DialTimeout != 2*time.Second ||
		config.DownlinkTerminal.NSQ.ReadTimeout != 50*time.Second ||
		config.DownlinkTerminal.NSQ.WriteTimeout != 3*time.Second ||
		config.DownlinkTerminal.NSQ.RetryAttempts != 1 ||
		config.DownlinkTerminal.RetryInterval != 8*time.Second ||
		config.DownlinkTerminal.RetryDelay != 14*time.Second ||
		config.DownlinkTerminal.RetryJitter != 4*time.Second ||
		config.DownlinkTerminal.BackoffMultiplier != 3 ||
		config.DownlinkTerminal.MaxRetryDelay != 2*time.Minute ||
		config.DownlinkTerminal.RetryLease != 16*time.Second ||
		config.DownlinkTerminal.ScanLimit != 66 {
		t.Fatalf("DownlinkTerminal = %+v", config.DownlinkTerminal)
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

func TestLoadServerConfigAdminConsole(t *testing.T) {
	path := writeConfig(t, `
internal_http:
  enabled: true
admin_console:
  enabled: true
  path: /ops
  assets_dir: web/admin/dist
  monitoring:
    prometheus_url: http://prometheus.local:9090
    grafana_url: /grafana
    dashboard_url: https://grafana.local/d/z-courier-overview
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if !config.AdminConsole.Enabled {
		t.Fatal("AdminConsole.Enabled = false, want true")
	}
	if config.AdminConsole.Path != "/ops/" {
		t.Fatalf("AdminConsole.Path = %q, want /ops/", config.AdminConsole.Path)
	}
	if config.AdminConsole.AssetsDir != "web/admin/dist" {
		t.Fatalf("AdminConsole.AssetsDir = %q, want web/admin/dist", config.AdminConsole.AssetsDir)
	}
	if config.AdminConsole.Monitoring.PrometheusURL != "http://prometheus.local:9090" {
		t.Fatalf("AdminConsole.Monitoring.PrometheusURL = %q", config.AdminConsole.Monitoring.PrometheusURL)
	}
	if config.AdminConsole.Monitoring.GrafanaURL != "/grafana" {
		t.Fatalf("AdminConsole.Monitoring.GrafanaURL = %q", config.AdminConsole.Monitoring.GrafanaURL)
	}
	if config.AdminConsole.Monitoring.DashboardURL != "https://grafana.local/d/z-courier-overview" {
		t.Fatalf("AdminConsole.Monitoring.DashboardURL = %q", config.AdminConsole.Monitoring.DashboardURL)
	}
}

func TestLoadServerConfigAdminAuditStorage(t *testing.T) {
	path := writeConfig(t, `
admin_console:
  audit:
    type: postgres
    capacity: 123
    postgres:
      dsn: postgres://zcourier:secret@postgres:5432/zcourier?sslmode=disable
      auto_migrate: false
      max_open_conns: 7
      max_idle_conns: 3
      conn_max_lifetime: 11m
      operation_timeout: 1500ms
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.AdminAuditStorage.Type != "postgres" {
		t.Fatalf("AdminAuditStorage.Type = %q, want postgres", config.AdminAuditStorage.Type)
	}
	if config.AdminAuditStorage.Capacity != 123 {
		t.Fatalf("AdminAuditStorage.Capacity = %d, want 123", config.AdminAuditStorage.Capacity)
	}
	if config.AdminAuditStorage.Postgres.DSN != "postgres://zcourier:secret@postgres:5432/zcourier?sslmode=disable" {
		t.Fatalf("AdminAuditStorage.Postgres.DSN = %q", config.AdminAuditStorage.Postgres.DSN)
	}
	if config.AdminAuditStorage.Postgres.AutoMigrate {
		t.Fatal("AdminAuditStorage.Postgres.AutoMigrate = true, want false")
	}
	if !config.AdminAuditStorage.Postgres.AutoMigrateSet {
		t.Fatal("AdminAuditStorage.Postgres.AutoMigrateSet = false, want true")
	}
	if config.AdminAuditStorage.Postgres.MaxOpenConns != 7 {
		t.Fatalf("AdminAuditStorage.Postgres.MaxOpenConns = %d, want 7", config.AdminAuditStorage.Postgres.MaxOpenConns)
	}
	if config.AdminAuditStorage.Postgres.MaxIdleConns != 3 {
		t.Fatalf("AdminAuditStorage.Postgres.MaxIdleConns = %d, want 3", config.AdminAuditStorage.Postgres.MaxIdleConns)
	}
	if config.AdminAuditStorage.Postgres.ConnMaxLifetime != 11*time.Minute {
		t.Fatalf("AdminAuditStorage.Postgres.ConnMaxLifetime = %v, want 11m", config.AdminAuditStorage.Postgres.ConnMaxLifetime)
	}
	if config.AdminAuditStorage.Postgres.OperationTimeout != 1500*time.Millisecond {
		t.Fatalf("AdminAuditStorage.Postgres.OperationTimeout = %v, want 1500ms", config.AdminAuditStorage.Postgres.OperationTimeout)
	}
}

func TestLoadServerConfigRejectsInvalidAdminConsolePath(t *testing.T) {
	path := writeConfig(t, `
admin_console:
  enabled: true
  path: /internal/admin
  assets_dir: web/admin/dist
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want admin console path error")
	}
	if !strings.Contains(err.Error(), "conflicts with internal HTTP routes") {
		t.Fatalf("LoadServerConfig() error = %q, want conflict message", err)
	}
}

func TestLoadServerConfigRejectsInvalidAdminConsoleMonitoringLink(t *testing.T) {
	path := writeConfig(t, `
admin_console:
  monitoring:
    prometheus_url: javascript:alert(1)
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want monitoring link error")
	}
	if !strings.Contains(err.Error(), "admin_console.monitoring.prometheus_url") {
		t.Fatalf("LoadServerConfig() error = %q, want monitoring field", err)
	}
}

func TestLoadServerConfigRejectsInvalidAdminConsoleSessionRole(t *testing.T) {
	path := writeConfig(t, `
admin_console:
  session:
    role: owner
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want session role error")
	}
	if !strings.Contains(err.Error(), "admin_console.session.role") {
		t.Fatalf("LoadServerConfig() error = %q, want session role field", err)
	}
}

func TestLoadServerConfigRejectsInvalidAdminSessionStore(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "unsupported type",
			yaml: `
admin_console:
  session:
    store:
      type: postgres
`,
			wantErr: "unsupported admin_console.session.store.type",
		},
		{
			name: "missing redis addr",
			yaml: `
admin_console:
  session:
    store:
      type: redis
`,
			wantErr: "admin_console.session.store.redis.addr is required",
		},
		{
			name: "negative redis db",
			yaml: `
admin_console:
  session:
    store:
      redis:
        db: -1
`,
			wantErr: "admin_console.session.store.redis.db",
		},
		{
			name: "invalid operation timeout",
			yaml: `
admin_console:
  session:
    store:
      redis:
        operation_timeout: -1s
`,
			wantErr: "admin_console.session.store.redis.operation_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.yaml)
			_, err := LoadServerConfig(path)
			if err == nil {
				t.Fatal("LoadServerConfig() error = nil, want admin session store error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadServerConfig() error = %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadServerConfigRejectsInvalidAdminAuditStorage(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "unsupported type",
			yaml: `
admin_console:
  audit:
    type: sqlite
`,
			wantErr: "unsupported admin_console.audit type",
		},
		{
			name: "missing postgres dsn",
			yaml: `
admin_console:
  audit:
    type: postgres
`,
			wantErr: "admin_console.audit.postgres dsn is required",
		},
		{
			name: "invalid operation timeout",
			yaml: `
admin_console:
  audit:
    postgres:
      operation_timeout: -1s
`,
			wantErr: "admin_console.audit.postgres.operation_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.yaml)
			_, err := LoadServerConfig(path)
			if err == nil {
				t.Fatal("LoadServerConfig() error = nil, want admin audit error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadServerConfig() error = %q, want %q", err, tt.wantErr)
			}
		})
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

func TestLoadServerConfigDownlinkTerminalDefaults(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `{}`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	terminal := config.DownlinkTerminal
	if terminal.PublisherType != "none" || terminal.RetryInterval != 5*time.Second ||
		terminal.RetryDelay != 30*time.Second || terminal.RetryJitter != 0 ||
		terminal.BackoffMultiplier != 2 || terminal.MaxRetryDelay != 5*time.Minute ||
		terminal.RetryLease != 30*time.Second || terminal.ScanLimit != 100 {
		t.Fatalf("DownlinkTerminal defaults = %+v", terminal)
	}
}

func TestLoadServerConfigDownlinkQueueCapacity(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
downlink:
  storage:
    type: memory
  capacity:
    max_pending_global: 5000
    max_pending_per_device: 50
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if config.DownlinkCapacity.MaxPendingGlobal != 5000 || config.DownlinkCapacity.MaxPendingPerDevice != 50 {
		t.Fatalf("DownlinkCapacity = %+v", config.DownlinkCapacity)
	}
}

func TestLoadServerConfigRejectsInvalidDownlinkQueueCapacity(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name:    "negative global",
			config:  "downlink:\n  capacity:\n    max_pending_global: -1\n",
			message: "max_pending_global must be greater than or equal to 0",
		},
		{
			name:    "negative device",
			config:  "downlink:\n  capacity:\n    max_pending_per_device: -1\n",
			message: "max_pending_per_device must be greater than or equal to 0",
		},
		{
			name:    "device exceeds global",
			config:  "downlink:\n  capacity:\n    max_pending_global: 10\n    max_pending_per_device: 11\n",
			message: "max_pending_per_device must not exceed max_pending_global",
		},
		{
			name:    "capacity without storage",
			config:  "downlink:\n  storage:\n    type: none\n  capacity:\n    max_pending_global: 10\n",
			message: "capacity requires downlink storage",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeConfig(t, test.config))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadServerConfigRejectsInvalidDownlinkTerminal(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name:    "unsupported publisher",
			config:  "downlink:\n  terminal:\n    publisher:\n      type: kafka\n",
			message: "unsupported downlink terminal publisher type",
		},
		{
			name:    "nsq without address",
			config:  "downlink:\n  terminal:\n    publisher:\n      type: nsq\n      nsq:\n        topic: terminal_events\n",
			message: "addr or nsqd_addrs is required",
		},
		{
			name:    "publisher without storage",
			config:  "downlink:\n  storage:\n    type: none\n  terminal:\n    publisher:\n      type: nsq\n      nsq:\n        addr: 127.0.0.1:4150\n        topic: terminal_events\n",
			message: "requires downlink storage",
		},
		{
			name:    "max delay below initial delay",
			config:  "downlink:\n  terminal:\n    retry_delay: 10s\n    max_retry_delay: 5s\n",
			message: "max_retry_delay must be greater than or equal to retry_delay",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeConfig(t, test.config))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %v, want substring %q", err, test.message)
			}
		})
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

func TestLoadServerConfigInvalidDownlinkRetryJitter(t *testing.T) {
	path := writeConfig(t, `
downlink:
  delivery:
    retry_jitter: -1s
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

func TestLoadServerConfigRejectsInvalidRetryFairness(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  string
		message string
	}{
		{
			name:    "negative candidate multiplier",
			config:  "downlink:\n  delivery:\n    retry_fairness:\n      candidate_multiplier: -1\n",
			message: "candidate_multiplier must be greater than or equal to 0",
		},
		{
			name:    "candidate multiplier too large",
			config:  "downlink:\n  delivery:\n    retry_fairness:\n      candidate_multiplier: 17\n",
			message: "candidate_multiplier must not exceed 16",
		},
		{
			name:    "fairness without storage",
			config:  "downlink:\n  storage:\n    type: none\n  delivery:\n    retry_fairness:\n      enabled: true\n",
			message: "retry_fairness requires downlink storage",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeConfig(t, test.config))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadServerConfigRejectsInvalidDownlinkPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  string
		message string
	}{
		{
			name: "overlapping ranges",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
      msg_id_max: 2099
    - name: bulk
      msg_id_min: 2099
      msg_id_max: 2199
`,
			message: "overlaps",
		},
		{
			name: "duplicate names",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
    - name: critical
      msg_id_min: 3000
`,
			message: "duplicate delivery policy name",
		},
		{
			name: "invalid duration",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
      max_age: tomorrow
`,
			message: "max_age",
		},
		{
			name: "multiplier without maximum",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
      backoff_multiplier: 2
`,
			message: "max_retry_delay is required",
		},
		{
			name: "maximum below initial delay",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
      retry_delay: 10s
      max_retry_delay: 5s
`,
			message: "max_retry_delay must be greater than or equal to retry_delay",
		},
		{
			name: "zero attempts",
			config: `
downlink:
  policies:
    - name: critical
      msg_id_min: 2000
      max_attempts: 0
`,
			message: "max_attempts",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.config)
			_, err := LoadServerConfig(path)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestLoadServerConfigIgnoresDisabledDownlinkPolicy(t *testing.T) {
	path := writeConfig(t, `
downlink:
  policies:
    - enabled: false
      name: Invalid Name
      msg_id_min: 0
      max_age: tomorrow
`)

	config, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
	if len(config.DownlinkPolicies) != 0 {
		t.Fatalf("DownlinkPolicies = %+v, want empty", config.DownlinkPolicies)
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

func TestValidateFileRejectsOverlappingUpstreamRoutes(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: first
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        url: http://backend-a.local
    - name: second
      msg_id_min: 1500
      msg_id_max: 2500
      target:
        type: http
        url: http://backend-b.local
`)

	_, err := ValidateFile(path)
	if err == nil {
		t.Fatal("ValidateFile() error = nil, want overlap error")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("ValidateFile() error = %q, want overlap message", err)
	}
}

func TestLoadServerConfigRejectsReservedUpstreamMsgID(t *testing.T) {
	path := writeConfig(t, `
upstream:
  routes:
    - name: bind-conflict
      msg_id_min: 999
      msg_id_max: 1001
      target:
        type: http
        url: http://backend.local
`)

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("LoadServerConfig() error = nil, want reserved MsgID error")
	}
	if !strings.Contains(err.Error(), "reserved msg_id 1000") {
		t.Fatalf("LoadServerConfig() error = %q, want reserved MsgID message", err)
	}
}

func TestValidateFileWarnsForMemoryClusterRegistry(t *testing.T) {
	path := writeConfig(t, `
cluster:
  enabled: true
  internal_addr: http://127.0.0.1:18080
  registry:
    type: memory
downlink:
  storage:
    type: memory
`)

	report, err := ValidateFile(path)
	if err != nil {
		t.Fatalf("ValidateFile() error = %v", err)
	}
	if len(report.Warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 warnings", report.Warnings)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "memory") {
		t.Fatalf("warnings = %v, want memory warnings", report.Warnings)
	}
}

func TestValidateFileWarnsForMemoryAdminSessionStoreInCluster(t *testing.T) {
	path := writeConfig(t, `
cluster:
  enabled: true
  internal_addr: http://127.0.0.1:18080
  registry:
    type: redis
    redis:
      addr: 127.0.0.1:6379
admin_console:
  session:
    enabled: true
    store:
      type: memory
downlink:
  storage:
    type: postgres
    postgres:
      dsn: postgres://zcourier:zcourier@127.0.0.1:5432/zcourier?sslmode=disable
`)

	report, err := ValidateFile(path)
	if err != nil {
		t.Fatalf("ValidateFile() error = %v", err)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 warning", report.Warnings)
	}
	if !strings.Contains(report.Warnings[0], "memory admin console session store") {
		t.Fatalf("warnings = %v, want admin session store warning", report.Warnings)
	}
}

func TestValidateFileDoesNotFetchJWKS(t *testing.T) {
	path := writeConfig(t, `
auth:
  type: jwt
  jwt:
    issuer: https://issuer.local
    audience: z-courier
    jwks_url: http://127.0.0.1:1/.well-known/jwks.json
    algorithms:
      - RS256
`)

	if _, err := ValidateFile(path); err != nil {
		t.Fatalf("ValidateFile() error = %v", err)
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
