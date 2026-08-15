package query

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/httpapi"
)

func TestAddressTransactionsUseSnapshotCursorAndIndexedCandidateBranches(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("11", 20)
	db := testDatabase(t,
		queryExpectation{
			contains: "ORDER BY canonical.number DESC",
			columns:  columns(2),
			rows:     [][]driver.Value{{"2", testHashBytes(3)}},
		},
		queryExpectation{
			contains: "lower(raw->>'contractAddress') = $3",
			columns:  columns(17),
			rows: [][]driver.Value{
				{testTransactionRawAt(2, 3, 102, 1), testReceiptRawAt(2, 3, 102, 1, "0x1"), "2", testHashBytes(3), int64(1), testTransactionHashBytes(102), true, "1", "0", testBlockRaw(2, 3, 2, 1), false, nil, nil, nil, nil, nil, nil},
				{testTransactionRawAt(2, 3, 101, 0), testReceiptRawAt(2, 3, 101, 0, "0x1"), "2", testHashBytes(3), int64(0), testTransactionHashBytes(101), true, "1", "0", testBlockRaw(2, 3, 2, 1), false, nil, nil, nil, nil, nil, nil},
			},
			check: func(arguments []driver.NamedValue) error {
				if arguments[2].Value != address {
					return errors.New("address candidate query did not normalize the address")
				}
				return nil
			},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	items, next, err := reader.AddressTransactions(context.Background(), address, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || next == "" || items[0].BlockTimestamp == nil {
		t.Fatalf("items=%+v next=%q", items, next)
	}
}

func TestAddressTransactionsRejectCursorForAnotherAddress(t *testing.T) {
	t.Parallel()
	cursor, err := httpapi.EncodeCursor(addressTransactionCursor{
		Version: 1, ChainID: "1", Address: "0x" + strings.Repeat("11", 20),
		SnapshotNumber: 10, SnapshotHash: testHash(10),
		BeforeBlockNumber: 8, BeforeBlockHash: testHash(8),
		BeforeTxIndex: 1, BeforeTxHash: testHash(80),
	})
	if err != nil {
		t.Fatal(err)
	}
	db := testDatabase(t)
	reader := testReader(t, db, Options{ChainID: 1})
	_, _, err = reader.AddressTransactions(
		context.Background(), "0x"+strings.Repeat("22", 20), cursor, 25,
	)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error=%v, want ErrInvalidCursor", err)
	}
}
