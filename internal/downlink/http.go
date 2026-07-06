package downlink

import (
	"errors"
	"io"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/capacity"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"go.uber.org/zap"
)

type HandlerConfig struct {
	Service            *Service
	InternalToken      string
	MaxRequestBodySize int64
	MaxBatchMessages   int
	RetryScanLimit     int
	GatewayNode        string
	PushLimiter        *capacity.Limiter
	Logger             *zap.Logger
	Audit              adminaudit.Recorder
}

func NewHandler(config HandlerConfig) http.Handler {
	return &handler{config: normalizeHandlerConfig(config)}
}

func NewBatchHandler(config HandlerConfig) http.Handler {
	return &handler{config: normalizeHandlerConfig(config), batch: true}
}

func normalizeHandlerConfig(config HandlerConfig) HandlerConfig {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	if config.MaxRequestBodySize <= 0 {
		config.MaxRequestBodySize = 10 << 20
	}
	if config.MaxBatchMessages <= 0 {
		config.MaxBatchMessages = 100
	}
	if config.RetryScanLimit <= 0 {
		config.RetryScanLimit = 100
	}

	return config
}

type handler struct {
	config HandlerConfig
	batch  bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		metrics.RecordDownlinkPush(0, "method_not_allowed")
		h.writeFailure(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}

	if h.config.InternalToken != "" && r.Header.Get(InternalTokenHeader) != h.config.InternalToken {
		metrics.RecordDownlinkPush(0, "unauthorized")
		h.writeFailure(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	if !h.acquireCapacity(w, r) {
		return
	}
	defer h.releaseCapacity(r)

	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		metrics.RecordDownlinkPush(0, "request_too_large")
		h.writeFailure(w, http.StatusRequestEntityTooLarge, "request_too_large", err.Error())
		return
	}

	if h.batch {
		h.serveBatch(w, r, body)
		return
	}

	h.serveSingle(w, r, body)
}

func (h *handler) acquireCapacity(w http.ResponseWriter, r *http.Request) bool {
	if h.config.PushLimiter == nil {
		return true
	}

	path := r.URL.Path
	if !h.config.PushLimiter.TryAcquire() {
		metrics.RecordDownlinkPush(0, resilience.ReasonOverloaded)
		metrics.RecordInternalHTTPOverloadRejected(path)
		h.writeFailure(w, http.StatusTooManyRequests, resilience.ReasonOverloaded, "internal push capacity exceeded")
		return false
	}

	metrics.AddInternalHTTPInFlight(path, 1)
	return true
}

func (h *handler) releaseCapacity(r *http.Request) {
	if h.config.PushLimiter == nil {
		return
	}

	h.config.PushLimiter.Release()
	metrics.AddInternalHTTPInFlight(r.URL.Path, -1)
}

func (h *handler) serveSingle(w http.ResponseWriter, r *http.Request, body []byte) {
	var req PushRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		metrics.RecordDownlinkPush(0, "bad_request")
		h.writeFailure(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	resp, status := h.pushOne(r, req)
	writeJSON(w, status, resp)
}

func (h *handler) serveBatch(w http.ResponseWriter, r *http.Request, body []byte) {
	var req BatchPushRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		metrics.RecordDownlinkPush(0, "bad_request")
		h.writeFailure(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.Messages) == 0 {
		metrics.RecordDownlinkPush(0, "bad_request")
		h.writeFailure(w, http.StatusBadRequest, "bad_request", "messages is required")
		return
	}
	if len(req.Messages) > h.config.MaxBatchMessages {
		metrics.RecordDownlinkPush(0, "bad_request")
		h.writeFailure(w, http.StatusBadRequest, "bad_request", "too many messages")
		return
	}

	resp := BatchPushResponse{
		Code:    "ok",
		Total:   len(req.Messages),
		Results: make([]PushResponse, 0, len(req.Messages)),
	}
	for _, message := range req.Messages {
		itemResp, status := h.pushOne(r, message)
		resp.Results = append(resp.Results, itemResp)
		if status >= http.StatusBadRequest {
			resp.Failed++
			continue
		}
		resp.Success++
	}

	status := http.StatusOK
	switch {
	case resp.Failed == 0:
		resp.Code = "ok"
	case resp.Success == 0:
		resp.Code = "failed"
		status = http.StatusMultiStatus
	default:
		resp.Code = "partial_failure"
		status = http.StatusMultiStatus
	}

	writeJSON(w, status, resp)
}

func (h *handler) pushOne(r *http.Request, req PushRequest) (PushResponse, int) {
	resp, err := h.config.Service.Push(r.Context(), req)
	if err != nil {
		status := statusFromError(err)
		code := pushFailureCode(err, status)
		metrics.RecordDownlinkPush(req.MsgID, code)
		h.config.Logger.Warn(
			"downlink push failed",
			zap.String("client_id", req.ClientID),
			zap.String("device_id", req.DeviceID),
			zap.Uint32("msg_id", req.MsgID),
			zap.String("message_id", req.MessageID),
			zap.String("trace_id", req.TraceID),
			zap.Error(err),
		)
		failureResp := PushResponse{
			Code:      code,
			Reason:    err.Error(),
			ClientID:  req.ClientID,
			DeviceID:  req.DeviceID,
			MessageID: req.MessageID,
			TraceID:   req.TraceID,
		}
		annotatePushResponseFailure(&failureResp, err)
		return failureResp, status
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

	return *resp, status
}

func (h *handler) writeFailure(w http.ResponseWriter, status int, code, reason string) {
	if h.batch {
		writeJSON(w, status, BatchPushResponse{Code: code, Reason: reason})
		return
	}
	writeJSON(w, status, PushResponse{Code: code, Reason: reason})
}

func statusFromError(err error) int {
	switch {
	case errors.Is(err, ErrMissingClientID), errors.Is(err, ErrMissingDeviceID), errors.Is(err, ErrMissingSessionID), errors.Is(err, ErrInvalidMsgID):
		return http.StatusBadRequest
	case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrSessionMismatch):
		return http.StatusNotFound
	case errors.Is(err, ErrConnectionNotFound):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func pushFailureCode(err error, status int) string {
	failure := deliveryFailureFromError(err)
	if failure.Code != "" && failure.Code != "error" {
		return failure.Code
	}
	return codeFromStatus(status)
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

func writeJSON(w http.ResponseWriter, status int, resp any) {
	data, err := sonic.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
