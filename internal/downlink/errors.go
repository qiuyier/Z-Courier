package downlink

import "errors"

var (
	ErrMissingClientID       = errors.New("downlink: missing client_id")
	ErrMissingDeviceID       = errors.New("downlink: missing device_id")
	ErrMissingSessionID      = errors.New("downlink: missing session_id")
	ErrInvalidMsgID          = errors.New("downlink: invalid msg_id")
	ErrMissingMessageID      = errors.New("downlink: missing message_id")
	ErrInvalidAckCode        = errors.New("downlink: invalid ack code")
	ErrInvalidStatus         = errors.New("downlink: invalid message status")
	ErrInvalidLimit          = errors.New("downlink: invalid limit")
	ErrInvalidCursor         = errors.New("downlink: invalid cursor")
	ErrInvalidTransition     = errors.New("downlink: invalid message status transition")
	ErrSessionNotFound       = errors.New("downlink: session not found")
	ErrSessionMismatch       = errors.New("downlink: session mismatch")
	ErrConnectionNotFound    = errors.New("downlink: connection not found")
	ErrRegistry              = errors.New("downlink: registry error")
	ErrPeerDispatch          = errors.New("downlink: peer dispatch error")
	ErrPeerNotConfigured     = errors.New("downlink: peer dispatcher is not configured")
	ErrStore                 = errors.New("downlink: store error")
	ErrStoreNotConfigured    = errors.New("downlink: store is not configured")
	ErrMessageNotFound       = errors.New("downlink: message not found")
	ErrMessageIDConflict     = errors.New("downlink: message_id immutable identity conflict")
	ErrQueueCapacityExceeded = errors.New("downlink: queue capacity exceeded")
)
