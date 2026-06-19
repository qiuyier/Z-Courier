package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
)

const (
	CacheResultHitPositive = "hit_positive"
	CacheResultHitNegative = "hit_negative"
	CacheResultMiss        = "miss"
)

type CacheConfig struct {
	MaxEntries  int
	PositiveTTL time.Duration
	NegativeTTL time.Duration
	Clock       func() time.Time
}

type CachedVerifier struct {
	provider    string
	delegate    Verifier
	maxEntries  int
	positiveTTL time.Duration
	negativeTTL time.Duration
	clock       func() time.Time

	mu       sync.Mutex
	sequence uint64
	entries  map[[sha256.Size]byte]cacheEntry
}

type cacheEntry struct {
	principal *Principal
	err       error
	expiresAt time.Time
	sequence  uint64
}

func NewCachedVerifier(delegate Verifier, config CacheConfig) (Verifier, error) {
	if delegate == nil {
		return nil, fmt.Errorf("%w: auth cache requires a verifier", ErrMisconfigured)
	}
	if config.MaxEntries <= 0 {
		return nil, fmt.Errorf("%w: auth cache max_entries must be greater than 0", ErrMisconfigured)
	}
	if config.PositiveTTL <= 0 {
		return nil, fmt.Errorf("%w: auth cache positive_ttl must be greater than 0", ErrMisconfigured)
	}
	if config.NegativeTTL <= 0 {
		return nil, fmt.Errorf("%w: auth cache negative_ttl must be greater than 0", ErrMisconfigured)
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}

	return &CachedVerifier{
		provider:    ProviderName(delegate),
		delegate:    delegate,
		maxEntries:  config.MaxEntries,
		positiveTTL: config.PositiveTTL,
		negativeTTL: config.NegativeTTL,
		clock:       clock,
		entries:     make(map[[sha256.Size]byte]cacheEntry),
	}, nil
}

func (v *CachedVerifier) Provider() string {
	if v == nil {
		return ProviderCustom
	}
	return v.provider
}

func (v *CachedVerifier) Close() error {
	if v == nil || v.delegate == nil {
		return nil
	}
	if closer, ok := v.delegate.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (v *CachedVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	if v == nil || v.delegate == nil {
		return nil, ErrMisconfigured
	}

	key := sha256.Sum256([]byte(token))
	now := v.clock()
	if entry, ok := v.load(key, now); ok {
		if entry.err != nil {
			metrics.RecordAuthCache(v.provider, CacheResultHitNegative)
			return nil, entry.err
		}
		metrics.RecordAuthCache(v.provider, CacheResultHitPositive)
		return cloneCachedPrincipal(entry.principal), nil
	}
	metrics.RecordAuthCache(v.provider, CacheResultMiss)

	principal, err := v.delegate.Verify(ctx, token)
	v.store(key, principal, err, v.clock())
	return principal, err
}

func (v *CachedVerifier) load(key [sha256.Size]byte, now time.Time) (cacheEntry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	entry, ok := v.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if !now.Before(entry.expiresAt) {
		delete(v.entries, key)
		return cacheEntry{}, false
	}
	return entry, true
}

func (v *CachedVerifier) store(key [sha256.Size]byte, principal *Principal, err error, now time.Time) {
	entry, ok := v.newEntry(principal, err, now)
	if !ok {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.entries[key]; !exists && len(v.entries) >= v.maxEntries {
		v.evictOldest()
	}
	v.sequence++
	entry.sequence = v.sequence
	v.entries[key] = entry
}

func (v *CachedVerifier) newEntry(principal *Principal, err error, now time.Time) (cacheEntry, bool) {
	if err == nil && principal != nil {
		expiresAt := now.Add(v.positiveTTL)
		if !principal.ExpiresAt.IsZero() && principal.ExpiresAt.Before(expiresAt) {
			expiresAt = principal.ExpiresAt
		}
		if !expiresAt.After(now) {
			return cacheEntry{}, false
		}
		return cacheEntry{principal: cloneCachedPrincipal(principal), expiresAt: expiresAt}, true
	}
	if errors.Is(err, ErrInvalidToken) {
		return cacheEntry{err: ErrInvalidToken, expiresAt: now.Add(v.negativeTTL)}, true
	}
	return cacheEntry{}, false
}

func (v *CachedVerifier) evictOldest() {
	var oldestKey [sha256.Size]byte
	var oldestSequence uint64
	first := true
	for key, entry := range v.entries {
		if first || entry.sequence < oldestSequence {
			oldestKey = key
			oldestSequence = entry.sequence
			first = false
		}
	}
	if !first {
		delete(v.entries, oldestKey)
	}
}

func cloneCachedPrincipal(principal *Principal) *Principal {
	if principal == nil {
		return nil
	}
	cloned := copyPrincipal(*principal)
	return &cloned
}
