package pipeline

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

func TestTrafficPolicySelectorUsesPriorityRouteAndDefault(t *testing.T) {
	config := TrafficPoliciesConfig{
		DefaultPolicy: "standard",
		Routes: []TrafficPolicyRoute{
			{Name: "orders-http", MsgIDMin: 1001, MsgIDMax: 1999},
			{Name: "events-nsq", MsgIDMin: 2000, MsgIDMax: 2999},
		},
		Policies: []TrafficPolicy{
			{
				Name:     "standard",
				Priority: 100,
				Match:    TrafficPolicyMatch{MsgIDMin: 1001, MsgIDMax: 2999},
			},
			{
				Name:     "orders",
				Priority: 200,
				Match:    TrafficPolicyMatch{Routes: []string{"orders-http"}},
			},
			{
				Name:     "priority-client",
				Priority: 300,
				Match: TrafficPolicyMatch{
					ClientIDs: []string{"client-priority"},
					Routes:    []string{"orders-http"},
				},
			},
		},
	}
	selector := NewTrafficPolicySelector(config)

	tests := []struct {
		name     string
		clientID string
		msgID    uint32
		want     string
	}{
		{name: "highest matching priority", clientID: "client-priority", msgID: 1001, want: "priority-client"},
		{name: "route policy", clientID: "client-a", msgID: 1500, want: "orders"},
		{name: "msg id policy", clientID: "client-a", msgID: 2500, want: "standard"},
		{name: "default policy", clientID: "client-a", msgID: protocol.MsgIDBind, want: "standard"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, ok := selector.Select(test.clientID, test.msgID)
			if !ok {
				t.Fatal("Select() selected = false, want true")
			}
			if selected.Name != test.want {
				t.Fatalf("Select() policy = %q, want %q", selected.Name, test.want)
			}
		})
	}
}

func TestTrafficPolicySelectorNoMatchWithoutDefault(t *testing.T) {
	selector := NewTrafficPolicySelector(TrafficPoliciesConfig{
		Policies: []TrafficPolicy{{
			Name:     "upstream",
			Priority: 100,
			Match:    TrafficPolicyMatch{MsgIDMin: 1001, MsgIDMax: 1999},
		}},
	})

	if selected, ok := selector.Select("client-a", protocol.MsgIDBind); ok {
		t.Fatalf("Select() = %+v, true; want no policy", selected)
	}
}

func TestTrafficPolicyHandlerBurstAndRefill(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	handler := newTestTrafficPolicyHandler(10, time.Minute, TokenBucketConfig{
		Capacity:       2,
		RefillTokens:   2,
		RefillInterval: time.Second,
	})
	handler.now = func() time.Time { return now }
	ctx := trafficPolicyContext("client-a", 1001)

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() first error = %v", err)
	}
	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() second error = %v", err)
	}
	assertTrafficPolicyReason(t, handler.Handle(ctx), resilience.ReasonRateLimited)

	now = now.Add(500 * time.Millisecond)
	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() after refill error = %v", err)
	}
	assertTrafficPolicyReason(t, handler.Handle(ctx), resilience.ReasonRateLimited)
}

func TestTrafficPolicyHandlerBoundsKeysAndExpiresIdleBuckets(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	handler := newTestTrafficPolicyHandler(1, time.Second, TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	})
	handler.now = func() time.Time { return now }

	if err := handler.Handle(trafficPolicyContext("client-a", 1001)); err != nil {
		t.Fatalf("Handle(client-a) error = %v", err)
	}
	assertTrafficPolicyReason(
		t,
		handler.Handle(trafficPolicyContext("client-b", 1001)),
		resilience.ReasonOverloaded,
	)
	if got := handler.bucketCount(); got != 1 {
		t.Fatalf("bucketCount() = %d, want 1", got)
	}

	now = now.Add(time.Second)
	if err := handler.Handle(trafficPolicyContext("client-b", 1001)); err != nil {
		t.Fatalf("Handle(client-b) after idle TTL error = %v", err)
	}
	if got := handler.bucketCount(); got != 1 {
		t.Fatalf("bucketCount() after eviction = %d, want 1", got)
	}
}

func TestTrafficPolicyHandlerNoMatchDoesNotCreateBucket(t *testing.T) {
	handler := NewTrafficPolicyHandler(TrafficPoliciesConfig{
		Enabled: true,
		Mode:    TrafficPolicyModeLocal,
		MaxKeys: 10,
		IdleTTL: time.Minute,
		Policies: []TrafficPolicy{{
			Name:     "upstream",
			Priority: 100,
			Match:    TrafficPolicyMatch{MsgIDMin: 1001, MsgIDMax: 1999},
			Key:      TrafficPolicyKeyClientID,
			TokenBucket: TokenBucketConfig{
				Capacity:       1,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
		}},
	})

	if err := handler.Handle(trafficPolicyContext("client-a", 3000)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := handler.bucketCount(); got != 0 {
		t.Fatalf("bucketCount() = %d, want 0", got)
	}
}

func TestTrafficPolicyHandlerConcurrentAdmission(t *testing.T) {
	handler := newTestTrafficPolicyHandler(10, time.Minute, TokenBucketConfig{
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	})
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }

	var accepted atomic.Int64
	var waitGroup sync.WaitGroup
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := handler.Handle(trafficPolicyContext("client-a", 1001)); err == nil {
				accepted.Add(1)
				return
			}
		}()
	}
	waitGroup.Wait()

	if got := accepted.Load(); got != 10 {
		t.Fatalf("accepted = %d, want 10", got)
	}
}

func TestNewTrafficPolicyHandlerDisabled(t *testing.T) {
	if handler := NewTrafficPolicyHandler(TrafficPoliciesConfig{}); handler != nil {
		t.Fatalf("NewTrafficPolicyHandler() = %#v, want nil", handler)
	}
}

func newTestTrafficPolicyHandler(maxKeys int, idleTTL time.Duration, bucket TokenBucketConfig) *TrafficPolicyHandler {
	return NewTrafficPolicyHandler(TrafficPoliciesConfig{
		Enabled:       true,
		Mode:          TrafficPolicyModeLocal,
		MaxKeys:       maxKeys,
		IdleTTL:       idleTTL,
		DefaultPolicy: "standard",
		Policies: []TrafficPolicy{{
			Name:        "standard",
			Priority:    100,
			Key:         TrafficPolicyKeyClientID,
			TokenBucket: bucket,
		}},
	})
}

func trafficPolicyContext(clientID string, msgID uint32) *Context {
	return &Context{
		Principal: &auth.Principal{ClientID: clientID},
		Packet:    protocol.NewPacket(msgID, nil),
	}
}

func assertTrafficPolicyReason(t *testing.T, err error, want string) {
	t.Helper()
	code, reason := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("AckError() code = %s, want %s", code, protocol.AckRejected)
	}
	if reason != want {
		t.Fatalf("AckError() reason = %q, want %q", reason, want)
	}
}
