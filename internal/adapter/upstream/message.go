package upstream

import "github.com/qiuyier/Z-Courier/internal/protocol"

type Message struct {
	Version   uint8          `json:"version"`
	Flags     protocol.Flags `json:"flags"`
	MsgID     uint32         `json:"msg_id"`
	Seq       uint64         `json:"seq"`
	Timestamp int64          `json:"timestamp"`

	ClientID  string `json:"client_id"`
	DeviceID  string `json:"device_id"`
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	TraceID   string `json:"trace_id"`
	Body      []byte `json:"body"`
}

func NewMessage(packet *protocol.Packet) Message {
	return Message{
		Version:   packet.Version,
		Flags:     packet.Flags,
		MsgID:     packet.MsgID,
		Seq:       packet.Seq,
		Timestamp: packet.Timestamp,
		ClientID:  packet.ClientID,
		DeviceID:  packet.DeviceID,
		SessionID: packet.SessionID,
		MessageID: packet.MessageID,
		TraceID:   packet.TraceID,
		Body:      packet.Body,
	}
}
