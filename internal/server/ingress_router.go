package server

import (
	"context"
	"errors"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

type IngressRouter struct {
	znet.BaseRouter

	logger      *zap.Logger
	connManager ziface.IConnManager
	chain       *pipeline.Chain
	upstream    *router.Engine
}

const sessionIDProperty = "z-courier.session_id"

func NewIngressRouter(
	logger *zap.Logger,
	connManager ziface.IConnManager,
	chain *pipeline.Chain,
	upstream *router.Engine,
) *IngressRouter {
	if logger == nil {
		logger = defaultLogger()
	}

	return &IngressRouter{
		logger:      logger,
		connManager: connManager,
		chain:       chain,
		upstream:    upstream,
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
		metrics.RecordIngressPacket(request.GetMsgID(), "rejected")
		metrics.RecordIngressRejected(request.GetMsgID(), protocol.AckDecodeFailed)
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

	pipelineContext := pipeline.NewContext(request, packet, r.logger)
	if err := r.chain.Run(pipelineContext); err != nil {
		code, reason := pipeline.AckError(err)
		metrics.RecordIngressPacket(packet.MsgID, "rejected")
		metrics.RecordIngressRejected(packet.MsgID, code)
		r.sendAck(request, packet, code, reason)
		return
	}

	if pipelineContext.BindResult != nil {
		r.stopReplacedConnection(request.GetConnection().GetConnID(), pipelineContext.BindResult.Replaced)
	}

	if !r.forwardUpstream(request, packet) {
		return
	}

	metrics.RecordIngressPacket(packet.MsgID, "accepted")
	r.sendAck(request, packet, protocol.AckAccepted, "")
}

func (r *IngressRouter) forwardUpstream(request ziface.IRequest, packet *protocol.Packet) bool {
	if r.upstream == nil {
		return true
	}

	startedAt := time.Now()
	result, err := r.upstream.Forward(connectionContext(request.GetConnection()), packet)
	duration := time.Since(startedAt)
	if errors.Is(err, router.ErrRouteNotFound) {
		r.logger.Debug(
			"no upstream route matched",
			zap.Uint32("msg_id", packet.MsgID),
			zap.String("client_id", packet.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
		)
		return true
	}
	if err != nil {
		metrics.RecordUpstreamForward(resultRouteName(result), resultTargetType(result), "failure", duration)
		metrics.RecordIngressPacket(packet.MsgID, "rejected")
		metrics.RecordIngressRejected(packet.MsgID, protocol.AckRejected)
		r.logger.Warn(
			"failed to forward upstream packet",
			zap.Uint32("msg_id", packet.MsgID),
			zap.String("client_id", packet.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.Error(err),
		)
		r.sendAck(request, packet, protocol.AckRejected, err.Error())
		return false
	}

	metrics.RecordUpstreamForward(result.RouteName, result.TargetType, "success", duration)
	r.logger.Info(
		"forwarded upstream packet",
		zap.Uint32("msg_id", packet.MsgID),
		zap.String("route", result.RouteName),
		zap.String("target_type", result.TargetType),
		zap.String("status", result.Status),
		zap.Int("status_code", result.StatusCode),
		zap.String("client_id", packet.ClientID),
		zap.String("device_id", packet.DeviceID),
		zap.String("message_id", packet.MessageID),
		zap.String("trace_id", packet.TraceID),
	)
	return true
}

func resultRouteName(result *router.ForwardResult) string {
	if result == nil {
		return ""
	}

	return result.RouteName
}

func resultTargetType(result *router.ForwardResult) string {
	if result == nil {
		return ""
	}

	return result.TargetType
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
