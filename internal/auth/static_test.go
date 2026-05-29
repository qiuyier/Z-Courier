package auth

import (
	"context"
	"errors"
	"testing"
)

func TestStaticTokenVerifier(t *testing.T) {
	verifier := NewStaticTokenVerifier(map[string]Principal{
		"token-1": {
			ClientID: "client-1",
			TokenID:  "token-id-1",
			Scopes:   []string{"push"},
		},
	})

	principal, err := verifier.Verify(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	if principal.ClientID != "client-1" {
		t.Fatalf("ClientID = %q, want %q", principal.ClientID, "client-1")
	}

	principal.Scopes[0] = "mutated"

	again, err := verifier.Verify(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("Verify() second error = %v", err)
	}

	if again.Scopes[0] != "push" {
		t.Fatalf("Scopes were mutated through returned principal: %v", again.Scopes)
	}
}

func TestStaticTokenVerifierRejectsUnknownToken(t *testing.T) {
	verifier := NewStaticTokenVerifier(nil)

	_, err := verifier.Verify(context.Background(), "missing")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrInvalidToken)
	}
}
