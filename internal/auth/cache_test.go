package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCachedVerifierPositiveHitAndClone(t *testing.T) {
	delegate := &countingVerifier{principal: &Principal{ClientID: "client-1", Scopes: []string{"scope-1"}}}
	verifier := newTestCachedVerifier(t, delegate, CacheConfig{
		MaxEntries:  10,
		PositiveTTL: time.Minute,
		NegativeTTL: time.Second,
	})

	first, err := verifier.Verify(context.Background(), "token")
	if err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	first.Scopes[0] = "mutated"
	second, err := verifier.Verify(context.Background(), "token")
	if err != nil {
		t.Fatalf("second Verify() error = %v", err)
	}
	if delegate.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", delegate.calls)
	}
	if second.Scopes[0] != "scope-1" {
		t.Fatalf("cached scopes = %v", second.Scopes)
	}
}

func TestCachedVerifierNegativeCacheOnlyStoresInvalidTokens(t *testing.T) {
	invalid := &countingVerifier{err: ErrInvalidToken}
	invalidVerifier := newTestCachedVerifier(t, invalid, testCacheConfig())
	for range 2 {
		_, err := invalidVerifier.Verify(context.Background(), "invalid")
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("invalid Verify() error = %v", err)
		}
	}
	if invalid.calls != 1 {
		t.Fatalf("invalid delegate calls = %d, want 1", invalid.calls)
	}

	unavailable := &countingVerifier{err: ErrProviderUnavailable}
	unavailableVerifier := newTestCachedVerifier(t, unavailable, testCacheConfig())
	for range 2 {
		_, err := unavailableVerifier.Verify(context.Background(), "unavailable")
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("unavailable Verify() error = %v", err)
		}
	}
	if unavailable.calls != 2 {
		t.Fatalf("unavailable delegate calls = %d, want 2", unavailable.calls)
	}
}

func TestCachedVerifierExpiresEntries(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	delegate := &countingVerifier{principal: &Principal{ClientID: "client-1"}}
	config := testCacheConfig()
	config.PositiveTTL = time.Second
	config.Clock = func() time.Time { return now }
	verifier := newTestCachedVerifier(t, delegate, config)

	_, _ = verifier.Verify(context.Background(), "token")
	now = now.Add(500 * time.Millisecond)
	_, _ = verifier.Verify(context.Background(), "token")
	now = now.Add(time.Second)
	_, _ = verifier.Verify(context.Background(), "token")
	if delegate.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2", delegate.calls)
	}
}

func TestCachedVerifierRespectsPrincipalExpiry(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	delegate := &countingVerifier{principal: &Principal{
		ClientID:  "client-1",
		ExpiresAt: now.Add(time.Second),
	}}
	config := testCacheConfig()
	config.PositiveTTL = time.Hour
	config.Clock = func() time.Time { return now }
	verifier := newTestCachedVerifier(t, delegate, config)

	_, _ = verifier.Verify(context.Background(), "token")
	now = now.Add(2 * time.Second)
	_, _ = verifier.Verify(context.Background(), "token")
	if delegate.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2", delegate.calls)
	}
}

func TestCachedVerifierEvictsOldestEntry(t *testing.T) {
	delegate := &countingVerifier{principal: &Principal{ClientID: "client-1"}}
	config := testCacheConfig()
	config.MaxEntries = 2
	verifier := newTestCachedVerifier(t, delegate, config)

	_, _ = verifier.Verify(context.Background(), "token-a")
	_, _ = verifier.Verify(context.Background(), "token-b")
	_, _ = verifier.Verify(context.Background(), "token-c")
	_, _ = verifier.Verify(context.Background(), "token-a")
	if delegate.calls != 4 {
		t.Fatalf("delegate calls = %d, want 4", delegate.calls)
	}
}

func TestNewCachedVerifierRejectsInvalidConfig(t *testing.T) {
	delegate := &countingVerifier{}
	for _, config := range []CacheConfig{
		{MaxEntries: 0, PositiveTTL: time.Second, NegativeTTL: time.Second},
		{MaxEntries: 1, PositiveTTL: 0, NegativeTTL: time.Second},
		{MaxEntries: 1, PositiveTTL: time.Second, NegativeTTL: 0},
	} {
		if _, err := NewCachedVerifier(delegate, config); !errors.Is(err, ErrMisconfigured) {
			t.Fatalf("NewCachedVerifier(%+v) error = %v, want ErrMisconfigured", config, err)
		}
	}
}

func newTestCachedVerifier(t *testing.T, delegate Verifier, config CacheConfig) Verifier {
	t.Helper()
	verifier, err := NewCachedVerifier(delegate, config)
	if err != nil {
		t.Fatalf("NewCachedVerifier() error = %v", err)
	}
	return verifier
}

func testCacheConfig() CacheConfig {
	return CacheConfig{
		MaxEntries:  10,
		PositiveTTL: time.Minute,
		NegativeTTL: time.Second,
	}
}

type countingVerifier struct {
	principal *Principal
	err       error
	calls     int
}

func (*countingVerifier) Provider() string {
	return ProviderHTTP
}

func (v *countingVerifier) Verify(context.Context, string) (*Principal, error) {
	v.calls++
	return v.principal, v.err
}
