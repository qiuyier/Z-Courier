package signing

import (
	"container/heap"
	"fmt"
	"sync"
	"time"
)

// NonceStore atomically records one valid signature nonce.
type NonceStore interface {
	Consume(keyID, nonce string, now, expiresAt time.Time) error
}

// MemoryNonceStore is a bounded in-process replay cache.
type MemoryNonceStore struct {
	mu          sync.Mutex
	maxEntries  int
	entries     map[string]*nonceEntry
	expirations nonceExpiryHeap
}

type nonceEntry struct {
	key       string
	expiresAt time.Time
}

type nonceExpiryHeap []*nonceEntry

func (h nonceExpiryHeap) Len() int           { return len(h) }
func (h nonceExpiryHeap) Less(i, j int) bool { return h[i].expiresAt.Before(h[j].expiresAt) }
func (h nonceExpiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *nonceExpiryHeap) Push(value any)    { *h = append(*h, value.(*nonceEntry)) }
func (h *nonceExpiryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	entry := old[last]
	old[last] = nil
	*h = old[:last]
	return entry
}

// NewMemoryNonceStore creates a bounded replay cache.
func NewMemoryNonceStore(maxEntries int) (*MemoryNonceStore, error) {
	if maxEntries <= 0 {
		return nil, fmt.Errorf("%w: max nonce entries must be greater than zero", ErrInvalidConfig)
	}
	return &MemoryNonceStore{
		maxEntries:  maxEntries,
		entries:     make(map[string]*nonceEntry, maxEntries),
		expirations: make(nonceExpiryHeap, 0, maxEntries),
	}, nil
}

// Consume stores a nonce or returns ErrReplay when it is already active. The
// store fails closed with ErrNonceStoreFull after expired entries are removed.
func (s *MemoryNonceStore) Consume(keyID, nonce string, now, expiresAt time.Time) error {
	if s == nil {
		return ErrNonceStoreFull
	}
	cacheKey := keyID + "\x00" + nonce

	s.mu.Lock()
	defer s.mu.Unlock()

	for s.expirations.Len() > 0 && !s.expirations[0].expiresAt.After(now) {
		entry := heap.Pop(&s.expirations).(*nonceEntry)
		delete(s.entries, entry.key)
	}
	if _, exists := s.entries[cacheKey]; exists {
		return ErrReplay
	}
	if len(s.entries) >= s.maxEntries {
		return ErrNonceStoreFull
	}
	if !expiresAt.After(now) {
		return ErrExpired
	}
	entry := &nonceEntry{key: cacheKey, expiresAt: expiresAt}
	s.entries[cacheKey] = entry
	heap.Push(&s.expirations, entry)
	return nil
}

// Len returns the current number of entries, including entries that have not
// yet been lazily cleaned by Consume.
func (s *MemoryNonceStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
