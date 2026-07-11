package downlink

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestDeliveryPolicySetResolvesRangeAndDefault(t *testing.T) {
	defaultPolicy := testDeliveryPolicy(DefaultDeliveryPolicyName)
	rules := []DeliveryPolicyRule{
		{
			Policy:   testDeliveryPolicy("bulk"),
			MsgIDMin: 3000,
			MsgIDMax: 3999,
		},
		{
			Policy:   testDeliveryPolicy("critical"),
			MsgIDMin: 2001,
			MsgIDMax: 2099,
		},
		{
			Policy:   testDeliveryPolicy("single"),
			MsgIDMin: 5000,
		},
	}

	set, err := NewDeliveryPolicySet(defaultPolicy, rules)
	if err != nil {
		t.Fatalf("NewDeliveryPolicySet() error = %v", err)
	}

	for _, test := range []struct {
		msgID uint32
		name  string
	}{
		{msgID: 1, name: DefaultDeliveryPolicyName},
		{msgID: 2001, name: "critical"},
		{msgID: 2099, name: "critical"},
		{msgID: 2100, name: DefaultDeliveryPolicyName},
		{msgID: 3500, name: "bulk"},
		{msgID: 5000, name: "single"},
		{msgID: 5001, name: DefaultDeliveryPolicyName},
	} {
		if got := set.Resolve(test.msgID).Name; got != test.name {
			t.Errorf("Resolve(%d).Name = %q, want %q", test.msgID, got, test.name)
		}
	}

	ordered := set.Rules()
	if len(ordered) != 3 || ordered[0].Policy.Name != "critical" || ordered[1].Policy.Name != "bulk" || ordered[2].Policy.Name != "single" {
		t.Fatalf("Rules() order = %#v, want critical, bulk, single", ordered)
	}
	if ordered[2].MsgIDMax != ordered[2].MsgIDMin {
		t.Fatalf("single range = %d-%d, want one MsgID", ordered[2].MsgIDMin, ordered[2].MsgIDMax)
	}

	policy, ok := set.Policy("critical")
	if !ok || policy.Name != "critical" {
		t.Fatalf("Policy(critical) = %#v, %v", policy, ok)
	}
}

func TestDeliveryPolicySetRejectsDuplicateName(t *testing.T) {
	policy := testDeliveryPolicy("critical")
	_, err := NewDeliveryPolicySet(testDeliveryPolicy(DefaultDeliveryPolicyName), []DeliveryPolicyRule{
		{Policy: policy, MsgIDMin: 2000, MsgIDMax: 2099},
		{Policy: policy, MsgIDMin: 3000, MsgIDMax: 3099},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate delivery policy name") {
		t.Fatalf("NewDeliveryPolicySet() error = %v, want duplicate name", err)
	}
}

func TestDeliveryPolicySetRejectsOverlappingRanges(t *testing.T) {
	_, err := NewDeliveryPolicySet(testDeliveryPolicy(DefaultDeliveryPolicyName), []DeliveryPolicyRule{
		{Policy: testDeliveryPolicy("critical"), MsgIDMin: 2000, MsgIDMax: 2099},
		{Policy: testDeliveryPolicy("bulk"), MsgIDMin: 2099, MsgIDMax: 2199},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("NewDeliveryPolicySet() error = %v, want overlap", err)
	}
}

func TestDeliveryPolicySetRejectsInvalidRule(t *testing.T) {
	valid := testDeliveryPolicy("critical")
	for _, test := range []struct {
		name    string
		policy  DeliveryPolicy
		min     uint32
		max     uint32
		message string
	}{
		{name: "reserved name", policy: testDeliveryPolicy(DefaultDeliveryPolicyName), min: 2000, max: 2099, message: "reserved name"},
		{name: "invalid name", policy: func() DeliveryPolicy { policy := valid; policy.Name = "Critical"; return policy }(), min: 2000, max: 2099, message: "must start with a lowercase letter"},
		{name: "zero min", policy: valid, min: 0, max: 2099, message: "msg_id_min"},
		{name: "reversed range", policy: valid, min: 2099, max: 2000, message: "msg_id_max"},
		{name: "zero attempts", policy: func() DeliveryPolicy { policy := valid; policy.MaxAttempts = 0; return policy }(), min: 2000, max: 2099, message: "max_attempts"},
		{name: "negative age", policy: func() DeliveryPolicy { policy := valid; policy.MaxAge = -time.Second; return policy }(), min: 2000, max: 2099, message: "max_age"},
		{name: "zero ack timeout", policy: func() DeliveryPolicy { policy := valid; policy.AckTimeout = 0; return policy }(), min: 2000, max: 2099, message: "ack_timeout"},
		{name: "zero retry delay", policy: func() DeliveryPolicy { policy := valid; policy.InitialRetryDelay = 0; return policy }(), min: 2000, max: 2099, message: "retry_delay"},
		{name: "small multiplier", policy: func() DeliveryPolicy { policy := valid; policy.BackoffMultiplier = .5; return policy }(), min: 2000, max: 2099, message: "backoff_multiplier"},
		{name: "non-finite multiplier", policy: func() DeliveryPolicy { policy := valid; policy.BackoffMultiplier = math.NaN(); return policy }(), min: 2000, max: 2099, message: "backoff_multiplier"},
		{name: "small max delay", policy: func() DeliveryPolicy { policy := valid; policy.MaxRetryDelay = time.Second; return policy }(), min: 2000, max: 2099, message: "max_retry_delay"},
		{name: "negative jitter", policy: func() DeliveryPolicy { policy := valid; policy.RetryJitter = -time.Second; return policy }(), min: 2000, max: 2099, message: "retry_jitter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewDeliveryPolicySet(testDeliveryPolicy(DefaultDeliveryPolicyName), []DeliveryPolicyRule{{
				Policy:   test.policy,
				MsgIDMin: test.min,
				MsgIDMax: test.max,
			}})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("NewDeliveryPolicySet() error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestServiceDeliveryPolicyUsesConfiguredSet(t *testing.T) {
	set, err := NewDeliveryPolicySet(testDeliveryPolicy(DefaultDeliveryPolicyName), []DeliveryPolicyRule{{
		Policy:   testDeliveryPolicy("critical"),
		MsgIDMin: 2000,
		MsgIDMax: 2099,
	}})
	if err != nil {
		t.Fatalf("NewDeliveryPolicySet() error = %v", err)
	}
	service := NewService(nil, nil, WithDeliveryPolicies(set))

	if got := service.DeliveryPolicy(2001).Name; got != "critical" {
		t.Fatalf("DeliveryPolicy(2001).Name = %q, want critical", got)
	}
	if got := service.DeliveryPolicy(3001).Name; got != DefaultDeliveryPolicyName {
		t.Fatalf("DeliveryPolicy(3001).Name = %q, want default", got)
	}
}

func testDeliveryPolicy(name string) DeliveryPolicy {
	return DeliveryPolicy{
		Name:              name,
		MaxAttempts:       5,
		MaxAge:            24 * time.Hour,
		AckTimeout:        30 * time.Second,
		InitialRetryDelay: 2 * time.Second,
		BackoffMultiplier: 2,
		MaxRetryDelay:     time.Minute,
		RetryJitter:       time.Second,
	}
}
