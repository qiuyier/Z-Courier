package router

import "fmt"

type FailureClass string

const (
	FailureClassEncoding  FailureClass = "encoding"
	FailureClassDiscovery FailureClass = "discovery"
	FailureClassRequest   FailureClass = "request"
	FailureClassTransport FailureClass = "transport"
	FailureClassTimeout   FailureClass = "timeout"
	FailureClassCanceled  FailureClass = "canceled"
	FailureClassResponse  FailureClass = "response"
)

type FailoverDecision string

const (
	FailoverDecisionSucceeded    FailoverDecision = "succeeded"
	FailoverDecisionDisabled     FailoverDecision = "disabled"
	FailoverDecisionNotRetryable FailoverDecision = "not_retryable"
	FailoverDecisionExhausted    FailoverDecision = "exhausted"
	FailoverDecisionNoAlternate  FailoverDecision = "no_alternate"
)

// ForwardError carries audit-safe forwarding metadata. Error deliberately
// excludes Endpoint and Cause so it is safe to surface through generic logs.
type ForwardError struct {
	Class       FailureClass
	Endpoint    string
	Attempts    int
	MaxAttempts int
	Retryable   bool
	Decision    FailoverDecision
	Cause       error
}

func (e *ForwardError) Error() string {
	if e == nil {
		return ""
	}
	failureClass := e.Class
	if failureClass == "" {
		failureClass = "unknown"
	}
	if e.Decision == "" {
		return fmt.Sprintf("upstream forward failed: class=%s", failureClass)
	}
	return fmt.Sprintf("upstream forward failed: class=%s decision=%s", failureClass, e.Decision)
}

func (e *ForwardError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
