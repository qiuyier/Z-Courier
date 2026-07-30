package pipeline

import (
	"sort"
	"sync"
	"time"
)

const (
	TrafficPolicyBucketStateAvailable            = "available"
	TrafficPolicyBucketStateDepleted             = "depleted"
	TrafficPolicyBucketStateKeyCapacityExhausted = "key_capacity_exhausted"
	TrafficPolicyBucketStateStoreUnavailable     = "store_unavailable"
)

type TrafficPolicyDecisionTotals struct {
	Allowed              uint64
	RateLimited          uint64
	Overloaded           uint64
	AdmissionUnavailable uint64
}

type TrafficPolicyRuntimePolicySnapshot struct {
	Name           string
	SelectionTotal uint64
	Decisions      TrafficPolicyDecisionTotals
	LastResult     QuotaDecision
	LastState      string
	LastDecisionAt time.Time
}

type TrafficPolicyRuntimeSnapshot struct {
	Mode              string
	NoMatchTotal      uint64
	Decisions         TrafficPolicyDecisionTotals
	LastResult        QuotaDecision
	LastState         string
	LastDecisionAt    time.Time
	LastSuccessAt     time.Time
	LastUnavailableAt time.Time
	LocalKeys         int
	LocalKeyLimit     int
	Policies          []TrafficPolicyRuntimePolicySnapshot
}

type TrafficPolicyRuntime struct {
	mu sync.RWMutex

	mode              string
	noMatchTotal      uint64
	decisions         TrafficPolicyDecisionTotals
	lastResult        QuotaDecision
	lastState         string
	lastDecisionAt    time.Time
	lastSuccessAt     time.Time
	lastUnavailableAt time.Time
	localKeys         int
	localKeyLimit     int
	policies          map[string]*trafficPolicyRuntimePolicy
	policyNames       []string
	now               func() time.Time
}

type trafficPolicyRuntimePolicy struct {
	selectionTotal uint64
	decisions      TrafficPolicyDecisionTotals
	lastResult     QuotaDecision
	lastState      string
	lastDecisionAt time.Time
}

func newTrafficPolicyRuntime(config TrafficPoliciesConfig, now func() time.Time) *TrafficPolicyRuntime {
	if now == nil {
		now = time.Now
	}
	runtime := &TrafficPolicyRuntime{
		mode:          normalizedTrafficPolicyMode(config.Mode),
		localKeyLimit: config.MaxKeys,
		policies:      make(map[string]*trafficPolicyRuntimePolicy, len(config.Policies)),
		policyNames:   make([]string, 0, len(config.Policies)),
		now:           now,
	}
	for _, policy := range config.Policies {
		if policy.Name == "" {
			continue
		}
		if _, exists := runtime.policies[policy.Name]; exists {
			continue
		}
		runtime.policies[policy.Name] = &trafficPolicyRuntimePolicy{}
		runtime.policyNames = append(runtime.policyNames, policy.Name)
	}
	sort.Strings(runtime.policyNames)
	return runtime
}

func (r *TrafficPolicyRuntime) recordNoMatch() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.noMatchTotal++
	r.mu.Unlock()
}

func (r *TrafficPolicyRuntime) recordSelection(policyName string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if policy := r.policies[policyName]; policy != nil {
		policy.selectionTotal++
	}
	r.mu.Unlock()
}

func (r *TrafficPolicyRuntime) recordDecision(policyName string, decision QuotaDecision) {
	if r == nil {
		return
	}
	decision = normalizedQuotaDecision(decision)
	state := trafficPolicyBucketState(decision)
	now := r.now()
	if now.IsZero() {
		now = time.Now()
	}

	r.mu.Lock()
	policy := r.policies[policyName]
	if policy == nil {
		r.mu.Unlock()
		return
	}
	addTrafficPolicyDecision(&policy.decisions, decision)
	policy.lastResult = decision
	policy.lastState = state
	policy.lastDecisionAt = now

	addTrafficPolicyDecision(&r.decisions, decision)
	r.lastResult = decision
	r.lastState = state
	r.lastDecisionAt = now
	if decision == QuotaDecisionAdmissionUnavailable {
		r.lastUnavailableAt = now
	} else {
		r.lastSuccessAt = now
	}
	r.mu.Unlock()
}

func (r *TrafficPolicyRuntime) setLocalKeyState(keys, limit int) {
	if r == nil || r.mode != TrafficPolicyModeLocal {
		return
	}
	if keys < 0 {
		keys = 0
	}
	if limit < 0 {
		limit = 0
	}
	r.mu.Lock()
	r.localKeys = keys
	r.localKeyLimit = limit
	r.mu.Unlock()
}

func (r *TrafficPolicyRuntime) Snapshot() TrafficPolicyRuntimeSnapshot {
	if r == nil {
		return TrafficPolicyRuntimeSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := TrafficPolicyRuntimeSnapshot{
		Mode:              r.mode,
		NoMatchTotal:      r.noMatchTotal,
		Decisions:         r.decisions,
		LastResult:        r.lastResult,
		LastState:         r.lastState,
		LastDecisionAt:    r.lastDecisionAt,
		LastSuccessAt:     r.lastSuccessAt,
		LastUnavailableAt: r.lastUnavailableAt,
		LocalKeys:         r.localKeys,
		LocalKeyLimit:     r.localKeyLimit,
		Policies:          make([]TrafficPolicyRuntimePolicySnapshot, 0, len(r.policyNames)),
	}
	for _, name := range r.policyNames {
		policy := r.policies[name]
		if policy == nil {
			continue
		}
		snapshot.Policies = append(snapshot.Policies, TrafficPolicyRuntimePolicySnapshot{
			Name:           name,
			SelectionTotal: policy.selectionTotal,
			Decisions:      policy.decisions,
			LastResult:     policy.lastResult,
			LastState:      policy.lastState,
			LastDecisionAt: policy.lastDecisionAt,
		})
	}
	return snapshot
}

func addTrafficPolicyDecision(totals *TrafficPolicyDecisionTotals, decision QuotaDecision) {
	if totals == nil {
		return
	}
	switch decision {
	case QuotaDecisionAllowed:
		totals.Allowed++
	case QuotaDecisionRateLimited:
		totals.RateLimited++
	case QuotaDecisionOverloaded:
		totals.Overloaded++
	case QuotaDecisionAdmissionUnavailable:
		totals.AdmissionUnavailable++
	}
}

func normalizedQuotaDecision(decision QuotaDecision) QuotaDecision {
	switch decision {
	case QuotaDecisionAllowed,
		QuotaDecisionRateLimited,
		QuotaDecisionOverloaded,
		QuotaDecisionAdmissionUnavailable:
		return decision
	default:
		return QuotaDecisionAdmissionUnavailable
	}
}

func trafficPolicyBucketState(decision QuotaDecision) string {
	switch decision {
	case QuotaDecisionAllowed:
		return TrafficPolicyBucketStateAvailable
	case QuotaDecisionRateLimited:
		return TrafficPolicyBucketStateDepleted
	case QuotaDecisionOverloaded:
		return TrafficPolicyBucketStateKeyCapacityExhausted
	default:
		return TrafficPolicyBucketStateStoreUnavailable
	}
}
