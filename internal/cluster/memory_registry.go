package cluster

import (
	"context"
	"sync"
	"time"
)

type MemoryRegistryConfig struct {
	TTL time.Duration
	Now func() time.Time
}

type MemoryRegistry struct {
	mu      sync.RWMutex
	entries map[RouteKey]RouteEntry
	ttl     time.Duration
	now     func() time.Time
	closed  bool
}

func NewMemoryRegistry(config MemoryRegistryConfig) *MemoryRegistry {
	now := config.Now
	if now == nil {
		now = time.Now
	}

	return &MemoryRegistry{
		entries: make(map[RouteKey]RouteEntry),
		ttl:     config.TTL,
		now:     now,
	}
}

func (r *MemoryRegistry) Bind(ctx context.Context, entry RouteEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRouteEntry(entry); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}

	now := r.now()
	entry = r.normalizeEntry(entry, now)
	r.entries[entry.Key()] = entry
	return nil
}

func (r *MemoryRegistry) Unbind(ctx context.Context, key RouteKey, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRouteKey(key); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrInvalidRouteEntry
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}

	entry, ok := r.entries[key]
	if !ok {
		return nil
	}
	if entry.Expired(r.now()) {
		delete(r.entries, key)
		return nil
	}
	if entry.SessionID != sessionID {
		return nil
	}

	delete(r.entries, key)
	return nil
}

func (r *MemoryRegistry) Lookup(ctx context.Context, key RouteKey) (RouteEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return RouteEntry{}, false, err
	}
	if err := validateRouteKey(key); err != nil {
		return RouteEntry{}, false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return RouteEntry{}, false, ErrClosed
	}

	entry, ok := r.entries[key]
	if !ok {
		return RouteEntry{}, false, nil
	}
	if entry.Expired(r.now()) {
		delete(r.entries, key)
		return RouteEntry{}, false, nil
	}

	return entry, true, nil
}

func (r *MemoryRegistry) Touch(ctx context.Context, entry RouteEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRouteEntry(entry); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}

	key := entry.Key()
	current, ok := r.entries[key]
	if !ok {
		return ErrRouteNotFound
	}
	if current.Expired(r.now()) {
		delete(r.entries, key)
		return ErrRouteNotFound
	}
	if current.SessionID != entry.SessionID {
		return ErrSessionMismatch
	}

	now := r.now()
	entry = r.normalizeEntry(entry, now)
	r.entries[key] = entry
	return nil
}

func (r *MemoryRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	r.entries = nil
	return nil
}

func (r *MemoryRegistry) normalizeEntry(entry RouteEntry, now time.Time) RouteEntry {
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = now
	}
	if r.ttl > 0 {
		entry.ExpiresAt = now.Add(r.ttl)
	}
	return entry
}
