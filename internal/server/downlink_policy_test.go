package server

import (
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/downlink"
)

func TestNewDownlinkPolicySetUsesDeliveryAsDefault(t *testing.T) {
	config := DefaultConfig()
	config.DownlinkDelivery.RetryDelay = 7 * time.Second
	config.DownlinkDelivery.RetryJitter = 2 * time.Second
	config.DownlinkDelivery.AckTimeout = 9 * time.Second
	config.DownlinkDelivery.MaxAttempts = 8
	config.DownlinkPolicies = []downlink.DeliveryPolicyRule{{
		Policy: downlink.DeliveryPolicy{
			Name:              "critical",
			MaxAttempts:       12,
			MaxAge:            time.Hour,
			AckTimeout:        3 * time.Second,
			InitialRetryDelay: time.Second,
			BackoffMultiplier: 2,
			MaxRetryDelay:     20 * time.Second,
			RetryJitter:       500 * time.Millisecond,
		},
		MsgIDMin: 2000,
		MsgIDMax: 2099,
	}}

	set, err := newDownlinkPolicySet(config)
	if err != nil {
		t.Fatalf("newDownlinkPolicySet() error = %v", err)
	}
	defaultPolicy := set.Resolve(3000)
	if defaultPolicy.Name != downlink.DefaultDeliveryPolicyName ||
		defaultPolicy.MaxAttempts != 8 ||
		defaultPolicy.AckTimeout != 9*time.Second ||
		defaultPolicy.InitialRetryDelay != 7*time.Second ||
		defaultPolicy.BackoffMultiplier != 1 ||
		defaultPolicy.MaxRetryDelay != 7*time.Second ||
		defaultPolicy.RetryJitter != 2*time.Second {
		t.Fatalf("default policy = %+v", defaultPolicy)
	}
	if got := set.Resolve(2001).Name; got != "critical" {
		t.Fatalf("Resolve(2001).Name = %q, want critical", got)
	}
}

func TestNewDownlinkPolicySetRejectsInvalidProgrammaticConfig(t *testing.T) {
	config := DefaultConfig()
	policy := downlink.DeliveryPolicy{
		Name:              "critical",
		MaxAttempts:       5,
		AckTimeout:        30 * time.Second,
		InitialRetryDelay: time.Second,
		BackoffMultiplier: 1,
		MaxRetryDelay:     time.Second,
	}
	bulkPolicy := policy
	bulkPolicy.Name = "bulk"
	config.DownlinkPolicies = []downlink.DeliveryPolicyRule{
		{Policy: policy, MsgIDMin: 2000, MsgIDMax: 2099},
		{Policy: bulkPolicy, MsgIDMin: 2099, MsgIDMax: 2199},
	}

	_, err := newDownlinkPolicySet(config)
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("newDownlinkPolicySet() error = %v, want overlap", err)
	}
}
