package downlink

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

func NewStatusHandler(config HandlerConfig) http.Handler {
	return &statusHandler{config: normalizeHandlerConfig(config)}
}

type statusHandler struct {
	config HandlerConfig
}

func (h *statusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, MessageStatusResponse{Code: "method_not_allowed"})
		return
	}

	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		writeJSON(w, http.StatusUnauthorized, MessageStatusResponse{Code: "unauthorized"})
		return
	}

	messageID, ok := h.messageID(w, r)
	if !ok {
		return
	}

	resp, status := h.lookup(r, messageID)
	writeJSON(w, status, resp)
}

func (h *statusHandler) messageID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method == http.MethodGet {
		return strings.TrimSpace(r.URL.Query().Get("message_id")), true
	}

	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, MessageStatusResponse{Code: "request_too_large", Reason: err.Error()})
		return "", false
	}

	var req MessageStatusRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, MessageStatusResponse{Code: "bad_request", Reason: err.Error()})
		return "", false
	}

	return strings.TrimSpace(req.MessageID), true
}

func (h *statusHandler) lookup(r *http.Request, messageID string) (MessageStatusResponse, int) {
	message, ok, err := h.config.Service.MessageStatus(r.Context(), messageID)
	if err != nil {
		status := statusFromMessageStatusError(err)
		return MessageStatusResponse{
			Code:      codeFromMessageStatus(status),
			Reason:    err.Error(),
			MessageID: messageID,
		}, status
	}
	if !ok {
		return MessageStatusResponse{
			Code:      "message_not_found",
			Reason:    ErrMessageNotFound.Error(),
			MessageID: messageID,
		}, http.StatusNotFound
	}

	return responseFromMessage(message), http.StatusOK
}

func statusFromMessageStatusError(err error) int {
	switch {
	case errors.Is(err, ErrMissingMessageID):
		return http.StatusBadRequest
	case errors.Is(err, ErrStoreNotConfigured):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func codeFromMessageStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusNotFound:
		return "message_not_found"
	case http.StatusServiceUnavailable:
		return "store_not_configured"
	default:
		return "status_failed"
	}
}

func responseFromMessage(message Message) MessageStatusResponse {
	return MessageStatusResponse{
		Code:                    "ok",
		MessageID:               message.MessageID,
		ClientID:                message.ClientID,
		DeviceID:                message.DeviceID,
		MsgID:                   message.MsgID,
		TraceID:                 message.TraceID,
		SessionID:               message.SessionID,
		PolicyName:              message.Policy.Name,
		Status:                  message.Status,
		Attempts:                message.Attempts,
		LastError:               message.LastError,
		TerminalReason:          message.TerminalReason,
		TerminalAt:              optionalTime(message.TerminalAt),
		TerminalPublishStatus:   terminalPublicationStatusForResponse(message),
		TerminalPublishAttempts: message.TerminalPublishAttempts,
		TerminalNextPublishAt:   optionalTime(message.TerminalNextPublishAt),
		TerminalPublishError:    message.TerminalPublishError,
		TerminalPublishedAt:     optionalTime(message.TerminalPublishedAt),
		NextRetryAt:             optionalTime(message.NextRetryAt),
		ClaimOwner:              message.ClaimOwner,
		ClaimUntil:              optionalTime(message.ClaimUntil),
		CreatedAt:               optionalTime(message.CreatedAt),
		UpdatedAt:               optionalTime(message.UpdatedAt),
		SentAt:                  optionalTime(message.SentAt),
		DeliveredAt:             optionalTime(message.DeliveredAt),
		AckRequired:             message.AckRequired,
		BodySizeBytes:           len(message.Body),
	}
}

func terminalPublicationStatusForResponse(message Message) string {
	if message.TerminalReason == "" && message.TerminalPublishStatus == "" {
		return ""
	}
	return terminalPublicationStatusValue(message.TerminalPublishStatus)
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
