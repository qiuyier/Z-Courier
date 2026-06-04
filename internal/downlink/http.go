package downlink

import (
	"errors"
	"io"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const InternalTokenHeader = "X-ZCourier-Internal-Token"

type HandlerConfig struct {
	Service            *Service
	InternalToken      string
	MaxRequestBodySize int64
	Logger             *zap.Logger
}

func NewHandler(config HandlerConfig) http.Handler {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	if config.MaxRequestBodySize <= 0 {
		config.MaxRequestBodySize = 10 << 20
	}

	return &handler{config: config}
}

type handler struct {
	config HandlerConfig
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		metrics.RecordDownlinkPush(0, "method_not_allowed")
		writeJSON(w, http.StatusMethodNotAllowed, PushResponse{Code: "method_not_allowed"})
		return
	}

	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		metrics.RecordDownlinkPush(0, "unauthorized")
		writeJSON(w, http.StatusUnauthorized, PushResponse{Code: "unauthorized"})
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		metrics.RecordDownlinkPush(0, "request_too_large")
		writeJSON(w, http.StatusRequestEntityTooLarge, PushResponse{Code: "request_too_large", Reason: err.Error()})
		return
	}

	var req PushRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		metrics.RecordDownlinkPush(0, "bad_request")
		writeJSON(w, http.StatusBadRequest, PushResponse{Code: "bad_request", Reason: err.Error()})
		return
	}

	resp, err := h.config.Service.Push(r.Context(), req)
	if err != nil {
		status := statusFromError(err)
		metrics.RecordDownlinkPush(req.MsgID, codeFromStatus(status))
		h.config.Logger.Warn(
			"downlink push failed",
			zap.String("client_id", req.ClientID),
			zap.String("device_id", req.DeviceID),
			zap.Uint32("msg_id", req.MsgID),
			zap.String("message_id", req.MessageID),
			zap.String("trace_id", req.TraceID),
			zap.Error(err),
		)
		writeJSON(w, status, PushResponse{
			Code:      codeFromStatus(status),
			Reason:    err.Error(),
			ClientID:  req.ClientID,
			DeviceID:  req.DeviceID,
			MessageID: req.MessageID,
			TraceID:   req.TraceID,
		})
		return
	}

	status := http.StatusOK
	if resp.DeliveryState == DeliveryStateQueued {
		status = http.StatusAccepted
	}

	metrics.RecordDownlinkPush(req.MsgID, nonEmpty(resp.DeliveryState, "success"))
	h.config.Logger.Info(
		"downlink push accepted",
		zap.String("delivery_state", resp.DeliveryState),
		zap.String("client_id", resp.ClientID),
		zap.String("device_id", resp.DeviceID),
		zap.String("session_id", resp.SessionID),
		zap.Uint64("conn_id", resp.ConnID),
		zap.String("message_id", resp.MessageID),
		zap.String("trace_id", resp.TraceID),
	)

	writeJSON(w, status, *resp)
}

func statusFromError(err error) int {
	switch {
	case errors.Is(err, ErrMissingClientID), errors.Is(err, ErrMissingDeviceID), errors.Is(err, ErrInvalidMsgID):
		return http.StatusBadRequest
	case errors.Is(err, ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConnectionNotFound):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func codeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusNotFound:
		return "session_not_found"
	case http.StatusConflict:
		return "connection_not_found"
	default:
		return "push_failed"
	}
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func writeJSON(w http.ResponseWriter, status int, resp PushResponse) {
	data, err := sonic.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
