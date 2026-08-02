package server

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

const (
	adminRouteStatusPath = "/internal/admin/routes/status"
	adminRouteReloadPath = "/internal/admin/routes/reload"
	maxRouteReloadBody   = int64(8 << 10)
)

type adminRouteReloadRequest struct {
	DryRun             *bool  `json:"dry_run"`
	ExpectedGeneration uint64 `json:"expected_generation,omitempty"`
}

type adminRouteGeneration struct {
	Number         uint64     `json:"number"`
	Fingerprint    string     `json:"fingerprint"`
	ActivatedAt    *time.Time `json:"activated_at,omitempty"`
	RetiringAt     *time.Time `json:"retiring_at,omitempty"`
	RetiringForMS  int64      `json:"retiring_for_ms,omitempty"`
	DrainTimeoutMS int64      `json:"drain_timeout_ms,omitempty"`
	Slow           bool       `json:"slow,omitempty"`
	RouteCount     int        `json:"route_count"`
	InFlight       int64      `json:"in_flight"`
	State          string     `json:"state"`
}

type adminRouteReloadAttempt struct {
	Operation            string    `json:"operation"`
	Trigger              string    `json:"trigger"`
	Stage                string    `json:"stage"`
	Result               string    `json:"result"`
	Reason               string    `json:"reason"`
	StartedAt            time.Time `json:"started_at"`
	CompletedAt          time.Time `json:"completed_at"`
	DurationMS           int64     `json:"duration_ms"`
	ExpectedGeneration   uint64    `json:"expected_generation,omitempty"`
	OldGeneration        uint64    `json:"old_generation,omitempty"`
	CandidateGeneration  uint64    `json:"candidate_generation,omitempty"`
	CandidateFingerprint string    `json:"candidate_fingerprint,omitempty"`
	CandidateRouteCount  int       `json:"candidate_route_count,omitempty"`
	ActiveGeneration     uint64    `json:"active_generation,omitempty"`
	Changed              bool      `json:"changed,omitempty"`
	WarningCount         int       `json:"warning_count,omitempty"`
}

type adminRouteReloadResponse struct {
	Code               string                    `json:"code"`
	Result             string                    `json:"result,omitempty"`
	Reason             string                    `json:"reason,omitempty"`
	Stage              string                    `json:"stage,omitempty"`
	GatewayNode        string                    `json:"gateway_node"`
	ReloadEnabled      bool                      `json:"reload_enabled"`
	Trigger            string                    `json:"trigger,omitempty"`
	DryRun             bool                      `json:"dry_run,omitempty"`
	Changed            bool                      `json:"changed,omitempty"`
	WarningCount       int                       `json:"warning_count,omitempty"`
	DurationMS         int64                     `json:"duration_ms,omitempty"`
	DrainTimeoutMS     int64                     `json:"drain_timeout_ms,omitempty"`
	OperationsInFlight int                       `json:"operations_in_flight"`
	LastStartedAt      *time.Time                `json:"last_started_at,omitempty"`
	LastSuccessAt      *time.Time                `json:"last_success_at,omitempty"`
	LastFailureAt      *time.Time                `json:"last_failure_at,omitempty"`
	LastAttempt        *adminRouteReloadAttempt  `json:"last_attempt,omitempty"`
	RecentAttempts     []adminRouteReloadAttempt `json:"recent_attempts,omitempty"`
	Active             *adminRouteGeneration     `json:"active,omitempty"`
	Candidate          *adminRouteGeneration     `json:"candidate,omitempty"`
	Retiring           *adminRouteGeneration     `json:"retiring,omitempty"`
}

type adminRouteStatusHandler struct {
	gatewayNode   string
	internalToken string
	control       *routeControl
}

type adminRouteReloadHandler struct {
	gatewayNode        string
	internalToken      string
	maxRequestBodySize int64
	control            *routeControl
}

func newAdminRouteStatusHandler(config Config) http.Handler {
	return &adminRouteStatusHandler{
		gatewayNode:   config.GatewayNode,
		internalToken: config.InternalToken,
		control:       config.routeControl,
	}
}

func newAdminRouteReloadHandler(config Config) http.Handler {
	maxBodySize := config.InternalMaxRequestBodySize
	if maxBodySize <= 0 || maxBodySize > maxRouteReloadBody {
		maxBodySize = maxRouteReloadBody
	}
	return &adminRouteReloadHandler{
		gatewayNode:        config.GatewayNode,
		internalToken:      config.InternalToken,
		maxRequestBodySize: maxBodySize,
		control:            config.routeControl,
	}
}

func (h *adminRouteStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminRouteReloadJSON(w, http.StatusMethodNotAllowed, adminRouteReloadResponse{
			Code:        "method_not_allowed",
			GatewayNode: h.gatewayNode,
		})
		return
	}
	if h.internalToken != "" && r.Header.Get(downlink.InternalTokenHeader) != h.internalToken {
		writeAdminRouteReloadJSON(w, http.StatusUnauthorized, adminRouteReloadResponse{
			Code:        "unauthorized",
			GatewayNode: h.gatewayNode,
		})
		return
	}

	snapshot := h.control.Status()
	writeAdminRouteReloadJSON(w, http.StatusOK, routeControlStatusResponse(h.gatewayNode, snapshot))
}

func (h *adminRouteReloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminRouteReloadJSON(w, http.StatusMethodNotAllowed, adminRouteReloadResponse{
			Code:        "method_not_allowed",
			GatewayNode: h.gatewayNode,
		})
		return
	}
	if h.internalToken != "" && r.Header.Get(downlink.InternalTokenHeader) != h.internalToken {
		writeAdminRouteReloadJSON(w, http.StatusUnauthorized, adminRouteReloadResponse{
			Code:        "unauthorized",
			GatewayNode: h.gatewayNode,
		})
		return
	}

	request, ok := h.readRequest(w, r)
	if !ok {
		return
	}
	actor := routeReloadActorFromRequest(r, h.internalToken)
	outcome, _ := h.control.Execute(r.Context(), routeReloadOptions{
		DryRun:             *request.DryRun,
		ExpectedGeneration: request.ExpectedGeneration,
		Trigger:            routeReloadTriggerAdminAPI,
	}, actor)
	writeAdminRouteReloadJSON(w, outcome.HTTPStatus, routeReloadOutcomeResponse(h.gatewayNode, h.control.Status(), outcome))
}

func (h *adminRouteReloadHandler) readRequest(w http.ResponseWriter, r *http.Request) (adminRouteReloadRequest, bool) {
	if r.Body == nil {
		h.writeBadRequest(w, r, "dry_run is required")
		return adminRouteReloadRequest{}, false
	}
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxRequestBodySize))
	if err != nil {
		h.writeBadRequestStatus(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "route reload request exceeds the size limit")
		return adminRouteReloadRequest{}, false
	}
	decoder := sonic.ConfigStd.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request adminRouteReloadRequest
	if err := decoder.Decode(&request); err != nil {
		h.writeBadRequest(w, r, "route reload request must be valid JSON")
		return adminRouteReloadRequest{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		h.writeBadRequest(w, r, "route reload request must contain exactly one JSON value")
		return adminRouteReloadRequest{}, false
	}
	if request.DryRun == nil {
		h.writeBadRequest(w, r, "dry_run is required")
		return adminRouteReloadRequest{}, false
	}
	return request, true
}

func (h *adminRouteReloadHandler) writeBadRequest(w http.ResponseWriter, r *http.Request, reason string) {
	h.writeBadRequestStatus(w, r, http.StatusBadRequest, "bad_request", reason)
}

func (h *adminRouteReloadHandler) writeBadRequestStatus(w http.ResponseWriter, r *http.Request, status int, code string, reason string) {
	outcome := routeReloadOutcome{
		Code:       code,
		Result:     code,
		Reason:     reason,
		Stage:      routeReloadStageOperation,
		HTTPStatus: status,
		Trigger:    routeReloadTriggerAdminAPI,
	}
	if h.control != nil {
		h.control.record(outcome, routeReloadActorFromRequest(r, h.internalToken))
	}
	writeAdminRouteReloadJSON(w, status, adminRouteReloadResponse{
		Code:          code,
		Result:        code,
		Reason:        reason,
		GatewayNode:   h.gatewayNode,
		ReloadEnabled: h.control.Status().Enabled,
	})
}

func routeReloadActorFromRequest(r *http.Request, internalToken string) routeReloadActor {
	identity := debugAuthIdentity(r, internalToken)
	role := identity.Role
	if role != "" {
		role = normalizeAdminRole(role)
	}
	return routeReloadActor{
		AuthMode:       identity.Mode,
		Principal:      identity.Principal,
		Role:           role,
		AdminSessionID: identity.SessionID,
		AuthKeyID:      identity.KeyID,
		Method:         r.Method,
		Path:           r.URL.Path,
		RemoteAddr:     r.RemoteAddr,
	}
}

func routeControlStatusResponse(gatewayNode string, snapshot routeControlSnapshot) adminRouteReloadResponse {
	response := adminRouteReloadResponse{
		Code:           "ok",
		GatewayNode:    gatewayNode,
		ReloadEnabled:  snapshot.Enabled,
		DrainTimeoutMS: snapshot.DrainTimeout.Milliseconds(),
		Active:         adminRouteGenerationFromSnapshot(snapshot.Active, snapshot.DrainTimeout),
		Retiring:       adminRouteGenerationFromSnapshot(snapshot.Retiring, snapshot.DrainTimeout),
	}
	applyRouteControlRuntime(&response, snapshot.Runtime)
	return response
}

func routeReloadOutcomeResponse(gatewayNode string, status routeControlSnapshot, outcome routeReloadOutcome) adminRouteReloadResponse {
	response := adminRouteReloadResponse{
		Code:           outcome.Code,
		Result:         outcome.Result,
		Reason:         outcome.Reason,
		Stage:          outcome.Stage,
		GatewayNode:    gatewayNode,
		ReloadEnabled:  status.Enabled,
		Trigger:        outcome.Trigger,
		DryRun:         outcome.DryRun,
		Changed:        outcome.Changed,
		WarningCount:   outcome.WarningCount,
		DurationMS:     outcome.Duration.Milliseconds(),
		DrainTimeoutMS: status.DrainTimeout.Milliseconds(),
		Active:         adminRouteGenerationFromValue(outcome.Active, status.DrainTimeout),
		Candidate:      adminRouteGenerationFromValue(outcome.Candidate, status.DrainTimeout),
		Retiring:       adminRouteGenerationFromSnapshot(outcome.Retiring, status.DrainTimeout),
	}
	applyRouteControlRuntime(&response, status.Runtime)
	return response
}

func applyRouteControlRuntime(response *adminRouteReloadResponse, runtime routeControlRuntimeSnapshot) {
	if response == nil {
		return
	}
	response.OperationsInFlight = runtime.OperationsInFlight
	response.LastStartedAt = optionalRouteReloadTime(runtime.LastStartedAt)
	response.LastSuccessAt = optionalRouteReloadTime(runtime.LastSuccessAt)
	response.LastFailureAt = optionalRouteReloadTime(runtime.LastFailureAt)
	response.RecentAttempts = make([]adminRouteReloadAttempt, 0, len(runtime.RecentAttempts))
	for _, attempt := range runtime.RecentAttempts {
		response.RecentAttempts = append(response.RecentAttempts, adminRouteReloadAttemptFromSnapshot(attempt))
	}
	if len(response.RecentAttempts) > 0 {
		last := response.RecentAttempts[0]
		response.LastAttempt = &last
	}
}

func adminRouteReloadAttemptFromSnapshot(snapshot routeReloadAttemptSnapshot) adminRouteReloadAttempt {
	return adminRouteReloadAttempt{
		Operation:            snapshot.Operation,
		Trigger:              snapshot.Trigger,
		Stage:                snapshot.Stage,
		Result:               snapshot.Result,
		Reason:               snapshot.Reason,
		StartedAt:            snapshot.StartedAt,
		CompletedAt:          snapshot.CompletedAt,
		DurationMS:           snapshot.Duration.Milliseconds(),
		ExpectedGeneration:   snapshot.ExpectedGeneration,
		OldGeneration:        snapshot.OldGeneration,
		CandidateGeneration:  snapshot.CandidateGeneration,
		CandidateFingerprint: snapshot.CandidateFingerprint,
		CandidateRouteCount:  snapshot.CandidateRouteCount,
		ActiveGeneration:     snapshot.ActiveGeneration,
		Changed:              snapshot.Changed,
		WarningCount:         snapshot.WarningCount,
	}
}

func optionalRouteReloadTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	out := value
	return &out
}

func adminRouteGenerationFromSnapshot(snapshot *routeGenerationSnapshot, drainTimeout time.Duration) *adminRouteGeneration {
	if snapshot == nil {
		return nil
	}
	return adminRouteGenerationFromValue(*snapshot, drainTimeout)
}

func adminRouteGenerationFromValue(snapshot routeGenerationSnapshot, drainTimeout time.Duration) *adminRouteGeneration {
	if snapshot.Fingerprint == "" && snapshot.Number == 0 && snapshot.RouteCount == 0 {
		return nil
	}
	var activatedAt *time.Time
	if !snapshot.ActivatedAt.IsZero() {
		value := snapshot.ActivatedAt
		activatedAt = &value
	}
	var retiringAt *time.Time
	var retiringFor time.Duration
	if !snapshot.RetiringAt.IsZero() {
		value := snapshot.RetiringAt
		retiringAt = &value
		retiringFor = time.Since(snapshot.RetiringAt)
		if retiringFor < 0 {
			retiringFor = 0
		}
	}
	return &adminRouteGeneration{
		Number:         snapshot.Number,
		Fingerprint:    snapshot.Fingerprint,
		ActivatedAt:    activatedAt,
		RetiringAt:     retiringAt,
		RetiringForMS:  retiringFor.Milliseconds(),
		DrainTimeoutMS: drainTimeout.Milliseconds(),
		Slow:           retiringAt != nil && drainTimeout > 0 && retiringFor > drainTimeout,
		RouteCount:     snapshot.RouteCount,
		InFlight:       snapshot.InFlight,
		State:          snapshot.State,
	}
}

func writeAdminRouteReloadJSON(w http.ResponseWriter, status int, response adminRouteReloadResponse) {
	body, err := sonic.Marshal(response)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
