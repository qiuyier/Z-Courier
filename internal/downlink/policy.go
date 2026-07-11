package downlink

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const DefaultDeliveryPolicyName = "default"

// DeliveryPolicy is the resolved retry contract for one message class.
// Policy execution is intentionally integrated separately from selection so
// the resolver remains deterministic and independently testable.
type DeliveryPolicy struct {
	Name              string
	MaxAttempts       int
	MaxAge            time.Duration
	AckTimeout        time.Duration
	InitialRetryDelay time.Duration
	BackoffMultiplier float64
	MaxRetryDelay     time.Duration
	RetryJitter       time.Duration
}

// DeliveryPolicyRule assigns one named policy to an inclusive MsgID range.
// MsgIDMax may be zero to select only MsgIDMin.
type DeliveryPolicyRule struct {
	Policy   DeliveryPolicy
	MsgIDMin uint32
	MsgIDMax uint32
}

// DeliveryPolicySet resolves exactly one policy for every non-zero MsgID.
// Configured ranges win over the default policy and may not overlap.
type DeliveryPolicySet struct {
	defaultPolicy DeliveryPolicy
	rules         []DeliveryPolicyRule
	byName        map[string]DeliveryPolicy
}

func NewDeliveryPolicySet(defaultPolicy DeliveryPolicy, rules []DeliveryPolicyRule) (*DeliveryPolicySet, error) {
	defaultPolicy.Name = strings.TrimSpace(defaultPolicy.Name)
	if defaultPolicy.Name == "" {
		defaultPolicy.Name = DefaultDeliveryPolicyName
	}
	if defaultPolicy.Name != DefaultDeliveryPolicyName {
		return nil, fmt.Errorf("downlink: default delivery policy name must be %q", DefaultDeliveryPolicyName)
	}
	if err := validateDeliveryPolicy(defaultPolicy); err != nil {
		return nil, err
	}

	normalized := make([]DeliveryPolicyRule, 0, len(rules))
	byName := map[string]DeliveryPolicy{defaultPolicy.Name: defaultPolicy}
	for index, rule := range rules {
		rule.Policy.Name = strings.TrimSpace(rule.Policy.Name)
		if rule.Policy.Name == DefaultDeliveryPolicyName {
			return nil, fmt.Errorf("downlink: delivery policy rule #%d uses reserved name %q", index+1, DefaultDeliveryPolicyName)
		}
		if err := validateDeliveryPolicy(rule.Policy); err != nil {
			return nil, fmt.Errorf("downlink: delivery policy rule #%d: %w", index+1, err)
		}
		if _, exists := byName[rule.Policy.Name]; exists {
			return nil, fmt.Errorf("downlink: duplicate delivery policy name %q", rule.Policy.Name)
		}
		if rule.MsgIDMin == 0 {
			return nil, fmt.Errorf("downlink: delivery policy %q msg_id_min must be greater than 0", rule.Policy.Name)
		}
		if rule.MsgIDMax == 0 {
			rule.MsgIDMax = rule.MsgIDMin
		}
		if rule.MsgIDMax < rule.MsgIDMin {
			return nil, fmt.Errorf(
				"downlink: delivery policy %q msg_id_max must be greater than or equal to msg_id_min",
				rule.Policy.Name,
			)
		}

		byName[rule.Policy.Name] = rule.Policy
		normalized = append(normalized, rule)
	}

	sort.Slice(normalized, func(left, right int) bool {
		if normalized[left].MsgIDMin == normalized[right].MsgIDMin {
			if normalized[left].MsgIDMax == normalized[right].MsgIDMax {
				return normalized[left].Policy.Name < normalized[right].Policy.Name
			}
			return normalized[left].MsgIDMax < normalized[right].MsgIDMax
		}
		return normalized[left].MsgIDMin < normalized[right].MsgIDMin
	})
	for index := 1; index < len(normalized); index++ {
		previous := normalized[index-1]
		current := normalized[index]
		if current.MsgIDMin <= previous.MsgIDMax {
			return nil, fmt.Errorf(
				"downlink: delivery policy %q range %d-%d overlaps policy %q range %d-%d",
				current.Policy.Name,
				current.MsgIDMin,
				current.MsgIDMax,
				previous.Policy.Name,
				previous.MsgIDMin,
				previous.MsgIDMax,
			)
		}
	}

	return &DeliveryPolicySet{
		defaultPolicy: defaultPolicy,
		rules:         normalized,
		byName:        byName,
	}, nil
}

func (s *DeliveryPolicySet) Resolve(msgID uint32) DeliveryPolicy {
	if s == nil {
		return DeliveryPolicy{}
	}
	for _, rule := range s.rules {
		if msgID < rule.MsgIDMin {
			break
		}
		if msgID <= rule.MsgIDMax {
			return rule.Policy
		}
	}
	return s.defaultPolicy
}

func (s *DeliveryPolicySet) Policy(name string) (DeliveryPolicy, bool) {
	if s == nil {
		return DeliveryPolicy{}, false
	}
	policy, ok := s.byName[strings.TrimSpace(name)]
	return policy, ok
}

func (s *DeliveryPolicySet) Default() DeliveryPolicy {
	if s == nil {
		return DeliveryPolicy{}
	}
	return s.defaultPolicy
}

func (s *DeliveryPolicySet) Rules() []DeliveryPolicyRule {
	if s == nil {
		return nil
	}
	return append([]DeliveryPolicyRule(nil), s.rules...)
}

func validateDeliveryPolicy(policy DeliveryPolicy) error {
	if !validDeliveryPolicyName(policy.Name) {
		return fmt.Errorf(
			"delivery policy name %q must start with a lowercase letter and contain only lowercase letters, digits, '_' or '-' (maximum 64 characters)",
			policy.Name,
		)
	}
	if policy.MaxAttempts <= 0 {
		return fmt.Errorf("delivery policy %q max_attempts must be greater than 0", policy.Name)
	}
	if policy.MaxAge < 0 {
		return fmt.Errorf("delivery policy %q max_age must be greater than or equal to 0", policy.Name)
	}
	if policy.AckTimeout <= 0 {
		return fmt.Errorf("delivery policy %q ack_timeout must be greater than 0", policy.Name)
	}
	if policy.InitialRetryDelay <= 0 {
		return fmt.Errorf("delivery policy %q retry_delay must be greater than 0", policy.Name)
	}
	if math.IsNaN(policy.BackoffMultiplier) || math.IsInf(policy.BackoffMultiplier, 0) || policy.BackoffMultiplier < 1 {
		return fmt.Errorf("delivery policy %q backoff_multiplier must be greater than or equal to 1", policy.Name)
	}
	if policy.MaxRetryDelay < policy.InitialRetryDelay {
		return fmt.Errorf("delivery policy %q max_retry_delay must be greater than or equal to retry_delay", policy.Name)
	}
	if policy.RetryJitter < 0 {
		return fmt.Errorf("delivery policy %q retry_jitter must be greater than or equal to 0", policy.Name)
	}
	return nil
}

func validDeliveryPolicyName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if char == '_' || char == '-' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}
