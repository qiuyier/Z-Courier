package protocol

import "bytes"

const (
	Magic           uint16 = 0x5A43
	Version         uint8  = 1
	FixedHeaderSize        = 41

	DefaultMaxBodySize = 4 << 20
)

const (
	MsgIDAck uint32 = 1
)

type Flags uint16

const (
	FlagAckRequired Flags = 1 << iota
	FlagCompressed
)

type Header struct {
	Version   uint8
	Flags     Flags
	MsgID     uint32
	Seq       uint64
	Timestamp int64
}

type Packet struct {
	Header

	ClientID  string
	DeviceID  string
	SessionID string
	MessageID string
	TraceID   string
	Token     string
	Body      []byte
}

func NewPacket(msgID uint32, body []byte) *Packet {
	return &Packet{
		Header: Header{
			Version: Version,
			MsgID:   msgID,
		},
		Body: cloneBytes(body),
	}
}

func cloneBytes(in []byte) []byte {
	return bytes.Clone(in)
}
