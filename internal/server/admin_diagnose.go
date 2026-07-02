package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
)

const (
	defaultAdminDiagnoseMessageLimit = 20
	maxAdminDiagnoseMessageLimit     = 1000
)

type adminDiagnoseResponse struct {
	Code             string                          `json:"code"`
	Reason           string                          `json:"reason,omitempty"`
	GeneratedAt      time.Time                       `json:"generated_at,omitempty"`
	TargetURL        string                          `json:"target_url,omitempty"`
	CollectionStatus string                          `json:"collection_status,omitempty"`
	Sections         map[string]adminDiagnoseSection `json:"sections,omitempty"`
}

type adminDiagnoseSection struct {
	Endpoint   string `json:"endpoint"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
	Body       any    `json:"body,omitempty"`
}

type adminDiagnoseHandler struct {
	config   adminHandlerConfig
	health   *gatewayHealth
	registry cluster.OnlineRegistry
	runtime  *gatewayRuntime
	service  *downlink.Service
}

type adminDiagnoseRequest struct {
	ProbeTimeout time.Duration
	MessageLimit int
	SessionLimit int
	ClientID     string
	DeviceID     string
}

type adminDiagnoseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newAdminDiagnoseHandler(config Config, health *gatewayHealth, registry cluster.OnlineRegistry, runtime *gatewayRuntime, service *downlink.Service) http.Handler {
	if runtime == nil {
		runtime = newGatewayRuntime()
	}
	return &adminDiagnoseHandler{
		config:   adminConfig(config, health, registry),
		health:   health,
		registry: registry,
		runtime:  runtime,
		service:  service,
	}
}

func (h *adminDiagnoseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminJSON(w, http.StatusMethodNotAllowed, adminDiagnoseResponse{Code: "method_not_allowed", TargetURL: adminDiagnoseTargetURL(r)})
		return
	}
	if !h.config.authorized(r) {
		writeAdminJSON(w, http.StatusUnauthorized, adminDiagnoseResponse{Code: "unauthorized", TargetURL: adminDiagnoseTargetURL(r)})
		return
	}

	request, err := parseAdminDiagnoseRequest(r)
	if err != nil {
		writeAdminJSON(w, http.StatusBadRequest, adminDiagnoseResponse{
			Code:      "bad_request",
			Reason:    err.Error(),
			TargetURL: adminDiagnoseTargetURL(r),
		})
		return
	}

	bundle := adminDiagnoseResponse{
		Code:        "ok",
		GeneratedAt: time.Now().UTC(),
		TargetURL:   adminDiagnoseTargetURL(r),
		Sections:    make(map[string]adminDiagnoseSection),
	}

	h.collect(r.Context(), bundle.Sections, "overview", "/internal/admin/overview", newAdminOverviewHandler(h.config.config, h.health, h.registry))
	h.collect(r.Context(), bundle.Sections, "diagnostics", "/internal/admin/diagnostics", newAdminDiagnosticsHandler(h.config.config, h.health, h.registry, h.runtime, h.service != nil && h.service.HasStore()))

	checkQuery := url.Values{}
	checkQuery.Set("timeout", request.ProbeTimeout.String())
	h.collect(r.Context(), bundle.Sections, "check", "/internal/admin/check?"+checkQuery.Encode(), newAdminCheckHandler(h.config.config, h.service, h.registry))
	h.collect(r.Context(), bundle.Sections, "routes", "/internal/admin/routes", newAdminRoutesHandler(h.config.config))

	messagesQuery := url.Values{}
	messagesQuery.Set("status", string(sdkbackend.MessageStatusFailed))
	messagesQuery.Set("limit", strconv.Itoa(request.MessageLimit))
	h.collect(r.Context(), bundle.Sections, "failed_messages", "/internal/messages?"+messagesQuery.Encode(), downlink.NewMessageListHandler(downlink.HandlerConfig{
		Service:       h.service,
		InternalToken: h.config.internalToken,
		GatewayNode:   h.config.gatewayNode,
	}))

	if request.ClientID != "" {
		sessionsQuery := url.Values{}
		sessionsQuery.Set("client_id", request.ClientID)
		sessionsQuery.Set("limit", strconv.Itoa(request.SessionLimit))
		h.collect(r.Context(), bundle.Sections, "sessions", "/internal/debug/sessions?"+sessionsQuery.Encode(), newDebugSessionsHandler(h.config.config))
	}
	if request.ClientID != "" && request.DeviceID != "" {
		routeQuery := url.Values{}
		routeQuery.Set("client_id", request.ClientID)
		routeQuery.Set("device_id", request.DeviceID)
		h.collect(r.Context(), bundle.Sections, "route", "/internal/debug/route?"+routeQuery.Encode(), newDebugRouteHandler(h.config.config, h.registry))
	}

	bundle.CollectionStatus = adminDiagnoseCollectionStatus(bundle.Sections)
	writeAdminJSON(w, http.StatusOK, bundle)
}

func (h *adminDiagnoseHandler) collect(ctx context.Context, sections map[string]adminDiagnoseSection, name, endpoint string, handler http.Handler) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		sections[name] = adminDiagnoseSection{Endpoint: endpoint, Error: err.Error()}
		return
	}
	if h.config.internalToken != "" {
		req.Header.Set(downlink.InternalTokenHeader, h.config.internalToken)
	}

	rec := &adminDiagnoseCapture{header: make(http.Header)}
	handler.ServeHTTP(rec, req)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}

	section := adminDiagnoseSection{
		Endpoint:   endpoint,
		HTTPStatus: status,
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		section.Error = fmt.Sprintf("gateway returned status %d", status)
	}
	if rec.body.Len() > 0 {
		section.Body = decodeAdminDiagnoseBody(rec.body.Bytes())
	}
	sections[name] = section
}

func (r *adminDiagnoseCapture) Header() http.Header {
	return r.header
}

func (r *adminDiagnoseCapture) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
}

func (r *adminDiagnoseCapture) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(body)
}

func parseAdminDiagnoseRequest(r *http.Request) (adminDiagnoseRequest, error) {
	query := r.URL.Query()
	request := adminDiagnoseRequest{
		ProbeTimeout: defaultAdminCheckTimeout,
		MessageLimit: defaultAdminDiagnoseMessageLimit,
		SessionLimit: defaultDebugSessionLimit,
		ClientID:     strings.TrimSpace(query.Get("client_id")),
		DeviceID:     strings.TrimSpace(query.Get("device_id")),
	}

	if raw := strings.TrimSpace(query.Get("probe_timeout")); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return request, fmt.Errorf("probe_timeout must be greater than 0")
		}
		if duration > maxAdminCheckTimeout {
			duration = maxAdminCheckTimeout
		}
		request.ProbeTimeout = duration
	}

	if raw := strings.TrimSpace(query.Get("message_limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return request, fmt.Errorf("message_limit must be greater than 0")
		}
		if limit > maxAdminDiagnoseMessageLimit {
			limit = maxAdminDiagnoseMessageLimit
		}
		request.MessageLimit = limit
	}

	if raw := strings.TrimSpace(query.Get("session_limit")); raw != "" {
		limit, err := parseDebugSessionLimit(raw)
		if err != nil {
			return request, fmt.Errorf("session_limit %s", err.Error())
		}
		request.SessionLimit = limit
	}

	if request.DeviceID != "" && request.ClientID == "" {
		return request, fmt.Errorf("client_id is required when device_id is set")
	}
	return request, nil
}

func decodeAdminDiagnoseBody(body []byte) any {
	var value any
	if err := sonic.Unmarshal(body, &value); err != nil {
		return string(body)
	}
	return value
}

func adminDiagnoseCollectionStatus(sections map[string]adminDiagnoseSection) string {
	successes := 0
	failures := 0
	for _, section := range sections {
		if section.Error != "" || section.HTTPStatus < http.StatusOK || section.HTTPStatus >= http.StatusMultipleChoices {
			failures++
			continue
		}
		successes++
	}
	switch {
	case successes == 0:
		return "failed"
	case failures > 0:
		return "partial"
	default:
		return "complete"
	}
}

func adminDiagnoseTargetURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = strings.TrimSpace(r.URL.Host)
	}
	if host == "" {
		return ""
	}
	return sanitizeAdminURL(scheme + "://" + host)
}
