package auth

import (
	"context"
	"errors"
)

var ErrInvalidToken = errors.New("auth: invalid token")

type Principal struct {
	ClientID string
	TokenID  string
	Subject  string
	Scopes   []string
}

type Verifier interface {
	Verify(ctx context.Context, token string) (*Principal, error)
}
