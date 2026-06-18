package auth

import (
	"context"
	"fmt"
	"slices"
)

type StaticTokenVerifier struct {
	tokens map[string]Principal
}

func (*StaticTokenVerifier) Provider() string {
	return ProviderStatic
}

func NewStaticTokenVerifier(tokens map[string]Principal) *StaticTokenVerifier {
	copied := make(map[string]Principal, len(tokens))
	for token, principal := range tokens {
		copied[token] = copyPrincipal(principal)
	}

	return &StaticTokenVerifier{tokens: copied}
}

func (v *StaticTokenVerifier) Verify(_ context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}

	principal, ok := v.tokens[token]
	if !ok {
		return nil, ErrInvalidToken
	}

	if principal.ClientID == "" {
		return nil, fmt.Errorf("%w: missing client id", ErrInvalidToken)
	}

	return new(copyPrincipal(principal)), nil
}

func copyPrincipal(principal Principal) Principal {
	principal.Scopes = slices.Clone(principal.Scopes)
	return principal
}
