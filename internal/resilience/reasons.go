package resilience

const (
	ReasonOverloaded     = "overloaded"
	ReasonRateLimited    = "rate_limited"
	ReasonUpstreamFailed = "upstream_failed"
	ResultOverloaded     = ReasonOverloaded
	ResultUpstreamFail   = "failure"
)
