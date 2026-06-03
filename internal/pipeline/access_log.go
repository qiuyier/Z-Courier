package pipeline

import "go.uber.org/zap"

type AccessLogHandler struct {
	Logger *zap.Logger
}

func NewAccessLogHandler(logger *zap.Logger) *AccessLogHandler {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &AccessLogHandler{Logger: logger}
}

func (h *AccessLogHandler) Handle(ctx *Context) error {
	h.Logger.Info(
		"accepted upstream packet",
		zap.Uint32("msg_id", ctx.Packet.MsgID),
		zap.String("client_id", ctx.Packet.ClientID),
		zap.String("device_id", ctx.Packet.DeviceID),
		zap.String("session_id", ctx.Packet.SessionID),
		zap.String("message_id", ctx.Packet.MessageID),
		zap.String("trace_id", ctx.Packet.TraceID),
		zap.Int("body_size", len(ctx.Packet.Body)),
	)
	return nil
}
