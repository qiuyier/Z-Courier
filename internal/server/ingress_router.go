package server

import (
	"log/slog"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

type IngressRouter struct {
	znet.BaseRouter

	logger *slog.Logger
}

func NewIngressRouter(logger *slog.Logger) *IngressRouter {
	return &IngressRouter{logger: logger}
}

func (r *IngressRouter) Handle(request ziface.IRequest) {
	packet, err := protocol.Decode(request.GetData())
	if err != nil {
		r.logger.Warn("failed to decode upstream packet", "msg_id", request.GetMsgID(), "error", err)
		r.sendAck(request, nil, protocol.AckDecodeFailed, err.Error())
		return
	}

	if packet.MsgID != request.GetMsgID() {
		r.logger.Warn(
			"outer zinx msg id does not match protocol msg id",
			"outer_msg_id", request.GetMsgID(),
			"packet_msg_id", packet.MsgID,
			"client_id", packet.ClientID,
			"message_id", packet.MessageID,
			"trace_id", packet.TraceID,
		)
	}

	r.logger.Info(
		"accepted upstream packet",
		"msg_id", packet.MsgID,
		"client_id", packet.ClientID,
		"device_id", packet.DeviceID,
		"message_id", packet.MessageID,
		"trace_id", packet.TraceID,
		"body_size", len(packet.Body),
	)

	r.sendAck(request, packet, protocol.AckAccepted, "")
}

func (r *IngressRouter) sendAck(request ziface.IRequest, origin *protocol.Packet, code protocol.AckCode, reason string) {
	ackPacket, err := protocol.NewAckPacket(origin, code, reason)
	if err != nil {
		r.logger.Error("failed to build ack packet", "error", err)
		return
	}

	ackData, err := protocol.Encode(ackPacket)
	if err != nil {
		r.logger.Error("failed to encode ack packet", "error", err)
		return
	}

	if err := request.GetConnection().SendMsg(protocol.MsgIDAck, ackData); err != nil {
		r.logger.Warn("failed to send ack packet", "error", err)
	}
}
