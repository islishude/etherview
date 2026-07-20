package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/islishude/etherview/internal/httpapi"
)

const maximumCachedStatusBytes = 16 << 10

// redisStatusReader caches only the bounded status snapshot. Every status
// mutation appends a durable runtime event, whose relay advances the Redis
// generation before publishing. All other query methods continue to read
// PostgreSQL directly because their invalidation sources are broader.
type redisStatusReader struct {
	httpapi.Reader
	cache   statusCache
	chainID uint64
}

type statusCache interface {
	CacheLoad(context.Context, string, int) ([]byte, int64, bool)
	CacheStore(context.Context, string, int64, []byte)
}

func (reader redisStatusReader) Status(ctx context.Context) (httpapi.StatusSnapshot, error) {
	if reader.Reader == nil {
		return httpapi.StatusSnapshot{}, errors.New("status cache reader is missing its PostgreSQL fallback")
	}
	if reader.cache == nil {
		return reader.Reader.Status(ctx)
	}
	key := "status:" + strconv.FormatUint(reader.chainID, 10)
	value, generation, found := reader.cache.CacheLoad(ctx, key, maximumCachedStatusBytes)
	if found {
		var snapshot httpapi.StatusSnapshot
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&snapshot); err == nil {
			var trailing any
			if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
				return snapshot, nil
			}
		}
	}
	snapshot, err := reader.Reader.Status(ctx)
	if err != nil {
		return httpapi.StatusSnapshot{}, err
	}
	if value, err := json.Marshal(snapshot); err == nil && len(value) <= maximumCachedStatusBytes {
		reader.cache.CacheStore(ctx, key, generation, value)
	}
	return snapshot, nil
}
