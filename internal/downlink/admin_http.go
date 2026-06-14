package downlink

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const (
	defaultMessageListLimit = 100
	maxMessageListLimit     = 1000

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

func (h *messageActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.record("method_not_allowed")
		writeJSON(w, http.StatusMethodNotAllowed, MessageStatusResponse{Code: "method_not_allowed"})
		return
	}
	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		h.record("unauthorized")
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
		h.config.Logger.Warn(
			"downlink message action failed",
			zap.String("action", h.action),
			zap.String("message_id", req.MessageID),
			zap.Error(err),
		)
		writeJSON(w, statusCode, MessageStatusResponse{
			Code:      result,
			Reason:    err.Error(),
			MessageID: req.MessageID,
		})
		return
	}

	h.record("success")
	h.config.Logger.Info(
		"downlink message action accepted",
		zap.String("action", h.action),
		zap.String("message_id", message.MessageID),
		zap.String("status", string(message.Status)),
	)
	writeJSON(w, http.StatusOK, responseFromMessage(message))
}

func (h *messageActionHandler) request(w http.ResponseWriter, r *http.Request) (MessageActionRequest, bool) {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		h.record("request_too_large")
		writeJSON(w, http.StatusRequestEntityTooLarge, MessageStatusResponse{Code: "request_too_large", Reason: err.Error()})
		return MessageActionRequest{}, false
	}

	var req MessageActionRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		h.record("bad_request")
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
