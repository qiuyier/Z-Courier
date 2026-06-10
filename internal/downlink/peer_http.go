package downlink

import (
	"errors"
	"io"
	"net/http"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

type PeerHandlerConfig struct {
	Service            *Service
	GatewayNode        string
	PeerToken          string
	MaxRequestBodySize int64
	Logger             *zap.Logger
}

func NewPeerHandler(config PeerHandlerConfig) http.Handler {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	if config.MaxRequestBodySize <= 0 {
		config.MaxRequestBodySize = 10 << 20
	}

	return &peerHandler{config: config}
}

type peerHandler struct {
	config PeerHandlerConfig
}

func (h *peerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, PeerPushResponse{Code: "method_not_allowed"})
		return
	}

	if h.config.PeerToken != "" && r.Header.Get(InternalTokenHeader) != h.config.PeerToken {
		writeJSON(w, http.StatusUnauthorized, PeerPushResponse{Code: "unauthorized"})
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.MaxRequestBodySize))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, PeerPushResponse{Code: "request_too_large", Reason: err.Error()})
		return
	}

	var req PeerPushRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, PeerPushResponse{Code: "bad_request", Reason: err.Error()})
		return
	}

	resp, err := h.config.Service.PushPeer(r.Context(), req, h.config.GatewayNode)
	if err != nil {
		status := peerStatusFromError(err)
		h.config.Logger.Warn(
			"peer downlink push failed",
			zap.String("origin_node", req.OriginNode),
			zap.String("client_id", req.ClientID),
			zap.String("device_id", req.DeviceID),
			zap.String("session_id", req.SessionID),
			zap.Uint32("msg_id", req.MsgID),
			zap.String("message_id", req.MessageID),
			zap.String("trace_id", req.TraceID),
			zap.Error(err),
		)
		writeJSON(w, status, PeerPushResponse{
			Code:        peerCodeFromError(err, status),
			Reason:      err.Error(),
			GatewayNode: h.config.GatewayNode,
			ClientID:    req.ClientID,
			DeviceID:    req.DeviceID,
			SessionID:   req.SessionID,
			MessageID:   req.MessageID,
			TraceID:     req.TraceID,
		})
		return
	}

	h.config.Logger.Info(
		"peer downlink push accepted",
		zap.String("origin_node", req.OriginNode),
		zap.String("delivery_state", resp.DeliveryState),
		zap.String("client_id", resp.ClientID),
		zap.String("device_id", resp.DeviceID),
		zap.String("session_id", resp.SessionID),
		zap.Uint64("conn_id", resp.ConnID),
		zap.String("message_id", resp.MessageID),
		zap.String("trace_id", resp.TraceID),
	)

	writeJSON(w, http.StatusOK, *resp)
}

func peerStatusFromError(err error) int {
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

func peerCodeFromError(err error, status int) string {
	switch {
	case errors.Is(err, ErrSessionMismatch):
		return "session_mismatch"
	default:
		return codeFromStatus(status)
	}
}
