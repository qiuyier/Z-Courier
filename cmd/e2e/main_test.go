package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

func TestTerminalHTTPEventCollectorVerifiesAndInjectsFailure(t *testing.T) {
	cfg := config{
		TerminalWebhookAddress:  "127.0.0.1:0",
		TerminalWebhookPath:     defaultTerminalWebhookPath,
		TerminalWebhookKeyID:    defaultTerminalWebhookHMACKeyID,
		TerminalWebhookSecret:   defaultTerminalWebhookHMACSecret,
		TerminalWebhookFailures: 1,
	}
	collector, err := newTerminalHTTPEventCollector(cfg)
	if err != nil {
		t.Fatalf("newTerminalHTTPEventCollector() error = %v", err)
	}
	defer collector.Close()

	event := downlink.TerminalEvent{
		Version:        downlink.TerminalEventVersion,
		Type:           downlink.TerminalEventType,
		EventID:        "message-1:failed",
		MessageID:      "message-1",
		TerminalStatus: downlink.MessageStatusFailed,
		TerminalReason: downlink.TerminalReasonMaxAttempts,
	}
	body, err := sonic.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	endpoint := "http://" + collector.listener.Addr().String() + cfg.TerminalWebhookPath

	unsigned, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unsigned POST error = %v", err)
	}
	unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want %d", unsigned.StatusCode, http.StatusUnauthorized)
	}

	signer, err := signing.NewSigner(signing.SignerConfig{
		KeyID:  cfg.TerminalWebhookKeyID,
		Secret: []byte(cfg.TerminalWebhookSecret),
	})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	if status := postSignedTerminalEvent(t, endpoint, signer, body); status != http.StatusServiceUnavailable {
		t.Fatalf("first signed status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if status := postSignedTerminalEvent(t, endpoint, signer, body); status != http.StatusNoContent {
		t.Fatalf("second signed status = %d, want %d", status, http.StatusNoContent)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, raw, err := collector.Wait(ctx, event.MessageID)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got.EventID != event.EventID || !bytes.Equal(raw, body) {
		t.Fatalf("Wait() event = %+v body = %s", got, raw)
	}
	attempts, successes := collector.counts(event.EventID)
	if attempts != 2 || successes != 1 {
		t.Fatalf("counts = attempts:%d successes:%d, want 2/1", attempts, successes)
	}
}

func postSignedTerminalEvent(t *testing.T, endpoint string, signer *signing.Signer, body []byte) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if err := signer.Sign(request, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestValidateDebugRoute(t *testing.T) {
	cfg := config{
		InternalURL:            "http://127.0.0.1:18182",
		ClientID:               "client-1",
		DeviceID:               "device-1",
		ExpectRouteNode:        "gateway-b",
		ExpectRouteInternalURL: "http://127.0.0.1:18183",
	}
	resp := debugRouteResponse{
		Code:              "ok",
		GatewayNode:       "gateway-a",
		ClientID:          "client-1",
		DeviceID:          "device-1",
		LocalSessionFound: false,
		ClusterEnabled:    true,
		ClusterRouteFound: true,
		ClusterRoute: &debugClusterRoute{
			ClientID:     "client-1",
			DeviceID:     "device-1",
			SessionID:    "session-1",
			GatewayNode:  "gateway-b",
			InternalAddr: "http://127.0.0.1:18183",
		},
	}

	if err := validateDebugRoute(cfg, resp); err != nil {
		t.Fatalf("validateDebugRoute() error = %v", err)
	}
}

func TestValidateDebugRouteRejectsLocalSession(t *testing.T) {
	cfg := config{
		InternalURL:     "http://127.0.0.1:18182",
		ClientID:        "client-1",
		DeviceID:        "device-1",
		ExpectRouteNode: "gateway-b",
	}
	resp := debugRouteResponse{
		Code:              "ok",
		LocalSessionFound: true,
		ClusterEnabled:    true,
		ClusterRouteFound: true,
		ClusterRoute:      &debugClusterRoute{ClientID: "client-1", DeviceID: "device-1", SessionID: "session-1", GatewayNode: "gateway-b"},
	}

	if err := validateDebugRoute(cfg, resp); err == nil {
		t.Fatal("validateDebugRoute() error = nil, want local session rejection")
	}
}

func TestValidateDebugSessions(t *testing.T) {
	cfg := config{
		ClientID:          "client-1",
		DeviceID:          "device-1",
		ExpectSessionNode: "gateway-b",
	}
	resp := debugSessionsResponse{
		Code:          "ok",
		GatewayNode:   "gateway-b",
		ClientID:      "client-1",
		Total:         1,
		UniqueClients: 1,
		Sessions: []debugSession{{
			SessionID:   "session-1",
			ClientID:    "client-1",
			DeviceID:    "device-1",
			GatewayNode: "gateway-b",
		}},
	}

	if err := validateDebugSessions(cfg, resp); err != nil {
		t.Fatalf("validateDebugSessions() error = %v", err)
	}
}

func TestValidateDebugSessionsGone(t *testing.T) {
	cfg := config{
		ClientID:          "client-1",
		DeviceID:          "device-1",
		ExpectSessionNode: "gateway-b",
	}
	resp := debugSessionsResponse{
		Code:          "ok",
		GatewayNode:   "gateway-b",
		ClientID:      "client-1",
		Total:         0,
		UniqueClients: 0,
		Sessions:      []debugSession{},
	}

	if err := validateDebugSessionsGone(cfg, resp); err != nil {
		t.Fatalf("validateDebugSessionsGone() error = %v", err)
	}
}

func TestValidateDebugSessionsGoneRejectsStillPresentSession(t *testing.T) {
	cfg := config{
		ClientID:          "client-1",
		DeviceID:          "device-1",
		ExpectSessionNode: "gateway-b",
	}
	resp := debugSessionsResponse{
		Code:        "ok",
		GatewayNode: "gateway-b",
		ClientID:    "client-1",
		Sessions: []debugSession{{
			SessionID:   "session-1",
			ClientID:    "client-1",
			DeviceID:    "device-1",
			GatewayNode: "gateway-b",
		}},
	}

	if err := validateDebugSessionsGone(cfg, resp); err == nil {
		t.Fatal("validateDebugSessionsGone() error = nil, want still-present rejection")
	}
}

func TestValidateAdminStorageDiagnostics(t *testing.T) {
	resp := adminDiagnosticsResponse{
		Code:        "ok",
		GatewayNode: "gateway-a",
		AdminConsole: adminConsoleSummary{
			Session: adminConsoleSessionSummary{
				Enabled:         true,
				CookieName:      "zcourier_admin_session",
				StorageType:     "redis",
				RedisConfigured: true,
			},
			Audit: adminConsoleAuditSummary{
				StorageType:        "postgres",
				StoreConfigured:    true,
				PostgresConfigured: true,
			},
		},
	}

	if err := validateAdminStorageDiagnostics(resp, true); err != nil {
		t.Fatalf("validateAdminStorageDiagnostics() error = %v", err)
	}
}

func TestValidateAdminStorageDiagnosticsRejectsMemorySession(t *testing.T) {
	resp := adminDiagnosticsResponse{
		Code: "ok",
		AdminConsole: adminConsoleSummary{
			Session: adminConsoleSessionSummary{
				Enabled:     true,
				CookieName:  "zcourier_admin_session",
				StorageType: "memory",
			},
			Audit: adminConsoleAuditSummary{
				StorageType:        "postgres",
				StoreConfigured:    true,
				PostgresConfigured: true,
			},
		},
	}

	if err := validateAdminStorageDiagnostics(resp, true); err == nil {
		t.Fatal("validateAdminStorageDiagnostics() error = nil, want memory session rejection")
	}
}

func TestValidateInternalAuthConfigAcceptsBulkRequeueFixture(t *testing.T) {
	cfg := config{
		InternalAuthMode:     internalAuthModeToken,
		CheckAdminStorage:    true,
		CheckBulkRequeue:     true,
		CheckQueueCapacity:   true,
		ExpectPerDeviceLimit: 8,
		AdminSessionPeerURL:  "http://127.0.0.1:18183",
		ExpectTerminalPolicy: "integration-terminal",
	}
	if err := validateInternalAuthConfig(cfg); err != nil {
		t.Fatalf("validateInternalAuthConfig() error = %v", err)
	}
}

func TestValidateInternalAuthConfigRejectsBulkRequeueWithoutAdminStorage(t *testing.T) {
	cfg := config{
		InternalAuthMode:     internalAuthModeToken,
		CheckBulkRequeue:     true,
		CheckQueueCapacity:   true,
		ExpectPerDeviceLimit: 8,
		AdminSessionPeerURL:  "http://127.0.0.1:18183",
		ExpectTerminalPolicy: "integration-terminal",
	}
	if err := validateInternalAuthConfig(cfg); err == nil {
		t.Fatal("validateInternalAuthConfig() error = nil, want missing admin storage rejection")
	}
}

func TestValidateBulkRequeueResponse(t *testing.T) {
	response := downlink.BulkRequeueResponse{
		Code:    "partial_failure",
		Total:   2,
		Success: 1,
		Failed:  1,
		Results: []downlink.MessageStatusResponse{
			{
				Code:       "ok",
				MessageID:  "message-1",
				Status:     downlink.MessageStatusPending,
				PolicyName: "integration-terminal",
			},
			{
				Code:            "queue_capacity_exceeded",
				MessageID:       "message-2",
				Status:          downlink.MessageStatusFailed,
				PolicyName:      "integration-terminal",
				TerminalReason:  downlink.TerminalReasonMaxAttempts,
				CapacityScope:   downlink.QueueCapacityScopeDevice,
				CapacityLimit:   8,
				CapacityPending: 8,
			},
		},
	}
	if err := validateBulkRequeueResponse(response, []string{"message-1", "message-2"}, "integration-terminal", 8); err != nil {
		t.Fatalf("validateBulkRequeueResponse() error = %v", err)
	}
}

func TestSumMetricSamplesMatchesLabelsInAnyOrder(t *testing.T) {
	metricsText := `
# HELP z_courier_cluster_peer_push_total Total number of cluster peer push attempts.
z_courier_cluster_peer_push_total{result="success",target_node="gateway-b"} 2
z_courier_cluster_peer_push_total{result="failure",target_node="gateway-b"} 1
`
	value, found, err := sumMetricSamples(metricsText, "z_courier_cluster_peer_push_total", map[string]string{
		"target_node": "gateway-b",
		"result":      "success",
	})
	if err != nil {
		t.Fatalf("sumMetricSamples() error = %v", err)
	}
	if !found || value != 2 {
		t.Fatalf("sumMetricSamples() = value:%v found:%v, want 2 true", value, found)
	}
}

func TestCheckIdempotencyMetrics(t *testing.T) {
	metricsText := `
z_courier_downlink_push_total{msg_id="2001",result="idempotent_replay"} 2
z_courier_downlink_push_total{msg_id="2001",result="message_id_conflict"} 2
`
	if err := checkIdempotencyMetrics(metricsText); err != nil {
		t.Fatalf("checkIdempotencyMetrics() error = %v", err)
	}
}

func TestCheckIdempotencyMetricsRejectsMissingOutcome(t *testing.T) {
	metricsText := `
z_courier_downlink_push_total{msg_id="2001",result="idempotent_replay"} 2
`
	if err := checkIdempotencyMetrics(metricsText); err == nil {
		t.Fatal("checkIdempotencyMetrics() error = nil, want missing conflict rejection")
	}
}

func TestCheckReconnectRetryMetrics(t *testing.T) {
	metricsText := `
z_courier_downlink_push_total{msg_id="2001",result="queued"} 2
z_courier_cluster_registry_lookup_total{result="hit"} 3
z_courier_cluster_registry_unbind_total{result="success"} 1
z_courier_downlink_retry_scan_total{result="success"} 4
z_courier_downlink_retry_claim_duration_seconds_count{owner="gateway-a",result="success"} 4
z_courier_cluster_peer_push_total{target_node="gateway-b",result="success"} 1
z_courier_cluster_peer_signature_total{result="success"} 1
`
	err := checkReconnectRetryMetrics(metricsText, config{ExpectRouteNode: "gateway-b"})
	if err != nil {
		t.Fatalf("checkReconnectRetryMetrics() error = %v", err)
	}
}

func TestCheckReconnectRetryMetricsRejectsMissingMetric(t *testing.T) {
	metricsText := `
z_courier_downlink_push_total{msg_id="2001",result="queued"} 1
`
	err := checkReconnectRetryMetrics(metricsText, config{ExpectRouteNode: "gateway-b"})
	if err == nil {
		t.Fatal("checkReconnectRetryMetrics() error = nil, want missing metric rejection")
	}
}

func TestValidateRetryFairnessScan(t *testing.T) {
	scan := downlink.RetryScanResponse{
		Code:            "ok",
		Limit:           3,
		Scanned:         3,
		Queued:          3,
		SelectionMode:   downlink.RetrySelectionModeFair,
		SelectedDevices: 3,
		MaxPerDevice:    1,
	}
	if err := validateRetryFairnessScan(scan, 3); err != nil {
		t.Fatalf("validateRetryFairnessScan() error = %v", err)
	}
}

func TestValidateRetryFairnessScanRejectsHotDeviceDominance(t *testing.T) {
	scan := downlink.RetryScanResponse{
		Code:            "ok",
		Limit:           3,
		Scanned:         3,
		Queued:          3,
		SelectionMode:   downlink.RetrySelectionModeFair,
		SelectedDevices: 1,
		MaxPerDevice:    3,
	}
	if err := validateRetryFairnessScan(scan, 3); err == nil {
		t.Fatal("validateRetryFairnessScan() error = nil, want hot-device dominance rejection")
	}
}

func TestCheckRetryFairnessMetrics(t *testing.T) {
	metricsText := `
z_courier_downlink_retry_selected_devices_sum{mode="fair"} 3
z_courier_downlink_retry_max_per_device_sum{mode="fair"} 1
z_courier_downlink_retry_claim_messages_total{owner="gateway-a",result="success"} 3
`
	if err := checkRetryFairnessMetrics(metricsText); err != nil {
		t.Fatalf("checkRetryFairnessMetrics() error = %v", err)
	}
}

func TestCheckRetryFairnessMetricsRejectsMissingSelection(t *testing.T) {
	metricsText := `
z_courier_downlink_retry_claim_messages_total{owner="gateway-a",result="success"} 3
`
	if err := checkRetryFairnessMetrics(metricsText); err == nil {
		t.Fatal("checkRetryFairnessMetrics() error = nil, want missing selection rejection")
	}
}

func TestCheckAdminStorageMetrics(t *testing.T) {
	metricsText := `
z_courier_admin_audit_write_total{store="postgres",result="success"} 1
z_courier_admin_session_store_operation_total{store="redis",operation="save",result="success"} 1
z_courier_admin_session_store_operation_total{store="redis",operation="lookup",result="hit"} 2
z_courier_admin_audit_write_duration_seconds_count{store="postgres",result="success"} 1
z_courier_admin_session_store_operation_duration_seconds_count{store="redis",operation="save",result="success"} 1
`

	if err := checkAdminStorageMetrics(metricsText); err != nil {
		t.Fatalf("checkAdminStorageMetrics() error = %v", err)
	}
}

func TestCheckAdminStorageMetricsRejectsMissingMetric(t *testing.T) {
	metricsText := `
z_courier_admin_audit_write_total{store="postgres",result="success"} 1
`

	if err := checkAdminStorageMetrics(metricsText); err == nil {
		t.Fatal("checkAdminStorageMetrics() error = nil, want missing metric rejection")
	}
}

func TestCheckBulkRequeueMetrics(t *testing.T) {
	metricsText := `
z_courier_downlink_bulk_requeue_total{result="partial_failure"} 1
z_courier_downlink_requeue_total{result="success"} 1
z_courier_downlink_requeue_total{result="queue_capacity_exceeded"} 1
z_courier_admin_action_total{action="downlink_message_bulk_requeue",result="partial_failure"} 1
`
	if err := checkBulkRequeueMetrics(metricsText); err != nil {
		t.Fatalf("checkBulkRequeueMetrics() error = %v", err)
	}
}

func TestCheckBulkRequeueMetricsRejectsMissingBatchOutcome(t *testing.T) {
	metricsText := `
z_courier_downlink_requeue_total{result="success"} 1
z_courier_downlink_requeue_total{result="queue_capacity_exceeded"} 1
z_courier_admin_action_total{action="downlink_message_bulk_requeue",result="partial_failure"} 1
`
	if err := checkBulkRequeueMetrics(metricsText); err == nil {
		t.Fatal("checkBulkRequeueMetrics() error = nil, want missing batch outcome rejection")
	}
}
