package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisKeyPrefix = "zcourier"
)

var (
	redisUnbindScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
  return 0
end
if redis.call("HGET", KEYS[1], "session_id") ~= ARGV[1] then
  return 0
end
redis.call("DEL", KEYS[1])
return 1
`)

	redisTouchScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
  return 0
end
if redis.call("HGET", KEYS[1], "session_id") ~= ARGV[1] then
  return -1
end
redis.call(
  "HSET",
  KEYS[1],
  "client_id", ARGV[2],
  "device_id", ARGV[3],
  "session_id", ARGV[4],
  "gateway_node", ARGV[5],
  "internal_addr", ARGV[6],
  "token_id", ARGV[7],
  "updated_at_unix_nano", ARGV[8],
  "expires_at_unix_nano", ARGV[9]
)
if tonumber(ARGV[10]) > 0 then
  redis.call("PEXPIRE", KEYS[1], ARGV[10])
else
  redis.call("PERSIST", KEYS[1])
end
return 1
`)
)

type RedisRegistryConfig struct {
	Addr         string
	Username     string
	Password     string
	DB           int
	KeyPrefix    string
	TTL          time.Duration
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	Now          func() time.Time
}

type RedisRegistry struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	now       func() time.Time
	closed    atomic.Bool
}

func NewRedisRegistry(config RedisRegistryConfig) (*RedisRegistry, error) {
	addr := strings.TrimSpace(config.Addr)
	if addr == "" {
		return nil, fmt.Errorf("cluster: redis addr is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("cluster: redis db must be greater than or equal to 0")
	}

	keyPrefix := strings.TrimSpace(config.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultRedisKeyPrefix
	}

	now := config.Now
	if now == nil {
		now = time.Now
	}

	return &RedisRegistry{
		client: redis.NewClient(&redis.Options{
			Addr:         addr,
			Username:     config.Username,
			Password:     config.Password,
			DB:           config.DB,
			DialTimeout:  config.DialTimeout,
			ReadTimeout:  config.ReadTimeout,
			WriteTimeout: config.WriteTimeout,
		}),
		keyPrefix: keyPrefix,
		ttl:       config.TTL,
		now:       now,
	}, nil
}

func (r *RedisRegistry) Ping(ctx context.Context) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	return r.client.Ping(ctx).Err()
}

func (r *RedisRegistry) Bind(ctx context.Context, entry RouteEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if err := validateRouteEntry(entry); err != nil {
		return err
	}

	now := r.now()
	entry = r.normalizeEntry(entry, now)
	key := r.routeKey(entry.Key())
	fields := routeEntryFields(entry)

	pipe := r.client.TxPipeline()
	pipe.HSet(ctx, key, fields)
	if r.ttl > 0 {
		pipe.PExpire(ctx, key, r.ttl)
	} else {
		pipe.Persist(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisRegistry) Unbind(ctx context.Context, key RouteKey, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if err := validateRouteKey(key); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrInvalidRouteEntry
	}

	return redisUnbindScript.Run(ctx, r.client, []string{r.routeKey(key)}, sessionID).Err()
}

func (r *RedisRegistry) Lookup(ctx context.Context, key RouteKey) (RouteEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return RouteEntry{}, false, err
	}
	if err := r.ensureOpen(); err != nil {
		return RouteEntry{}, false, err
	}
	if err := validateRouteKey(key); err != nil {
		return RouteEntry{}, false, err
	}

	fields, err := r.client.HGetAll(ctx, r.routeKey(key)).Result()
	if err != nil {
		return RouteEntry{}, false, err
	}
	if len(fields) == 0 {
		return RouteEntry{}, false, nil
	}

	entry, err := routeEntryFromFields(fields)
	if err != nil {
		return RouteEntry{}, false, err
	}
	if err := validateRouteEntry(entry); err != nil {
		return RouteEntry{}, false, err
	}
	if entry.Key() != key {
		return RouteEntry{}, false, ErrInvalidRouteEntry
	}
	if entry.Expired(r.now()) {
		_ = r.client.Del(ctx, r.routeKey(key)).Err()
		return RouteEntry{}, false, nil
	}

	return entry, true, nil
}

func (r *RedisRegistry) Touch(ctx context.Context, entry RouteEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if err := validateRouteEntry(entry); err != nil {
		return err
	}

	now := r.now()
	entry = r.normalizeEntry(entry, now)
	result, err := redisTouchScript.Run(
		ctx,
		r.client,
		[]string{r.routeKey(entry.Key())},
		entry.SessionID,
		entry.ClientID,
		entry.DeviceID,
		entry.SessionID,
		entry.GatewayNode,
		entry.InternalAddr,
		entry.TokenID,
		formatUnixNano(entry.UpdatedAt),
		formatUnixNano(entry.ExpiresAt),
		strconv.FormatInt(r.ttl.Milliseconds(), 10),
	).Int()
	if err != nil {
		return err
	}

	switch result {
	case 1:
		return nil
	case -1:
		return ErrSessionMismatch
	default:
		return ErrRouteNotFound
	}
}

func (r *RedisRegistry) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	return r.client.Close()
}

func (r *RedisRegistry) ensureOpen() error {
	if r == nil || r.client == nil || r.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (r *RedisRegistry) normalizeEntry(entry RouteEntry, now time.Time) RouteEntry {
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = now
	}
	if r.ttl > 0 {
		entry.ExpiresAt = now.Add(r.ttl)
	}
	return entry
}

func (r *RedisRegistry) routeKey(key RouteKey) string {
	return r.keyPrefix + ":online:" + encodeKeyPart(key.ClientID) + ":" + encodeKeyPart(key.DeviceID)
}

func encodeKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func routeEntryFields(entry RouteEntry) map[string]any {
	return map[string]any{
		"client_id":            entry.ClientID,
		"device_id":            entry.DeviceID,
		"session_id":           entry.SessionID,
		"gateway_node":         entry.GatewayNode,
		"internal_addr":        entry.InternalAddr,
		"token_id":             entry.TokenID,
		"updated_at_unix_nano": formatUnixNano(entry.UpdatedAt),
		"expires_at_unix_nano": formatUnixNano(entry.ExpiresAt),
	}
}

func routeEntryFromFields(fields map[string]string) (RouteEntry, error) {
	updatedAt, err := parseUnixNano(fields["updated_at_unix_nano"])
	if err != nil {
		return RouteEntry{}, err
	}
	expiresAt, err := parseUnixNano(fields["expires_at_unix_nano"])
	if err != nil {
		return RouteEntry{}, err
	}

	return RouteEntry{
		ClientID:     fields["client_id"],
		DeviceID:     fields["device_id"],
		SessionID:    fields["session_id"],
		GatewayNode:  fields["gateway_node"],
		InternalAddr: fields["internal_addr"],
		TokenID:      fields["token_id"],
		UpdatedAt:    updatedAt,
		ExpiresAt:    expiresAt,
	}, nil
}

func formatUnixNano(value time.Time) string {
	if value.IsZero() {
		return "0"
	}
	return strconv.FormatInt(value.UnixNano(), 10)
}

func parseUnixNano(raw string) (time.Time, error) {
	if raw == "" || raw == "0" {
		return time.Time{}, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, value), nil
}
