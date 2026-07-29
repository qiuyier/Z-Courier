package pipeline

import "time"

type Config struct {
	Policy          PolicyConfig
	RateLimit       RateLimitConfig
	TrafficPolicies TrafficPoliciesConfig
}

type PolicyConfig struct {
	AllowClientIDs []string
	BlockClientIDs []string
	AllowMsgIDs    []uint32
	BlockMsgIDs    []uint32
}

type RateLimitConfig struct {
	Enabled     bool
	MaxRequests int
	Window      time.Duration
}

const (
	TrafficPolicyModeLocal             = "local"
	TrafficPolicyModeRedis             = "redis"
	TrafficPolicyKeyClientID           = "client_id"
	TrafficPolicyFailureModeFailClosed = "fail_closed"
)

type TrafficPoliciesConfig struct {
	Enabled       bool
	Mode          string
	MaxKeys       int
	IdleTTL       time.Duration
	Redis         RedisQuotaStoreConfig
	DefaultPolicy string
	Policies      []TrafficPolicy
	Routes        []TrafficPolicyRoute
}

type TrafficPolicy struct {
	Name        string
	Priority    int
	Match       TrafficPolicyMatch
	Key         string
	TokenBucket TokenBucketConfig
}

type TrafficPolicyMatch struct {
	ClientIDs []string
	MsgIDMin  uint32
	MsgIDMax  uint32
	Routes    []string
}

type TokenBucketConfig struct {
	Capacity       int
	RefillTokens   int
	RefillInterval time.Duration
}

type TrafficPolicyRoute struct {
	Name     string
	MsgIDMin uint32
	MsgIDMax uint32
}
