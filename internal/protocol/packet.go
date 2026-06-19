// Package protocol preserves the original internal import path while the
// canonical wire implementation lives in pkg/sdk/protocol.
package protocol

import sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"

const (
	Magic              = sdkprotocol.Magic
	Version            = sdkprotocol.Version
	FixedHeaderSize    = sdkprotocol.FixedHeaderSize
	DefaultMaxBodySize = sdkprotocol.DefaultMaxBodySize
)

const (
	MsgIDAck         = sdkprotocol.MsgIDAck
	MsgIDDownlinkAck = sdkprotocol.MsgIDDownlinkAck
	MsgIDBind        = sdkprotocol.MsgIDBind
)

type Flags = sdkprotocol.Flags

const (
	FlagAckRequired = sdkprotocol.FlagAckRequired
	FlagCompressed  = sdkprotocol.FlagCompressed
)

type Header = sdkprotocol.Header
type Packet = sdkprotocol.Packet

func NewPacket(msgID uint32, body []byte) *Packet {
	return sdkprotocol.NewPacket(msgID, body)
}

func IsReservedMsgID(msgID uint32) bool {
	return sdkprotocol.IsReservedMsgID(msgID)
}
