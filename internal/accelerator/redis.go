package accelerator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/events"
	"github.com/redis/go-redis/v9"
)

const redisTokenBucketScript = `
local tokens = tonumber(redis.call('HGET', KEYS[1], 'tokens'))
local last = tonumber(redis.call('HGET', KEYS[1], 'last'))
local server_time = redis.call('TIME')
local now = (tonumber(server_time[1]) * 1000) + math.floor(tonumber(server_time[2]) / 1000)
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
if tokens == nil or last == nil then
  tokens = burst
  last = now
end
if now > last then
  tokens = math.min(burst, tokens + ((now - last) * rate / 1000))
  last = now
end
local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = math.ceil((1 - tokens) * 1000 / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'last', last)
redis.call('PEXPIRE', KEYS[1], ttl)
return {allowed, retry}
`

const redisInvalidateScript = `
local current = redis.call('GET', KEYS[1]) or '0'
local incoming = ARGV[1]
if string.match(current, '^%d+$') == nil or string.match(incoming, '^%d+$') == nil then
  return redis.error_reply('invalid event cursor')
end
local generation = tonumber(redis.call('GET', KEYS[2])) or 0
local newer = string.len(incoming) > string.len(current) or
  (string.len(incoming) == string.len(current) and incoming > current)
if not newer then
  return generation
end
generation = redis.call('INCR', KEYS[2])
redis.call('SET', KEYS[1], incoming)
return generation
`

const redisFenceScript = `
return redis.call('INCR', KEYS[1])
`

type RedisOptions struct {
	Namespace        string
	ChainID          uint64
	OperationTimeout time.Duration
	CacheTTL         time.Duration
	Logger           *slog.Logger
}

type redisBackend interface {
	Eval(context.Context, string, []string, ...any) (any, error)
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	Close() error
}

type goRedisBackend struct{ client *redis.Client }

func (backend goRedisBackend) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return backend.client.Eval(ctx, script, keys, args...).Result()
}

func (backend goRedisBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return backend.client.Get(ctx, key).Bytes()
}

func (backend goRedisBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return backend.client.Set(ctx, key, value, ttl).Err()
}

func (backend goRedisBackend) Close() error { return backend.client.Close() }

// RedisAccelerator provides an optional cache generation and shared rate
// limiter. Backend failures disable/bypass caching and use the caller's local
// limiter; they never become API availability failures.
type RedisAccelerator struct {
	backend          redisBackend
	prefix           string
	operationTimeout time.Duration
	cacheTTL         time.Duration
	logger           *slog.Logger
	cacheEnabled     atomic.Bool
	cacheFenced      atomic.Bool
}

func NewRedisAccelerator(rawURL string, options RedisOptions) (*RedisAccelerator, error) {
	parsed, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("parse Redis accelerator URL")
	}
	if options.Namespace == "" {
		options.Namespace = "etherview"
	}
	if options.ChainID == 0 {
		return nil, errors.New("redis accelerator chain ID is zero")
	}
	if options.OperationTimeout <= 0 {
		options.OperationTimeout = 500 * time.Millisecond
	}
	if options.CacheTTL <= 0 {
		options.CacheTTL = 30 * time.Second
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	parsed.DialTimeout = options.OperationTimeout
	parsed.ReadTimeout = options.OperationTimeout
	parsed.WriteTimeout = options.OperationTimeout
	accelerator := newRedisAccelerator(goRedisBackend{client: redis.NewClient(parsed)}, options)
	// A new process must first advance the cache generation. Otherwise stale
	// entries written before an outage could become visible after restart.
	accelerator.cacheEnabled.Store(false)
	accelerator.cacheFenced.Store(false)
	return accelerator, nil
}

func newRedisAccelerator(backend redisBackend, options RedisOptions) *RedisAccelerator {
	accelerator := &RedisAccelerator{
		backend:          backend,
		prefix:           fmt.Sprintf("%s:%d", options.Namespace, options.ChainID),
		operationTimeout: options.OperationTimeout,
		cacheTTL:         options.CacheTTL,
		logger:           options.Logger,
	}
	accelerator.cacheEnabled.Store(true)
	accelerator.cacheFenced.Store(true)
	return accelerator
}

func (accelerator *RedisAccelerator) Close() error {
	if accelerator == nil || accelerator.backend == nil {
		return nil
	}
	return accelerator.backend.Close()
}

func (accelerator *RedisAccelerator) Limiter(fallback auth.Limiter) auth.Limiter {
	if fallback == nil {
		fallback = auth.NewMemoryLimiter(nil)
	}
	return &redisLimiter{accelerator: accelerator, fallback: fallback}
}

// FenceCache advances a fresh process generation. Failure is intentionally
// swallowed after disabling reads; a later durable event invalidation retries
// the fence and re-enables the cache safely.
func (accelerator *RedisAccelerator) FenceCache(ctx context.Context) {
	if accelerator == nil || accelerator.backend == nil {
		return
	}
	operationCtx, cancel := accelerator.operationContext(ctx)
	defer cancel()
	if _, err := accelerator.backend.Eval(operationCtx, redisFenceScript, []string{accelerator.prefix + ":cache:generation"}); err != nil {
		accelerator.cacheFenced.Store(false)
		accelerator.disableCache(ctx, err)
		return
	}
	accelerator.cacheFenced.Store(true)
	accelerator.cacheEnabled.Store(true)
}

type redisLimiter struct {
	accelerator *RedisAccelerator
	fallback    auth.Limiter
}

func (limiter *redisLimiter) Allow(ctx context.Context, key string, limit auth.Limit) (bool, time.Duration) {
	if limiter == nil || limiter.fallback == nil {
		return false, time.Second
	}
	if limiter.accelerator == nil || limiter.accelerator.backend == nil || limit.Rate <= 0 || limit.Burst <= 0 {
		return limiter.fallback.Allow(ctx, key, limit)
	}
	accelerator := limiter.accelerator
	digest := sha256.Sum256([]byte(key + "\x00" + strconv.Itoa(limit.Rate) + "\x00" + strconv.Itoa(limit.Burst)))
	redisKey := accelerator.prefix + ":rate:" + hex.EncodeToString(digest[:])
	// Retain idle buckets long enough to preserve a full refill without keeping
	// abandoned client identities indefinitely.
	ttl := max(time.Duration(limit.Burst*2)*time.Second/time.Duration(limit.Rate), time.Second)
	operationCtx, cancel := accelerator.operationContext(ctx)
	defer cancel()
	result, err := accelerator.backend.Eval(
		operationCtx, redisTokenBucketScript, []string{redisKey},
		limit.Rate, limit.Burst, ttl.Milliseconds(),
	)
	allowed, retry, parseErr := parseRedisLimitResult(result)
	if err != nil || parseErr != nil {
		accelerator.logBypass(ctx, "optional Redis rate limiter unavailable; using process-local limiter", errors.Join(err, parseErr))
		return limiter.fallback.Allow(ctx, key, limit)
	}
	return allowed, retry
}

func parseRedisLimitResult(result any) (bool, time.Duration, error) {
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return false, 0, errors.New("redis limiter returned an invalid tuple")
	}
	allowed, ok := redisInteger(values[0])
	if !ok || (allowed != 0 && allowed != 1) {
		return false, 0, errors.New("redis limiter returned an invalid decision")
	}
	retryMilliseconds, ok := redisInteger(values[1])
	if !ok || retryMilliseconds < 0 {
		return false, 0, errors.New("redis limiter returned an invalid retry delay")
	}
	return allowed == 1, time.Duration(retryMilliseconds) * time.Millisecond, nil
}

func redisInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// Invalidate advances one idempotent cache generation for each durable event.
// On backend loss it disables cache reads before returning success, allowing
// the event relay to advance and publish without making Redis authoritative.
func (accelerator *RedisAccelerator) Invalidate(ctx context.Context, event events.Event) error {
	if accelerator == nil || accelerator.backend == nil {
		return nil
	}
	operationCtx, cancel := accelerator.operationContext(ctx)
	defer cancel()
	if !accelerator.cacheFenced.Load() {
		if _, err := accelerator.backend.Eval(operationCtx, redisFenceScript, []string{accelerator.prefix + ":cache:generation"}); err != nil {
			accelerator.cacheFenced.Store(false)
			accelerator.disableCache(ctx, err)
			return nil
		}
		accelerator.cacheFenced.Store(true)
	}
	_, err := accelerator.backend.Eval(operationCtx, redisInvalidateScript,
		[]string{accelerator.prefix + ":cache:last-event", accelerator.prefix + ":cache:generation"}, strconv.FormatUint(event.ID, 10))
	if err != nil {
		accelerator.cacheEnabled.Store(false)
		accelerator.logBypass(ctx, "optional Redis cache invalidation unavailable; cache bypassed", err)
		return nil
	}
	accelerator.cacheEnabled.Store(true)
	return nil
}

// CacheLoad returns both the payload and the generation observed before the
// caller reads PostgreSQL. CacheStore must reuse that generation so an
// invalidation racing the database read makes the late write unreachable.
func (accelerator *RedisAccelerator) CacheLoad(ctx context.Context, logicalKey string, maximumBytes int) ([]byte, int64, bool) {
	if accelerator == nil || accelerator.backend == nil || !accelerator.cacheEnabled.Load() || maximumBytes <= 0 {
		return nil, 0, false
	}
	operationCtx, cancel := accelerator.operationContext(ctx)
	defer cancel()
	generation, err := accelerator.cacheGeneration(operationCtx)
	if err != nil {
		accelerator.disableCache(ctx, err)
		return nil, 0, false
	}
	value, err := accelerator.backend.Get(operationCtx, accelerator.cacheKey(logicalKey, generation))
	if errors.Is(err, redis.Nil) {
		return nil, generation, false
	}
	if err != nil {
		accelerator.disableCache(ctx, err)
		return nil, 0, false
	}
	if len(value) > maximumBytes {
		return nil, generation, false
	}
	currentGeneration, err := accelerator.cacheGeneration(operationCtx)
	if err != nil {
		accelerator.disableCache(ctx, err)
		return nil, 0, false
	}
	if currentGeneration != generation {
		return nil, currentGeneration, false
	}
	return append([]byte(nil), value...), generation, true
}

func (accelerator *RedisAccelerator) CacheStore(ctx context.Context, logicalKey string, generation int64, value []byte) {
	if accelerator == nil || accelerator.backend == nil || !accelerator.cacheEnabled.Load() || generation < 0 {
		return
	}
	operationCtx, cancel := accelerator.operationContext(ctx)
	defer cancel()
	if err := accelerator.backend.Set(operationCtx, accelerator.cacheKey(logicalKey, generation), value, accelerator.cacheTTL); err != nil {
		accelerator.disableCache(ctx, err)
	}
}

func (accelerator *RedisAccelerator) cacheGeneration(ctx context.Context) (int64, error) {
	value, err := accelerator.backend.Get(ctx, accelerator.prefix+":cache:generation")
	if errors.Is(err, redis.Nil) {
		return 0, errors.New("redis cache generation is missing")
	}
	if err != nil {
		return 0, err
	}
	generation, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	if err != nil || generation < 0 {
		return 0, errors.New("redis cache generation is invalid")
	}
	return generation, nil
}

func (accelerator *RedisAccelerator) cacheKey(logicalKey string, generation int64) string {
	digest := sha256.Sum256([]byte(logicalKey))
	return accelerator.prefix + ":cache:" + strconv.FormatInt(generation, 10) + ":" + hex.EncodeToString(digest[:])
}

func (accelerator *RedisAccelerator) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, accelerator.operationTimeout)
}

func (accelerator *RedisAccelerator) disableCache(ctx context.Context, err error) {
	accelerator.cacheEnabled.Store(false)
	accelerator.logBypass(ctx, "optional Redis cache unavailable; cache bypassed", err)
}

func (accelerator *RedisAccelerator) logBypass(ctx context.Context, message string, err error) {
	if accelerator.logger != nil && err != nil {
		accelerator.logger.WarnContext(ctx, message, "error_type", fmt.Sprintf("%T", err))
	}
}
