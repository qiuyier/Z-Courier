package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/downlink"
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

	server := newInternalHTTPServer(config, zap.NewNop(), service)
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

	server := newInternalHTTPServer(config, zap.NewNop(), service)
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

	server := newInternalHTTPServer(config, zap.NewNop(), service)
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
