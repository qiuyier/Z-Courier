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
	if len(config.UpstreamRoutes) != 1 {
		t.Fatalf("UpstreamRoutes length = %d, want 1", len(config.UpstreamRoutes))
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
