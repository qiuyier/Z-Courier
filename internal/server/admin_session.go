package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
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
	mu      sync.Mutex
	config  AdminConsoleSessionConfig
	now     func() time.Time
	entries map[[sha256.Size]byte]adminSession
}

func newAdminSessionManager(config AdminConsoleSessionConfig) *adminSessionManager {
	if !config.Enabled {
		return nil
	}
	config = normalizeConfig(Config{AdminConsole: AdminConsoleConfig{Session: config}}).AdminConsole.Session
	return &adminSessionManager{
		config:  config,
		now:     time.Now,
		entries: make(map[[sha256.Size]byte]adminSession),
	}
}

func (m *adminSessionManager) Create(principal string) (string, adminSession, error) {
	if m == nil {
		return "", adminSession{}, errors.New("admin session: manager is nil")
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

	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[adminSessionKey(token)] = session
	return token, session, nil
}

func (m *adminSessionManager) Lookup(token string) (adminSession, bool) {
	if m == nil {
		return adminSession{}, false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return adminSession{}, false
	}

	key := adminSessionKey(token)
	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.entries[key]
	if !ok {
		return adminSession{}, false
	}
	if !session.ExpiresAt.After(now) {
		delete(m.entries, key)
		return adminSession{}, false
	}
	session.LastSeenAt = now
	m.entries[key] = session
	return session, true
}

func (m *adminSessionManager) Delete(token string) bool {
	if m == nil {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	key := adminSessionKey(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[key]; !ok {
		return false
	}
	delete(m.entries, key)
	return true
}

func adminSessionKey(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
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
