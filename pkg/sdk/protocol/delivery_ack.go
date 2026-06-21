package protocol

import (
	"fmt"

	"github.com/bytedance/sonic"
)

// DeliveryAckCode describes the client's downlink processing result.
type DeliveryAckCode string

const (
	// DeliveryAckDelivered confirms that the application processed a downlink.
	DeliveryAckDelivered DeliveryAckCode = "delivered"
)

// DeliveryAck is the JSON body carried by a MsgIDDownlinkAck packet.
type DeliveryAck struct {
	MessageID string          `json:"message_id"`
	Code      DeliveryAckCode `json:"code"`
}

// EncodeDeliveryAck creates the canonical JSON body for a delivery ACK.
func EncodeDeliveryAck(ack DeliveryAck) ([]byte, error) {
	if ack.MessageID == "" || ack.Code != DeliveryAckDelivered {
		return nil, ErrInvalidDeliveryAck
	}
	body, err := sonic.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDeliveryAck, err)
	}
	return body, nil
}

// DecodeDeliveryAck validates a MsgIDDownlinkAck packet and decodes its body.
func DecodeDeliveryAck(packet *Packet) (DeliveryAck, error) {
	if packet == nil || packet.MsgID != MsgIDDownlinkAck {
		return DeliveryAck{}, ErrInvalidDeliveryAck
	}
	var ack DeliveryAck
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		return DeliveryAck{}, fmt.Errorf("%w: %v", ErrInvalidDeliveryAck, err)
	}
	if ack.MessageID == "" || ack.Code != DeliveryAckDelivered {
		return DeliveryAck{}, ErrInvalidDeliveryAck
	}
	return ack, nil
}
