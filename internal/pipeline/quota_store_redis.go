package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisQuotaKeyPrefix = "zcourier:traffic-policy"
	maxRedisLuaExactInteger    = int64(1<<53 - 1)
)

var redisQuotaAdmitScript = redis.NewScript(`
local capacity = tonumber(ARGV[1])
local refill_tokens = tonumber(ARGV[2])
local refill_interval_us = tonumber(ARGV[3])
local idle_ttl_ms = tonumber(ARGV[4])
if capacity == nil or capacity <= 0 or
   refill_tokens == nil or refill_tokens <= 0 or
   refill_interval_us == nil or refill_interval_us <= 0 or
   idle_ttl_ms == nil or idle_ttl_ms <= 0 then
  return redis.error_reply("invalid quota arguments")
end

local redis_time = redis.call("TIME")
local now_us = tonumber(redis_time[1]) * 1000000 + tonumber(redis_time[2])
local state = redis.call("HMGET", KEYS[1], "tokens", "last_refill_us")

local tokens = capacity
local last_refill_us = now_us
if state[1] ~= false or state[2] ~= false then
  if state[1] == false or state[2] == false then
    return redis.error_reply("invalid quota state")
  end
  tokens = tonumber(state[1])
  last_refill_us = tonumber(state[2])
  if tokens == nil or last_refill_us == nil then
    return redis.error_reply("invalid quota state")
  end
end

if now_us < last_refill_us then
  now_us = last_refill_us
end
tokens = math.min(capacity, tokens)
local elapsed_us = now_us - last_refill_us
if elapsed_us > 0 then
  tokens = math.min(
    capacity,
    tokens + (elapsed_us * refill_tokens / refill_interval_us)
  )
end

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call(
  "HSET",
  KEYS[1],
  "tokens", string.format("%.17g", tokens),
  "last_refill_us", string.format("%.0f", now_us)
)
redis.call("PEXPIRE", KEYS[1], idle_ttl_ms)
return allowed
`)

type RedisQuotaStoreConfig struct {
	Addr             string
	Username         string
	Password         string
	DB               int
	KeyPrefix        string
	IdleTTL          time.Duration
	DialTimeout      time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	OperationTimeout time.Duration
	FailureMode      string
}

type RedisQuotaStore struct {
	client           *redis.Client
	keyPrefix        string
	idleTTL          time.Duration
	operationTimeout time.Duration
	closed           atomic.Bool
}

func NewRedisQuotaStore(config RedisQuotaStoreConfig) (*RedisQuotaStore, error) {
	addr := strings.TrimSpace(config.Addr)
	if addr == "" {
		return nil, fmt.Errorf("traffic policy redis quota addr is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("traffic policy redis quota db must be greater than or equal to 0")
	}
	if config.IdleTTL < time.Millisecond {
		return nil, fmt.Errorf("traffic policy redis quota idle TTL must be at least 1ms")
	}
	if config.OperationTimeout <= 0 {
		return nil, fmt.Errorf("traffic policy redis quota operation timeout must be greater than zero")
	}

	keyPrefix := strings.TrimSpace(config.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultRedisQuotaKeyPrefix
	}
	failureMode := strings.ToLower(strings.TrimSpace(config.FailureMode))
	if failureMode == "" {
		failureMode = TrafficPolicyFailureModeFailClosed
	}
	if failureMode != TrafficPolicyFailureModeFailClosed {
		return nil, fmt.Errorf(
			"traffic policy redis quota failure mode supports only %q",
			TrafficPolicyFailureModeFailClosed,
		)
	}

	return &RedisQuotaStore{
		client: redis.NewClient(&redis.Options{
			Addr:         addr,
			Username:     config.Username,
			Password:     config.Password,
			DB:           config.DB,
			DialTimeout:  config.DialTimeout,
			ReadTimeout:  config.ReadTimeout,
			WriteTimeout: config.WriteTimeout,
			MaxRetries:   -1,
		}),
		keyPrefix:        keyPrefix,
		idleTTL:          config.IdleTTL,
		operationTimeout: config.OperationTimeout,
	}, nil
}

func (s *RedisQuotaStore) Admit(ctx context.Context, request QuotaRequest) (QuotaDecision, error) {
	if err := s.ensureOpen(); err != nil {
		return QuotaDecisionAdmissionUnavailable, err
	}
	if err := validateQuotaRequest(request); err != nil {
		return QuotaDecisionAdmissionUnavailable, err
	}
	if int64(request.TokenBucket.Capacity) > maxRedisLuaExactInteger ||
		int64(request.TokenBucket.RefillTokens) > maxRedisLuaExactInteger {
		return QuotaDecisionAdmissionUnavailable, fmt.Errorf(
			"traffic policy redis quota bucket value exceeds exact integer range",
		)
	}

	operationContext, cancel := s.withOperationTimeout(ctx)
	defer cancel()

	result, err := redisQuotaAdmitScript.Run(
		operationContext,
		s.client,
		[]string{s.quotaKey(request)},
		strconv.Itoa(request.TokenBucket.Capacity),
		strconv.Itoa(request.TokenBucket.RefillTokens),
		strconv.FormatInt(ceilDuration(request.TokenBucket.RefillInterval, time.Microsecond), 10),
		strconv.FormatInt(ceilDuration(s.idleTTL, time.Millisecond), 10),
	).Int64()
	if err != nil {
		return QuotaDecisionAdmissionUnavailable, err
	}
	switch result {
	case 0:
		return QuotaDecisionRateLimited, nil
	case 1:
		return QuotaDecisionAllowed, nil
	default:
		return QuotaDecisionAdmissionUnavailable, fmt.Errorf(
			"traffic policy redis quota returned invalid decision",
		)
	}
}

func (s *RedisQuotaStore) Ping(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	operationContext, cancel := s.withOperationTimeout(ctx)
	defer cancel()
	return s.client.Ping(operationContext).Err()
}

func (s *RedisQuotaStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.client.Close()
}

func (s *RedisQuotaStore) quotaKey(request QuotaRequest) string {
	identity := sha256.Sum256([]byte(request.KeyValue))
	return s.keyPrefix +
		":quota:" +
		base64.RawURLEncoding.EncodeToString([]byte(request.PolicyName)) +
		":" +
		base64.RawURLEncoding.EncodeToString([]byte(request.KeyScope)) +
		":" +
		hex.EncodeToString(identity[:])
}

func (s *RedisQuotaStore) withOperationTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, s.operationTimeout)
}

func (s *RedisQuotaStore) ensureOpen() error {
	if s == nil || s.client == nil {
		return fmt.Errorf("traffic policy redis quota store is nil")
	}
	if s.closed.Load() {
		return fmt.Errorf("traffic policy redis quota store is closed")
	}
	return nil
}

func ceilDuration(duration, unit time.Duration) int64 {
	value := int64(duration / unit)
	if duration%unit != 0 {
		value++
	}
	return max(value, 1)
}

var _ QuotaStore = (*RedisQuotaStore)(nil)
