package auth

import (
	"context"
	"errors"
	"testing"
)

func TestObservedVerifierPreservesResult(t *testing.T) {
	wantPrincipal := &Principal{ClientID: "client-a", TokenID: "token-a"}
	delegate := &stubVerifier{principal: wantPrincipal}
	verifier := NewObservedVerifier(delegate)

	principal, err := verifier.Verify(context.Background(), "raw-token")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal != wantPrincipal {
		t.Fatalf("Verify() principal = %p, want %p", principal, wantPrincipal)
	}
	if delegate.token != "raw-token" {
		t.Fatalf("delegate token = %q, want raw-token", delegate.token)
	}
	if got := ProviderName(verifier); got != ProviderHTTP {
		t.Fatalf("ProviderName() = %q, want %q", got, ProviderHTTP)
	}
}

func TestObservedVerifierPreservesError(t *testing.T) {
	wantErr := errors.New("verify failed")
	verifier := NewObservedVerifier(&stubVerifier{err: wantErr})

	_, err := verifier.Verify(context.Background(), "raw-token")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Verify() error = %v, want %v", err, wantErr)
	}
}

func TestNewObservedVerifierHandlesNilAndDoubleWrap(t *testing.T) {
	if got := NewObservedVerifier(nil); got != nil {
		t.Fatalf("NewObservedVerifier(nil) = %#v, want nil", got)
	}

	first := NewObservedVerifier(&stubVerifier{})
	if second := NewObservedVerifier(first); second != first {
		t.Fatal("NewObservedVerifier() wrapped an observed verifier twice")
	}
}

func TestObservedVerifierClosesDelegate(t *testing.T) {
	delegate := &closableStubVerifier{}
	verifier := NewObservedVerifier(delegate)
	closer, ok := verifier.(interface{ Close() error })
	if !ok {
		t.Fatal("observed verifier does not implement Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if delegate.closeCalls != 1 {
		t.Fatalf("delegate close calls = %d, want 1", delegate.closeCalls)
	}
}

type stubVerifier struct {
	principal *Principal
	err       error
	token     string
}

type closableStubVerifier struct {
	stubVerifier
	closeCalls int
}

func (v *closableStubVerifier) Close() error {
	v.closeCalls++
	return nil
}

func (*stubVerifier) Provider() string {
	return ProviderHTTP
}

func (v *stubVerifier) Verify(_ context.Context, token string) (*Principal, error) {
	v.token = token
	return v.principal, v.err
}
