package query

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/httpapi"
)

func TestAddressWithdrawalsUseNumericIndexOrderingAndSnapshotCursor(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("11", 20)
	db := testDatabase(t,
		queryExpectation{
			contains: "ORDER BY canonical.number DESC",
			columns:  columns(2),
			rows:     [][]driver.Value{{"12", testHashBytes(12)}},
		},
		queryExpectation{
			contains: "ORDER BY withdrawal.withdrawal_index DESC",
			columns:  columns(7),
			rows: [][]driver.Value{
				{"10", "110", testWithdrawalAddressBytes(), "3200000000", "12", testHashBytes(12), "1700000012"},
				{"9", "109", testWithdrawalAddressBytes(), "1", "11", testHashBytes(11), "1700000011"},
				{"2", "102", testWithdrawalAddressBytes(), "2", "10", testHashBytes(10), "1700000010"},
			},
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 4 || arguments[2].Value != "12" || arguments[3].Value != 3 {
					return errors.New("address withdrawal query did not preserve the numeric snapshot and limit")
				}
				return nil
			},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	items, next, err := reader.AddressWithdrawals(context.Background(), address, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Index != "10" || items[1].Index != "9" || next == "" {
		t.Fatalf("items=%+v next=%q", items, next)
	}
	if items[0].BlockHash != testHash(12) || items[0].BlockTimestamp.IsZero() {
		t.Fatalf("first withdrawal identity=%+v", items[0])
	}
}

func TestAddressWithdrawalsRejectCursorForAnotherAddress(t *testing.T) {
	t.Parallel()
	cursor, err := httpapi.EncodeCursor(addressWithdrawalCursor{
		Version: 1, ChainID: "1", Address: "0x" + strings.Repeat("11", 20),
		SnapshotNumber: 12, SnapshotHash: testHash(12), BeforeIndex: 9,
		BeforeBlockNumber: 11, BeforeBlockHash: testHash(11),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := testReader(t, testDatabase(t), Options{ChainID: 1})
	_, _, err = reader.AddressWithdrawals(
		context.Background(), "0x"+strings.Repeat("22", 20), cursor, 25,
	)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error=%v, want ErrInvalidCursor", err)
	}
}

func TestAddressWithdrawalsRejectMalformedStoredQuantity(t *testing.T) {
	t.Parallel()
	db := testDatabase(t,
		queryExpectation{contains: "ORDER BY canonical.number DESC", columns: columns(2), rows: [][]driver.Value{{"12", testHashBytes(12)}}},
		queryExpectation{
			contains: "ORDER BY withdrawal.withdrawal_index DESC", columns: columns(7),
			rows: [][]driver.Value{{"18446744073709551616", "1", testWithdrawalAddressBytes(), "1", "12", testHashBytes(12), "1"}},
		},
	)
	reader := testReader(t, db, Options{ChainID: 1})
	_, _, err := reader.AddressWithdrawals(context.Background(), "0x"+strings.Repeat("11", 20), "", 25)
	if err == nil || !strings.Contains(err.Error(), "withdrawal index") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testWithdrawalAddressBytes() []byte {
	value := make([]byte, 20)
	for index := range value {
		value[index] = 0x11
	}
	return value
}
