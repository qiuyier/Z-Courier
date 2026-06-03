package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if config.InternalMaxRequestBodySize != 12345 {
		t.Fatalf("InternalMaxRequestBodySize = %d, want 12345", config.InternalMaxRequestBodySize)
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

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "z-courier.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
