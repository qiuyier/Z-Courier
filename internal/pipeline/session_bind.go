package pipeline

import (
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type SessionBindHandler struct {
	Sessions        *session.Manager
	GatewayNode     string
	SessionProperty string
	Logger          *zap.Logger
}

func NewSessionBindHandler(sessions *session.Manager, gatewayNode, sessionProperty string, logger *zap.Logger) *SessionBindHandler {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &SessionBindHandler{
		Sessions:        sessions,
		GatewayNode:     gatewayNode,
		SessionProperty: sessionProperty,
		Logger:          logger,
	}
}

func (h *SessionBindHandler) Handle(ctx *Context) error {
	if h.Sessions == nil {
		return Reject(protocol.AckRejected, fmt.Errorf("session manager is not configured"))
	}
	if ctx.Principal == nil {
		return Reject(protocol.AckUnauthorized, fmt.Errorf("principal is not verified"))
	}

	bindResult, err := h.Sessions.Bind(session.BindInput{
		ConnID:      ctx.ConnID(),
		ClientID:    ctx.Principal.ClientID,
		DeviceID:    ctx.Packet.DeviceID,
		TokenID:     ctx.Principal.TokenID,
		GatewayNode: h.GatewayNode,
	})
	if err != nil {
		h.Logger.Warn(
			"failed to bind session",
			zap.Uint32("msg_id", ctx.Packet.MsgID),
			zap.String("client_id", ctx.Principal.ClientID),
			zap.String("device_id", ctx.Packet.DeviceID),
			zap.String("message_id", ctx.Packet.MessageID),
			zap.String("trace_id", ctx.Packet.TraceID),
			zap.Error(err),
		)
		return Reject(protocol.AckRejected, err)
	}

	ctx.Packet.ClientID = bindResult.Session.ClientID
	ctx.Packet.SessionID = bindResult.Session.SessionID
	ctx.BindResult = bindResult
	metrics.SetSessionsOnline(h.Sessions.Len())
	if conn := ctx.Conn(); conn != nil && h.SessionProperty != "" {
		conn.SetProperty(h.SessionProperty, bindResult.Session.SessionID)
	}

	return nil
}
