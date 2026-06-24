package httpauth

import (
	"context"
	"strings"
)

const (
	ModeNone  = "none"
	ModeToken = "token"
	ModeHMAC  = "hmac"
)

type contextKey struct{}

// Identity describes the authenticated internal HTTP caller without exposing
// credentials such as bearer tokens or HMAC secrets.
type Identity struct {
	Mode  string
	KeyID string
}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	identity.Mode = strings.TrimSpace(identity.Mode)
	identity.KeyID = strings.TrimSpace(identity.KeyID)
	return context.WithValue(ctx, contextKey{}, identity)
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	if ctx == nil {
		return Identity{}, false
	}
	identity, ok := ctx.Value(contextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	identity.Mode = strings.TrimSpace(identity.Mode)
	identity.KeyID = strings.TrimSpace(identity.KeyID)
	return identity, identity.Mode != "" || identity.KeyID != ""
}
