package pipeline

import (
	"errors"
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"go.uber.org/zap"
)

type AuthHandler struct {
	Verifier auth.Verifier
	Logger   *zap.Logger
}

func NewAuthHandler(verifier auth.Verifier, logger *zap.Logger) *AuthHandler {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &AuthHandler{
		Verifier: verifier,
		Logger:   logger,
	}
}

func (h *AuthHandler) Handle(ctx *Context) error {
	if h.Verifier == nil {
		return Reject(protocol.AckUnauthorized, fmt.Errorf("auth verifier is not configured"))
	}

	principal, err := h.Verifier.Verify(ctx.Context(), ctx.Packet.Token)
	if err != nil {
		h.Logger.Warn(
			"failed to verify upstream token",
			zap.Uint32("msg_id", ctx.Packet.MsgID),
			zap.String("claimed_client_id", ctx.Packet.ClientID),
			zap.String("device_id", ctx.Packet.DeviceID),
			zap.String("message_id", ctx.Packet.MessageID),
			zap.String("trace_id", ctx.Packet.TraceID),
			zap.Error(err),
		)
		return Reject(authAckCode(err), err)
	}
	if principal == nil || principal.ClientID == "" {
		return Reject(protocol.AckUnauthorized, fmt.Errorf("auth verifier returned empty principal"))
	}

	if ctx.Packet.ClientID != "" && ctx.Packet.ClientID != principal.ClientID {
		h.Logger.Warn(
			"packet client id differs from token principal",
			zap.String("claimed_client_id", ctx.Packet.ClientID),
			zap.String("principal_client_id", principal.ClientID),
			zap.String("device_id", ctx.Packet.DeviceID),
			zap.String("message_id", ctx.Packet.MessageID),
			zap.String("trace_id", ctx.Packet.TraceID),
		)
	}

	ctx.Principal = clonePrincipal(principal)
	return nil
}

func authAckCode(err error) protocol.AckCode {
	switch {
	case errors.Is(err, auth.ErrProviderTimeout), errors.Is(err, auth.ErrProviderUnavailable):
		return protocol.AckAuthUnavailable
	case errors.Is(err, auth.ErrMisconfigured):
		return protocol.AckRejected
	default:
		return protocol.AckUnauthorized
	}
}

func clonePrincipal(in *auth.Principal) *auth.Principal {
	if in == nil {
		return nil
	}

	return &auth.Principal{
		ClientID: in.ClientID,
		TokenID:  in.TokenID,
		Subject:  in.Subject,
		Scopes:   append([]string(nil), in.Scopes...),
	}
}
