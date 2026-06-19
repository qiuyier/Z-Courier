package protocol

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
)

// AckCode is the stable gateway ACK result code.
type AckCode string

const (
	AckAccepted        AckCode = "accepted"
	AckDecodeFailed    AckCode = "decode_failed"
	AckUnauthorized    AckCode = "unauthorized"
	AckAuthUnavailable AckCode = "auth_unavailable"
	AckRejected        AckCode = "rejected"
)

// Ack is the JSON body carried by a MsgIDAck packet.
type Ack struct {
	Code      AckCode `json:"code"`
	MsgID     uint32  `json:"msg_id"`
	MessageID string  `json:"message_id,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// NewAckPacket creates a gateway ACK derived from origin metadata.
func NewAckPacket(origin *Packet, code AckCode, reason string) (*Packet, error) {
	ack := Ack{
		Code:   code,
		Reason: reason,
	}

	packet := NewPacket(MsgIDAck, nil)
	packet.Timestamp = time.Now().UnixMilli()

	if origin != nil {
		ack.MsgID = origin.MsgID
		ack.MessageID = origin.MessageID
		packet.ClientID = origin.ClientID
		packet.DeviceID = origin.DeviceID
		packet.SessionID = origin.SessionID
		packet.MessageID = origin.MessageID
		packet.TraceID = origin.TraceID
		packet.Seq = origin.Seq
	}

	body, err := sonic.Marshal(ack)
	if err != nil {
		return nil, err
	}

	packet.Body = body
	return packet, nil
}

// DecodeAck validates and decodes the JSON body of a gateway ACK packet.
func DecodeAck(packet *Packet) (Ack, error) {
	if packet == nil || packet.MsgID != MsgIDAck {
		return Ack{}, ErrInvalidAck
	}
	var ack Ack
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		return Ack{}, fmt.Errorf("%w: %v", ErrInvalidAck, err)
	}
	if ack.Code == "" {
		return Ack{}, ErrInvalidAck
	}
	return ack, nil
}
