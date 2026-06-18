package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestVerificationResult(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: ResultSuccess},
		{name: "invalid", err: ErrInvalidToken, want: ResultInvalid},
		{name: "wrapped expired", err: fmt.Errorf("verify: %w", ErrExpiredToken), want: ResultExpired},
		{name: "forbidden", err: ErrForbidden, want: ResultForbidden},
		{name: "timeout", err: ErrProviderTimeout, want: ResultTimeout},
		{name: "unavailable", err: ErrProviderUnavailable, want: ResultUnavailable},
		{name: "misconfigured", err: ErrMisconfigured, want: ResultConfigError},
		{name: "unknown", err: errors.New("unknown"), want: ResultError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := VerificationResult(test.err); got != test.want {
				t.Fatalf("VerificationResult() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProviderName(t *testing.T) {
	if got := ProviderName(NewStaticTokenVerifier(nil)); got != ProviderStatic {
		t.Fatalf("ProviderName(static) = %q, want %q", got, ProviderStatic)
	}
	if got := ProviderName(unnamedVerifier{}); got != ProviderCustom {
		t.Fatalf("ProviderName(unnamed) = %q, want %q", got, ProviderCustom)
	}
}

type unnamedVerifier struct{}

func (unnamedVerifier) Verify(context.Context, string) (*Principal, error) {
	return nil, ErrInvalidToken
}
