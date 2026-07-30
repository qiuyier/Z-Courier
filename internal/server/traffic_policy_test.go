package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"go.uber.org/zap"
)

func TestNewTrafficPolicyHandlerDisabled(t *testing.T) {
	handler, closer, err := newTrafficPolicyHandler(pipeline.TrafficPoliciesConfig{})
	if err != nil {
		t.Fatalf("newTrafficPolicyHandler() error = %v", err)
	}
	if handler != nil || closer != nil {
		t.Fatalf("newTrafficPolicyHandler() = %T/%T, want nil/nil", handler, closer)
	}
}

func TestNewTrafficPolicyHandlerCreatesLocalWithoutCloser(t *testing.T) {
	handler, closer, err := newTrafficPolicyHandler(testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeLocal))
	if err != nil {
		t.Fatalf("newTrafficPolicyHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("newTrafficPolicyHandler() handler = nil")
	}
	if closer != nil {
		t.Fatalf("newTrafficPolicyHandler() closer = %T, want nil", closer)
	}
}

func TestNewTrafficPolicyHandlerCreatesAndPingsRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	config := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeRedis)
	config.Redis = testServerRedisQuotaConfig(redisServer.Addr())

	handler, closer, err := newTrafficPolicyHandler(config)
	if err != nil {
		t.Fatalf("newTrafficPolicyHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("newTrafficPolicyHandler() handler = nil")
	}
	store, ok := closer.(*pipeline.RedisQuotaStore)
	if !ok {
		t.Fatalf("newTrafficPolicyHandler() closer = %T, want *pipeline.RedisQuotaStore", closer)
	}
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Ping(context.Background()); err == nil {
		t.Fatal("Ping() after Close error = nil, want closed error")
	}
}

func TestNewTrafficPolicyHandlerRejectsUnavailableRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	addr := redisServer.Addr()
	redisServer.Close()

	config := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeRedis)
	config.Redis = testServerRedisQuotaConfig(addr)
	handler, closer, err := newTrafficPolicyHandler(config)
	if err == nil {
		t.Fatal("newTrafficPolicyHandler() error = nil, want ping error")
	}
	if !strings.Contains(err.Error(), "redis quota ping") {
		t.Fatalf("newTrafficPolicyHandler() error = %q, want ping context", err)
	}
	if handler != nil || closer != nil {
		t.Fatalf("newTrafficPolicyHandler() = %T/%T, want nil/nil", handler, closer)
	}
}

func TestNewTrafficPolicyHandlerRejectsUnsupportedMode(t *testing.T) {
	config := testServerTrafficPolicyConfig("unsupported")
	if _, _, err := newTrafficPolicyHandler(config); err == nil {
		t.Fatal("newTrafficPolicyHandler() error = nil, want unsupported mode")
	}
}

func TestGatewayShutdownTrafficPolicyRecordsCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	closer := &stubTrafficPolicyCloser{err: wantErr}
	gateway := &Gateway{
		logger:              zap.NewNop(),
		trafficPolicyCloser: closer,
	}

	gateway.shutdownTrafficPolicy()

	if closer.calls != 1 {
		t.Fatalf("Close() calls = %d, want 1", closer.calls)
	}
	if !errors.Is(gateway.shutdownErr, wantErr) {
		t.Fatalf("shutdownErr = %v, want %v", gateway.shutdownErr, wantErr)
	}
}

func TestGatewayNewAndShutdownRedisTrafficPolicy(t *testing.T) {
	redisServer := miniredis.RunT(t)
	trafficPolicies := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeRedis)
	trafficPolicies.Redis = testServerRedisQuotaConfig(redisServer.Addr())

	gateway, err := New(Config{
		DisableInternalHTTP: true,
		Pipeline: pipeline.Config{
			TrafficPolicies: trafficPolicies,
		},
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store, ok := gateway.trafficPolicyCloser.(*pipeline.RedisQuotaStore)
	if !ok {
		t.Fatalf("trafficPolicyCloser = %T, want *pipeline.RedisQuotaStore", gateway.trafficPolicyCloser)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gateway.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := store.Ping(context.Background()); err == nil {
		t.Fatal("Ping() after Gateway.Shutdown error = nil, want closed error")
	}
}

func TestGatewayNewAttachesTrafficPolicyRuntimeToDiagnostics(t *testing.T) {
	trafficPolicies := testServerTrafficPolicyConfig(pipeline.TrafficPolicyModeLocal)
	trafficPolicies.MaxKeys = 10
	trafficPolicies.IdleTTL = time.Minute

	gateway, err := New(Config{
		InternalHTTPAddr: "127.0.0.1:18080",
		InternalToken:    "internal-secret",
		Pipeline: pipeline.Config{
			TrafficPolicies: trafficPolicies,
		},
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if gateway.internalHTTP == nil {
		t.Fatal("Gateway internal HTTP server = nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/admin/diagnostics", nil)
	req.Header.Set(downlink.InternalTokenHeader, "internal-secret")
	rec := httptest.NewRecorder()
	gateway.internalHTTP.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response adminDiagnosticsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !response.TrafficPolicy.Enabled ||
		response.TrafficPolicy.Mode != pipeline.TrafficPolicyModeLocal ||
		response.TrafficPolicy.StoreStatus != "configured" ||
		response.TrafficPolicy.Local == nil ||
		response.TrafficPolicy.Local.MaxKeys != 10 {
		t.Fatalf("traffic policy diagnostics = %+v", response.TrafficPolicy)
	}
}

func testServerTrafficPolicyConfig(mode string) pipeline.TrafficPoliciesConfig {
	return pipeline.TrafficPoliciesConfig{
		Enabled: true,
		Mode:    mode,
		Policies: []pipeline.TrafficPolicy{
			{
				Name:     "shared",
				Priority: 100,
				Key:      pipeline.TrafficPolicyKeyClientID,
				TokenBucket: pipeline.TokenBucketConfig{
					Capacity:       1,
					RefillTokens:   1,
					RefillInterval: time.Second,
				},
			},
		},
	}
}

func testServerRedisQuotaConfig(addr string) pipeline.RedisQuotaStoreConfig {
	return pipeline.RedisQuotaStoreConfig{
		Addr:             addr,
		KeyPrefix:        "zcourier:test:server-quota",
		IdleTTL:          time.Minute,
		DialTimeout:      50 * time.Millisecond,
		ReadTimeout:      50 * time.Millisecond,
		WriteTimeout:     50 * time.Millisecond,
		OperationTimeout: 100 * time.Millisecond,
		FailureMode:      pipeline.TrafficPolicyFailureModeFailClosed,
	}
}

type stubTrafficPolicyCloser struct {
	calls int
	err   error
}

func (s *stubTrafficPolicyCloser) Close() error {
	s.calls++
	return s.err
}
