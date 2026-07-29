package pipeline

import (
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type TrafficPolicyHandler struct {
	selector *TrafficPolicySelector
	store    QuotaStore
}

func NewTrafficPolicyHandler(config TrafficPoliciesConfig) *TrafficPolicyHandler {
	if !config.Enabled {
		return nil
	}
	var store QuotaStore
	if config.Mode == TrafficPolicyModeLocal {
		store = NewLocalQuotaStore(LocalQuotaStoreConfig{
			MaxKeys: config.MaxKeys,
			IdleTTL: config.IdleTTL,
		})
	}
	return NewTrafficPolicyHandlerWithStore(config, store)
}

func NewTrafficPolicyHandlerWithStore(
	config TrafficPoliciesConfig,
	store QuotaStore,
) *TrafficPolicyHandler {
	if !config.Enabled {
		return nil
	}
	return &TrafficPolicyHandler{
		selector: NewTrafficPolicySelector(config),
		store:    store,
	}
}

func (h *TrafficPolicyHandler) Handle(ctx *Context) error {
	if h == nil {
		return nil
	}
	if h.selector == nil || h.store == nil {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policies are misconfigured"))
	}
	if ctx == nil || ctx.Packet == nil {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policy packet is missing"))
	}
	if ctx.Principal == nil || ctx.Principal.ClientID == "" {
		return Reject(protocol.AckUnauthorized, fmt.Errorf("traffic policy requires an authenticated client"))
	}

	policy, selected := h.selector.Select(ctx.Principal.ClientID, ctx.Packet.MsgID)
	if !selected {
		return nil
	}
	if policy.Key != TrafficPolicyKeyClientID ||
		policy.TokenBucket.Capacity <= 0 ||
		policy.TokenBucket.RefillTokens <= 0 ||
		policy.TokenBucket.RefillInterval <= 0 {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policy %q is misconfigured", policy.Name))
	}

	decision, err := h.store.Admit(ctx.Context(), QuotaRequest{
		PolicyName:  policy.Name,
		KeyScope:    policy.Key,
		KeyValue:    ctx.Principal.ClientID,
		TokenBucket: policy.TokenBucket,
	})
	if err != nil {
		decision = QuotaDecisionAdmissionUnavailable
	}
	if decision == QuotaDecisionAllowed {
		return nil
	}

	metrics.RecordRateLimitRejected(ctx.Packet.MsgID)
	switch decision {
	case QuotaDecisionRateLimited:
		return RejectWithReason(
			protocol.AckRejected,
			resilience.ReasonRateLimited,
			fmt.Errorf("traffic policy %q rate limit exceeded", policy.Name),
		)
	case QuotaDecisionOverloaded:
		return RejectWithReason(
			protocol.AckRejected,
			resilience.ReasonOverloaded,
			fmt.Errorf("traffic policy %q key capacity is exhausted", policy.Name),
		)
	default:
		return RejectWithReason(
			protocol.AckRejected,
			resilience.ReasonAdmissionUnavailable,
			fmt.Errorf("traffic policy %q admission is unavailable", policy.Name),
		)
	}
}
