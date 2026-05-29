package protocol

import (
	"time"

	"github.com/bytedance/sonic"
)

type AckCode string

const (
	AckAccepted     AckCode = "accepted"
	AckDecodeFailed AckCode = "decode_failed"
	AckUnauthorized AckCode = "unauthorized"
	AckRejected     AckCode = "rejected"
)

type Ack struct {
	Code      AckCode `json:"code"`
	MsgID     uint32  `json:"msg_id"`
	MessageID string  `json:"message_id,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

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
