package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/resilience"
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
	if _, err := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil, nil); err == nil {
		t.Fatal("newInternalHTTPServer() error = nil, want unsupported auth mode error")
	}
}

func TestInternalHTTPRejectsUnknownProgrammaticPeerAuthMode(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		Cluster: ClusterConfig{
			Enabled: true,
			Peer: ClusterPeerConfig{
				Auth: ClusterPeerAuthConfig{Mode: "unknown"},
			},
		},
	})
	if _, err := newInternalHTTPServer(config, zap.NewNop(), service, &gatewayHealth{}, nil, nil); err == nil {
		t.Fatal("newInternalHTTPServer() error = nil, want unsupported peer auth mode error")
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

func TestInternalHTTPAdminOverview(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-1", ConnID: 1, ClientID: "client-1", DeviceID: "device-1", GatewayNode: "gateway-a"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-2", ConnID: 2, ClientID: "client-1", DeviceID: "device-2", GatewayNode: "gateway-a"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute})
	store := downlink.NewMemoryStore()
	service := downlink.NewService(sessions, testConnectionFinder{}, downlink.WithStore(store))
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		DownlinkStore:    store,
		AdminConsole: AdminConsoleConfig{
			Enabled:   true,
			Path:      "/console/",
			AssetsDir: "web/admin/dist",
			Monitoring: AdminConsoleMonitoringConfig{
				PrometheusURL: "http://prometheus.local:9090",
				GrafanaURL:    "http://grafana.local:3000",
				DashboardURL:  "http://grafana.local:3000/d/z-courier-overview",
			},
		},
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Registry: ClusterRegistryConfig{
				Type: "memory",
				TTL:  time.Minute,
			},
			Peer: ClusterPeerConfig{
				Auth: ClusterPeerAuthConfig{
					Mode: ClusterPeerAuthModeHMAC,
					HMAC: ClusterPeerHMACConfig{
						KeyID: "gateway-1",
						Keys:  map[string][]byte{"gateway-1": peerHMACTestSecret},
					},
				},
			},
		},
		UpstreamRoutes: []UpstreamRouteConfig{{
			Name:     "events",
			MsgIDMin: 2000,
			MsgIDMax: 2999,
			NSQ:      &NSQUpstreamConfig{Topic: "message_events"},
		}},
	})

	health := &gatewayHealth{}
	server := mustInternalHTTPServer(t, config, service, health, registry)
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/overview", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp adminOverviewResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.GatewayNode != "gateway-a" || !resp.Readiness.Ready || resp.Readiness.Status != "ready" {
		t.Fatalf("overview = %+v, want ready gateway-a", resp)
	}
	if resp.Sessions.Online != 2 || resp.Sessions.UniqueClients != 1 {
		t.Fatalf("sessions = %+v, want 2 online and 1 unique client", resp.Sessions)
	}
	if !resp.Cluster.Enabled || resp.Cluster.RegistryType != "memory" || resp.Cluster.PeerAuthMode != ClusterPeerAuthModeHMAC {
		t.Fatalf("cluster = %+v, want enabled memory hmac", resp.Cluster)
	}
	if !resp.Downlink.StoreConfigured || resp.Upstream.Routes != 1 {
		t.Fatalf("downlink/upstream = %+v/%+v, want store configured and one route", resp.Downlink, resp.Upstream)
	}
	if !resp.AdminConsole.Enabled || resp.AdminConsole.Monitoring.PrometheusURL != "http://prometheus.local:9090" {
		t.Fatalf("admin console = %+v, want enabled monitoring links", resp.AdminConsole)
	}

	drainStartedAt := time.UnixMilli(1760000000000)
	health.BeginDrainAt(drainStartedAt)
	req = httptest.NewRequest(http.MethodGet, "/internal/admin/overview", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec = httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draining status = %d, want %d", rec.Code, http.StatusOK)
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal draining error = %v", err)
	}
	if resp.Readiness.Ready || resp.Readiness.Status != "draining" {
		t.Fatalf("draining readiness = %+v, want draining", resp.Readiness)
	}
	if !resp.Readiness.DrainingSince.Equal(drainStartedAt.UTC()) || resp.Readiness.DrainDuration == "" {
		t.Fatalf("draining readiness timing = %+v, want since and duration", resp.Readiness)
	}
}

func TestInternalHTTPAdminConsoleServesSPA(t *testing.T) {
	assetsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetsDir, "index.html"), []byte("<html>console</html>"), 0o644); err != nil {
		t.Fatalf("WriteFile(index) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(assetsDir, "assets"), 0o755); err != nil {
		t.Fatalf("Mkdir(assets) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "assets", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("WriteFile(asset) error = %v", err)
	}

	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		AdminConsole: AdminConsoleConfig{
			Enabled:   true,
			Path:      "/console/",
			AssetsDir: assetsDir,
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
		wantCache  string
	}{
		{name: "index", method: http.MethodGet, path: "/console/", wantStatus: http.StatusOK, wantBody: "console", wantCache: adminConsoleIndexCacheControl},
		{name: "asset", method: http.MethodGet, path: "/console/assets/app.js", wantStatus: http.StatusOK, wantBody: "ok", wantCache: adminConsoleAssetCacheControl},
		{name: "spa route", method: http.MethodGet, path: "/console/routes", wantStatus: http.StatusOK, wantBody: "console", wantCache: adminConsoleIndexCacheControl},
		{name: "missing asset", method: http.MethodGet, path: "/console/assets/missing.js", wantStatus: http.StatusNotFound, wantCache: adminConsoleIndexCacheControl},
		{name: "method", method: http.MethodPost, path: "/console/", wantStatus: http.StatusMethodNotAllowed, wantCache: adminConsoleIndexCacheControl},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			server.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want substring %q", rec.Body.String(), tt.wantBody)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != adminConsoleCSP {
				t.Fatalf("Content-Security-Policy = %q, want %q", got, adminConsoleCSP)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Fatalf("X-Frame-Options = %q, want DENY", got)
			}
		})
	}
}

func TestInternalHTTPAdminConsoleDisabled(t *testing.T) {
	assetsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetsDir, "index.html"), []byte("<html>console</html>"), 0o644); err != nil {
		t.Fatalf("WriteFile(index) error = %v", err)
	}

	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		AdminConsole: AdminConsoleConfig{
			Enabled:   false,
			Path:      "/console/",
			AssetsDir: assetsDir,
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("Content-Security-Policy = %q, want empty when console is disabled", got)
	}
}

func TestAdminReadinessIncludesDrainTiming(t *testing.T) {
	health := &gatewayHealth{}
	startedAt := time.UnixMilli(1760000000000)
	health.BeginDrainAt(startedAt)

	readiness := adminReadinessFromHealth(health, startedAt.Add(2*time.Second))
	if readiness.Ready || readiness.Status != "draining" {
		t.Fatalf("readiness = %+v, want draining", readiness)
	}
	if !readiness.DrainingSince.Equal(startedAt.UTC()) {
		t.Fatalf("DrainingSince = %v, want %v", readiness.DrainingSince, startedAt.UTC())
	}
	if readiness.DrainDuration != "2s" {
		t.Fatalf("DrainDuration = %q, want 2s", readiness.DrainDuration)
	}
}

func TestInternalHTTPAdminDiagnostics(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-1", ConnID: 1, ClientID: "client-1", DeviceID: "device-1", GatewayNode: "gateway-a"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute})
	store := downlink.NewMemoryStore()
	service := downlink.NewService(sessions, testConnectionFinder{}, downlink.WithStore(store))
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "0.0.0.0:18080",
		InternalToken:    "secret",
		DownlinkStore:    store,
		DownlinkDelivery: DownlinkDeliveryConfig{
			RetryJitter: 5 * time.Second,
		},
		DownlinkPolicies: []downlink.DeliveryPolicyRule{{
			Policy: downlink.DeliveryPolicy{
				Name:              "critical",
				MaxAttempts:       5,
				AckTimeout:        time.Second,
				InitialRetryDelay: time.Second,
				BackoffMultiplier: 1,
				MaxRetryDelay:     time.Second,
			},
			MsgIDMin: 2001,
			MsgIDMax: 2001,
		}},
		DownlinkCapacity: downlink.QueueCapacity{
			MaxPendingGlobal:    5000,
			MaxPendingPerDevice: 50,
		},
		DownlinkTerminal: DownlinkTerminalConfig{
			PublisherType: "nsq",
			NSQ: NSQUpstreamConfig{
				Topic:      "terminal_events",
				AuthSecret: "terminal-secret",
			},
			RetryInterval: 5 * time.Second,
			RetryDelay:    30 * time.Second,
		},
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Registry: ClusterRegistryConfig{
				Type: "memory",
				TTL:  time.Minute,
			},
		},
		UpstreamRoutes: []UpstreamRouteConfig{
			{
				Name:        "http-route",
				MsgIDMin:    1001,
				MsgIDMax:    1999,
				MaxInFlight: 10,
				HTTP: &HTTPUpstreamConfig{
					URL:   "http://user:password@backend:8080/gateway/upstream?token=secret#fragment",
					Token: "upstream-token",
				},
			},
			{
				Name:     "nsq-route",
				MsgIDMin: 2000,
				MsgIDMax: 2999,
				NSQ: &NSQUpstreamConfig{
					Addresses:  []string{"nsqd:4150"},
					Topic:      "message_events",
					AuthSecret: "nsq-secret",
				},
			},
		},
	})
	config.UpstreamRuntime = newUpstreamRuntime(config.UpstreamRoutes)

	health := &gatewayHealth{}
	server := mustInternalHTTPServer(t, config, service, health, registry)
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnostics", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"upstream-token", "nsq-secret", "terminal-secret", "user:password", "token=secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("diagnostics leaked secret %q in body %s", secret, body)
		}
	}

	var resp adminDiagnosticsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.GatewayNode != "gateway-a" || !resp.Runtime.Started || resp.Runtime.StartedAt.IsZero() || resp.Runtime.Uptime == "" {
		t.Fatalf("runtime diagnostics = %+v, want started gateway-a", resp)
	}
	if !resp.Readiness.Ready || resp.Sessions.Online != 1 || resp.Sessions.UniqueClients != 1 {
		t.Fatalf("readiness/sessions = %+v/%+v", resp.Readiness, resp.Sessions)
	}
	if resp.Auth.Provider == "" || !resp.Auth.VerifierLoaded {
		t.Fatalf("auth diagnostics = %+v, want loaded provider", resp.Auth)
	}
	if resp.Upstream.Routes != 2 || resp.Upstream.HTTPRoutes != 1 || resp.Upstream.NSQRoutes != 1 || resp.Upstream.RoutesWithCapacity != 1 {
		t.Fatalf("upstream diagnostics = %+v, want http/nsq/capacity counts", resp.Upstream)
	}
	if len(resp.Upstream.HTTPRouteStates) != 1 || resp.Upstream.HTTPRouteStates[0].Name != "http-route" || resp.Upstream.HTTPRouteStates[0].Status != resilience.DependencyStatusHealthy {
		t.Fatalf("upstream route states = %+v, want healthy http-route", resp.Upstream.HTTPRouteStates)
	}
	if resp.Capacity.InternalHTTPMaxInFlight == 0 || resp.Capacity.UpstreamLimitedRoutes != 1 {
		t.Fatalf("capacity diagnostics = %+v, want configured limits", resp.Capacity)
	}
	if resp.Downlink.RetryJitter != "5s" {
		t.Fatalf("downlink diagnostics retry_jitter = %q, want 5s", resp.Downlink.RetryJitter)
	}
	if resp.Downlink.PolicyCount != 2 || strings.Join(resp.Downlink.PolicyNames, ",") != "default,critical" {
		t.Fatalf("downlink diagnostics policies = %d/%v, want 2/[default critical]", resp.Downlink.PolicyCount, resp.Downlink.PolicyNames)
	}
	if resp.Downlink.MaxPendingGlobal != 5000 || resp.Downlink.MaxPendingPerDevice != 50 {
		t.Fatalf("downlink capacity diagnostics = %+v", resp.Downlink)
	}
	if resp.Downlink.TerminalPublisher != "nsq" || resp.Downlink.TerminalTopic != "terminal_events" ||
		resp.Downlink.TerminalRetryInterval != "5s" || resp.Downlink.TerminalRetryDelay != "30s" {
		t.Fatalf("downlink terminal diagnostics = %+v", resp.Downlink)
	}
	if len(resp.Dependencies) == 0 || len(resp.Warnings) == 0 {
		t.Fatalf("dependencies/warnings = %+v/%+v, want non-empty", resp.Dependencies, resp.Warnings)
	}
	if dependency := findAdminDependency(resp.Dependencies, "admin_audit_store"); dependency.Status != "configured" || dependency.Reason != "memory" {
		t.Fatalf("admin audit dependency = %+v, want configured memory", dependency)
	}
	if dependency := findAdminDependency(resp.Dependencies, "admin_session_store"); dependency.Status != "disabled" || dependency.Reason != "memory" {
		t.Fatalf("admin session dependency = %+v, want disabled memory", dependency)
	}
	if !hasAdminWarning(resp.Warnings, "non_durable_admin_audit_store") {
		t.Fatalf("warnings = %+v, want non_durable_admin_audit_store", resp.Warnings)
	}
}

func TestInternalHTTPAdminDiagnosticsReportsDegradedHTTPUpstream(t *testing.T) {
	routes := []UpstreamRouteConfig{{
		Name:     "http-route",
		MsgIDMin: 1001,
		MsgIDMax: 1999,
		HTTP: &HTTPUpstreamConfig{
			URL: "http://backend.local/gateway/upstream",
		},
	}}
	runtime := newUpstreamRuntime(routes)
	tracker := runtime.ensureRoute("http-route", "http")
	for range 3 {
		tracker.MarkFailure("http_status_502")
	}
	config := normalizeConfig(Config{
		InternalToken:   "secret",
		UpstreamRoutes:  routes,
		UpstreamRuntime: runtime,
	})

	health := &gatewayHealth{}
	server := mustInternalHTTPServer(t, config, downlink.NewService(nil, nil), health, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnostics", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp adminDiagnosticsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Upstream.HTTPRouteStates) != 1 {
		t.Fatalf("HTTPRouteStates = %+v, want one route state", resp.Upstream.HTTPRouteStates)
	}
	state := resp.Upstream.HTTPRouteStates[0]
	if state.Status != resilience.DependencyStatusDegraded || state.ConsecutiveFailures != 3 || state.LastReason != "http_status_502" {
		t.Fatalf("HTTP route state = %+v, want degraded state", state)
	}
	var httpDependency adminDependency
	for _, dependency := range resp.Dependencies {
		if dependency.Name == "http_upstream" {
			httpDependency = dependency
			break
		}
	}
	if httpDependency.Status != "degraded" || httpDependency.Reason != "degraded routes: 1/1" {
		t.Fatalf("http dependency = %+v, want degraded", httpDependency)
	}
}

func TestInternalHTTPAdminCheck(t *testing.T) {
	var gotMethod string
	var gotToken string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotToken = r.Header.Get("X-ZCourier-Internal-Token")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute})
	store := downlink.NewMemoryStore()
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(store))
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		DownlinkStore:    store,
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18080",
			Registry: ClusterRegistryConfig{
				Type: "memory",
				TTL:  time.Minute,
			},
		},
		UpstreamRoutes: []UpstreamRouteConfig{{
			Name:     "http-route",
			MsgIDMin: 1001,
			MsgIDMax: 1999,
			HTTP: &HTTPUpstreamConfig{
				URL:   upstream.URL + "/gateway/upstream?token=secret#fragment",
				Token: "upstream-token",
			},
		}},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, newMetricsRegistry(registry))
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/check?timeout=1s", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotMethod != http.MethodHead || gotToken != "upstream-token" {
		t.Fatalf("upstream request = method %s token %q, want HEAD with token", gotMethod, gotToken)
	}
	body := rec.Body.String()
	for _, secret := range []string{"upstream-token", "token=secret", "#fragment"} {
		if strings.Contains(body, secret) {
			t.Fatalf("check leaked secret %q in body %s", secret, body)
		}
	}

	var resp adminCheckResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.GatewayNode != "gateway-a" || resp.Status != adminCheckStatusOK || resp.Timeout != "1s" {
		t.Fatalf("check response = %+v, want ok gateway-a timeout", resp)
	}
	if findAdminCheck(resp.Checks, "downlink_store").Status != adminCheckStatusOK {
		t.Fatalf("downlink check = %+v, want ok", findAdminCheck(resp.Checks, "downlink_store"))
	}
	if findAdminCheck(resp.Checks, "admin_audit_store").Status != adminCheckStatusOK {
		t.Fatalf("admin audit check = %+v, want ok", findAdminCheck(resp.Checks, "admin_audit_store"))
	}
	if findAdminCheck(resp.Checks, "admin_session_store").Status != adminCheckStatusSkipped {
		t.Fatalf("admin session check = %+v, want skipped", findAdminCheck(resp.Checks, "admin_session_store"))
	}
	if findAdminCheck(resp.Checks, "cluster_registry").Status != adminCheckStatusOK {
		t.Fatalf("cluster check = %+v, want ok", findAdminCheck(resp.Checks, "cluster_registry"))
	}
	if findAdminCheck(resp.Checks, "http_upstream:http-route").Status != adminCheckStatusOK {
		t.Fatalf("http upstream check = %+v, want ok", findAdminCheck(resp.Checks, "http_upstream:http-route"))
	}
}

func TestInternalHTTPAdminDiagnoseBundle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-1",
		ConnID:      1,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		TokenID:     "token-1",
		GatewayNode: "gateway-a",
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute})
	if err := registry.Bind(context.Background(), cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    "session-1",
		GatewayNode:  "gateway-a",
		InternalAddr: "http://gateway-a:18182",
		TokenID:      "token-1",
	}); err != nil {
		t.Fatalf("registry Bind() error = %v", err)
	}

	store := downlink.NewMemoryStore()
	service := downlink.NewService(sessions, testConnectionFinder{}, downlink.WithStore(store))
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		DownlinkStore:    store,
		Cluster: ClusterConfig{
			Enabled:      true,
			InternalAddr: "http://gateway-a:18182",
			Registry: ClusterRegistryConfig{
				Type: "memory",
				TTL:  time.Minute,
			},
		},
		UpstreamRoutes: []UpstreamRouteConfig{{
			Name:     "http-route",
			MsgIDMin: 1001,
			MsgIDMax: 1999,
			HTTP: &HTTPUpstreamConfig{
				URL:   upstream.URL + "/gateway/upstream?token=secret#fragment",
				Token: "upstream-token",
			},
		}},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, registry)
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnose?probe_timeout=1s&message_limit=5&session_limit=10&client_id=client-1&device_id=device-1", nil)
	req.Host = "gateway-a:18182"
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"secret", "upstream-token", "token=secret", "#fragment"} {
		if strings.Contains(body, secret) {
			t.Fatalf("diagnose response leaked %q: %s", secret, body)
		}
	}

	var resp adminDiagnoseResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.TargetURL != "http://gateway-a:18182" || resp.CollectionStatus != "complete" {
		t.Fatalf("diagnose response = %+v, want complete sanitized target", resp)
	}
	for _, section := range []string{"overview", "diagnostics", "check", "routes", "failed_messages", "sessions", "route"} {
		if _, ok := resp.Sections[section]; !ok {
			t.Fatalf("missing section %q in %+v", section, resp.Sections)
		}
	}
	if resp.Sections["check"].Endpoint != "/internal/admin/check?timeout=1s" {
		t.Fatalf("check endpoint = %q, want timeout=1s", resp.Sections["check"].Endpoint)
	}
	if resp.Sections["failed_messages"].Endpoint != "/internal/messages?limit=5&status=failed" {
		t.Fatalf("failed messages endpoint = %q, want bounded failed list", resp.Sections["failed_messages"].Endpoint)
	}
}

func TestInternalHTTPAdminRoutesRedactsSensitiveRouteConfig(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		UpstreamRoutes: []UpstreamRouteConfig{
			{
				Name:     "http-route",
				MsgIDMin: 1001,
				MsgIDMax: 1999,
				HTTP: &HTTPUpstreamConfig{
					URL:     "http://user:password@backend:8080/gateway/upstream?token=secret#fragment",
					Token:   "upstream-token",
					Timeout: time.Second,
				},
			},
			{
				Name:        "nsq-route",
				MsgIDMin:    2000,
				MsgIDMax:    2999,
				MaxInFlight: 10,
				NSQ: &NSQUpstreamConfig{
					Addresses:     []string{"nsqd-a:4150", "nsqd-b:4150"},
					Topic:         "message_events",
					AuthSecret:    "nsq-secret",
					DialTimeout:   time.Second,
					ReadTimeout:   time.Minute,
					WriteTimeout:  time.Second,
					PublishMode:   "round_robin",
					RetryAttempts: 2,
				},
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/routes", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"user:password", "token=secret", "upstream-token", "nsq-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}

	var resp adminRoutesResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.Total != 2 || len(resp.Routes) != 2 {
		t.Fatalf("routes = %+v, want two routes", resp)
	}
	if resp.Routes[0].TargetType != "http" || resp.Routes[0].HTTP == nil || resp.Routes[0].HTTP.URL != "http://backend:8080/gateway/upstream" {
		t.Fatalf("http route = %+v, want sanitized backend URL", resp.Routes[0])
	}
	if resp.Routes[1].TargetType != "nsq" || resp.Routes[1].NSQ == nil || len(resp.Routes[1].NSQ.Addresses) != 2 || resp.Routes[1].NSQ.Topic != "message_events" {
		t.Fatalf("nsq route = %+v, want nsq addresses/topic", resp.Routes[1])
	}
}

func TestInternalHTTPAdminRequiresToken(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	for _, path := range []string{"/internal/admin/overview", "/internal/admin/routes", "/internal/admin/diagnostics", "/internal/admin/check", "/internal/admin/diagnose"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestInternalHTTPAdminSessionTokenMode(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	loginReq := httptest.NewRequest(http.MethodPost, adminSessionLoginPath, strings.NewReader(`{"token":"secret"}`))
	loginRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	cookie := firstCookie(loginRec.Result(), config.AdminConsole.Session.CookieName)
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie = %+v, want http-only lax cookie", cookie)
	}
	var loginResp adminSessionResponse
	if err := sonic.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("Unmarshal(login) error = %v", err)
	}
	if loginResp.Session == nil || loginResp.Session.CSRFToken == "" {
		t.Fatalf("login response = %+v, want csrf token", loginResp)
	}

	meReq := httptest.NewRequest(http.MethodGet, adminSessionMePath, nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want %d, body = %s", meRec.Code, http.StatusOK, meRec.Body.String())
	}
	var sessionResp adminSessionResponse
	if err := sonic.Unmarshal(meRec.Body.Bytes(), &sessionResp); err != nil {
		t.Fatalf("Unmarshal(me) error = %v", err)
	}
	if sessionResp.Session == nil || sessionResp.Session.Role != adminSessionRoleAdmin || sessionResp.Session.Principal != "internal-token" {
		t.Fatalf("me response = %+v, want admin internal-token session", sessionResp)
	}
	if sessionResp.Session.CSRFToken == "" || sessionResp.Session.CSRFToken != loginResp.Session.CSRFToken {
		t.Fatalf("me csrf token = %q, want login csrf token %q", sessionResp.Session.CSRFToken, loginResp.Session.CSRFToken)
	}

	overviewReq := httptest.NewRequest(http.MethodGet, "/internal/admin/overview", nil)
	overviewReq.AddCookie(cookie)
	overviewRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(overviewRec, overviewReq)
	if overviewRec.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want %d, body = %s", overviewRec.Code, http.StatusOK, overviewRec.Body.String())
	}

	messagesReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed", nil)
	messagesReq.AddCookie(cookie)
	messagesRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want %d, body = %s", messagesRec.Code, http.StatusOK, messagesRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, adminSessionLogoutPath, nil)
	addAdminSessionMutationHeaders(logoutReq, cookie, loginResp.Session.CSRFToken)
	logoutRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d, body = %s", logoutRec.Code, http.StatusOK, logoutRec.Body.String())
	}
	clearCookie := firstCookie(logoutRec.Result(), config.AdminConsole.Session.CookieName)
	if clearCookie == nil || clearCookie.MaxAge >= 0 {
		t.Fatalf("logout clear cookie = %+v, want MaxAge < 0", clearCookie)
	}

	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/internal/admin/overview", nil)
	afterLogoutReq.AddCookie(cookie)
	afterLogoutRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout status = %d, want %d", afterLogoutRec.Code, http.StatusUnauthorized)
	}
}

func TestInternalHTTPAdminSessionMutationRequiresCSRF(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, _ := loginAdminSessionCredentials(t, server, config, "secret")

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var denied adminSessionCSRFResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal(denied) error = %v", err)
	}
	if denied.Code != "csrf_failed" || denied.GatewayNode != "gateway-a" {
		t.Fatalf("denied response = %+v, want csrf_failed gateway-a", denied)
	}

	auditReq := httptest.NewRequest(http.MethodGet, adminAuditPath+"?limit=1", nil)
	auditReq.AddCookie(cookie)
	auditRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want %d, body = %s", auditRec.Code, http.StatusOK, auditRec.Body.String())
	}
	var auditResp adminAuditResponse
	if err := sonic.Unmarshal(auditRec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("Unmarshal(audit) error = %v", err)
	}
	if auditResp.Total < 1 || len(auditResp.Events) != 1 {
		t.Fatalf("audit response = %+v, want latest mutation rejection", auditResp)
	}
	event := auditResp.Events[0]
	if event.Action != "admin_session_mutation_rejected" || event.Result != "csrf_failed" || event.HTTPStatus != http.StatusForbidden {
		t.Fatalf("audit event = %+v, want csrf rejection", event)
	}
}

func TestInternalHTTPAdminSessionMutationRejectsCrossOrigin(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var denied adminSessionCSRFResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal(denied) error = %v", err)
	}
	if denied.Code != "same_origin_failed" {
		t.Fatalf("denied response = %+v, want same_origin_failed", denied)
	}
}

func TestInternalHTTPAdminSessionMutationRequiresJSONContentType(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	req.AddCookie(cookie)
	req.Header.Set(adminCSRFHeader, csrfToken)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
	var denied adminSessionCSRFResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal(denied) error = %v", err)
	}
	if denied.Code != "unsupported_media_type" {
		t.Fatalf("denied response = %+v, want unsupported_media_type", denied)
	}
}

func TestInternalHTTPAdminSessionRejectsInvalidToken(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	loginReq := httptest.NewRequest(http.MethodPost, adminSessionLoginPath, strings.NewReader(`{"token":"wrong"}`))
	loginRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusUnauthorized)
	}
	if cookie := firstCookie(loginRec.Result(), config.AdminConsole.Session.CookieName); cookie != nil {
		t.Fatalf("login set cookie = %+v, want nil", cookie)
	}
}

func TestInternalHTTPAdminSessionReadonlyDeniesMessageRepair(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleReadonly,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	loginReq := httptest.NewRequest(http.MethodPost, adminSessionLoginPath, strings.NewReader(`{"token":"secret"}`))
	loginRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	cookie := firstCookie(loginRec.Result(), config.AdminConsole.Session.CookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("login cookie = %+v, want session cookie", cookie)
	}
	var loginResp adminSessionResponse
	if err := sonic.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("Unmarshal(login) error = %v", err)
	}
	if loginResp.Session == nil || loginResp.Session.Role != adminSessionRoleReadonly || len(loginResp.Session.Permissions) != 1 || loginResp.Session.Permissions[0] != adminPermissionRead {
		t.Fatalf("login response = %+v, want readonly read-only permissions", loginResp)
	}

	messagesReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed", nil)
	messagesReq.AddCookie(cookie)
	messagesRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want %d, body = %s", messagesRec.Code, http.StatusOK, messagesRec.Body.String())
	}

	requeueReq := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	addAdminSessionMutationHeaders(requeueReq, cookie, loginResp.Session.CSRFToken)
	requeueRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(requeueRec, requeueReq)
	if requeueRec.Code != http.StatusForbidden {
		t.Fatalf("readonly requeue status = %d, want %d, body = %s", requeueRec.Code, http.StatusForbidden, requeueRec.Body.String())
	}
	var denied adminPermissionDeniedResponse
	if err := sonic.Unmarshal(requeueRec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal(denied) error = %v", err)
	}
	if denied.Code != "permission_denied" || denied.Role != adminSessionRoleReadonly || denied.Permission != adminPermissionMessageRepair {
		t.Fatalf("denied response = %+v, want readonly message repair denial", denied)
	}

	directReq := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	directReq.Header.Set(downlink.InternalTokenHeader, "secret")
	directRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(directRec, directReq)
	if directRec.Code == http.StatusForbidden {
		t.Fatalf("direct internal token status = %d, want non-permission response", directRec.Code)
	}
}

func TestInternalHTTPAdminAuditListsLoginAndPermissionDenied(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleReadonly,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)

	unauthReq := httptest.NewRequest(http.MethodGet, adminAuditPath, nil)
	unauthRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth audit status = %d, want %d, body = %s", unauthRec.Code, http.StatusUnauthorized, unauthRec.Body.String())
	}

	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")

	requeueReq := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"missing"}`))
	addAdminSessionMutationHeaders(requeueReq, cookie, csrfToken)
	requeueRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(requeueRec, requeueReq)
	if requeueRec.Code != http.StatusForbidden {
		t.Fatalf("requeue status = %d, want %d, body = %s", requeueRec.Code, http.StatusForbidden, requeueRec.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, adminAuditPath+"?limit=10", nil)
	auditReq.AddCookie(cookie)
	auditRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want %d, body = %s", auditRec.Code, http.StatusOK, auditRec.Body.String())
	}
	var auditResp adminAuditResponse
	if err := sonic.Unmarshal(auditRec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("Unmarshal(audit) error = %v", err)
	}
	if auditResp.Code != "ok" || auditResp.GatewayNode != "gateway-a" || auditResp.Total < 2 || len(auditResp.Events) < 2 {
		t.Fatalf("audit response = %+v, want at least login and permission denial", auditResp)
	}
	denied := auditResp.Events[0]
	if denied.Action != "admin_permission_denied" || denied.Result != "permission_denied" || denied.Permission != adminPermissionMessageRepair {
		t.Fatalf("latest audit event = %+v, want message repair permission denial", denied)
	}
	if denied.Principal != "internal-token" || denied.Role != adminSessionRoleReadonly || denied.AdminSessionID == "" {
		t.Fatalf("denied identity = %+v, want readonly admin session identity", denied)
	}

	foundLogin := false
	for _, event := range auditResp.Events {
		if event.Action == "admin_session_login" && event.Result == "success" {
			foundLogin = true
			if event.Principal != "internal-token" || event.Role != adminSessionRoleReadonly {
				t.Fatalf("login event = %+v, want readonly internal-token", event)
			}
		}
	}
	if !foundLogin {
		t.Fatalf("audit events = %+v, want successful login event", auditResp.Events)
	}
}

func TestInternalHTTPAdminAuditFiltersRetryScan(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleOperator,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")

	scanReq := httptest.NewRequest(http.MethodPost, "/internal/messages/retry/scan", strings.NewReader(`{"limit":7}`))
	addAdminSessionMutationHeaders(scanReq, cookie, csrfToken)
	scanRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(scanRec, scanReq)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("retry scan status = %d, want %d, body = %s", scanRec.Code, http.StatusOK, scanRec.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, adminAuditPath+"?action=admin_retry_scan&result=success&limit=1", nil)
	auditReq.AddCookie(cookie)
	auditRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want %d, body = %s", auditRec.Code, http.StatusOK, auditRec.Body.String())
	}
	var auditResp adminAuditResponse
	if err := sonic.Unmarshal(auditRec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("Unmarshal(audit) error = %v", err)
	}
	if auditResp.Total != 1 || len(auditResp.Events) != 1 {
		t.Fatalf("audit response = %+v, want one retry scan event", auditResp)
	}
	event := auditResp.Events[0]
	if event.Action != "admin_retry_scan" || event.Result != "success" || event.Details["limit"] != "7" {
		t.Fatalf("retry scan event = %+v, want success limit 7", event)
	}
	if event.Principal != "internal-token" || event.Role != adminSessionRoleOperator {
		t.Fatalf("retry scan identity = %+v, want operator internal-token", event)
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

	health.BeginDrainAt(time.UnixMilli(1760000000000))
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

func TestInternalHTTPHMACStoresAuthIdentity(t *testing.T) {
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
	})
	var gotIdentity httpauth.Identity
	var gotIdentityOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity, gotIdentityOK = httpauth.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := newInternalHMACHandler(next, config, zap.NewNop())
	if err != nil {
		t.Fatalf("newInternalHMACHandler() error = %v", err)
	}
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "backend-1", Secret: internalHMACTestSecret})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	body := []byte(`{"message_id":"message-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(string(body)))
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if !gotIdentityOK || gotIdentity.Mode != httpauth.ModeHMAC || gotIdentity.KeyID != "backend-1" {
		t.Fatalf("identity = %+v ok=%v, want hmac backend-1", gotIdentity, gotIdentityOK)
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

func TestInternalHTTPAdminSessionBypassesHMACForConsoleAPIs(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalHTTPAuth: InternalHTTPAuthConfig{
			Mode: InternalHTTPAuthModeHMAC,
			HMAC: InternalHTTPHMACConfig{
				Keys: map[string][]byte{"backend-1": internalHMACTestSecret},
			},
		},
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
			},
		},
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "backend-1", Secret: internalHMACTestSecret})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	body := []byte(`{}`)
	loginReq := httptest.NewRequest(http.MethodPost, adminSessionLoginPath, strings.NewReader(string(body)))
	if err := signer.Sign(loginReq, body); err != nil {
		t.Fatalf("Sign(login) error = %v", err)
	}
	loginRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	cookie := firstCookie(loginRec.Result(), config.AdminConsole.Session.CookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("login cookie = %+v, want session cookie", cookie)
	}

	messagesReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed", nil)
	messagesReq.AddCookie(cookie)
	messagesRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want %d, body = %s", messagesRec.Code, http.StatusOK, messagesRec.Body.String())
	}

	unsignedReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed", nil)
	unsignedRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(unsignedRec, unsignedReq)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want %d", unsignedRec.Code, http.StatusUnauthorized)
	}
}

func firstCookie(resp *http.Response, name string) *http.Cookie {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestInternalHTTPPeerHMACAcceptsSignatureAndRejectsReplay(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		Cluster: ClusterConfig{
			Enabled: true,
			Peer: ClusterPeerConfig{
				Auth: ClusterPeerAuthConfig{
					Mode: ClusterPeerAuthModeHMAC,
					HMAC: ClusterPeerHMACConfig{
						KeyID: "gateway-2026-01",
						Keys:  map[string][]byte{"gateway-2026-01": peerHMACTestSecret},
					},
				},
			},
		},
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "gateway-2026-01", Secret: peerHMACTestSecret})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(string(body)))
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("signed status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	replay := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(string(body)))
	replay.Header = req.Header.Clone()
	replayRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(replayRec, replay)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d, body = %s", replayRec.Code, http.StatusUnauthorized, replayRec.Body.String())
	}

	unsigned := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(string(body)))
	unsignedRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want %d", unsignedRec.Code, http.StatusUnauthorized)
	}

	tamperBase := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(string(body)))
	if err := signer.Sign(tamperBase, body); err != nil {
		t.Fatalf("Sign(tamper) error = %v", err)
	}
	tampered := httptest.NewRequest(http.MethodPost, downlink.PeerPushPath, strings.NewReader(`{"msg_id":2001}`))
	tampered.Header = tamperBase.Header.Clone()
	tamperedRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(tamperedRec, tampered)
	if tamperedRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d, want %d", tamperedRec.Code, http.StatusUnauthorized)
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

func TestInternalHTTPDebugSessionsFiltersBySessionAndDevice(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-1", ConnID: 1, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind(session-1) error = %v", err)
	}
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-2", ConnID: 2, ClientID: "client-1", DeviceID: "device-2"}); err != nil {
		t.Fatalf("Bind(session-2) error = %v", err)
	}

	service := downlink.NewService(sessions, testConnectionFinder{})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})
	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)

	tests := []struct {
		name       string
		path       string
		wantTotal  int
		wantFirst  string
		wantClient string
		wantDevice string
		wantID     string
	}{
		{
			name:       "session id",
			path:       "/internal/debug/sessions?session_id=session-2",
			wantTotal:  1,
			wantFirst:  "session-2",
			wantID:     "session-2",
			wantClient: "",
			wantDevice: "",
		},
		{
			name:       "client device",
			path:       "/internal/debug/sessions?client_id=client-1&device_id=device-1",
			wantTotal:  1,
			wantFirst:  "session-1",
			wantClient: "client-1",
			wantDevice: "device-1",
		},
		{
			name:       "session id mismatch",
			path:       "/internal/debug/sessions?session_id=session-2&device_id=device-1",
			wantTotal:  0,
			wantID:     "session-2",
			wantDevice: "device-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
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
			if resp.Total != tt.wantTotal || len(resp.Sessions) != tt.wantTotal {
				t.Fatalf("response = %+v, want total=%d", resp, tt.wantTotal)
			}
			if resp.SessionID != tt.wantID || resp.ClientID != tt.wantClient || resp.DeviceID != tt.wantDevice {
				t.Fatalf("filters = session:%q client:%q device:%q, want session:%q client:%q device:%q", resp.SessionID, resp.ClientID, resp.DeviceID, tt.wantID, tt.wantClient, tt.wantDevice)
			}
			if tt.wantFirst != "" && resp.Sessions[0].SessionID != tt.wantFirst {
				t.Fatalf("first session = %+v, want %s", resp.Sessions[0], tt.wantFirst)
			}
		})
	}
}

func TestInternalHTTPDebugClusterRoutesListsRegistryRoutes(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-local",
		ConnID:      9,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		GatewayNode: "gateway-a",
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	registry := cluster.NewMemoryRegistry(cluster.MemoryRegistryConfig{TTL: time.Minute, Now: func() time.Time { return now }})
	entries := []cluster.RouteEntry{
		{
			ClientID:     "client-1",
			DeviceID:     "device-1",
			SessionID:    "session-local",
			GatewayNode:  "gateway-a",
			InternalAddr: "http://gateway-a:18182",
			TokenID:      "token-1",
		},
		{
			ClientID:     "client-1",
			DeviceID:     "device-2",
			SessionID:    "session-remote",
			GatewayNode:  "gateway-b",
			InternalAddr: "http://gateway-b:18183",
			TokenID:      "token-2",
		},
		{
			ClientID:     "client-2",
			DeviceID:     "device-1",
			SessionID:    "session-other",
			GatewayNode:  "gateway-b",
			InternalAddr: "http://gateway-b:18183",
			TokenID:      "token-3",
		},
	}
	for _, entry := range entries {
		if err := registry.Bind(context.Background(), entry); err != nil {
			t.Fatalf("registry Bind(%s) error = %v", entry.SessionID, err)
		}
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

	req := httptest.NewRequest(http.MethodGet, "/internal/debug/cluster/routes?client_id=client-1&limit=1", nil)
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp debugClusterRoutesResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || !resp.ClusterEnabled || resp.ClientID != "client-1" || resp.Total != 2 || resp.Limit != 1 || len(resp.Routes) != 1 {
		t.Fatalf("response = %+v, want filtered total=2 limit=1 one route", resp)
	}
	if resp.UniqueClients != 1 {
		t.Fatalf("UniqueClients = %d, want 1", resp.UniqueClients)
	}
	if got := resp.Routes[0]; got.SessionID != "session-local" || !got.LocalRoute || !got.LocalSession {
		t.Fatalf("first route = %+v, want local session route", got)
	}
}

func TestInternalHTTPDebugClusterRoutesRequiresToken(t *testing.T) {
	sessions := session.NewManager()
	service := downlink.NewService(sessions, testConnectionFinder{})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/debug/cluster/routes", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestInternalHTTPDebugSessionDisconnectOperatorStopsLocalConnection(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-1",
		ConnID:      7,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		GatewayNode: "gateway-a",
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	conn := &testStoppableConnection{}
	finder := &testStaticConnectionFinder{conn: conn}
	service := downlink.NewService(sessions, finder)
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleOperator,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/debug/session/disconnect", strings.NewReader(`{"session_id":"session-1","client_id":"client-1","device_id":"device-1"}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp debugSessionDisconnectResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || !resp.Disconnected || !resp.LocalSessionFound || resp.ConnID != 7 {
		t.Fatalf("response = %+v, want disconnected local conn 7", resp)
	}
	if !conn.stopped {
		t.Fatal("connection stopped = false, want true")
	}
	if finder.gotConnID != 7 {
		t.Fatalf("connection finder connID = %d, want 7", finder.gotConnID)
	}
	if _, ok := sessions.GetBySessionID("session-1"); ok {
		t.Fatal("session still exists after disconnect")
	}
}

func TestInternalHTTPDebugSessionDisconnectReadonlyDenied(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{SessionID: "session-1", ConnID: 7, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	conn := &testStoppableConnection{}
	service := downlink.NewService(sessions, &testStaticConnectionFinder{conn: conn})
	config := normalizeConfig(Config{
		Sessions:         sessions,
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleReadonly,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/debug/session/disconnect", strings.NewReader(`{"session_id":"session-1"}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var denied adminPermissionDeniedResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if denied.Code != "permission_denied" || denied.Permission != adminPermissionSessionDisconnect {
		t.Fatalf("denied = %+v, want session disconnect permission denial", denied)
	}
	if conn.stopped {
		t.Fatal("connection stopped = true, want false")
	}
	if _, ok := sessions.GetBySessionID("session-1"); !ok {
		t.Fatal("session was removed despite readonly denial")
	}
}

func TestInternalHTTPDebugPushOperatorSendsDownlink(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-1",
		ConnID:      7,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		GatewayNode: "gateway-a",
	}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	conn := &testStoppableConnection{}
	finder := &testStaticConnectionFinder{conn: conn}
	service := downlink.NewService(sessions, finder)
	config := normalizeConfig(Config{
		Sessions:         sessions,
		GatewayNode:      "gateway-a",
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleOperator,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/debug/push", strings.NewReader(`{
		"client_id":"client-1",
		"device_id":"device-1",
		"msg_id":2001,
		"message_id":"message-1",
		"trace_id":"trace-1",
		"ack_required":true,
		"body":"aGVsbG8="
	}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp downlink.PushResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.DeliveryState != sdkbackend.DeliveryStateSent || resp.SessionID != "session-1" || resp.ConnID != 7 {
		t.Fatalf("response = %+v, want sent session-1 conn 7", resp)
	}
	if resp.DeliveryPath != downlink.DeliveryPathLocal || resp.TargetGatewayNode != "gateway-a" {
		t.Fatalf("routing response = %+v, want local gateway-a", resp)
	}
	if finder.gotConnID != 7 {
		t.Fatalf("connection finder connID = %d, want 7", finder.gotConnID)
	}
}

func TestInternalHTTPDebugPushReadonlyDenied(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{})
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleReadonly,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/debug/push", strings.NewReader(`{"client_id":"client-1","device_id":"device-1","msg_id":2001}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var denied adminPermissionDeniedResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if denied.Code != "permission_denied" || denied.Permission != adminPermissionDownlinkTestPush {
		t.Fatalf("denied = %+v, want downlink test push permission denial", denied)
	}
}

func TestInternalHTTPRetryScanReadonlyDenied(t *testing.T) {
	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(downlink.NewMemoryStore()))
	config := normalizeConfig(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "secret",
		AdminConsole: AdminConsoleConfig{
			Session: AdminConsoleSessionConfig{
				Enabled:        true,
				TTL:            time.Hour,
				CookieName:     "zcourier_admin_session",
				CookieSameSite: "lax",
				Role:           adminSessionRoleReadonly,
			},
		},
	})

	server := mustInternalHTTPServer(t, config, service, &gatewayHealth{}, nil)
	cookie, csrfToken := loginAdminSessionCredentials(t, server, config, "secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/messages/retry/scan", strings.NewReader(`{}`))
	addAdminSessionMutationHeaders(req, cookie, csrfToken)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var denied adminPermissionDeniedResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if denied.Code != "permission_denied" || denied.Permission != adminPermissionRetryScan {
		t.Fatalf("denied = %+v, want retry scan permission denial", denied)
	}
}

type testStoppableConnection struct {
	stopped bool
}

func (c *testStoppableConnection) SendMsg(uint32, []byte) error {
	return nil
}

func (c *testStoppableConnection) Stop() {
	c.stopped = true
}

type testStaticConnectionFinder struct {
	conn      downlink.Connection
	err       error
	gotConnID uint64
}

func (f *testStaticConnectionFinder) Get(connID uint64) (downlink.Connection, error) {
	f.gotConnID = connID
	if f.err != nil {
		return nil, f.err
	}
	return f.conn, nil
}

func loginAdminSessionCookie(t *testing.T, server *http.Server, config Config, token string) *http.Cookie {
	t.Helper()

	cookie, _ := loginAdminSessionCredentials(t, server, config, token)
	return cookie
}

func loginAdminSessionCredentials(t *testing.T, server *http.Server, config Config, token string) (*http.Cookie, string) {
	t.Helper()

	loginReq := httptest.NewRequest(http.MethodPost, adminSessionLoginPath, strings.NewReader(`{"token":"`+token+`"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	cookie := firstCookie(loginRec.Result(), config.AdminConsole.Session.CookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("login cookie = %+v, want session cookie", cookie)
	}
	var resp adminSessionResponse
	if err := sonic.Unmarshal(loginRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(login) error = %v", err)
	}
	if resp.Session == nil || resp.Session.CSRFToken == "" {
		t.Fatalf("login response = %+v, want csrf token", resp)
	}
	return cookie, resp.Session.CSRFToken
}

func addAdminSessionMutationHeaders(req *http.Request, cookie *http.Cookie, csrfToken string) {
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(adminCSRFHeader, csrfToken)
}

func mustInternalHTTPServer(t *testing.T, config Config, service *downlink.Service, health *gatewayHealth, registry cluster.OnlineRegistry) *http.Server {
	t.Helper()
	runtime := newGatewayRuntime()
	runtime.MarkStarted(time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC))
	server, err := newInternalHTTPServer(config, zap.NewNop(), service, health, registry, runtime)
	if err != nil {
		t.Fatalf("newInternalHTTPServer() error = %v", err)
	}
	if server == nil {
		t.Fatal("newInternalHTTPServer() = nil")
	}
	return server
}

func findAdminCheck(checks []adminCheckResult, name string) adminCheckResult {
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	return adminCheckResult{}
}

func findAdminDependency(dependencies []adminDependency, name string) adminDependency {
	for _, dependency := range dependencies {
		if dependency.Name == name {
			return dependency
		}
	}
	return adminDependency{}
}

func hasAdminWarning(warnings []adminDiagnosticWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

var internalHMACTestSecret = []byte("0123456789abcdef0123456789abcdef")
var peerHMACTestSecret = []byte("cluster-peer-secret-0123456789abcdef")
