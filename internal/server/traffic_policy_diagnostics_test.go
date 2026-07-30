package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestAdminTrafficPolicyDiagnosticsReportsLocalCapacity(t *testing.T) {
	trafficConfig := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeLocal)
	trafficConfig.MaxKeys = 2
	trafficConfig.IdleTTL = time.Minute
	trafficConfig.Policies[0].TokenBucket.Capacity = 1
	trafficConfig.Policies[0].TokenBucket.RefillInterval = time.Hour
	handler := pipeline.NewTrafficPolicyHandler(trafficConfig)

	if err := handler.Handle(serverTrafficPolicyContext("client-a", 1001)); err != nil {
		t.Fatalf("Handle(client-a first) error = %v", err)
	}
	if err := handler.Handle(serverTrafficPolicyContext("client-a", 1001)); err == nil {
		t.Fatal("Handle(client-a second) error = nil, want rate limit")
	}
	if err := handler.Handle(serverTrafficPolicyContext("client-b", 1001)); err != nil {
		t.Fatalf("Handle(client-b) error = %v", err)
	}

	config := Config{
		Cluster: ClusterConfig{Enabled: true},
		Pipeline: pipeline.Config{
			TrafficPolicies: trafficConfig,
		},
		TrafficPolicyRuntime: handler.Runtime(),
	}
	diagnostics := adminTrafficPolicyFromConfig(config)
	if !diagnostics.Enabled ||
		diagnostics.Mode != pipeline.TrafficPolicyModeLocal ||
		diagnostics.StoreStatus != "degraded" ||
		diagnostics.PolicyCount != 1 ||
		strings.Join(diagnostics.PolicyNames, ",") != "shared" ||
		diagnostics.KeyScope != pipeline.TrafficPolicyKeyClientID ||
		diagnostics.Local == nil ||
		diagnostics.Local.LiveKeys != 2 ||
		diagnostics.Local.MaxKeys != 2 ||
		diagnostics.Local.Utilization != 1 {
		t.Fatalf("local traffic policy diagnostics = %+v", diagnostics)
	}
	if diagnostics.Decisions.Allowed != 2 ||
		diagnostics.Decisions.RateLimited != 1 ||
		diagnostics.LastResult != string(pipeline.QuotaDecisionAllowed) ||
		diagnostics.LastState != pipeline.TrafficPolicyBucketStateAvailable ||
		diagnostics.LastDecisionAt == nil ||
		diagnostics.LastSuccessAt == nil {
		t.Fatalf("local traffic policy outcomes = %+v", diagnostics)
	}
	if len(diagnostics.Policies) != 1 ||
		diagnostics.Policies[0].SelectionTotal != 3 ||
		diagnostics.Policies[0].Capacity != 1 ||
		diagnostics.Policies[0].RefillInterval != "1h0m0s" {
		t.Fatalf("local policy summary = %+v", diagnostics.Policies)
	}

	dependencies := adminDiagnosticDependencies(config, nil, false, false, diagnostics)
	if dependency := findAdminDependency(dependencies, "traffic_policy_store"); dependency.Status != "degraded" || dependency.Reason != "local keys: 2/2" {
		t.Fatalf("traffic policy dependency = %+v", dependency)
	}
	warnings := adminDiagnosticWarnings(config, nil, false, diagnostics)
	if !hasAdminWarning(warnings, "node_local_traffic_policy_store") ||
		!hasAdminWarning(warnings, "traffic_policy_local_key_capacity_high") {
		t.Fatalf("traffic policy warnings = %+v", warnings)
	}
}

func TestAdminTrafficPolicyDiagnosticsLocalCapacityThreshold(t *testing.T) {
	tests := []struct {
		name        string
		liveKeys    int
		wantStatus  string
		wantWarning bool
	}{
		{name: "below threshold", liveKeys: 7, wantStatus: "configured"},
		{name: "at threshold", liveKeys: 8, wantStatus: "degraded", wantWarning: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trafficConfig := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeLocal)
			trafficConfig.MaxKeys = 10
			trafficConfig.IdleTTL = time.Minute
			handler := pipeline.NewTrafficPolicyHandler(trafficConfig)
			for index := range test.liveKeys {
				clientID := fmt.Sprintf("capacity-client-%d", index)
				if err := handler.Handle(serverTrafficPolicyContext(clientID, 1001)); err != nil {
					t.Fatalf("Handle(%q) error = %v", clientID, err)
				}
			}

			config := Config{
				Pipeline:             pipeline.Config{TrafficPolicies: trafficConfig},
				TrafficPolicyRuntime: handler.Runtime(),
			}
			diagnostics := adminTrafficPolicyFromConfig(config)
			if diagnostics.StoreStatus != test.wantStatus {
				t.Fatalf("StoreStatus = %q, want %q", diagnostics.StoreStatus, test.wantStatus)
			}
			warnings := adminDiagnosticWarnings(config, nil, false, diagnostics)
			if got := hasAdminWarning(warnings, "traffic_policy_local_key_capacity_high"); got != test.wantWarning {
				t.Fatalf("capacity warning = %v, want %v; warnings = %+v", got, test.wantWarning, warnings)
			}
		})
	}
}

func TestInternalHTTPAdminDiagnosticsSanitizesRedisTrafficPolicy(t *testing.T) {
	const (
		clientID    = "private-traffic-policy-client"
		redisAddr   = "private-redis.internal:6379"
		redisUser   = "private-redis-user"
		redisSecret = "private-redis-password"
		keyPrefix   = "private:tenant:quota"
	)
	trafficConfig := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeRedis)
	trafficConfig.IdleTTL = time.Minute
	trafficConfig.Redis = pipeline.RedisQuotaStoreConfig{
		Addr:             redisAddr,
		Username:         redisUser,
		Password:         redisSecret,
		KeyPrefix:        keyPrefix,
		IdleTTL:          time.Minute,
		OperationTimeout: time.Second,
		FailureMode:      pipeline.TrafficPolicyFailureModeFailClosed,
	}
	trafficConfig.Policies[0].Match.ClientIDs = []string{clientID}
	store := &diagnosticQuotaStore{decision: pipeline.QuotaDecisionAdmissionUnavailable}
	handler := pipeline.NewTrafficPolicyHandlerWithStore(trafficConfig, store)
	if err := handler.Handle(serverTrafficPolicyContext(clientID, 1001)); err == nil {
		t.Fatal("Handle() error = nil, want admission unavailable")
	}

	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "internal-secret",
		Pipeline: pipeline.Config{
			TrafficPolicies: trafficConfig,
		},
		TrafficPolicyRuntime: handler.Runtime(),
	})
	service := downlink.NewService(nil, nil)
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnostics", nil)
	req.Header.Set(downlink.InternalTokenHeader, "internal-secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertTrafficPolicyDiagnosticsSecretsAbsent(
		t,
		rec.Body.String(),
		clientID,
		redisAddr,
		redisUser,
		redisSecret,
		keyPrefix,
		"internal-secret",
	)

	var response adminDiagnosticsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	diagnostics := response.TrafficPolicy
	if diagnostics.Mode != pipeline.TrafficPolicyModeRedis ||
		diagnostics.StoreStatus != "unavailable" ||
		diagnostics.FailureMode != pipeline.TrafficPolicyFailureModeFailClosed ||
		diagnostics.Decisions.AdmissionUnavailable != 1 ||
		diagnostics.LastResult != string(pipeline.QuotaDecisionAdmissionUnavailable) ||
		diagnostics.LastState != pipeline.TrafficPolicyBucketStateStoreUnavailable ||
		diagnostics.LastUnavailableAt == nil ||
		diagnostics.Local != nil {
		t.Fatalf("redis traffic policy diagnostics = %+v", diagnostics)
	}
	if dependency := findAdminDependency(response.Dependencies, "traffic_policy_store"); dependency.Status != "unavailable" {
		t.Fatalf("traffic policy dependency = %+v", dependency)
	}
	if !hasAdminWarning(response.Warnings, "traffic_policy_store_unavailable") {
		t.Fatalf("warnings = %+v, want traffic_policy_store_unavailable", response.Warnings)
	}

	diagnoseReq := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnose", nil)
	diagnoseReq.Header.Set(downlink.InternalTokenHeader, "internal-secret")
	diagnoseRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(diagnoseRec, diagnoseReq)
	if diagnoseRec.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d, want %d, body = %s", diagnoseRec.Code, http.StatusOK, diagnoseRec.Body.String())
	}
	assertTrafficPolicyDiagnosticsSecretsAbsent(
		t,
		diagnoseRec.Body.String(),
		clientID,
		redisAddr,
		redisUser,
		redisSecret,
		keyPrefix,
		"internal-secret",
	)
	var diagnoseResponse adminDiagnoseResponse
	if err := sonic.Unmarshal(diagnoseRec.Body.Bytes(), &diagnoseResponse); err != nil {
		t.Fatalf("Unmarshal(diagnose) error = %v", err)
	}
	diagnosticsSection, ok := diagnoseResponse.Sections["diagnostics"]
	if !ok {
		t.Fatalf("diagnose sections = %+v, want diagnostics", diagnoseResponse.Sections)
	}
	sectionJSON, err := sonic.Marshal(diagnosticsSection.Body)
	if err != nil {
		t.Fatalf("Marshal(diagnostics section) error = %v", err)
	}
	if !strings.Contains(string(sectionJSON), `"traffic_policy"`) {
		t.Fatalf("diagnostics section does not contain traffic_policy: %s", sectionJSON)
	}

	store.decision = pipeline.QuotaDecisionAllowed
	if err := handler.Handle(serverTrafficPolicyContext(clientID, 1001)); err != nil {
		t.Fatalf("Handle(recovered) error = %v", err)
	}
	recovered := adminTrafficPolicyFromConfig(config)
	if recovered.StoreStatus != "configured" ||
		recovered.LastResult != string(pipeline.QuotaDecisionAllowed) ||
		recovered.LastSuccessAt == nil ||
		recovered.LastUnavailableAt == nil {
		t.Fatalf("recovered traffic policy diagnostics = %+v", recovered)
	}
}

func TestAdminTrafficPolicyDiagnosticsReportsMissingRuntime(t *testing.T) {
	trafficConfig := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeRedis)
	diagnostics := adminTrafficPolicyFromConfig(Config{
		Pipeline: pipeline.Config{TrafficPolicies: trafficConfig},
	})
	if diagnostics.StoreStatus != "not_configured" {
		t.Fatalf("StoreStatus = %q, want not_configured", diagnostics.StoreStatus)
	}
	warnings := adminDiagnosticWarnings(Config{}, nil, false, diagnostics)
	if !hasAdminWarning(warnings, "traffic_policy_runtime_not_attached") {
		t.Fatalf("warnings = %+v, want traffic_policy_runtime_not_attached", warnings)
	}
}

type diagnosticQuotaStore struct {
	decision pipeline.QuotaDecision
	err      error
}

func (s *diagnosticQuotaStore) Admit(context.Context, pipeline.QuotaRequest) (pipeline.QuotaDecision, error) {
	return s.decision, s.err
}

func serverTrafficPolicyContext(clientID string, msgID uint32) *pipeline.Context {
	return &pipeline.Context{
		Principal: &auth.Principal{ClientID: clientID},
		Packet:    protocol.NewPacket(msgID, nil),
	}
}

func assertTrafficPolicyDiagnosticsSecretsAbsent(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Fatalf("traffic policy diagnostics leaked %q in body %s", secret, body)
		}
	}
}
