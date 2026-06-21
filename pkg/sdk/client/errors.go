package client

import "errors"

var (
	// ErrInvalidConfig means a required client setting is missing or conflicts
	// with another setting.
	ErrInvalidConfig = errors.New("client: invalid configuration")
	// ErrClientClosed means the Client has been permanently closed.
	ErrClientClosed = errors.New("client: closed")
	// ErrNotReady means an operation requires an accepted AUTH/BIND connection.
	ErrNotReady = errors.New("client: not ready")
	// ErrConnectionClosed means the active gateway connection ended.
	ErrConnectionClosed = errors.New("client: connection closed")
	// ErrConnectTimeout means the token lookup or network dial exceeded the
	// configured connect timeout.
	ErrConnectTimeout = errors.New("client: connect timeout")
	// ErrTokenUnavailable means the configured token provider failed or returned
	// an empty credential.
	ErrTokenUnavailable = errors.New("client: token unavailable")
	// ErrBindTimeout means AUTH/BIND did not complete before its deadline.
	ErrBindTimeout = errors.New("client: bind timeout")
	// ErrAuthenticationFailed means the gateway rejected the credential.
	ErrAuthenticationFailed = errors.New("client: authentication failed")
	// ErrAuthenticationUnavailable means the gateway could not reach its
	// authentication provider.
	ErrAuthenticationUnavailable = errors.New("client: authentication unavailable")
	// ErrBindRejected means the gateway rejected AUTH/BIND for another reason.
	ErrBindRejected = errors.New("client: bind rejected")
	// ErrUnexpectedBindAck means the bind ACK is malformed or does not match the
	// active bind request.
	ErrUnexpectedBindAck = errors.New("client: unexpected bind acknowledgment")
	// ErrPendingBeforeReadyOverflow means too many non-ACK packets arrived before
	// AUTH/BIND completed.
	ErrPendingBeforeReadyOverflow = errors.New("client: pending packets before ready overflow")
	// ErrInboundOverflow means the application did not receive packets quickly
	// enough to stay within the configured inbound buffer.
	ErrInboundOverflow = errors.New("client: inbound packet buffer overflow")
	// ErrReservedMsgID means Send was called with a protocol-owned message ID.
	ErrReservedMsgID = errors.New("client: reserved message ID")
	// ErrDuplicateMessageID means another ACK-required Send already uses the
	// same MessageID.
	ErrDuplicateMessageID = errors.New("client: duplicate message ID")
	// ErrAckTimeout means a requested gateway ACK did not arrive in time.
	ErrAckTimeout = errors.New("client: acknowledgment timeout")
	// ErrAckRejected means the gateway returned a non-success ACK.
	ErrAckRejected = errors.New("client: acknowledgment rejected")
	// ErrUnexpectedAck means an ACK matching a pending request was malformed.
	ErrUnexpectedAck = errors.New("client: unexpected acknowledgment")
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
