package protocol

import sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"

var (
	ErrNilPacket          = sdkprotocol.ErrNilPacket
	ErrInvalidMagic       = sdkprotocol.ErrInvalidMagic
	ErrUnsupportedVersion = sdkprotocol.ErrUnsupportedVersion
	ErrBodyTooLarge       = sdkprotocol.ErrBodyTooLarge
	ErrPacketTooShort     = sdkprotocol.ErrPacketTooShort
	ErrLengthMismatch     = sdkprotocol.ErrLengthMismatch
	ErrFieldTooLarge      = sdkprotocol.ErrFieldTooLarge
	ErrInvalidAck         = sdkprotocol.ErrInvalidAck
)
