package protocol_test

import (
	"errors"
	"testing"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestDeliveryAckCodec(t *testing.T) {
	body, err := protocol.EncodeDeliveryAck(protocol.DeliveryAck{
		MessageID: "message-1",
		Code:      protocol.DeliveryAckDelivered,
	})
	if err != nil {
		t.Fatalf("EncodeDeliveryAck() error = %v", err)
	}
	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, body)
	ack, err := protocol.DecodeDeliveryAck(packet)
	if err != nil {
		t.Fatalf("DecodeDeliveryAck() error = %v", err)
	}
	if ack.MessageID != "message-1" || ack.Code != protocol.DeliveryAckDelivered {
		t.Fatalf("DecodeDeliveryAck() = %+v", ack)
	}
}

func TestDeliveryAckCodecRejectsInvalidValues(t *testing.T) {
	if _, err := protocol.EncodeDeliveryAck(protocol.DeliveryAck{}); !errors.Is(err, protocol.ErrInvalidDeliveryAck) {
		t.Fatalf("EncodeDeliveryAck() error = %v", err)
	}
	if _, err := protocol.EncodeDeliveryAck(protocol.DeliveryAck{MessageID: "message-1", Code: "failed"}); !errors.Is(err, protocol.ErrInvalidDeliveryAck) {
		t.Fatalf("EncodeDeliveryAck(unsupported code) error = %v", err)
	}
	if _, err := protocol.DecodeDeliveryAck(protocol.NewPacket(2001, []byte(`{}`))); !errors.Is(err, protocol.ErrInvalidDeliveryAck) {
		t.Fatalf("DecodeDeliveryAck() error = %v", err)
	}
	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, []byte(`{"code":"delivered"}`))
	if _, err := protocol.DecodeDeliveryAck(packet); !errors.Is(err, protocol.ErrInvalidDeliveryAck) {
		t.Fatalf("DecodeDeliveryAck(missing message_id) error = %v", err)
	}
}
