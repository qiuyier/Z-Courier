package config

import (
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/pipeline"
)

func TestLoadServerConfigTrafficPolicies(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: true
    mode: local
    max_keys: 50
    idle_ttl: 30s
    default_policy: standard
    policies:
      - name: standard
        priority: 100
        match:
          msg_id_min: 1001
          msg_id_max: 2999
        key: client_id
        token_bucket:
          capacity: 100
          refill_tokens: 50
          refill_interval: 1s
      - name: orders
        priority: 200
        match:
          client_ids: [client-a]
          routes: [orders-http]
        key: client_id
        token_bucket:
          capacity: 20
          refill_tokens: 10
          refill_interval: 500ms
upstream:
  routes:
    - name: orders-http
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        type: http
        url: http://backend.local/gateway/upstream
    - name: events-nsq
      msg_id_min: 2000
      msg_id_max: 2999
      target:
        type: nsq
        nsqd_addrs: [127.0.0.1:4150]
        topic: message_events
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	traffic := config.Pipeline.TrafficPolicies
	if !traffic.Enabled || traffic.Mode != pipeline.TrafficPolicyModeLocal {
		t.Fatalf("TrafficPolicies enabled/mode = %t/%q", traffic.Enabled, traffic.Mode)
	}
	if traffic.MaxKeys != 50 || traffic.IdleTTL != 30*time.Second || traffic.DefaultPolicy != "standard" {
		t.Fatalf("TrafficPolicies global config = %+v", traffic)
	}
	if len(traffic.Policies) != 2 {
		t.Fatalf("TrafficPolicies policies = %d, want 2", len(traffic.Policies))
	}
	orders := traffic.Policies[1]
	if orders.Name != "orders" ||
		orders.Priority != 200 ||
		orders.Key != pipeline.TrafficPolicyKeyClientID ||
		orders.TokenBucket.Capacity != 20 ||
		orders.TokenBucket.RefillTokens != 10 ||
		orders.TokenBucket.RefillInterval != 500*time.Millisecond {
		t.Fatalf("orders policy = %+v", orders)
	}
	if len(orders.Match.ClientIDs) != 1 || orders.Match.ClientIDs[0] != "client-a" ||
		len(orders.Match.Routes) != 1 || orders.Match.Routes[0] != "orders-http" {
		t.Fatalf("orders selectors = %+v", orders.Match)
	}
	if len(traffic.Routes) != 2 ||
		traffic.Routes[0].Name != "orders-http" ||
		traffic.Routes[0].MsgIDMax != 1999 {
		t.Fatalf("TrafficPolicies routes = %+v", traffic.Routes)
	}
}

func TestLoadServerConfigTrafficPolicyDefaults(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        token_bucket:
          capacity: 1
          refill_tokens: 1
          refill_interval: 1s
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	traffic := config.Pipeline.TrafficPolicies
	if traffic.Mode != pipeline.TrafficPolicyModeLocal ||
		traffic.MaxKeys != defaultTrafficPolicyMaxKeys ||
		traffic.IdleTTL != defaultTrafficPolicyIdleTTL {
		t.Fatalf("TrafficPolicies defaults = %+v", traffic)
	}
	if traffic.Policies[0].Key != pipeline.TrafficPolicyKeyClientID {
		t.Fatalf("policy key = %q, want client_id", traffic.Policies[0].Key)
	}
}

func TestLoadServerConfigTrafficPolicyRedisContractWhenDisabled(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    idle_ttl: 45s
    redis:
      addr: 127.0.0.1:16379
      username: quota-user
      password: quota-password
      db: 4
      key_prefix: zcourier:test:quota
      dial_timeout: 700ms
      read_timeout: 350ms
      write_timeout: 400ms
      operation_timeout: 175ms
      failure_mode: fail_closed
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	traffic := config.Pipeline.TrafficPolicies
	if traffic.Enabled || traffic.Mode != pipeline.TrafficPolicyModeRedis {
		t.Fatalf("TrafficPolicies enabled/mode = %t/%q", traffic.Enabled, traffic.Mode)
	}
	redis := traffic.Redis
	if redis.Addr != "127.0.0.1:16379" ||
		redis.Username != "quota-user" ||
		redis.Password != "quota-password" ||
		redis.DB != 4 ||
		redis.KeyPrefix != "zcourier:test:quota" ||
		redis.IdleTTL != 45*time.Second ||
		redis.DialTimeout != 700*time.Millisecond ||
		redis.ReadTimeout != 350*time.Millisecond ||
		redis.WriteTimeout != 400*time.Millisecond ||
		redis.OperationTimeout != 175*time.Millisecond ||
		redis.FailureMode != pipeline.TrafficPolicyFailureModeFailClosed {
		t.Fatalf("TrafficPolicies Redis = %+v", redis)
	}
}

func TestLoadServerConfigTrafficPolicyRedisDefaultsWhenDisabled(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    redis:
      addr: 127.0.0.1:16379
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	redis := config.Pipeline.TrafficPolicies.Redis
	if redis.KeyPrefix != defaultTrafficPolicyRedisKeyPrefix ||
		redis.IdleTTL != defaultTrafficPolicyIdleTTL ||
		redis.DialTimeout != defaultTrafficPolicyRedisDialTimeout ||
		redis.ReadTimeout != defaultTrafficPolicyRedisReadTimeout ||
		redis.WriteTimeout != defaultTrafficPolicyRedisWriteTimeout ||
		redis.OperationTimeout != defaultTrafficPolicyRedisOperationTimeout ||
		redis.FailureMode != pipeline.TrafficPolicyFailureModeFailClosed {
		t.Fatalf("TrafficPolicies Redis defaults = %+v", redis)
	}
}

func TestLoadServerConfigTrafficPolicyRedisEnabled(t *testing.T) {
	config, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: true
    mode: redis
    idle_ttl: 30s
    redis:
      addr: 127.0.0.1:16379
      key_prefix: zcourier:test:enabled
      operation_timeout: 200ms
      failure_mode: fail_closed
    policies:
      - name: shared
        priority: 100
        match:
          msg_id_min: 2201
        key: client_id
        token_bucket:
          capacity: 3
          refill_tokens: 1
          refill_interval: 1m
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}

	traffic := config.Pipeline.TrafficPolicies
	if !traffic.Enabled || traffic.Mode != pipeline.TrafficPolicyModeRedis {
		t.Fatalf("TrafficPolicies enabled/mode = %t/%q", traffic.Enabled, traffic.Mode)
	}
	if traffic.Redis.Addr != "127.0.0.1:16379" ||
		traffic.Redis.KeyPrefix != "zcourier:test:enabled" ||
		traffic.Redis.OperationTimeout != 200*time.Millisecond {
		t.Fatalf("TrafficPolicies Redis = %+v", traffic.Redis)
	}
	if len(traffic.Policies) != 1 || traffic.Policies[0].Name != "shared" {
		t.Fatalf("TrafficPolicies policies = %+v", traffic.Policies)
	}
}

func TestLoadServerConfigTrafficPoliciesRejectInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name: "legacy conflict",
			config: `
pipeline:
  rate_limit:
    enabled: true
    max_requests: 10
    window: 1s
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "cannot both be enabled",
		},
		{
			name: "redis addr required",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
`,
			message: "redis.addr is required",
		},
		{
			name: "redis db invalid",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    redis:
      addr: 127.0.0.1:16379
      db: -1
`,
			message: "redis.db",
		},
		{
			name: "redis key prefix blank",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    redis:
      addr: 127.0.0.1:16379
      key_prefix: "   "
`,
			message: "redis.key_prefix",
		},
		{
			name: "redis operation timeout invalid",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    redis:
      addr: 127.0.0.1:16379
      operation_timeout: 0s
`,
			message: "redis.operation_timeout",
		},
		{
			name: "redis failure mode invalid",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    redis:
      addr: 127.0.0.1:16379
      failure_mode: local_fallback
`,
			message: "supports only",
		},
		{
			name: "redis idle ttl below precision",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: redis
    idle_ttl: 500us
    redis:
      addr: 127.0.0.1:16379
`,
			message: "at least 1ms",
		},
		{
			name: "redis settings conflict with local mode",
			config: `
pipeline:
  traffic_policies:
    enabled: false
    mode: local
    redis:
      addr: 127.0.0.1:16379
`,
			message: "require mode",
		},
		{
			name: "no enabled policy",
			config: `
pipeline:
  traffic_policies:
    enabled: true
`,
			message: "at least one enabled policy",
		},
		{
			name: "negative key capacity",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    max_keys: -1
`,
			message: "max_keys must not be negative",
		},
		{
			name: "invalid idle ttl",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    idle_ttl: 0s
`,
			message: "idle_ttl",
		},
		{
			name: "duplicate name",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: duplicate
        priority: 100
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
      - name: duplicate
        priority: 200
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "is duplicated",
		},
		{
			name: "missing default",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    default_policy: missing
    policies:
      - name: standard
        priority: 100
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "must name an enabled policy",
		},
		{
			name: "unsupported key",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        key: client_id_device_id
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "supports only",
		},
		{
			name: "invalid bucket",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        token_bucket: {capacity: 0, refill_tokens: 1, refill_interval: 1s}
`,
			message: "capacity must be greater than 0",
		},
		{
			name: "unknown route",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        match:
          routes: [missing]
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "unknown enabled route",
		},
		{
			name: "invalid msg id range",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: standard
        priority: 100
        match:
          msg_id_min: 2000
          msg_id_max: 1000
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "msg_id_max",
		},
		{
			name: "ambiguous overlap",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: first
        priority: 100
        match: {msg_id_min: 1001, msg_id_max: 2000}
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
      - name: second
        priority: 100
        match: {msg_id_min: 1500, msg_id_max: 2500}
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
`,
			message: "overlap at priority",
		},
		{
			name: "route and msg id cannot intersect",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: impossible
        priority: 100
        match:
          msg_id_min: 3000
          msg_id_max: 3999
          routes: [orders-http]
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
upstream:
  routes:
    - name: orders-http
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        url: http://backend.local/gateway/upstream
`,
			message: "cannot match the same packet",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeConfig(t, test.config))
			if err == nil {
				t.Fatal("LoadServerConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("LoadServerConfig() error = %q, want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadServerConfigAllowsDisjointPoliciesAtSamePriority(t *testing.T) {
	_, err := LoadServerConfig(writeConfig(t, `
pipeline:
  traffic_policies:
    enabled: true
    policies:
      - name: orders
        priority: 100
        match:
          routes: [orders-http]
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
      - name: events
        priority: 100
        match:
          msg_id_min: 3000
          msg_id_max: 3999
        token_bucket: {capacity: 1, refill_tokens: 1, refill_interval: 1s}
upstream:
  routes:
    - name: orders-http
      msg_id_min: 1001
      msg_id_max: 1999
      target:
        url: http://backend.local/gateway/upstream
`))
	if err != nil {
		t.Fatalf("LoadServerConfig() error = %v", err)
	}
}
