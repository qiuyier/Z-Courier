package protocol

import "errors"

var (
	ErrInvalidMagic       = errors.New("protocol: invalid magic")
	ErrUnsupportedVersion = errors.New("protocol: unsupported version")
	ErrBodyTooLarge       = errors.New("protocol: body too large")
	ErrPacketTooShort     = errors.New("protocol: packet too short")
	ErrLengthMismatch     = errors.New("protocol: packet length mismatch")
	ErrFieldTooLarge      = errors.New("protocol: field too large")
)
