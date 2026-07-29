package pipeline

import (
	"context"
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type QuotaDecision string

const (
	QuotaDecisionAllowed              QuotaDecision = "allowed"
	QuotaDecisionRateLimited          QuotaDecision = resilience.ReasonRateLimited
	QuotaDecisionOverloaded           QuotaDecision = resilience.ReasonOverloaded
	QuotaDecisionAdmissionUnavailable QuotaDecision = resilience.ReasonAdmissionUnavailable
)

type QuotaRequest struct {
	PolicyName  string
	KeyScope    string
	KeyValue    string
	TokenBucket TokenBucketConfig
}

type QuotaStore interface {
	Admit(context.Context, QuotaRequest) (QuotaDecision, error)
}

func validateQuotaRequest(request QuotaRequest) error {
	if request.PolicyName == "" {
		return fmt.Errorf("traffic policy quota request policy name is required")
	}
	if request.KeyScope == "" {
		return fmt.Errorf("traffic policy quota request key scope is required")
	}
	if request.KeyValue == "" {
		return fmt.Errorf("traffic policy quota request key value is required")
	}
	if request.TokenBucket.Capacity <= 0 {
		return fmt.Errorf("traffic policy quota request capacity must be greater than zero")
	}
	if request.TokenBucket.RefillTokens <= 0 {
		return fmt.Errorf("traffic policy quota request refill tokens must be greater than zero")
	}
	if request.TokenBucket.RefillInterval <= 0 {
		return fmt.Errorf("traffic policy quota request refill interval must be greater than zero")
	}
	return nil
}
