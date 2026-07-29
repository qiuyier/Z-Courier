package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisQuotaStoreRealRedisIntegration(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("ZCOURIER_TEST_REDIS_ADDR"))
	if addr == "" {
		t.Skip("ZCOURIER_TEST_REDIS_ADDR is not set")
	}

	config := RedisQuotaStoreConfig{
		Addr:             addr,
		Username:         os.Getenv("ZCOURIER_TEST_REDIS_USERNAME"),
		Password:         os.Getenv("ZCOURIER_TEST_REDIS_PASSWORD"),
		KeyPrefix:        fmt.Sprintf("zcourier:test:quota:%d", time.Now().UnixNano()),
		IdleTTL:          2 * time.Second,
		DialTimeout:      time.Second,
		ReadTimeout:      time.Second,
		WriteTimeout:     time.Second,
		OperationTimeout: time.Second,
		FailureMode:      TrafficPolicyFailureModeFailClosed,
	}
	gatewayA, err := NewRedisQuotaStore(config)
	if err != nil {
		t.Fatalf("NewRedisQuotaStore(gateway-a) error = %v", err)
	}
	defer gatewayA.Close()
	gatewayB, err := NewRedisQuotaStore(config)
	if err != nil {
		t.Fatalf("NewRedisQuotaStore(gateway-b) error = %v", err)
	}
	defer gatewayB.Close()
	if err := gatewayA.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	request := redisQuotaRequest("real-redis-client", TokenBucketConfig{
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	})
	key := gatewayA.quotaKey(request)
	defer func() {
		_ = gatewayA.client.Del(context.Background(), key).Err()
	}()

	var allowed atomic.Int64
	var limited atomic.Int64
	var failed atomic.Int64
	var waitGroup sync.WaitGroup
	for index := range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			store := gatewayA
			if index%2 == 1 {
				store = gatewayB
			}
			decision, err := store.Admit(context.Background(), request)
			if err != nil {
				failed.Add(1)
				return
			}
			switch decision {
			case QuotaDecisionAllowed:
				allowed.Add(1)
			case QuotaDecisionRateLimited:
				limited.Add(1)
			default:
				failed.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := allowed.Load(); got != 5 {
		t.Fatalf("allowed = %d, want 5 shared across stores", got)
	}
	if got := limited.Load(); got != 95 {
		t.Fatalf("rate limited = %d, want 95", got)
	}
	if got := failed.Load(); got != 0 {
		t.Fatalf("failed = %d, want 0", got)
	}
	ttl, err := gatewayA.client.PTTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("PTTL() error = %v", err)
	}
	if ttl <= 0 || ttl > config.IdleTTL {
		t.Fatalf("PTTL() = %v, want within (0, %v]", ttl, config.IdleTTL)
	}
}

func TestRedisQuotaStoreSharesAtomicQuotaAcrossInstances(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC))
	gatewayA := newTestRedisQuotaStore(t, server, "zcourier:test:shared")
	gatewayB := newTestRedisQuotaStore(t, server, "zcourier:test:shared")
	request := redisQuotaRequest("client-a", TokenBucketConfig{
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	})

	var allowed atomic.Int64
	var limited atomic.Int64
	var failed atomic.Int64
	var waitGroup sync.WaitGroup
	for index := range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			store := gatewayA
			if index%2 == 1 {
				store = gatewayB
			}
			decision, err := store.Admit(context.Background(), request)
			if err != nil {
				failed.Add(1)
				return
			}
			switch decision {
			case QuotaDecisionAllowed:
				allowed.Add(1)
			case QuotaDecisionRateLimited:
				limited.Add(1)
			default:
				failed.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed = %d, want 10", got)
	}
	if got := limited.Load(); got != 90 {
		t.Fatalf("rate limited = %d, want 90", got)
	}
	if got := failed.Load(); got != 0 {
		t.Fatalf("failed = %d, want 0", got)
	}
}

func TestRedisQuotaStoreUsesRedisTimeForRefill(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	server.SetTime(now)
	store := newTestRedisQuotaStore(t, server, "zcourier:test:refill")
	request := redisQuotaRequest("client-a", TokenBucketConfig{
		Capacity:       2,
		RefillTokens:   2,
		RefillInterval: time.Second,
	})

	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertRedisQuotaDecision(t, store, request, QuotaDecisionRateLimited)

	now = now.Add(500 * time.Millisecond)
	server.SetTime(now)
	server.FastForward(500 * time.Millisecond)
	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
	assertRedisQuotaDecision(t, store, request, QuotaDecisionRateLimited)
}

func TestRedisQuotaStoreRefreshesAndExpiresIdleTTL(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	server.SetTime(now)
	store := newTestRedisQuotaStore(t, server, "zcourier:test:ttl")
	request := redisQuotaRequest("client-a", TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Hour,
	})

	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
	keys := server.Keys()
	if len(keys) != 1 {
		t.Fatalf("Redis keys = %v, want one key", keys)
	}
	key := keys[0]
	if ttl := server.TTL(key); ttl != 10*time.Second {
		t.Fatalf("TTL = %v, want 10s", ttl)
	}

	now = now.Add(6 * time.Second)
	server.SetTime(now)
	server.FastForward(6 * time.Second)
	assertRedisQuotaDecision(t, store, request, QuotaDecisionRateLimited)

	now = now.Add(6 * time.Second)
	server.SetTime(now)
	server.FastForward(6 * time.Second)
	if !server.Exists(key) {
		t.Fatal("quota key expired before refreshed idle TTL")
	}

	now = now.Add(5 * time.Second)
	server.SetTime(now)
	server.FastForward(5 * time.Second)
	if server.Exists(key) {
		t.Fatal("quota key still exists after refreshed idle TTL")
	}
}

func TestRedisQuotaStoreHashesIdentityInKey(t *testing.T) {
	server := miniredis.RunT(t)
	store := newTestRedisQuotaStore(t, server, "zcourier:test:identity")
	request := redisQuotaRequest("private-client-id", TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	})

	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
	keys := server.Keys()
	if len(keys) != 1 {
		t.Fatalf("Redis keys = %v, want one key", keys)
	}
	if strings.Contains(keys[0], request.KeyValue) {
		t.Fatalf("Redis key %q exposes raw identity", keys[0])
	}
	if !strings.HasPrefix(keys[0], "zcourier:test:identity:quota:") {
		t.Fatalf("Redis key = %q, want configured namespace", keys[0])
	}
}

func TestRedisQuotaStoreFailsClosedAndRecovers(t *testing.T) {
	server := miniredis.RunT(t)
	store := newTestRedisQuotaStore(t, server, "zcourier:test:failure")
	request := redisQuotaRequest("client-a", TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	})

	server.SetError("LOADING Redis is loading the dataset in memory")
	decision, err := store.Admit(context.Background(), request)
	if decision != QuotaDecisionAdmissionUnavailable || err == nil {
		t.Fatalf("Admit(failed Redis) = %q, %v", decision, err)
	}

	server.SetError("")
	assertRedisQuotaDecision(t, store, request, QuotaDecisionAllowed)
}

func TestRedisQuotaStoreHonorsCancellationAndClose(t *testing.T) {
	server := miniredis.RunT(t)
	store := newTestRedisQuotaStore(t, server, "zcourier:test:lifecycle")
	request := redisQuotaRequest("client-a", TokenBucketConfig{
		Capacity:       1,
		RefillTokens:   1,
		RefillInterval: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := store.Admit(ctx, request)
	if decision != QuotaDecisionAdmissionUnavailable || err == nil {
		t.Fatalf("Admit(canceled) = %q, %v", decision, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	decision, err = store.Admit(context.Background(), request)
	if decision != QuotaDecisionAdmissionUnavailable || err == nil {
		t.Fatalf("Admit(closed) = %q, %v", decision, err)
	}
}

func TestNewRedisQuotaStoreRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config RedisQuotaStoreConfig
	}{
		{name: "missing addr", config: RedisQuotaStoreConfig{IdleTTL: time.Second, OperationTimeout: time.Second}},
		{
			name: "negative db",
			config: RedisQuotaStoreConfig{
				Addr:             "127.0.0.1:6379",
				DB:               -1,
				IdleTTL:          time.Second,
				OperationTimeout: time.Second,
			},
		},
		{
			name: "short ttl",
			config: RedisQuotaStoreConfig{
				Addr:             "127.0.0.1:6379",
				IdleTTL:          time.Microsecond,
				OperationTimeout: time.Second,
			},
		},
		{
			name: "missing operation timeout",
			config: RedisQuotaStoreConfig{
				Addr:    "127.0.0.1:6379",
				IdleTTL: time.Second,
			},
		},
		{
			name: "unsupported failure mode",
			config: RedisQuotaStoreConfig{
				Addr:             "127.0.0.1:6379",
				IdleTTL:          time.Second,
				OperationTimeout: time.Second,
				FailureMode:      "local_fallback",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewRedisQuotaStore(test.config)
			if err == nil {
				_ = store.Close()
				t.Fatal("NewRedisQuotaStore() error = nil, want error")
			}
		})
	}
}

func newTestRedisQuotaStore(
	t *testing.T,
	server *miniredis.Miniredis,
	keyPrefix string,
) *RedisQuotaStore {
	t.Helper()
	store, err := NewRedisQuotaStore(RedisQuotaStoreConfig{
		Addr:             server.Addr(),
		KeyPrefix:        keyPrefix,
		IdleTTL:          10 * time.Second,
		DialTimeout:      100 * time.Millisecond,
		ReadTimeout:      100 * time.Millisecond,
		WriteTimeout:     100 * time.Millisecond,
		OperationTimeout: 200 * time.Millisecond,
		FailureMode:      TrafficPolicyFailureModeFailClosed,
	})
	if err != nil {
		t.Fatalf("NewRedisQuotaStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	return store
}

func redisQuotaRequest(clientID string, bucket TokenBucketConfig) QuotaRequest {
	return QuotaRequest{
		PolicyName:  "standard",
		KeyScope:    TrafficPolicyKeyClientID,
		KeyValue:    clientID,
		TokenBucket: bucket,
	}
}

func assertRedisQuotaDecision(
	t *testing.T,
	store QuotaStore,
	request QuotaRequest,
	want QuotaDecision,
) {
	t.Helper()
	got, err := store.Admit(context.Background(), request)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if got != want {
		t.Fatalf("Admit() = %q, want %q", got, want)
	}
}
