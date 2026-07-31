package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"go.uber.org/zap"
)

func TestAdminRouteStatusAndDryRun(t *testing.T) {
	config, control := testAdminRouteControlConfig(t)

	statusReq := httptest.NewRequest(http.MethodGet, adminRouteStatusPath, nil)
	statusReq.Header.Set(downlink.InternalTokenHeader, "secret")
	statusRec := httptest.NewRecorder()
	newAdminRouteStatusHandler(config).ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", statusRec.Code, statusRec.Body.String())
	}
	var status adminRouteReloadResponse
	if err := sonic.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal status error = %v", err)
	}
	if !status.ReloadEnabled || status.Active == nil || status.Active.Number != 1 {
		t.Fatalf("status = %+v, want enabled generation 1", status)
	}

	body := []byte(`{"dry_run":true,"expected_generation":1}`)
	reloadReq := httptest.NewRequest(http.MethodPost, adminRouteReloadPath, bytes.NewReader(body))
	reloadReq.Header.Set(downlink.InternalTokenHeader, "secret")
	reloadRec := httptest.NewRecorder()
	newAdminRouteReloadHandler(config).ServeHTTP(reloadRec, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, body = %s", reloadRec.Code, reloadRec.Body.String())
	}
	var response adminRouteReloadResponse
	if err := sonic.Unmarshal(reloadRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal reload error = %v", err)
	}
	if response.Result != "validated" || response.Candidate == nil || response.Active == nil || response.Active.Number != 1 {
		t.Fatalf("dry-run response = %+v", response)
	}
	if response.Candidate.ActivatedAt != nil {
		t.Fatalf("candidate activated_at = %v, want omitted before activation", response.Candidate.ActivatedAt)
	}
	if control.Status().Active == nil || control.Status().Active.Number != 1 {
		t.Fatalf("control status after dry-run = %+v", control.Status())
	}
}

func TestAdminRouteReloadRejectsUnknownFieldsWithoutLoading(t *testing.T) {
	config, _ := testAdminRouteControlConfig(t)
	var loads atomic.Int64
	config.routeControl.loader = func(context.Context) (UpstreamRouteSnapshot, error) {
		loads.Add(1)
		return UpstreamRouteSnapshot{}, nil
	}

	req := httptest.NewRequest(http.MethodPost, adminRouteReloadPath, bytes.NewBufferString(`{"dry_run":true,"routes":[]}`))
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	newAdminRouteReloadHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if loads.Load() != 0 {
		t.Fatalf("loader calls = %d, want 0", loads.Load())
	}
}

func TestAdminRouteReloadReadonlyPermissionDeniedBeforeLoading(t *testing.T) {
	config, _ := testAdminRouteControlConfig(t)
	var loads atomic.Int64
	config.routeControl.loader = func(context.Context) (UpstreamRouteSnapshot, error) {
		loads.Add(1)
		return UpstreamRouteSnapshot{}, nil
	}
	handler := withAdminPermission(
		newAdminRouteReloadHandler(config),
		adminPermissionRouteReload,
		zap.NewNop(),
		config.AdminAudit,
		config.GatewayNode,
	)

	req := httptest.NewRequest(http.MethodPost, adminRouteReloadPath, bytes.NewBufferString(`{"dry_run":true}`))
	req.Header.Set(downlink.InternalTokenHeader, "secret")
	req = req.WithContext(httpauth.WithIdentity(req.Context(), httpauth.Identity{
		Mode:      httpauth.ModeAdminSession,
		Principal: "readonly-user",
		Role:      adminSessionRoleReadonly,
		SessionID: "session-1",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if loads.Load() != 0 {
		t.Fatalf("loader calls = %d, want 0", loads.Load())
	}
}

func testAdminRouteControlConfig(t *testing.T) (Config, *routeControl) {
	t.Helper()
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{})
	config := Config{
		GatewayNode:                "gateway-a",
		InternalToken:              "secret",
		InternalMaxRequestBodySize: maxRouteReloadBody,
		AdminAudit:                 audit,
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: testRouteSnapshotLoader("candidate", "http://candidate.internal/upstream"),
		},
	}
	control := newRouteControl(config, manager, audit, zap.NewNop())
	config.routeControl = control
	return config, control
}
