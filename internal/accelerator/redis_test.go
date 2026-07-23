package accelerator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/events"
	"github.com/redis/go-redis/v9"
)

type fakeRedisBackend struct {
	mu     sync.Mutex
	values map[string][]byte
	eval   func(string, []string, ...any) (any, error)
	get    func(string) ([]byte, error)
	closed bool
}

func (backend *fakeRedisBackend) Eval(_ context.Context, script string, keys []string, args ...any) (any, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.eval == nil {
		return nil, errors.New("eval unavailable")
	}
	return backend.eval(script, keys, args...)
}

func (backend *fakeRedisBackend) Get(_ context.Context, key string) ([]byte, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.get != nil {
		return backend.get(key)
	}
	value, ok := backend.values[key]
	if !ok {
		return nil, redis.Nil
	}
	return append([]byte(nil), value...), nil
}

func (backend *fakeRedisBackend) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.values[key] = append([]byte(nil), value...)
	return nil
}

func (backend *fakeRedisBackend) Close() error {
	backend.closed = true
	return nil
}

func TestRedisLimiterUsesSharedDecisionAndHashesIdentity(t *testing.T) {
	t.Parallel()
	var receivedKey string
	backend := &fakeRedisBackend{values: make(map[string][]byte)}
	backend.eval = func(script string, keys []string, args ...any) (any, error) {
		if script != redisTokenBucketScript || len(keys) != 1 {
			return nil, errors.New("unexpected script")
		}
		if len(args) != 3 {
			return nil, errors.New("limiter must use Redis server time")
		}
		receivedKey = keys[0]
		return []any{int64(0), int64(250)}, nil
	}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	allowed, retry := accelerator.Limiter(auth.NewMemoryLimiter(nil)).Allow(context.Background(), "key:plaintext-prefix", auth.Limit{Rate: 4, Burst: 8})
	if allowed || retry != 250*time.Millisecond {
		t.Fatalf("allowed=%v retry=%s", allowed, retry)
	}
	if receivedKey == "" || strings.Contains(receivedKey, "plaintext-prefix") {
		t.Fatalf("Redis key leaked identity: %q", receivedKey)
	}
}

func TestRedisLimiterOutageFallsBackWithoutLoggingBackendText(t *testing.T) {
	t.Parallel()
	secret := "redis://user:very-secret@example"
	backend := &fakeRedisBackend{values: make(map[string][]byte), eval: func(string, []string, ...any) (any, error) {
		return nil, errors.New("dial " + secret)
	}}
	var logs bytes.Buffer
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	fallback := auth.NewMemoryLimiter(func() time.Time { return time.Unix(5, 0) })
	limiter := accelerator.Limiter(fallback)
	if allowed, _ := limiter.Allow(context.Background(), "anonymous:192.0.2.1", auth.Limit{Rate: 1, Burst: 1}); !allowed {
		t.Fatal("local fallback rejected its first request")
	}
	if allowed, retry := limiter.Allow(context.Background(), "anonymous:192.0.2.1", auth.Limit{Rate: 1, Burst: 1}); allowed || retry != time.Second {
		t.Fatalf("allowed=%v retry=%s", allowed, retry)
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "very-secret") {
		t.Fatalf("Redis error text leaked to logs: %s", logs.String())
	}
}

func TestRedisLimiterOutageCircuitBypassesRequestsAndAllowsOneRecoveryProbe(t *testing.T) {
	t.Parallel()
	var nowNanos atomic.Int64
	nowNanos.Store(time.Unix(100, 0).UnixNano())
	probeStarted := make(chan struct{}, 1)
	probeRelease := make(chan struct{})
	evalCalls := 0
	blocking := false
	failing := true
	backend := &fakeRedisBackend{values: make(map[string][]byte)}
	backend.eval = func(script string, _ []string, _ ...any) (any, error) {
		if script != redisTokenBucketScript {
			return nil, errors.New("unexpected script")
		}
		evalCalls++
		if blocking {
			probeStarted <- struct{}{}
			<-probeRelease
		}
		if failing {
			return nil, errors.New("backend unavailable")
		}
		return []any{int64(1), int64(0)}, nil
	}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: 250 * time.Millisecond, CacheTTL: time.Minute,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		circuitNow: func() time.Time { return time.Unix(0, nowNanos.Load()) },
	})
	limiter := accelerator.Limiter(auth.NewMemoryLimiter(func() time.Time {
		return time.Unix(0, nowNanos.Load())
	}))
	limit := auth.Limit{Rate: 10, Burst: 10}
	if allowed, _ := limiter.Allow(context.Background(), "anonymous:first", limit); !allowed {
		t.Fatal("initial Redis failure did not use local fallback")
	}

	var bypasses sync.WaitGroup
	for index := range 100 {
		bypasses.Add(1)
		go func() {
			defer bypasses.Done()
			if allowed, _ := limiter.Allow(
				context.Background(),
				"anonymous:bypass-"+strconv.Itoa(index),
				limit,
			); !allowed {
				t.Errorf("circuit fallback rejected request %d", index)
			}
		}()
	}
	bypasses.Wait()
	backend.mu.Lock()
	if evalCalls != 1 {
		t.Fatalf("open circuit called Redis %d times", evalCalls)
	}
	blocking = true
	backend.mu.Unlock()

	nowNanos.Add(int64(250 * time.Millisecond))
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		limiter.Allow(context.Background(), "anonymous:probe", limit)
	}()
	<-probeStarted
	var concurrent sync.WaitGroup
	for index := range 100 {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			limiter.Allow(context.Background(), "anonymous:concurrent-"+strconv.Itoa(index), limit)
		}()
	}
	concurrent.Wait()
	close(probeRelease)
	<-probeDone
	backend.mu.Lock()
	if evalCalls != 2 {
		t.Fatalf("half-open circuit issued %d Redis calls, want 2 total", evalCalls)
	}
	blocking = false
	failing = false
	backend.mu.Unlock()

	nowNanos.Add(int64(500 * time.Millisecond))
	if allowed, _ := limiter.Allow(context.Background(), "anonymous:recovery", limit); !allowed {
		t.Fatal("successful recovery probe was rejected")
	}
	if allowed, _ := limiter.Allow(context.Background(), "anonymous:closed", limit); !allowed {
		t.Fatal("closed circuit request was rejected")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if evalCalls != 4 {
		t.Fatalf("recovered circuit Redis calls=%d want 4", evalCalls)
	}
}

func TestRedisCacheGenerationMakesRacingWritesUnreachable(t *testing.T) {
	t.Parallel()
	backend := &fakeRedisBackend{values: map[string][]byte{"test:1:cache:generation": []byte("0")}}
	var lastEvent, generation int64
	backend.eval = func(script string, keys []string, args ...any) (any, error) {
		if script != redisInvalidateScript {
			return nil, errors.New("unexpected script")
		}
		incoming, ok := redisInteger(args[0])
		if !ok {
			return nil, errors.New("invalid event")
		}
		if incoming > lastEvent {
			lastEvent = incoming
			generation++
			backend.values[keys[0]] = []byte(string(rune('0' + lastEvent)))
			backend.values[keys[1]] = []byte(string(rune('0' + generation)))
		}
		return generation, nil
	}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	_, observed, found := accelerator.CacheLoad(context.Background(), "status", 1024)
	if found || observed != 0 {
		t.Fatalf("initial found=%v generation=%d", found, observed)
	}
	if err := accelerator.Invalidate(context.Background(), events.Event{ID: 1, Type: "status"}); err != nil {
		t.Fatal(err)
	}
	accelerator.CacheStore(context.Background(), "status", observed, []byte("stale"))
	if value, current, found := accelerator.CacheLoad(context.Background(), "status", 1024); found || current != 1 || value != nil {
		t.Fatalf("stale write visible: value=%q generation=%d found=%v", value, current, found)
	}
	if err := accelerator.Invalidate(context.Background(), events.Event{ID: 1, Type: "status"}); err != nil {
		t.Fatal(err)
	}
	if generation != 1 {
		t.Fatalf("duplicate invalidation advanced generation to %d", generation)
	}
}

func TestRedisCacheHitRechecksGenerationBeforeServing(t *testing.T) {
	t.Parallel()
	backend := &fakeRedisBackend{values: make(map[string][]byte)}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	generationKey := "test:1:cache:generation"
	cacheKey := accelerator.cacheKey("status", 1)
	backend.values[generationKey] = []byte("1")
	backend.values[cacheKey] = []byte("stale")
	generationReads := 0
	backend.get = func(key string) ([]byte, error) {
		if key == generationKey {
			generationReads++
			if generationReads == 1 {
				return []byte("1"), nil
			}
			return []byte("2"), nil
		}
		value, ok := backend.values[key]
		if !ok {
			return nil, redis.Nil
		}
		return append([]byte(nil), value...), nil
	}
	value, generation, found := accelerator.CacheLoad(context.Background(), "status", 1024)
	if found || value != nil || generation != 2 || generationReads != 2 {
		t.Fatalf("value=%q generation=%d found=%v generation_reads=%d", value, generation, found, generationReads)
	}
}

func TestRedisInvalidationKeepsLargeEventIDExact(t *testing.T) {
	t.Parallel()
	const eventID = uint64(9_007_199_254_740_993)
	backend := &fakeRedisBackend{values: make(map[string][]byte)}
	backend.eval = func(script string, _ []string, args ...any) (any, error) {
		if script != redisInvalidateScript || len(args) != 1 || args[0] != "9007199254740993" {
			return nil, errors.New("event identity lost precision")
		}
		return int64(1), nil
	}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := accelerator.Invalidate(context.Background(), events.Event{ID: eventID, Type: "status"}); err != nil {
		t.Fatal(err)
	}
}

func TestRedisInvalidationOutageDisablesCacheAndLaterEventRecovers(t *testing.T) {
	t.Parallel()
	failing := true
	fenceCalls := 0
	backend := &fakeRedisBackend{values: map[string][]byte{"test:1:cache:generation": []byte("0")}}
	backend.eval = func(script string, _ []string, _ ...any) (any, error) {
		if script == redisFenceScript {
			fenceCalls++
		}
		if failing {
			return nil, errors.New("backend unavailable")
		}
		return int64(1), nil
	}
	accelerator := newRedisAccelerator(backend, RedisOptions{
		Namespace: "test", ChainID: 1, OperationTimeout: time.Second, CacheTTL: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// Model a real process whose startup fence could not reach Redis.
	accelerator.cacheFenced.Store(false)
	accelerator.cacheEnabled.Store(false)
	if err := accelerator.Invalidate(context.Background(), events.Event{ID: 1, Type: "status"}); err != nil {
		t.Fatalf("optional invalidation outage escaped: %v", err)
	}
	if accelerator.cacheEnabled.Load() {
		t.Fatal("cache remained enabled after failed invalidation")
	}
	if value, _, found := accelerator.CacheLoad(context.Background(), "status", 1024); found || value != nil {
		t.Fatalf("disabled cache returned %q", value)
	}
	failing = false
	if err := accelerator.Invalidate(context.Background(), events.Event{ID: 2, Type: "status"}); err != nil {
		t.Fatal(err)
	}
	if !accelerator.cacheEnabled.Load() {
		t.Fatal("cache did not recover after a successful later invalidation")
	}
	if !accelerator.cacheFenced.Load() || fenceCalls != 2 {
		t.Fatalf("startup fence was not retried safely: fenced=%v calls=%d", accelerator.cacheFenced.Load(), fenceCalls)
	}
}
