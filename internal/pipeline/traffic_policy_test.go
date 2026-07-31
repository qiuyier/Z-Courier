package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

func TestTrafficPolicyHandlerUsesPinnedRouteResolution(t *testing.T) {
	store := &stubQuotaStore{decision: QuotaDecisionAllowed}
	handler := NewTrafficPolicyHandlerWithStore(TrafficPoliciesConfig{
		Enabled: true,
		Mode:    TrafficPolicyModeLocal,
		Routes: []TrafficPolicyRoute{{
			Name:     "orders-v1",
			MsgIDMin: 1001,
			MsgIDMax: 1001,
		}},
		Policies: []TrafficPolicy{
			{
				Name:     "old-route-policy",
				Priority: 200,
				Match:    TrafficPolicyMatch{Routes: []string{"orders-v1"}},
				Key:      TrafficPolicyKeyClientID,
				TokenBucket: TokenBucketConfig{
					Capacity:       10,
					RefillTokens:   1,
					RefillInterval: time.Second,
				},
			},
			{
				Name:     "new-route-policy",
				Priority: 300,
				Match:    TrafficPolicyMatch{Routes: []string{"orders-v2"}},
				Key:      TrafficPolicyKeyClientID,
				TokenBucket: TokenBucketConfig{
					Capacity:       10,
					RefillTokens:   1,
					RefillInterval: time.Second,
				},
			},
		},
	}, store)
	ctx := trafficPolicyContext("client-a", 1001)
	ctx.SetRouteResolution("orders-v2", true)

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.request.PolicyName != "new-route-policy" {
		t.Fatalf("selected policy = %q, want new-route-policy", store.request.PolicyName)
	}
}

func TestTrafficPolicyHandlerNoMatchDoesNotCreateBucket(t *testing.T) {
	store := &stubQuotaStore{decision: QuotaDecisionAllowed}
	handler := NewTrafficPolicyHandlerWithStore(TrafficPoliciesConfig{
		Enabled: true,
		Mode:    TrafficPolicyModeLocal,
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
	}, store)

	if err := handler.Handle(trafficPolicyContext("client-a", 3000)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("quota store calls = %d, want 0", store.calls)
	}
	snapshot := handler.Runtime().Snapshot()
	if snapshot.NoMatchTotal != 1 || snapshot.Decisions != (TrafficPolicyDecisionTotals{}) {
		t.Fatalf("runtime snapshot = %+v, want one no-match selection", snapshot)
	}
}

func TestTrafficPolicyHandlerDelegatesSelectedPolicy(t *testing.T) {
	bucket := TokenBucketConfig{
		Capacity:       7,
		RefillTokens:   3,
		RefillInterval: 2 * time.Second,
	}
	store := &stubQuotaStore{decision: QuotaDecisionAllowed}
	handler := NewTrafficPolicyHandlerWithStore(TrafficPoliciesConfig{
		Enabled:       true,
		Mode:          TrafficPolicyModeLocal,
		DefaultPolicy: "standard",
		Policies: []TrafficPolicy{{
			Name:        "standard",
			Priority:    100,
			Key:         TrafficPolicyKeyClientID,
			TokenBucket: bucket,
		}},
	}, store)
	baseContext := context.WithValue(context.Background(), trafficPolicyTestContextKey{}, "request")
	requestContext := trafficPolicyContext("client-a", 1001)
	requestContext.BaseContext = baseContext

	if err := handler.Handle(requestContext); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("quota store calls = %d, want 1", store.calls)
	}
	if store.ctx != baseContext {
		t.Fatal("quota store did not receive the packet context")
	}
	if store.request.PolicyName != "standard" ||
		store.request.KeyScope != TrafficPolicyKeyClientID ||
		store.request.KeyValue != "client-a" ||
		store.request.TokenBucket != bucket {
		t.Fatalf("quota request = %+v", store.request)
	}
}

func TestTrafficPolicyHandlerMapsQuotaDecisions(t *testing.T) {
	tests := []struct {
		name       string
		decision   QuotaDecision
		storeErr   error
		wantReason string
	}{
		{name: "allowed", decision: QuotaDecisionAllowed},
		{name: "rate limited", decision: QuotaDecisionRateLimited, wantReason: resilience.ReasonRateLimited},
		{name: "overloaded", decision: QuotaDecisionOverloaded, wantReason: resilience.ReasonOverloaded},
		{
			name:       "admission unavailable",
			decision:   QuotaDecisionAdmissionUnavailable,
			wantReason: resilience.ReasonAdmissionUnavailable,
		},
		{
			name:       "store error",
			decision:   QuotaDecisionAllowed,
			storeErr:   errors.New("store failed"),
			wantReason: resilience.ReasonAdmissionUnavailable,
		},
		{
			name:       "unknown decision",
			decision:   QuotaDecision("unknown"),
			wantReason: resilience.ReasonAdmissionUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &stubQuotaStore{decision: test.decision, err: test.storeErr}
			handler := NewTrafficPolicyHandlerWithStore(testTrafficPolicyConfig(), store)
			err := handler.Handle(trafficPolicyContext("client-a", 1001))
			if test.wantReason == "" {
				if err != nil {
					t.Fatalf("Handle() error = %v", err)
				}
				return
			}
			assertTrafficPolicyReason(t, err, test.wantReason)
		})
	}
}

func TestTrafficPolicyHandlerRecordsSelectionAndQuotaMetrics(t *testing.T) {
	const policyName = "pipeline-metrics-rate-limited"
	config := testTrafficPolicyConfig()
	config.Mode = TrafficPolicyModeRedis
	config.DefaultPolicy = policyName
	config.Policies[0].Name = policyName
	store := &stubQuotaStore{decision: QuotaDecisionRateLimited}
	handler := NewTrafficPolicyHandlerWithStore(config, store)

	assertTrafficPolicyReason(
		t,
		handler.Handle(trafficPolicyContext("metrics-client", 1001)),
		resilience.ReasonRateLimited,
	)

	if got := gatheredPipelineScalar(t, "z_courier_traffic_policy_selection_total", map[string]string{
		"mode":   TrafficPolicyModeRedis,
		"policy": policyName,
		"result": "selected",
	}); got != 1 {
		t.Fatalf("traffic policy selection counter = %v, want 1", got)
	}
	if got := gatheredPipelineScalar(t, "z_courier_traffic_policy_quota_store_total", map[string]string{
		"mode":      TrafficPolicyModeRedis,
		"policy":    policyName,
		"key_scope": TrafficPolicyKeyClientID,
		"result":    string(QuotaDecisionRateLimited),
	}); got != 1 {
		t.Fatalf("traffic policy quota store counter = %v, want 1", got)
	}
}

func TestNewTrafficPolicyHandlerUsesLocalStore(t *testing.T) {
	config := testTrafficPolicyConfig()
	config.Policies[0].TokenBucket = TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	}
	handler := NewTrafficPolicyHandler(config)
	ctx := trafficPolicyContext("client-a", 1001)

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() first error = %v", err)
	}
	assertTrafficPolicyReason(t, handler.Handle(ctx), resilience.ReasonRateLimited)

	snapshot := handler.Runtime().Snapshot()
	if snapshot.Mode != TrafficPolicyModeLocal ||
		snapshot.LocalKeys != 1 ||
		snapshot.LocalKeyLimit != config.MaxKeys ||
		snapshot.Decisions.Allowed != 1 ||
		snapshot.Decisions.RateLimited != 1 ||
		snapshot.LastState != TrafficPolicyBucketStateDepleted {
		t.Fatalf("local runtime snapshot = %+v", snapshot)
	}
}

func TestNewTrafficPolicyHandlerDisabled(t *testing.T) {
	if handler := NewTrafficPolicyHandler(TrafficPoliciesConfig{}); handler != nil {
		t.Fatalf("NewTrafficPolicyHandler() = %#v, want nil", handler)
	}
	if handler := NewTrafficPolicyHandlerWithStore(TrafficPoliciesConfig{}, &stubQuotaStore{}); handler != nil {
		t.Fatalf("NewTrafficPolicyHandlerWithStore() = %#v, want nil", handler)
	}
}

func testTrafficPolicyConfig() TrafficPoliciesConfig {
	return TrafficPoliciesConfig{
		Enabled:       true,
		Mode:          TrafficPolicyModeLocal,
		MaxKeys:       10,
		IdleTTL:       time.Minute,
		DefaultPolicy: "standard",
		Policies: []TrafficPolicy{{
			Name:     "standard",
			Priority: 100,
			Key:      TrafficPolicyKeyClientID,
			TokenBucket: TokenBucketConfig{
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
		}},
	}
}

type trafficPolicyTestContextKey struct{}

type stubQuotaStore struct {
	decision QuotaDecision
	err      error
	calls    int
	ctx      context.Context
	request  QuotaRequest
}

func (s *stubQuotaStore) Admit(ctx context.Context, request QuotaRequest) (QuotaDecision, error) {
	s.calls++
	s.ctx = ctx
	s.request = request
	return s.decision, s.err
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

func gatheredPipelineScalar(t *testing.T, metricName string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			matches := 0
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] == label.GetValue() {
					matches++
				}
			}
			if matches != len(labels) || len(metric.GetLabel()) != len(labels) {
				continue
			}
			switch {
			case metric.GetCounter() != nil:
				return metric.GetCounter().GetValue()
			case metric.GetGauge() != nil:
				return metric.GetGauge().GetValue()
			default:
				t.Fatalf("%s is not a scalar metric", metricName)
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", metricName, labels)
	return 0
}
