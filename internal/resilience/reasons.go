package resilience

const (
	ReasonOverloaded           = "overloaded"
	ReasonRateLimited          = "rate_limited"
	ReasonAdmissionUnavailable = "admission_unavailable"
	ReasonUpstreamFailed       = "upstream_failed"
	ResultOverloaded           = ReasonOverloaded
	ResultUpstreamFail         = "failure"
)
