package pipeline

import "time"

type Config struct {
	Policy    PolicyConfig
	RateLimit RateLimitConfig
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
