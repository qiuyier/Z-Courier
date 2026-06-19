package protocol

import "bytes"

const (
	// Magic identifies a Z-Courier packet on the wire.
	Magic uint16 = 0x5A43
	// Version is the current Z-Courier packet format version.
	Version uint8 = 1
	// FixedHeaderSize is the encoded header size before strings and body bytes.
	FixedHeaderSize = 41

	// DefaultMaxBodySize is the default body limit used by Decode.
	DefaultMaxBodySize = 4 << 20
)

const (
	// MsgIDAck identifies a gateway response ACK.
	MsgIDAck uint32 = 1
	// MsgIDDownlinkAck identifies a client delivery ACK.
	MsgIDDownlinkAck uint32 = 2
	// MsgIDBind identifies the AUTH/BIND control packet.
	MsgIDBind uint32 = 1000
)

// Flags controls optional packet behavior.
type Flags uint16

const (
	// FlagAckRequired asks the receiver to acknowledge the packet.
	FlagAckRequired Flags = 1 << iota
	// FlagCompressed marks a body encoded with an application-agreed
	// compression scheme. Z-Courier does not compress or decompress the body.
	FlagCompressed
)

// Header contains fixed-width packet metadata.
type Header struct {
	Version uint8
	Flags   Flags
	MsgID   uint32
	Seq     uint64
	// Timestamp is Unix time in milliseconds.
	Timestamp int64
}

// Packet is the transport-neutral Z-Courier protocol packet.
//
// Body is opaque to the gateway. String fields are encoded as UTF-8 bytes with
// uint16 lengths, and Body uses a uint32 length.
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

// NewPacket creates a versioned packet and clones body ownership.
func NewPacket(msgID uint32, body []byte) *Packet {
	return &Packet{
		Header: Header{
			Version: Version,
			MsgID:   msgID,
		},
		Body: cloneBytes(body),
	}
}

// IsReservedMsgID reports whether msgID is owned by the gateway protocol.
func IsReservedMsgID(msgID uint32) bool {
	switch msgID {
	case MsgIDAck, MsgIDDownlinkAck, MsgIDBind:
		return true
	default:
		return false
	}
}

func cloneBytes(in []byte) []byte {
	return bytes.Clone(in)
}
