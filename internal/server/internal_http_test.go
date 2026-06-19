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
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
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

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
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

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
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

func TestInternalHTTPRejectsUnknownProgrammaticAuthMode(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{Mode: "unknown"},
	})
	if _, err := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil); err == nil {
		t.Fatal("newInternalHTTPServer() error = nil, want unsupported auth mode error")
	}
}

func TestInternalHTTPRegistersMessageAdminRoutes(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
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

	server := mustInternalHTTPServer(t, config, service, health, nil)
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

func TestInternalHTTPHMACAcceptsSignedRequestAndRejectsReplay(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "backend-1", Secret: internalHMACTestSecret})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	body := []byte(`{"client_id":"client-1","device_id":"device-1","msg_id":2001,"body":"aGVsbG8="}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("signed request status = %d, want %d, body = %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	replay := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(string(body)))
	replay.Header = req.Header.Clone()
	replayRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(replayRec, replay)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d, body = %s", replayRec.Code, http.StatusUnauthorized, replayRec.Body.String())
	}

	tampered := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(`{"client_id":"other"}`))
	tampered.Header = req.Header.Clone()
	tamperedRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(tamperedRec, tampered)
	if tamperedRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d, want %d, body = %s", tamperedRec.Code, http.StatusUnauthorized, tamperedRec.Body.String())
	}
}

func TestInternalHTTPHMACBackendSDKIntegration(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
	})
	internalServer := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	httpServer := httptest.NewServer(internalServer.Handler)
	defer httpServer.Close()

	client, err := sdkbackend.NewClient(sdkbackend.Config{
		BaseURL: httpServer.URL,
		HMAC: &sdkbackend.HMACConfig{
			KeyID:  "backend-1",
			Secret: internalHMACTestSecret,
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	response, err := client.Push(context.Background(), sdkbackend.PushRequest{
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		MessageID: "message-1",
		Body:      []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if response.Code != "ok" || response.DeliveryState != sdkbackend.DeliveryStateQueued {
		t.Fatalf("response = %+v, want queued", response)
	}
}

func TestInternalHTTPHMACLeavesPublicAndPeerRoutesOutsideMiddleware(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
		Cluster: ClusterConfig{
			Enabled: true,
			Peer:    ClusterPeerConfig{Token: "peer-token"},
		},
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", healthRec.Code, http.StatusOK)
	}

	peerReq := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(`{}`))
	peerReq.Header.Set(downlink.InternalTokenHeader, "peer-token")
	peerRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(peerRec, peerReq)
	if peerRec.Code != http.StatusBadRequest {
		t.Fatalf("peer status = %d, want %d, body = %s", peerRec.Code, http.StatusBadRequest, peerRec.Body.String())
	}
}

func TestInternalHTTPHMACRequiresSignature(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/messages", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
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

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, registry)
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

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
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

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
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

func mustInternalHTTPServer(t *testing.T, config Config, service *downlink.Service, health *gatewayHealth, registry cluster.OnlineRegistry) *http.Server {
	t.Helper()
	server, err := newInternalHTTPServer(config, zap.NewNop(), service, health, registry)
	if err != nil {
		t.Fatalf("newInternalHTTPServer() error = %v", err)
	}
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}
	return server
}

var internalHMACTestSecret = []byte("0123456789abcdef0123456789abcdef")
