package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
)

const adminSessionIDPrefix = "zas_"

var errAdminSessionRandom = errors.New("admin session: random source failed")

type adminSession struct {
	SessionID  string
	Principal  string
	Role       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

type adminSessionManager struct {
	config AdminConsoleSessionConfig
	now    func() time.Time
	store  adminSessionStore
}

type adminSessionStore interface {
	Save(tokenKey [sha256.Size]byte, session adminSession, ttl time.Duration) error
	Lookup(tokenKey [sha256.Size]byte, now time.Time) (adminSession, bool, error)
	Delete(tokenKey [sha256.Size]byte) (bool, error)
}

type memoryAdminSessionStore struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]adminSession
}

func newAdminSessionManager(config AdminConsoleSessionConfig) *adminSessionManager {
	manager, _, _ := newConfiguredAdminSessionManager(config)
	return manager
}

func newConfiguredAdminSessionManager(config AdminConsoleSessionConfig) (*adminSessionManager, io.Closer, error) {
	if !config.Enabled {
		return nil, nil, nil
	}
	config = normalizeConfig(Config{AdminConsole: AdminConsoleConfig{Session: config}}).AdminConsole.Session

	store, closer, err := newAdminSessionStore(config.Store)
	if err != nil {
		return nil, nil, err
	}
	return &adminSessionManager{
		config: config,
		now:    time.Now,
		store:  store,
	}, closer, nil
}

func newAdminSessionStore(config AdminSessionStoreConfig) (adminSessionStore, io.Closer, error) {
	switch strings.ToLower(strings.TrimSpace(config.Type)) {
	case "", "memory":
		return &memoryAdminSessionStore{entries: make(map[[sha256.Size]byte]adminSession)}, nil, nil
	case "redis":
		store, err := newRedisAdminSessionStore(config.Redis)
		if err != nil {
			return nil, nil, err
		}
		return store, store, nil
	default:
		return nil, nil, errors.New("admin session: unsupported store type " + config.Type)
	}
}

func (m *adminSessionManager) Create(principal string) (string, adminSession, error) {
	if m == nil {
		return "", adminSession{}, errors.New("admin session: manager is nil")
	}
	if m.store == nil {
		return "", adminSession{}, errors.New("admin session: store is nil")
	}
	principal = strings.TrimSpace(principal)
	if principal == "" {
		principal = "internal"
	}

	token, err := randomAdminSessionToken(32)
	if err != nil {
		return "", adminSession{}, err
	}
	sessionID, err := randomAdminSessionID()
	if err != nil {
		return "", adminSession{}, err
	}

	now := m.now().UTC()
	session := adminSession{
		SessionID:  sessionID,
		Principal:  principal,
		Role:       normalizeAdminRole(m.config.Role),
		CreatedAt:  now,
		ExpiresAt:  now.Add(m.config.TTL),
		LastSeenAt: now,
	}

	if err := m.store.Save(adminSessionKey(token), session, session.ExpiresAt.Sub(now)); err != nil {
		return "", adminSession{}, err
	}
	return token, session, nil
}

func (m *adminSessionManager) Lookup(token string) (adminSession, bool, error) {
	if m == nil {
		return adminSession{}, false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return adminSession{}, false, nil
	}
	if m.store == nil {
		return adminSession{}, false, errors.New("admin session: store is nil")
	}

	key := adminSessionKey(token)
	now := m.now().UTC()
	return m.store.Lookup(key, now)
}

func (m *adminSessionManager) Delete(token string) (bool, error) {
	if m == nil {
		return false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	if m.store == nil {
		return false, errors.New("admin session: store is nil")
	}
	return m.store.Delete(adminSessionKey(token))
}

func (s *memoryAdminSessionStore) Save(tokenKey [sha256.Size]byte, session adminSession, ttl time.Duration) error {
	startedAt := time.Now()
	if s == nil {
		recordAdminSessionStoreOperation("memory", "save", "not_configured", startedAt)
		return errors.New("admin session: memory store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[tokenKey] = session
	recordAdminSessionStoreOperation("memory", "save", "success", startedAt)
	return nil
}

func (s *memoryAdminSessionStore) Lookup(tokenKey [sha256.Size]byte, now time.Time) (adminSession, bool, error) {
	startedAt := time.Now()
	if s == nil {
		recordAdminSessionStoreOperation("memory", "lookup", "not_configured", startedAt)
		return adminSession{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.entries[tokenKey]
	if !ok {
		recordAdminSessionStoreOperation("memory", "lookup", "miss", startedAt)
		return adminSession{}, false, nil
	}
	if !session.ExpiresAt.After(now) {
		delete(s.entries, tokenKey)
		recordAdminSessionStoreOperation("memory", "lookup", "expired", startedAt)
		return adminSession{}, false, nil
	}
	session.LastSeenAt = now
	s.entries[tokenKey] = session
	recordAdminSessionStoreOperation("memory", "lookup", "hit", startedAt)
	return session, true, nil
}

func (s *memoryAdminSessionStore) Delete(tokenKey [sha256.Size]byte) (bool, error) {
	startedAt := time.Now()
	if s == nil {
		recordAdminSessionStoreOperation("memory", "delete", "not_configured", startedAt)
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[tokenKey]; !ok {
		recordAdminSessionStoreOperation("memory", "delete", "miss", startedAt)
		return false, nil
	}
	delete(s.entries, tokenKey)
	recordAdminSessionStoreOperation("memory", "delete", "deleted", startedAt)
	return true, nil
}

func (s *memoryAdminSessionStore) Ping(ctx context.Context) error {
	return ctx.Err()
}

func adminSessionKey(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func recordAdminSessionStoreOperation(store, operation, result string, startedAt time.Time) {
	metrics.RecordAdminSessionStoreOperation(store, operation, result, time.Since(startedAt))
}

func randomAdminSessionID() (string, error) {
	token, err := randomAdminSessionToken(16)
	if err != nil {
		return "", err
	}
	return adminSessionIDPrefix + strings.ToUpper(token), nil
}

func randomAdminSessionToken(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", errAdminSessionRandom
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
