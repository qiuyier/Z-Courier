package downlink

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const (
	defaultMessageListLimit = 100
	maxMessageListLimit     = 1000
	maxRetryScanLimit       = 1000

	messageActionRequeue = "requeue"
	messageActionDiscard = "discard"
)

func NewMessageListHandler(config HandlerConfig) http.Handler {
	return &messageListHandler{config: normalizeHandlerConfig(config)}
}

func NewRequeueHandler(config HandlerConfig) http.Handler {
	return &messageActionHandler{config: normalizeHandlerConfig(config), action: messageActionRequeue}
}

func NewDiscardHandler(config HandlerConfig) http.Handler {
	return &messageActionHandler{config: normalizeHandlerConfig(config), action: messageActionDiscard}
}

func NewRetryScanHandler(config HandlerConfig) http.Handler {
	return &retryScanHandler{config: normalizeHandlerConfig(config)}
}

type messageListHandler struct {
	config HandlerConfig
}

func (h *messageListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ListMessagesResponse{Code: "method_not_allowed"})
		return
	}
	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		writeJSON(w, http.StatusUnauthorized, ListMessagesResponse{Code: "unauthorized"})
		return
	}

	status, err := parseMessageStatus(r.URL.Query().Get("status"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ListMessagesResponse{Code: "bad_request", Reason: err.Error()})
		return
	}
	limit, err := parseMessageListLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ListMessagesResponse{Code: "bad_request", Reason: err.Error()})
		return
	}

	messages, err := h.config.Service.ListMessages(r.Context(), status, limit)
	if err != nil {
		statusCode := statusFromAdminError(err)
		writeJSON(w, statusCode, ListMessagesResponse{
			Code:   codeFromAdminError(err, statusCode),
			Reason: err.Error(),
			Status: status,
			Limit:  limit,
		})
		return
	}

	resp := ListMessagesResponse{
		Code:     "ok",
		Status:   status,
		Limit:    limit,
		Total:    len(messages),
		Messages: make([]MessageStatusResponse, 0, len(messages)),
	}
	for _, message := range messages {
		resp.Messages = append(resp.Messages, responseFromMessage(message))
	}
	writeJSON(w, http.StatusOK, resp)
}

type messageActionHandler struct {
	config HandlerConfig
	action string
}

type retryScanHandler struct {
	config HandlerConfig
}

func (h *retryScanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.record("method_not_allowed")
		h.audit(r, "method_not_allowed", http.StatusMethodNotAllowed, 0, RetryResult{}, nil)
		writeJSON(w, http.StatusMethodNotAllowed, RetryScanResponse{Code: "method_not_allowed"})
		return
	}
	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		h.record("unauthorized")
		h.audit(r, "unauthorized", http.StatusUnauthorized, 0, RetryResult{}, nil)
		writeJSON(w, http.StatusUnauthorized, RetryScanResponse{Code: "unauthorized"})
		return
	}

	req, ok := h.request(w, r)
	if !ok {
		return
	}
	limit, err := parseRetryScanLimit(req.Limit, h.config.RetryScanLimit)
	if err != nil {
		h.record("bad_request")
		h.audit(r, "bad_request", http.StatusBadRequest, req.Limit, RetryResult{}, err)
		writeJSON(w, http.StatusBadRequest, RetryScanResponse{Code: "bad_request", Reason: err.Error()})
		return
	}

	result, err := h.config.Service.RetryDue(r.Context(), limit)
	if err != nil {
		statusCode := statusFromAdminError(err)
		code := retryScanCodeFromError(err, statusCode)
		h.record(code)
		h.audit(r, code, statusCode, limit, result, err)
		writeJSON(w, statusCode, RetryScanResponse{
			Code:    code,
			Reason:  err.Error(),
			Limit:   limit,
			Scanned: result.Scanned,
			Sent:    result.Sent,
			Queued:  result.Queued,
			Failed:  result.Failed,
		})
		return
	}

	h.record("success")
	h.audit(r, "success", http.StatusOK, limit, result, nil)
	writeJSON(w, http.StatusOK, RetryScanResponse{
		Code:    "ok",
		Limit:   limit,
		Scanned: result.Scanned,
		Sent:    result.Sent,
		Queued:  result.Queued,
		Failed:  result.Failed,
	})
}

func (h *retryScanHandler) request(w http.ResponseWriter, r *http.Request) (RetryScanRequest, bool) {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		h.record("request_too_large")
		h.audit(r, "request_too_large", http.StatusRequestEntityTooLarge, 0, RetryResult{}, err)
		writeJSON(w, http.StatusRequestEntityTooLarge, RetryScanResponse{Code: "request_too_large", Reason: err.Error()})
		return RetryScanRequest{}, false
	}
	if strings.TrimSpace(string(body)) == "" {
		return RetryScanRequest{}, true
	}

	var req RetryScanRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		h.record("bad_request")
		h.audit(r, "bad_request", http.StatusBadRequest, 0, RetryResult{}, err)
		writeJSON(w, http.StatusBadRequest, RetryScanResponse{Code: "bad_request", Reason: err.Error()})
		return RetryScanRequest{}, false
	}
	return req, true
}

func (h *retryScanHandler) record(result string) {
	metrics.RecordAdminRetryScan(result)
}

func (h *retryScanHandler) audit(r *http.Request, result string, statusCode int, limit int, scan RetryResult, err error) {
	if h.config.Logger == nil {
		return
	}

	identity := adminAuthIdentity(r, h.config.InternalToken)
	fields := []zap.Field{
		zap.String("audit_event", "admin_retry_scan"),
		zap.String("result", result),
		zap.Int("http_status", statusCode),
		zap.String("auth_mode", identity.Mode),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
		zap.Int("limit", limit),
		zap.Int("scanned", scan.Scanned),
		zap.Int("sent", scan.Sent),
		zap.Int("queued", scan.Queued),
		zap.Int("failed", scan.Failed),
	}
	if h.config.GatewayNode != "" {
		fields = append(fields, zap.String("gateway_node", h.config.GatewayNode))
	}
	if identity.KeyID != "" {
		fields = append(fields, zap.String("auth_key_id", identity.KeyID))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		h.config.Logger.Warn("admin retry scan audit", fields...)
		return
	}
	if statusCode >= http.StatusBadRequest {
		h.config.Logger.Warn("admin retry scan audit", fields...)
		return
	}
	h.config.Logger.Info("admin retry scan audit", fields...)
}

func (h *messageActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.record("method_not_allowed")
		h.audit(r, "method_not_allowed", http.StatusMethodNotAllowed, MessageActionRequest{}, Message{}, nil)
		writeJSON(w, http.StatusMethodNotAllowed, MessageStatusResponse{Code: "method_not_allowed"})
		return
	}
	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		h.record("unauthorized")
		h.audit(r, "unauthorized", http.StatusUnauthorized, MessageActionRequest{}, Message{}, nil)
		writeJSON(w, http.StatusUnauthorized, MessageStatusResponse{Code: "unauthorized"})
		return
	}

	req, ok := h.request(w, r)
	if !ok {
		return
	}

	message, err := h.apply(r, req)
	if err != nil {
		statusCode := statusFromAdminError(err)
		result := codeFromAdminError(err, statusCode)
		h.record(result)
		h.audit(r, result, statusCode, req, Message{}, err)
		writeJSON(w, statusCode, MessageStatusResponse{
			Code:      result,
			Reason:    err.Error(),
			MessageID: req.MessageID,
		})
		return
	}

	h.record("success")
	h.audit(r, "success", http.StatusOK, req, message, nil)
	writeJSON(w, http.StatusOK, responseFromMessage(message))
}

func (h *messageActionHandler) request(w http.ResponseWriter, r *http.Request) (MessageActionRequest, bool) {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		h.record("request_too_large")
		h.audit(r, "request_too_large", http.StatusRequestEntityTooLarge, MessageActionRequest{}, Message{}, err)
		writeJSON(w, http.StatusRequestEntityTooLarge, MessageStatusResponse{Code: "request_too_large", Reason: err.Error()})
		return MessageActionRequest{}, false
	}

	var req MessageActionRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		h.record("bad_request")
		h.audit(r, "bad_request", http.StatusBadRequest, MessageActionRequest{}, Message{}, err)
		writeJSON(w, http.StatusBadRequest, MessageStatusResponse{Code: "bad_request", Reason: err.Error()})
		return MessageActionRequest{}, false
	}
	req.MessageID = strings.TrimSpace(req.MessageID)
	req.Reason = strings.TrimSpace(req.Reason)
	return req, true
}

func (h *messageActionHandler) apply(r *http.Request, req MessageActionRequest) (Message, error) {
	switch h.action {
	case messageActionRequeue:
		return h.config.Service.Requeue(r.Context(), req.MessageID)
	case messageActionDiscard:
		return h.config.Service.Discard(r.Context(), req.MessageID, req.Reason)
	default:
		return Message{}, ErrInvalidTransition
	}
}

func (h *messageActionHandler) record(result string) {
	switch h.action {
	case messageActionRequeue:
		metrics.RecordDownlinkRequeue(result)
	case messageActionDiscard:
		metrics.RecordDownlinkDiscard(result)
	}
}

func (h *messageActionHandler) audit(r *http.Request, result string, statusCode int, req MessageActionRequest, message Message, err error) {
	if h.config.Logger == nil {
		return
	}

	identity := h.authIdentity(r)
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		messageID = message.MessageID
	}

	fields := []zap.Field{
		zap.String("audit_event", "downlink_message_action"),
		zap.String("action", h.action),
		zap.String("result", result),
		zap.Int("http_status", statusCode),
		zap.String("auth_mode", identity.Mode),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
	}
	if h.config.GatewayNode != "" {
		fields = append(fields, zap.String("gateway_node", h.config.GatewayNode))
	}
	if identity.KeyID != "" {
		fields = append(fields, zap.String("auth_key_id", identity.KeyID))
	}
	if messageID != "" {
		fields = append(fields, zap.String("message_id", messageID))
	}
	if req.Reason != "" {
		fields = append(fields, zap.String("reason", req.Reason))
	}
	if message.Status != "" {
		fields = append(fields, zap.String("message_status", string(message.Status)))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		h.config.Logger.Warn("admin message action audit", fields...)
		return
	}
	if statusCode >= http.StatusBadRequest {
		h.config.Logger.Warn("admin message action audit", fields...)
		return
	}
	h.config.Logger.Info("admin message action audit", fields...)
}

func (h *messageActionHandler) authIdentity(r *http.Request) httpauth.Identity {
	return adminAuthIdentity(r, h.config.InternalToken)
}

func adminAuthIdentity(r *http.Request, internalToken string) httpauth.Identity {
	if identity, ok := httpauth.IdentityFromContext(r.Context()); ok {
		if identity.Mode == "" {
			identity.Mode = httpauth.ModeNone
		}
		return identity
	}
	if internalToken != "" {
		return httpauth.Identity{Mode: httpauth.ModeToken}
	}
	return httpauth.Identity{Mode: httpauth.ModeNone}
}

func parseMessageStatus(raw string) (MessageStatus, error) {
	status := MessageStatus(strings.TrimSpace(raw))
	if status == "" {
		return MessageStatusFailed, nil
	}
	if !validMessageStatus(status) {
		return "", ErrInvalidStatus
	}
	return status, nil
}

func parseMessageListLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultMessageListLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, ErrInvalidLimit
	}
	if limit > maxMessageListLimit {
		return maxMessageListLimit, nil
	}
	return limit, nil
}

func parseRetryScanLimit(rawLimit int, defaultLimit int) (int, error) {
	if rawLimit < 0 {
		return 0, ErrInvalidLimit
	}
	if rawLimit == 0 {
		if defaultLimit <= 0 {
			return 0, ErrInvalidLimit
		}
		return defaultLimit, nil
	}
	if rawLimit > maxRetryScanLimit {
		return maxRetryScanLimit, nil
	}
	return rawLimit, nil
}

func statusFromAdminError(err error) int {
	switch {
	case errors.Is(err, ErrMissingMessageID), errors.Is(err, ErrInvalidStatus), errors.Is(err, ErrInvalidLimit):
		return http.StatusBadRequest
	case errors.Is(err, ErrInvalidTransition):
		return http.StatusConflict
	case errors.Is(err, ErrMessageNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrStoreNotConfigured):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func retryScanCodeFromError(err error, status int) string {
	switch {
	case errors.Is(err, ErrInvalidLimit), status == http.StatusBadRequest:
		return "bad_request"
	case errors.Is(err, ErrStoreNotConfigured), status == http.StatusServiceUnavailable:
		return "store_not_configured"
	default:
		return "retry_scan_failed"
	}
}

func codeFromAdminError(err error, status int) string {
	switch {
	case errors.Is(err, ErrInvalidTransition):
		return "invalid_transition"
	case errors.Is(err, ErrInvalidStatus), errors.Is(err, ErrInvalidLimit), status == http.StatusBadRequest:
		return "bad_request"
	case errors.Is(err, ErrMessageNotFound), status == http.StatusNotFound:
		return "message_not_found"
	case errors.Is(err, ErrStoreNotConfigured), status == http.StatusServiceUnavailable:
		return "store_not_configured"
	default:
		return "message_action_failed"
	}
}
