package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultAdminSessionRedisKeyPrefix = "zcourier:admin-session"

type redisAdminSessionStore struct {
	client           *redis.Client
	keyPrefix        string
	operationTimeout time.Duration
	closed           atomic.Bool
}

func newRedisAdminSessionStore(config AdminSessionRedisConfig) (*redisAdminSessionStore, error) {
	addr := strings.TrimSpace(config.Addr)
	if addr == "" {
		return nil, fmt.Errorf("admin session: redis addr is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("admin session: redis db must be greater than or equal to 0")
	}

	keyPrefix := strings.TrimSpace(config.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultAdminSessionRedisKeyPrefix
	}
	operationTimeout := config.OperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = 2 * time.Second
	}

	store := &redisAdminSessionStore{
		client: redis.NewClient(&redis.Options{
			Addr:         addr,
			Username:     config.Username,
			Password:     config.Password,
			DB:           config.DB,
			DialTimeout:  config.DialTimeout,
			ReadTimeout:  config.ReadTimeout,
			WriteTimeout: config.WriteTimeout,
		}),
		keyPrefix:        keyPrefix,
		operationTimeout: operationTimeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	if err := store.client.Ping(ctx).Err(); err != nil {
		_ = store.client.Close()
		return nil, fmt.Errorf("admin session redis ping: %w", err)
	}
	return store, nil
}

func (s *redisAdminSessionStore) Save(tokenKey [sha256.Size]byte, session adminSession, ttl time.Duration) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("admin session: ttl must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.operationTimeout)
	defer cancel()

	fields := map[string]any{
		"session_id":             session.SessionID,
		"principal":              session.Principal,
		"role":                   normalizeAdminRole(session.Role),
		"created_at_unix_nano":   session.CreatedAt.UTC().UnixNano(),
		"expires_at_unix_nano":   session.ExpiresAt.UTC().UnixNano(),
		"last_seen_at_unix_nano": session.LastSeenAt.UTC().UnixNano(),
	}

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.sessionKey(tokenKey), fields)
	pipe.PExpire(ctx, s.sessionKey(tokenKey), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *redisAdminSessionStore) Lookup(tokenKey [sha256.Size]byte, now time.Time) (adminSession, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return adminSession{}, false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.operationTimeout)
	defer cancel()

	key := s.sessionKey(tokenKey)
	fields, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		return adminSession{}, false, err
	}
	if len(fields) == 0 {
		return adminSession{}, false, nil
	}

	session, err := adminSessionFromRedisFields(fields)
	if err != nil {
		return adminSession{}, false, err
	}
	if !session.ExpiresAt.After(now) {
		_ = s.client.Del(ctx, key).Err()
		return adminSession{}, false, nil
	}

	session.LastSeenAt = now.UTC()
	remaining := session.ExpiresAt.Sub(now)
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, key, "last_seen_at_unix_nano", session.LastSeenAt.UnixNano())
	pipe.PExpire(ctx, key, remaining)
	if _, err := pipe.Exec(ctx); err != nil {
		return adminSession{}, false, err
	}
	return session, true, nil
}

func (s *redisAdminSessionStore) Delete(tokenKey [sha256.Size]byte) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.operationTimeout)
	defer cancel()
	deleted, err := s.client.Del(ctx, s.sessionKey(tokenKey)).Result()
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

func (s *redisAdminSessionStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.client.Close()
}

func (s *redisAdminSessionStore) sessionKey(tokenKey [sha256.Size]byte) string {
	return s.keyPrefix + ":" + hex.EncodeToString(tokenKey[:])
}

func (s *redisAdminSessionStore) ensureOpen() error {
	if s == nil || s.client == nil {
		return fmt.Errorf("admin session: redis store is nil")
	}
	if s.closed.Load() {
		return fmt.Errorf("admin session: redis store is closed")
	}
	return nil
}

func adminSessionFromRedisFields(fields map[string]string) (adminSession, error) {
	createdAt, err := redisUnixNano(fields, "created_at_unix_nano")
	if err != nil {
		return adminSession{}, err
	}
	expiresAt, err := redisUnixNano(fields, "expires_at_unix_nano")
	if err != nil {
		return adminSession{}, err
	}
	lastSeenAt, err := redisUnixNano(fields, "last_seen_at_unix_nano")
	if err != nil {
		return adminSession{}, err
	}
	session := adminSession{
		SessionID:  strings.TrimSpace(fields["session_id"]),
		Principal:  strings.TrimSpace(fields["principal"]),
		Role:       normalizeAdminRole(fields["role"]),
		CreatedAt:  createdAt,
		ExpiresAt:  expiresAt,
		LastSeenAt: lastSeenAt,
	}
	if session.SessionID == "" {
		return adminSession{}, fmt.Errorf("admin session: redis session_id is empty")
	}
	if session.Principal == "" {
		session.Principal = "internal"
	}
	return session, nil
}

func redisUnixNano(fields map[string]string, field string) (time.Time, error) {
	value := strings.TrimSpace(fields[field])
	if value == "" {
		return time.Time{}, fmt.Errorf("admin session: redis field %s is empty", field)
	}
	nano, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("admin session: redis field %s: %w", field, err)
	}
	return time.Unix(0, nano).UTC(), nil
}
