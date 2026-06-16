package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

func TestInternalHTTPRegistersPeerPushWhenClusterEnabled(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Peer: ClusterPeerConfig{
				Token: "peer-token",
			},
		},
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	req := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestInternalHTTPDoesNotRegisterPeerPushWhenClusterDisabled(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	req := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInternalHTTPRegistersMessageAdminRoutes(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list", method: http.MethodGet, path: "/internal/messages?status=failed", body: ""},
		{name: "requeue", method: http.MethodPost, path: "/internal/message/requeue", body: `{"message_id":"missing"}`},
		{name: "discard", method: http.MethodPost, path: "/internal/message/discard", body: `{"message_id":"missing"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			server.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestInternalHTTPHealthAndReady(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	health := &gatewayHealth{}
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, health, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec = httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz status = %d, want %d", rec.Code, http.StatusOK)
	}

	health.BeginDrain()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec = httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz draining status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestInternalHTTPDebugRouteReturnsLocalSessionAndClusterRoute(t *testing.T) {
	sessions := session.NewManager()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-1",
		ConnID:      7,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		TokenID:     "token-1",
		GatewayNode: "gateway-a",
		Now:         now,
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute, Now: func() time.Time { return now }})
	if err := registry.Bind(context.Background(), cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    "session-1",
		GatewayNode:  "gateway-b",
		InternalAddr: "http://gateway-b:18183",
		TokenID:      "token-1",
	}); err != nil {
		t.Fatalf("registry Bind() error = %v", err)
	}

	service := downlink.NewService(sessions, testConnectionFinder{})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18182",
		},
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, registry)
	req := httptest.NewRequest(http.MethodGet, "/internal/debug/route?client_id=client-1&device_id=device-1", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp debugRouteResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || !resp.LocalSessionFound || !resp.ClusterRouteFound || !resp.ClusterEnabled {
		t.Fatalf("response = %+v, want local and cluster route found", resp)
	}
	if resp.LocalSession == nil || resp.LocalSession.SessionID != "session-1" || resp.LocalSession.ConnID != 7 {
		t.Fatalf("local session = %+v, want session-1 conn 7", resp.LocalSession)
	}
	if resp.ClusterRoute == nil || resp.ClusterRoute.GatewayNode != "gateway-b" || resp.ClusterRoute.InternalAddr != "http://gateway-b:18183" {
		t.Fatalf("cluster route = %+v, want gateway-b", resp.ClusterRoute)
	}
}

func TestInternalHTTPDebugRouteRequiresToken(t *testing.T) {
	sessions := session.NewManager()
	service := downlink.NewService(sessions, testConnectionFinder{})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/debug/route?client_id=client-1&device_id=device-1", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestInternalHTTPDebugSessionsListsLocalSessions(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-1", ConnID: 1, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-2", ConnID: 2, ClientID: "client-1", DeviceID: "device-2"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-3", ConnID: 3, ClientID: "client-2", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	service := downlink.NewService(sessions, testConnectionFinder{})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/debug/sessions?client_id=client-1&limit=1", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp debugSessionsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.ClientID != "client-1" || resp.Total != 2 || resp.Limit != 1 || len(resp.Sessions) != 1 {
		t.Fatalf("response = %+v, want filtered total=2 limit=1 one session", resp)
	}
	if resp.Sessions[0].SessionID != "session-1" {
		t.Fatalf("first session = %+v, want session-1", resp.Sessions[0])
	}
}
