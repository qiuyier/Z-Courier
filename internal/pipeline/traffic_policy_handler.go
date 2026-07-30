package pipeline

import (
	"fmt"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type TrafficPolicyHandler struct {
	selector *TrafficPolicySelector
	store    QuotaStore
	mode     string
	runtime  *TrafficPolicyRuntime
}

func NewTrafficPolicyHandler(config TrafficPoliciesConfig) *TrafficPolicyHandler {
	if !config.Enabled {
		return nil
	}
	runtime := newTrafficPolicyRuntime(config, nil)
	var store QuotaStore
	if config.Mode == TrafficPolicyModeLocal {
		store = NewLocalQuotaStore(LocalQuotaStoreConfig{
			MaxKeys: config.MaxKeys,
			IdleTTL: config.IdleTTL,
			Runtime: runtime,
		})
	}
	return newTrafficPolicyHandlerWithRuntime(config, store, runtime)
}

func NewTrafficPolicyHandlerWithStore(
	config TrafficPoliciesConfig,
	store QuotaStore,
) *TrafficPolicyHandler {
	if !config.Enabled {
		return nil
	}
	return newTrafficPolicyHandlerWithRuntime(
		config,
		store,
		newTrafficPolicyRuntime(config, nil),
	)
}

func newTrafficPolicyHandlerWithRuntime(
	config TrafficPoliciesConfig,
	store QuotaStore,
	runtime *TrafficPolicyRuntime,
) *TrafficPolicyHandler {
	return &TrafficPolicyHandler{
		selector: NewTrafficPolicySelector(config),
		store:    store,
		mode:     normalizedTrafficPolicyMode(config.Mode),
		runtime:  runtime,
	}
}

func (h *TrafficPolicyHandler) Runtime() *TrafficPolicyRuntime {
	if h == nil {
		return nil
	}
	return h.runtime
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
		metrics.RecordTrafficPolicySelection(h.mode, "", "no_match")
		h.runtime.recordNoMatch()
		return nil
	}
	metrics.RecordTrafficPolicySelection(h.mode, policy.Name, "selected")
	h.runtime.recordSelection(policy.Name)
	if policy.Key != TrafficPolicyKeyClientID ||
		policy.TokenBucket.Capacity <= 0 ||
		policy.TokenBucket.RefillTokens <= 0 ||
		policy.TokenBucket.RefillInterval <= 0 {
		return Reject(protocol.AckRejected, fmt.Errorf("traffic policy %q is misconfigured", policy.Name))
	}

	startedAt := time.Now()
	decision, err := h.store.Admit(ctx.Context(), QuotaRequest{
		PolicyName:  policy.Name,
		KeyScope:    policy.Key,
		KeyValue:    ctx.Principal.ClientID,
		TokenBucket: policy.TokenBucket,
	})
	if err != nil {
		decision = QuotaDecisionAdmissionUnavailable
	}
	decision = normalizedQuotaDecision(decision)
	metrics.RecordTrafficPolicyQuotaStore(
		h.mode,
		policy.Name,
		policy.Key,
		string(decision),
		time.Since(startedAt),
	)
	h.runtime.recordDecision(policy.Name, decision)
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

func normalizedTrafficPolicyMode(mode string) string {
	switch mode {
	case TrafficPolicyModeLocal, TrafficPolicyModeRedis:
		return mode
	default:
		return "unknown"
	}
}
