package pipeline

import (
	"testing"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestPolicyHandlerBlocksClient(t *testing.T) {
	handler := NewPolicyHandler(PolicyConfig{BlockClientIDs: []string{"client-a"}})
	ctx := policyContext("client-a", 1000)

	err := handler.Handle(ctx)
	code, _ := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckRejected)
	}
}

func TestPolicyHandlerAllowlistRejectsMissingClient(t *testing.T) {
	handler := NewPolicyHandler(PolicyConfig{AllowClientIDs: []string{"client-a"}})
	ctx := policyContext("client-b", 1000)

	err := handler.Handle(ctx)
	code, _ := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckRejected)
	}
}

func TestPolicyHandlerAllowsMatchingClientAndMsgID(t *testing.T) {
	handler := NewPolicyHandler(PolicyConfig{
		AllowClientIDs: []string{"client-a"},
		AllowMsgIDs:    []uint32{1000},
	})
	ctx := policyContext("client-a", 1000)

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func policyContext(clientID string, msgID uint32) *Context {
	return &Context{
		Principal: &auth.Principal{ClientID: clientID},
		Packet:    protocol.NewPacket(msgID, nil),
	}
}
