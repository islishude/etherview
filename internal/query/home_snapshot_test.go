package query

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
)

func TestHomeSnapshotUsesOneTransactionAndReturnsBoundedCanonicalActivity(t *testing.T) {
	t.Parallel()
	tipHash := testHashBytes(3)
	checkLimit := func(arguments []driver.NamedValue) error {
		if len(arguments) != 3 || fmt.Sprint(arguments[2].Value) != "6" {
			return fmt.Errorf("home query arguments = %+v", arguments)
		}
		return nil
	}
	db := testDatabase(t,
		queryExpectation{
			contains: "SELECT MAX(id) FROM runtime_events",
			columns:  columns(1), rows: [][]driver.Value{{int64(42)}},
		},
		queryExpectation{
			contains: "configuration.configured_start::text",
			columns:  columns(9), rows: [][]driver.Value{{
				"0", "2", tipHash, "2", tipHash, "2", tipHash, "1", "0",
			}},
		},
		queryExpectation{
			contains: "FROM sync_runtime_status",
			columns:  columns(5), rows: [][]driver.Value{{"2", "2", "2", true, true}},
		},
		queryExpectation{
			contains: "ORDER BY canonical.number DESC",
			columns:  columns(2), rows: [][]driver.Value{{"2", tipHash}},
		},
		queryExpectation{
			contains: "canonical.number <= $2::numeric",
			columns:  columns(6), rows: [][]driver.Value{{
				testBlockRaw(2, 3, 1, 0), "2", tipHash, true, "1", "0",
			}},
			check: checkLimit,
		},
		queryExpectation{
			contains: "inclusion.block_number <= $2::numeric",
			columns:  columns(10), rows: [][]driver.Value{{
				testTransactionRawAt(2, 3, 102, 0),
				testReceiptRawAt(2, 3, 102, 0, "0x1"),
				"2", tipHash, int64(0), testTransactionHashBytes(102),
				true, "1", "0", testBlockRaw(2, 3, 1, 0),
			}},
			check: checkLimit,
		},
	)
	reader := testReader(t, db, Options{
		ChainID: 1,
		LatestBlock: func(context.Context) (uint64, error) {
			t.Fatal("home snapshot called external latest-block source")
			return 0, nil
		},
	})
	snapshot, err := reader.HomeSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventID != 42 || !snapshot.Status.CoreReady ||
		snapshot.Status.LatestBlock != 2 || snapshot.Status.IndexedBlock != 2 ||
		snapshot.Status.CoverageEnd != 2 ||
		len(snapshot.Blocks) != 1 || snapshot.Blocks[0].Number != "2" ||
		len(snapshot.Transactions) != 1 || !snapshot.Transactions[0].Canonical {
		t.Fatalf("home snapshot = %+v", snapshot)
	}
}

func TestHomeSnapshotSupportsEmptyChainWithoutRuntimeEvents(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{
			contains: "SELECT MAX(id) FROM runtime_events",
			columns:  columns(1), rows: [][]driver.Value{{nil}},
		},
		queryExpectation{
			contains: "configuration.configured_start::text",
			columns:  columns(9), rows: [][]driver.Value{{
				"0", nil, nil, nil, nil, nil, nil, nil, nil,
			}},
		},
		queryExpectation{
			contains: "FROM sync_runtime_status",
			columns:  columns(5),
		},
		queryExpectation{
			contains: "ORDER BY canonical.number DESC",
			columns:  columns(2),
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	snapshot, err := reader.HomeSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventID != 0 || len(snapshot.Blocks) != 0 ||
		len(snapshot.Transactions) != 0 || snapshot.Status.CoreReady {
		t.Fatalf("empty home snapshot = %+v", snapshot)
	}
}
