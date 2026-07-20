package catalog

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type fakeTraceBlobStore struct {
	value  []byte
	found  bool
	getErr error
	putErr error
	getKey string
	putKey string
	put    []byte
}

func (store *fakeTraceBlobStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	store.getKey = key
	return append([]byte(nil), store.value...), store.found, store.getErr
}

func (store *fakeTraceBlobStore) Put(_ context.Context, key string, value []byte) error {
	store.putKey = key
	store.put = append([]byte(nil), value...)
	return store.putErr
}

func cachedTraceFixture() TransactionTrace {
	input, output := "0x1234", "0x"
	from, to := "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"
	value, gas := "1", "2"
	return TransactionTrace{
		ChainID: "1", BlockNumber: "100", BlockHash: "0x" + strings.Repeat("aa", 32),
		TransactionHash: "0x" + strings.Repeat("bb", 32), TransactionIndex: "0", State: StageComplete,
		Frames: []TraceFrame{{
			Path: []uint32{}, ParentPath: []uint32{}, Depth: 0, CallType: "CALL",
			From: &from, To: &to, Value: &value, Gas: &gas, GasUsed: &gas,
			Input: &input, Output: &output,
		}},
	}
}

func traceStageGenerationStep(generation int64) catalogQueryStep {
	return catalogQueryStep{
		contains: "FROM published_block_stage_results",
		rows:     catalogRows(3, []driver.Value{"complete", int64(42), generation}),
	}
}

func traceIdentitySteps(generation int64) []catalogQueryStep {
	return []catalogQueryStep{
		{contains: "FROM transaction_inclusions AS inclusion", rows: catalogRows(3, []driver.Value{"100", bytesOf(0xaa, 32), "0"})},
		traceStageGenerationStep(generation),
	}
}

func TestTransactionTraceUsesGenerationBoundS3CacheAfterPostcheck(t *testing.T) {
	t.Parallel()
	trace := cachedTraceFixture()
	encoded, err := json.Marshal(cachedTransactionTrace{Schema: 1, JobID: 42, JobGeneration: 3, Trace: trace})
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeTraceBlobStore{value: encoded, found: true}
	steps := append(traceIdentitySteps(3), traceIdentitySteps(3)...)
	catalog, backend := openCatalogWithOptions(t, Options{
		TraceCache: cache, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, steps...)
	got, err := catalog.TransactionTrace(context.Background(), "1", trace.TransactionHash)
	if err != nil || len(got.Frames) != 1 || got.BlockHash != trace.BlockHash {
		t.Fatalf("trace=%+v err=%v", got, err)
	}
	if cache.getKey != "trace/v1/1/"+strings.Repeat("aa", 32)+"/42-3/"+strings.Repeat("bb", 32)+".json" || cache.putKey != "" {
		t.Fatalf("get=%q put=%q", cache.getKey, cache.putKey)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionTraceS3OutageFallsBackToPostgreSQL(t *testing.T) {
	t.Parallel()
	cache := &fakeTraceBlobStore{getErr: errors.New("s3://access:secret@example unavailable"), putErr: errors.New("write unavailable")}
	steps := traceIdentitySteps(3)
	steps = append(steps, traceIdentitySteps(3)...)
	steps = append(steps, catalogQueryStep{contains: "FROM normalized_traces", rows: catalogRows(14, traceRow("", nil, 0, "CALL"))})
	catalog, backend := openCatalogWithOptions(t, Options{
		TraceCache: cache, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, steps...)
	trace, err := catalog.TransactionTrace(context.Background(), "1", "0x"+strings.Repeat("bb", 32))
	if err != nil || len(trace.Frames) != 1 || cache.putKey == "" {
		t.Fatalf("trace=%+v put=%q err=%v", trace, cache.putKey, err)
	}
	assertCatalogConsumed(t, backend)
}

func TestTransactionTraceReplayGenerationNeverServesStaleS3Object(t *testing.T) {
	t.Parallel()
	trace := cachedTraceFixture()
	encoded, err := json.Marshal(cachedTransactionTrace{Schema: 1, JobID: 42, JobGeneration: 3, Trace: trace})
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeTraceBlobStore{value: encoded, found: true}
	steps := traceIdentitySteps(3)
	steps = append(steps, traceIdentitySteps(4)...)
	steps = append(steps, traceIdentitySteps(4)...)
	steps = append(steps, catalogQueryStep{contains: "FROM normalized_traces", rows: catalogRows(14, traceRow("", nil, 0, "CALL"))})
	catalog, backend := openCatalogWithOptions(t, Options{
		TraceCache: cache, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, steps...)
	if _, err := catalog.TransactionTrace(context.Background(), "1", trace.TransactionHash); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cache.putKey, "/42-4/") {
		t.Fatalf("new generation was not cached separately: %q", cache.putKey)
	}
	assertCatalogConsumed(t, backend)
}
