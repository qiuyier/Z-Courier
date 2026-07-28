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
			name: "redis not operational",
			config: `
pipeline:
  traffic_policies:
    enabled: true
    mode: redis
`,
			message: "not supported yet",
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
