package resilience

const (
	ReasonOverloaded   = "overloaded"
	ReasonRateLimited  = "rate_limited"
	ResultOverloaded   = ReasonOverloaded
	ResultUpstreamFail = "failure"
)
