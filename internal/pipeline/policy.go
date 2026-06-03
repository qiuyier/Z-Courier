package pipeline

import (
	"fmt"

	"github.com/qiuyier/Z-Courier/internal/protocol"
)

type PolicyHandler struct {
	allowClientIDs map[string]struct{}
	blockClientIDs map[string]struct{}
	allowMsgIDs    map[uint32]struct{}
	blockMsgIDs    map[uint32]struct{}
}

func NewPolicyHandler(config PolicyConfig) *PolicyHandler {
	return &PolicyHandler{
		allowClientIDs: stringSet(config.AllowClientIDs),
		blockClientIDs: stringSet(config.BlockClientIDs),
		allowMsgIDs:    uint32Set(config.AllowMsgIDs),
		blockMsgIDs:    uint32Set(config.BlockMsgIDs),
	}
}

func (h *PolicyHandler) Handle(ctx *Context) error {
	clientID := ctx.Packet.ClientID
	if ctx.Principal != nil && ctx.Principal.ClientID != "" {
		clientID = ctx.Principal.ClientID
	}
	msgID := ctx.Packet.MsgID

	if _, ok := h.blockClientIDs[clientID]; ok {
		return Reject(protocol.AckRejected, fmt.Errorf("client %q is blocked", clientID))
	}
	if _, ok := h.blockMsgIDs[msgID]; ok {
		return Reject(protocol.AckRejected, fmt.Errorf("msg_id %d is blocked", msgID))
	}
	if len(h.allowClientIDs) > 0 {
		if _, ok := h.allowClientIDs[clientID]; !ok {
			return Reject(protocol.AckRejected, fmt.Errorf("client %q is not allowed", clientID))
		}
	}
	if len(h.allowMsgIDs) > 0 {
		if _, ok := h.allowMsgIDs[msgID]; !ok {
			return Reject(protocol.AckRejected, fmt.Errorf("msg_id %d is not allowed", msgID))
		}
	}

	return nil
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = struct{}{}
		}
	}

	return out
}

func uint32Set(values []uint32) map[uint32]struct{} {
	out := make(map[uint32]struct{}, len(values))
	for _, value := range values {
		if value != 0 {
			out[value] = struct{}{}
		}
	}

	return out
}
