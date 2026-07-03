package server

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const maxDebugPushAuditBodyBytes = 64 << 10

type debugPushAuditHandler struct {
	next          http.Handler
	gatewayNode   string
	internalToken string
	logger        *zap.Logger
	audit         adminaudit.Recorder
}

func newDebugPushAuditHandler(next http.Handler, config debugHandlerConfig) http.Handler {
	return &debugPushAuditHandler{
		next:          next,
		gatewayNode:   config.gatewayNode,
		internalToken: config.internalToken,
		logger:        config.logger,
		audit:         config.audit,
	}
}

func (h *debugPushAuditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &debugPushAuditResponseWriter{ResponseWriter: w}
	h.next.ServeHTTP(rec, r)
	h.recordAndAudit(r, rec.statusCode(), rec.body.Bytes())
}

func (h *debugPushAuditHandler) recordAndAudit(r *http.Request, statusCode int, body []byte) {
	var resp downlink.PushResponse
	if len(body) > 0 {
		_ = sonic.Unmarshal(body, &resp)
	}

	result := debugPushAuditResult(statusCode, resp)
	metrics.RecordAdminDownlinkTestPush(result)

	identity := debugAuthIdentity(r, h.internalToken)
	role := strings.TrimSpace(identity.Role)
	if role == "" {
		role = "unknown"
	} else {
		role = normalizeAdminRole(role)
	}
	adminaudit.Record(h.audit, adminaudit.Entry{
		Action:          "admin_downlink_test_push",
		Result:          result,
		HTTPStatus:      statusCode,
		GatewayNode:     h.gatewayNode,
		AuthMode:        identity.Mode,
		Principal:       identity.Principal,
		Role:            role,
		AdminSessionID:  identity.SessionID,
		AuthKeyID:       identity.KeyID,
		Method:          r.Method,
		Path:            r.URL.Path,
		RemoteAddr:      r.RemoteAddr,
		TargetClientID:  resp.ClientID,
		TargetDeviceID:  resp.DeviceID,
		TargetSessionID: resp.SessionID,
		TargetConnID:    resp.ConnID,
		MessageID:       resp.MessageID,
		TraceID:         resp.TraceID,
		Reason:          resp.Reason,
		Details: map[string]string{
			"delivery_state": resp.DeliveryState,
		},
	})
	if h.logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("audit_event", "admin_downlink_test_push"),
		zap.String("result", result),
		zap.Int("http_status", statusCode),
		zap.String("auth_mode", identity.Mode),
		zap.String("principal", identity.Principal),
		zap.String("role", role),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("gateway_node", h.gatewayNode),
		zap.String("delivery_state", resp.DeliveryState),
		zap.String("target_client_id", resp.ClientID),
		zap.String("target_device_id", resp.DeviceID),
		zap.String("target_session_id", resp.SessionID),
		zap.Uint64("target_conn_id", resp.ConnID),
		zap.String("message_id", resp.MessageID),
		zap.String("trace_id", resp.TraceID),
	}
	if identity.KeyID != "" {
		fields = append(fields, zap.String("auth_key_id", identity.KeyID))
	}
	if identity.SessionID != "" {
		fields = append(fields, zap.String("admin_session_id", identity.SessionID))
	}
	if resp.Reason != "" {
		fields = append(fields, zap.String("reason", resp.Reason))
	}

	if statusCode >= http.StatusBadRequest {
		h.logger.Warn("admin downlink test push audit", fields...)
		return
	}
	h.logger.Info("admin downlink test push audit", fields...)
}

func debugPushAuditResult(statusCode int, resp downlink.PushResponse) string {
	if resp.DeliveryState != "" {
		return resp.DeliveryState
	}
	if resp.Code != "" {
		return resp.Code
	}
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return "success"
	case statusCode == http.StatusUnauthorized:
		return "unauthorized"
	case statusCode == http.StatusForbidden:
		return "permission_denied"
	case statusCode == http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case statusCode == http.StatusTooManyRequests:
		return "overloaded"
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return "bad_request"
	case statusCode >= http.StatusInternalServerError:
		return "error"
	default:
		return "unknown"
	}
}

type debugPushAuditResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *debugPushAuditResponseWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *debugPushAuditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < maxDebugPushAuditBodyBytes {
		remaining := maxDebugPushAuditBodyBytes - w.body.Len()
		if len(data) > remaining {
			_, _ = w.body.Write(data[:remaining])
		} else {
			_, _ = w.body.Write(data)
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *debugPushAuditResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
