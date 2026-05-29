package session

import "errors"

var (
	ErrInvalidConnID = errors.New("session: invalid conn id")
	ErrEmptyClientID = errors.New("session: empty client id")
	ErrEmptyDeviceID = errors.New("session: empty device id")
	ErrNotFound      = errors.New("session: not found")
)
