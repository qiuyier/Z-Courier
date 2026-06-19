package protocol

import "errors"

var (
	// ErrNilPacket means Encode received no packet.
	ErrNilPacket = errors.New("protocol: nil packet")
	// ErrInvalidMagic means the packet is not a Z-Courier packet.
	ErrInvalidMagic = errors.New("protocol: invalid magic")
	// ErrUnsupportedVersion means the packet uses an unknown wire version.
	ErrUnsupportedVersion = errors.New("protocol: unsupported version")
	// ErrBodyTooLarge means the body exceeds an encoder or decoder limit.
	ErrBodyTooLarge = errors.New("protocol: body too large")
	// ErrPacketTooShort means the fixed header could not be read.
	ErrPacketTooShort = errors.New("protocol: packet too short")
	// ErrLengthMismatch means encoded lengths do not match the available bytes.
	ErrLengthMismatch = errors.New("protocol: packet length mismatch")
	// ErrFieldTooLarge means a string cannot fit in its uint16 wire length.
	ErrFieldTooLarge = errors.New("protocol: field too large")
	// ErrInvalidAck means an ACK packet or body is invalid.
	ErrInvalidAck = errors.New("protocol: invalid ack")
)
