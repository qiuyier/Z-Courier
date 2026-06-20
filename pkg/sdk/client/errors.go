package client

import "errors"

var (
	// ErrFrameTooShort means the outer eight-byte frame header was incomplete.
	ErrFrameTooShort = errors.New("client: frame header too short")
	// ErrFrameTooLarge means the declared payload exceeds the configured limit.
	ErrFrameTooLarge = errors.New("client: frame payload too large")
	// ErrFrameLengthMismatch means the declared payload length does not match
	// the available bytes.
	ErrFrameLengthMismatch = errors.New("client: frame length mismatch")
	// ErrFrameMsgIDMismatch means the outer Zinx MsgID differs from the inner
	// Z-Courier packet MsgID.
	ErrFrameMsgIDMismatch = errors.New("client: outer and inner message IDs differ")
)
