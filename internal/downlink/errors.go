package downlink

import "errors"

var (
	ErrMissingClientID    = errors.New("downlink: missing client_id")
	ErrMissingDeviceID    = errors.New("downlink: missing device_id")
	ErrInvalidMsgID       = errors.New("downlink: invalid msg_id")
	ErrSessionNotFound    = errors.New("downlink: session not found")
	ErrConnectionNotFound = errors.New("downlink: connection not found")
)
