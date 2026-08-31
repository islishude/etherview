package store

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCoreJSONBatchWriterBoundsRowsAndBytes(t *testing.T) {
	t.Parallel()
	type row struct {
		Index int             `json:"index"`
		Raw   json.RawMessage `json:"raw"`
	}
	raw := json.RawMessage(`"` + string(make([]byte, 16<<10)) + `"`)
	// Replace NUL bytes with JSON-safe content without creating one allocation
	// per input row.
	for index := 1; index < len(raw)-1; index++ {
		raw[index] = 'x'
	}
	wantRows := coreWriteBatchMaxRows*2 + 17
	writtenRows := 0
	batchCount := 0
	writer := newCoreJSONBatchWriter[row]("test core rows", func(payload json.RawMessage) error {
		batchCount++
		if len(payload) > coreWriteBatchMaxBytes {
			t.Fatalf("batch bytes = %d, maximum = %d", len(payload), coreWriteBatchMaxBytes)
		}
		var batch []row
		if err := json.Unmarshal(payload, &batch); err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 || len(batch) > coreWriteBatchMaxRows {
			t.Fatalf("batch rows = %d, maximum = %d", len(batch), coreWriteBatchMaxRows)
		}
		writtenRows += len(batch)
		return nil
	})
	for index := range wantRows {
		if err := writer.add(row{Index: index, Raw: raw}, len(raw)+coreWriteRowOverhead); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if writtenRows != wantRows || batchCount < 3 {
		t.Fatalf("written rows/batches = %d/%d, want %d/at least 3", writtenRows, batchCount, wantRows)
	}
}

func TestCoreJSONBatchWriterAllowsOneOversizedAtomicRow(t *testing.T) {
	t.Parallel()
	type row struct {
		Raw json.RawMessage `json:"raw"`
	}
	raw := make(json.RawMessage, coreWriteBatchMaxBytes+1)
	raw[0], raw[len(raw)-1] = '"', '"'
	for index := 1; index < len(raw)-1; index++ {
		raw[index] = 'x'
	}
	calls := 0
	writer := newCoreJSONBatchWriter[row]("oversized row", func(payload json.RawMessage) error {
		calls++
		if len(payload) <= coreWriteBatchMaxBytes {
			t.Fatalf("oversized singleton bytes = %d", len(payload))
		}
		return nil
	})
	if err := writer.add(row{Raw: raw}, len(raw)+coreWriteRowOverhead); err != nil {
		t.Fatal(err)
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("execute calls = %d, want 1", calls)
	}
}

func TestCoreJSONBatchWriterPropagatesExecutionFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("write failed")
	writer := newCoreJSONBatchWriter[int]("failed rows", func(json.RawMessage) error {
		return want
	})
	if err := writer.add(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := writer.close(); !errors.Is(err, want) {
		t.Fatalf("close error = %v, want %v", err, want)
	}
}
