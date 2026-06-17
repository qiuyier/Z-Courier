package main

import "testing"

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

func TestCheckReconnectRetryMetrics(t *testing.T) {
	metricsText := `
z_courier_downlink_push_total{msg_id="2001",result="queued"} 2
z_courier_cluster_registry_lookup_total{result="hit"} 3
z_courier_cluster_registry_unbind_total{result="success"} 1
z_courier_downlink_retry_scan_total{result="success"} 4
z_courier_downlink_retry_claim_duration_seconds_count{owner="gateway-a",result="success"} 4
z_courier_cluster_peer_push_total{target_node="gateway-b",result="success"} 1
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
