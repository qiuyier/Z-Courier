package server

import (
	"context"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type IngressRouter struct {
	znet.BaseRouter

	logger      *zap.Logger
	verifier    auth.Verifier
	sessions    *session.Manager
	connManager ziface.IConnManager
	gatewayNode string
}

const sessionIDProperty = "z-courier.session_id"

func NewIngressRouter(
	logger *zap.Logger,
	verifier auth.Verifier,
	sessions *session.Manager,
	connManager ziface.IConnManager,
	gatewayNode string,
) *IngressRouter {
	if logger == nil {
		logger = defaultLogger()
	}

	return &IngressRouter{
		logger:      logger,
		verifier:    verifier,
		sessions:    sessions,
		connManager: connManager,
		gatewayNode: gatewayNode,
	}
}

func (r *IngressRouter) Handle(request ziface.IRequest) {
	packet, err := protocol.Decode(request.GetData())
	if err != nil {
		r.logger.Warn(
			"failed to decode upstream packet",
			zap.Uint32("msg_id", request.GetMsgID()),
			zap.Error(err),
		)
		r.sendAck(request, nil, protocol.AckDecodeFailed, err.Error())
		return
	}

	if packet.MsgID != request.GetMsgID() {
		r.logger.Warn(
			"outer zinx msg id does not match protocol msg id",
			zap.Uint32("outer_msg_id", request.GetMsgID()),
			zap.Uint32("packet_msg_id", packet.MsgID),
			zap.String("client_id", packet.ClientID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
		)
	}

	principal, err := r.verifier.Verify(connectionContext(request.GetConnection()), packet.Token)
	if err != nil {
		r.logger.Warn(
			"failed to verify upstream token",
			zap.Uint32("msg_id", packet.MsgID),
			zap.String("claimed_client_id", packet.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.Error(err),
		)
		r.sendAck(request, packet, protocol.AckUnauthorized, err.Error())
		return
	}

	if packet.ClientID != "" && packet.ClientID != principal.ClientID {
		r.logger.Warn(
			"packet client id differs from token principal",
			zap.String("claimed_client_id", packet.ClientID),
			zap.String("principal_client_id", principal.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
		)
	}

	bindResult, err := r.sessions.Bind(session.BindInput{
		ConnID:      request.GetConnection().GetConnID(),
		ClientID:    principal.ClientID,
		DeviceID:    packet.DeviceID,
		TokenID:     principal.TokenID,
		GatewayNode: r.gatewayNode,
	})
	if err != nil {
		r.logger.Warn(
			"failed to bind session",
			zap.Uint32("msg_id", packet.MsgID),
			zap.String("client_id", principal.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.Error(err),
		)
		r.sendAck(request, packet, protocol.AckRejected, err.Error())
		return
	}

	packet.ClientID = bindResult.Session.ClientID
	packet.SessionID = bindResult.Session.SessionID
	request.GetConnection().SetProperty(sessionIDProperty, bindResult.Session.SessionID)
	r.stopReplacedConnection(request.GetConnection().GetConnID(), bindResult.Replaced)

	r.logger.Info(
		"accepted upstream packet",
		zap.Uint32("msg_id", packet.MsgID),
		zap.String("client_id", packet.ClientID),
		zap.String("device_id", packet.DeviceID),
		zap.String("session_id", packet.SessionID),
		zap.String("message_id", packet.MessageID),
		zap.String("trace_id", packet.TraceID),
		zap.Int("body_size", len(packet.Body)),
	)

	r.sendAck(request, packet, protocol.AckAccepted, "")
}

func (r *IngressRouter) stopReplacedConnection(currentConnID uint64, replaced *session.Session) {
	if replaced == nil || replaced.ConnID == currentConnID || r.connManager == nil {
		return
	}

	conn, err := r.connManager.Get(replaced.ConnID)
	if err != nil {
		r.logger.Warn(
			"replaced session connection was not found",
			zap.Uint64("conn_id", replaced.ConnID),
			zap.String("session_id", replaced.SessionID),
			zap.String("client_id", replaced.ClientID),
			zap.String("device_id", replaced.DeviceID),
			zap.Error(err),
		)
		return
	}

	r.logger.Info(
		"closing replaced session connection",
		zap.Uint64("conn_id", replaced.ConnID),
		zap.String("session_id", replaced.SessionID),
		zap.String("client_id", replaced.ClientID),
		zap.String("device_id", replaced.DeviceID),
	)
	conn.Stop()
}

func (r *IngressRouter) sendAck(request ziface.IRequest, origin *protocol.Packet, code protocol.AckCode, reason string) {
	ackPacket, err := protocol.NewAckPacket(origin, code, reason)
	if err != nil {
		r.logger.Error("failed to build ack packet", zap.Error(err))
		return
	}

	ackData, err := protocol.Encode(ackPacket)
	if err != nil {
		r.logger.Error("failed to encode ack packet", zap.Error(err))
		return
	}

	if err := request.GetConnection().SendMsg(protocol.MsgIDAck, ackData); err != nil {
		r.logger.Warn("failed to send ack packet", zap.Error(err))
	}
}

func connectionContext(conn ziface.IConnection) context.Context {
	if conn == nil || conn.Context() == nil {
		return context.Background()
	}

	return conn.Context()
}
