package downlink

import "errors"

var (
	ErrMissingClientID    = errors.New("downlink: missing client_id")
	ErrMissingDeviceID    = errors.New("downlink: missing device_id")
	ErrInvalidMsgID       = errors.New("downlink: invalid msg_id")
	ErrMissingMessageID   = errors.New("downlink: missing message_id")
	ErrInvalidAckCode     = errors.New("downlink: invalid ack code")
	ErrSessionNotFound    = errors.New("downlink: session not found")
	ErrConnectionNotFound = errors.New("downlink: connection not found")
	ErrStore              = errors.New("downlink: store error")
	ErrStoreNotConfigured = errors.New("downlink: store is not configured")
	ErrMessageNotFound    = errors.New("downlink: message not found")
)
