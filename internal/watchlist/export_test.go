package watchlist

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCSVResourceFailuresReturnNoFile(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	raw, err := json.Marshal(map[string]any{
		"kind": "transaction", "block_number": "1", "block_hash": "0x" + strings.Repeat("11", 32),
		"timestamp": "1700000000", "transaction_hash": "0x" + strings.Repeat("22", 32),
		"transaction_index": "0", "from": address, "to": address, "value": "0xffff", "status": "0x1",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Export{BlockNumber: "1", BlockHash: "0x" + strings.Repeat("33", 32)}
	header, err := encodeCSV(t.Context(), "1", snapshot, nil, address)
	if err != nil {
		t.Fatal(err)
	}
	one, err := encodeCSV(t.Context(), "1", snapshot, [][]byte{raw}, address)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the byte cap independently of the earlier SQL row-count cap.
	count := (MaximumExportBytes - len(header)) / (len(one) - len(header))
	rows := make([][]byte, count+1)
	for i := range rows {
		rows[i] = raw
	}
	if file, err := encodeCSV(t.Context(), "1", snapshot, rows[:count], address); err != nil || len(file) > MaximumExportBytes {
		t.Fatalf("file below byte cap: size=%d error=%v", len(file), err)
	}
	if file, err := encodeCSV(t.Context(), "1", snapshot, rows, address); !errors.Is(err, ErrExportLimit) || len(file) != 0 {
		t.Fatalf("oversized file: size=%d error=%v", len(file), err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, records := range [][][]byte{nil, {raw}} {
		if file, err := encodeCSV(ctx, "1", snapshot, records, address); !errors.Is(err, context.DeadlineExceeded) || len(file) != 0 {
			t.Fatalf("expired generation: size=%d error=%v", len(file), err)
		}
	}
}

func TestCSVQuotingAndFormulaProtection(t *testing.T) {
	var b strings.Builder
	writer := csv.NewWriter(&b)
	cells := []string{"=cmd()", " @cmd()", "a,\"b\"\nc", "115792089237316195423570985008687907853269984665640564039457584007913129639935"}
	protected := make([]string, len(cells))
	for i, cell := range cells {
		protected[i] = safeCell(cell)
	}
	if err := writer.Write(protected); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil || len(rows) != 1 || rows[0][0] != "'=cmd()" || rows[0][1] != "' @cmd()" || rows[0][2] != cells[2] || rows[0][3] != cells[3] {
		t.Fatalf("CSV roundtrip: %v %v", rows, err)
	}
}
