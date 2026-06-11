package pipeline

import (
	"errors"
	"fmt"
	"time"

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
	if ctx.Packet.MsgID != protocol.MsgIDBind {
		return h.handleBoundPacket(ctx)
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
	ctx.Session = bindResult.Session.Clone()
	metrics.SetSessionsOnline(h.Sessions.Len())
	if conn := ctx.Conn(); conn != nil && h.SessionProperty != "" {
		conn.SetProperty(h.SessionProperty, bindResult.Session.SessionID)
	}

	return nil
}

func (h *SessionBindHandler) handleBoundPacket(ctx *Context) error {
	found, err := h.Sessions.TouchByConnID(ctx.ConnID(), time.Now())
	if errors.Is(err, session.ErrNotFound) {
		return Reject(protocol.AckRejected, fmt.Errorf("session is not bound; send AUTH/BIND first"))
	}
	if err != nil {
		return Reject(protocol.AckRejected, err)
	}

	if found.ClientID != ctx.Principal.ClientID {
		h.Logger.Warn(
			"bound session client differs from token principal",
			zap.Uint32("msg_id", ctx.Packet.MsgID),
			zap.String("bound_client_id", found.ClientID),
			zap.String("principal_client_id", ctx.Principal.ClientID),
			zap.String("device_id", ctx.Packet.DeviceID),
			zap.String("message_id", ctx.Packet.MessageID),
			zap.String("trace_id", ctx.Packet.TraceID),
		)
		return Reject(protocol.AckUnauthorized, fmt.Errorf("token principal does not match bound session"))
	}
	if ctx.Packet.DeviceID != "" && ctx.Packet.DeviceID != found.DeviceID {
		return Reject(protocol.AckRejected, fmt.Errorf("packet device_id does not match bound session"))
	}
	if ctx.Packet.SessionID != "" && ctx.Packet.SessionID != found.SessionID {
		return Reject(protocol.AckRejected, fmt.Errorf("packet session_id does not match bound session"))
	}

	ctx.Packet.ClientID = found.ClientID
	ctx.Packet.DeviceID = found.DeviceID
	ctx.Packet.SessionID = found.SessionID
	ctx.Session = found.Clone()
	return nil
}
