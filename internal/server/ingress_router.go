package server

import (
	"context"
	"errors"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
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
	downlink    *downlink.Service
	flushLimit  int
}

const sessionIDProperty = "z-courier.session_id"

func NewIngressRouter(
	logger *zap.Logger,
	connManager ziface.IConnManager,
	chain *pipeline.Chain,
	upstream *router.Engine,
	downlinkService *downlink.Service,
	flushLimit int,
) *IngressRouter {
	if logger == nil {
		logger = defaultLogger()
	}

	return &IngressRouter{
		logger:      logger,
		connManager: connManager,
		chain:       chain,
		upstream:    upstream,
		downlink:    downlinkService,
		flushLimit:  flushLimit,
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
		r.flushDownlinkPending(pipelineContext.BindResult)
	}

	if packet.MsgID == protocol.MsgIDBind {
		metrics.RecordIngressPacket(packet.MsgID, "accepted")
		r.sendAck(request, packet, protocol.AckAccepted, "")
		return
	}

	if packet.MsgID == protocol.MsgIDDownlinkAck {
		r.handleDownlinkAck(request, packet, pipelineContext)
		return
	}

	if !r.forwardUpstream(request, packet) {
		return
	}

	metrics.RecordIngressPacket(packet.MsgID, "accepted")
	r.sendAck(request, packet, protocol.AckAccepted, "")
}

func (r *IngressRouter) handleDownlinkAck(request ziface.IRequest, packet *protocol.Packet, pipelineContext *pipeline.Context) {
	if r.downlink == nil {
		metrics.RecordDownlinkAck(0, "store_not_configured")
		r.sendAck(request, packet, protocol.AckRejected, downlink.ErrStoreNotConfigured.Error())
		return
	}
	bound := contextSession(pipelineContext)
	if bound == nil {
		metrics.RecordDownlinkAck(0, "session_not_bound")
		r.sendAck(request, packet, protocol.AckRejected, "session is not bound")
		return
	}

	var ack downlink.ClientAckRequest
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		metrics.RecordDownlinkAck(0, "bad_request")
		r.logger.Warn(
			"failed to decode downlink ack",
			zap.String("client_id", packet.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.Error(err),
		)
		r.sendAck(request, packet, protocol.AckRejected, err.Error())
		return
	}
	if ack.MessageID == "" {
		ack.MessageID = packet.MessageID
	}

	message, err := r.downlink.Ack(connectionContext(request.GetConnection()), bound.ClientID, bound.DeviceID, ack)
	if err != nil {
		metrics.RecordDownlinkAck(0, "rejected")
		r.logger.Warn(
			"failed to handle downlink ack",
			zap.String("client_id", bound.ClientID),
			zap.String("device_id", bound.DeviceID),
			zap.String("message_id", ack.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.Error(err),
		)
		r.sendAck(request, packet, protocol.AckRejected, err.Error())
		return
	}

	metrics.RecordDownlinkAck(message.MsgID, "delivered")
	if !message.SentAt.IsZero() && !message.DeliveredAt.IsZero() {
		metrics.ObserveDownlinkAckLatency(message.MsgID, message.DeliveredAt.Sub(message.SentAt))
	}
	metrics.RecordIngressPacket(packet.MsgID, "accepted")
	r.logger.Info(
		"handled downlink ack",
		zap.Uint32("msg_id", message.MsgID),
		zap.String("client_id", bound.ClientID),
		zap.String("device_id", bound.DeviceID),
		zap.String("message_id", message.MessageID),
		zap.String("trace_id", packet.TraceID),
	)
	r.sendAck(request, packet, protocol.AckAccepted, "")
}

func contextSession(ctx *pipeline.Context) *session.Session {
	if ctx == nil {
		return nil
	}
	if ctx.Session != nil {
		return ctx.Session
	}
	if ctx.BindResult != nil {
		return ctx.BindResult.Session
	}

	return nil
}

func (r *IngressRouter) flushDownlinkPending(bindResult *session.BindResult) {
	if r.downlink == nil || !r.downlink.HasStore() || bindResult == nil || bindResult.Session == nil {
		return
	}
	if !bindResult.Created {
		return
	}

	session := bindResult.Session.Clone()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		startedAt := time.Now()
		result, err := r.downlink.FlushClientDevice(ctx, session.ClientID, session.DeviceID, r.flushLimit)
		if err != nil {
			r.logger.Warn(
				"failed to flush pending downlink messages after session bind",
				zap.String("session_id", session.SessionID),
				zap.String("client_id", session.ClientID),
				zap.String("device_id", session.DeviceID),
				zap.Error(err),
			)
			return
		}
		if result.Scanned == 0 {
			return
		}

		r.logger.Info(
			"flushed pending downlink messages after session bind",
			zap.String("session_id", session.SessionID),
			zap.String("client_id", session.ClientID),
			zap.String("device_id", session.DeviceID),
			zap.Int("scanned", result.Scanned),
			zap.Int("sent", result.Sent),
			zap.Int("queued", result.Queued),
			zap.Int("failed", result.Failed),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}()
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
		metrics.RecordUpstreamForward(resultRouteName(result), resultTargetType(result), upstreamForwardResult(err), duration)
		metrics.RecordIngressPacket(packet.MsgID, "rejected")
		metrics.RecordIngressRejected(packet.MsgID, protocol.AckRejected)
		fields := []zap.Field{
			zap.Uint32("msg_id", packet.MsgID),
			zap.String("route", resultRouteName(result)),
			zap.String("target_type", resultTargetType(result)),
			zap.String("client_id", packet.ClientID),
			zap.String("device_id", packet.DeviceID),
			zap.String("message_id", packet.MessageID),
			zap.String("trace_id", packet.TraceID),
			zap.String("upstream_result", upstreamForwardResult(err)),
			zap.Error(err),
		}
		fields = append(fields, upstreamForwardMetadataFields(result, err)...)
		r.logger.Warn("failed to forward upstream packet", fields...)
		r.sendAck(request, packet, protocol.AckRejected, upstreamAckReason(err))
		return false
	}

	metrics.RecordUpstreamForward(result.RouteName, result.TargetType, "success", duration)
	fields := []zap.Field{
		zap.Uint32("msg_id", packet.MsgID),
		zap.String("route", result.RouteName),
		zap.String("target_type", result.TargetType),
		zap.String("status", result.Status),
		zap.Int("status_code", result.StatusCode),
		zap.String("client_id", packet.ClientID),
		zap.String("device_id", packet.DeviceID),
		zap.String("message_id", packet.MessageID),
		zap.String("trace_id", packet.TraceID),
	}
	fields = append(fields, upstreamForwardMetadataFields(result, nil)...)
	r.logger.Info("forwarded upstream packet", fields...)
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

func upstreamForwardResult(err error) string {
	if errors.Is(err, router.ErrOverloaded) {
		return resilience.ResultOverloaded
	}
	return resilience.ResultUpstreamFail
}

func upstreamAckReason(err error) string {
	if errors.Is(err, router.ErrOverloaded) {
		return resilience.ReasonOverloaded
	}
	return resilience.ReasonUpstreamFailed
}

func upstreamForwardMetadataFields(result *router.ForwardResult, err error) []zap.Field {
	fields := make([]zap.Field, 0, 8)
	if result != nil {
		if result.Endpoint != "" {
			fields = append(fields, zap.String("endpoint", result.Endpoint))
		}
		if result.Attempts > 0 {
			fields = append(fields, zap.Int("attempt_count", result.Attempts))
		}
		if result.MaxAttempts > 0 {
			fields = append(fields, zap.Int("max_attempts", result.MaxAttempts))
		}
		if result.Attempts > 1 {
			fields = append(fields, zap.Bool("failover_attempted", true))
		}
	}

	var forwardErr *router.ForwardError
	if errors.As(err, &forwardErr) && forwardErr != nil {
		fields = append(fields,
			zap.String("failure_class", string(forwardErr.Class)),
			zap.Bool("retryable", forwardErr.Retryable),
			zap.String("failover_decision", string(forwardErr.Decision)),
		)
	}
	return fields
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
