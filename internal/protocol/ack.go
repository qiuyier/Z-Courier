package protocol

import sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"

type AckCode = sdkprotocol.AckCode

const (
	AckAccepted        = sdkprotocol.AckAccepted
	AckDecodeFailed    = sdkprotocol.AckDecodeFailed
	AckUnauthorized    = sdkprotocol.AckUnauthorized
	AckAuthUnavailable = sdkprotocol.AckAuthUnavailable
	AckRejected        = sdkprotocol.AckRejected
)

type Ack = sdkprotocol.Ack

func NewAckPacket(origin *Packet, code AckCode, reason string) (*Packet, error) {
	return sdkprotocol.NewAckPacket(origin, code, reason)
}

func DecodeAck(packet *Packet) (Ack, error) {
	return sdkprotocol.DecodeAck(packet)
}
