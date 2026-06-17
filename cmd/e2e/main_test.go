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
