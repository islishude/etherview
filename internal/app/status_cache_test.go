package app

import (
	"context"
	"testing"

	"github.com/islishude/etherview/internal/httpapi"
)

type appStatusReader struct {
	httpapi.Reader
	snapshot httpapi.StatusSnapshot
	calls    int
}

func (reader *appStatusReader) Status(context.Context) (httpapi.StatusSnapshot, error) {
	reader.calls++
	return reader.snapshot, nil
}

type appStatusCache struct {
	value      []byte
	generation int64
	storedAt   int64
	key        string
}

func (cache *appStatusCache) CacheLoad(context.Context, string, int) ([]byte, int64, bool) {
	return append([]byte(nil), cache.value...), cache.generation, cache.value != nil
}

func (cache *appStatusCache) CacheStore(_ context.Context, key string, generation int64, value []byte) {
	cache.key = key
	cache.storedAt = generation
	cache.value = append([]byte(nil), value...)
}

func TestRedisStatusReaderCachesOnlyBoundedSnapshotAtObservedGeneration(t *testing.T) {
	t.Parallel()
	fallback := &appStatusReader{snapshot: httpapi.StatusSnapshot{LatestBlock: 12, IndexedBlock: 10, CoreReady: true}}
	cache := &appStatusCache{generation: 7}
	reader := redisStatusReader{Reader: fallback, cache: cache, chainID: 11155111}
	first, err := reader.Status(context.Background())
	if err != nil || first.LatestBlock != 12 || fallback.calls != 1 || cache.storedAt != 7 || cache.key != "status:11155111" {
		t.Fatalf("first=%+v calls=%d cache=%+v err=%v", first, fallback.calls, cache, err)
	}
	second, err := reader.Status(context.Background())
	if err != nil || second != first || fallback.calls != 1 {
		t.Fatalf("second=%+v calls=%d err=%v", second, fallback.calls, err)
	}
}

func TestRedisStatusReaderCorruptCacheFallsBackToPostgreSQL(t *testing.T) {
	t.Parallel()
	fallback := &appStatusReader{snapshot: httpapi.StatusSnapshot{LatestBlock: 2}}
	cache := &appStatusCache{generation: 3, value: []byte(`{"unknown":true}`)}
	reader := redisStatusReader{Reader: fallback, cache: cache, chainID: 1}
	status, err := reader.Status(context.Background())
	if err != nil || status.LatestBlock != 2 || fallback.calls != 1 || cache.storedAt != 3 {
		t.Fatalf("status=%+v calls=%d cache=%+v err=%v", status, fallback.calls, cache, err)
	}
}
