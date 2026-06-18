package auth

import (
	"context"
	"errors"
	"time"
)

const (
	ProviderStatic = "static"
	ProviderHTTP   = "http"
	ProviderJWT    = "jwt"
	ProviderCustom = "custom"
)

var (
	ErrInvalidToken        = errors.New("auth: invalid token")
	ErrExpiredToken        = errors.New("auth: expired token")
	ErrForbidden           = errors.New("auth: forbidden")
	ErrProviderTimeout     = errors.New("auth: provider timeout")
	ErrProviderUnavailable = errors.New("auth: provider unavailable")
	ErrMisconfigured       = errors.New("auth: misconfigured")
)

const (
	ResultSuccess     = "success"
	ResultInvalid     = "invalid"
	ResultExpired     = "expired"
	ResultForbidden   = "forbidden"
	ResultTimeout     = "timeout"
	ResultUnavailable = "unavailable"
	ResultConfigError = "misconfigured"
	ResultError       = "error"
)

type Principal struct {
	ClientID  string
	TokenID   string
	Subject   string
	Scopes    []string
	ExpiresAt time.Time
}

type Verifier interface {
	Verify(ctx context.Context, token string) (*Principal, error)
}

type ProviderNamer interface {
	Provider() string
}

func ProviderName(verifier Verifier) string {
	if named, ok := verifier.(ProviderNamer); ok {
		if provider := named.Provider(); provider != "" {
			return provider
		}
	}

	return ProviderCustom
}

func VerificationResult(err error) string {
	switch {
	case err == nil:
		return ResultSuccess
	case errors.Is(err, ErrExpiredToken):
		return ResultExpired
	case errors.Is(err, ErrForbidden):
		return ResultForbidden
	case errors.Is(err, ErrInvalidToken):
		return ResultInvalid
	case errors.Is(err, ErrProviderTimeout):
		return ResultTimeout
	case errors.Is(err, ErrProviderUnavailable):
		return ResultUnavailable
	case errors.Is(err, ErrMisconfigured):
		return ResultConfigError
	default:
		return ResultError
	}
}
