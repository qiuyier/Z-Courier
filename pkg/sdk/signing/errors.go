package signing

import "errors"

var (
	ErrInvalidConfig    = errors.New("signing: invalid configuration")
	ErrInvalidRequest   = errors.New("signing: invalid request")
	ErrMissingSignature = errors.New("signing: missing signature headers")
	ErrUnknownKey       = errors.New("signing: unknown key id")
	ErrInvalidTimestamp = errors.New("signing: invalid timestamp")
	ErrExpired          = errors.New("signing: timestamp outside allowed clock skew")
	ErrInvalidNonce     = errors.New("signing: invalid nonce")
	ErrInvalidSignature = errors.New("signing: invalid signature")
	ErrReplay           = errors.New("signing: replayed nonce")
	ErrNonceStoreFull   = errors.New("signing: nonce store is full")
)
