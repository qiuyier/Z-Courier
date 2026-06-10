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
