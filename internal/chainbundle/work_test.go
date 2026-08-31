package chainbundle_test

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestMeasureWorkCountsOwnedRawSlicesAndRelationalRows(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:             1,
		TransactionTypes:   []uint8{types.LegacyTxType, types.LegacyTxType},
		LogsPerTransaction: 2,
		Withdrawals:        []*types.Withdrawal{{Index: 1, Validator: 2, Amount: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := chainbundle.MeasureWork(bundle)
	if err != nil {
		t.Fatal(err)
	}
	// One block, three transaction/receipt rows per transaction, four logs,
	// and one withdrawal.
	if work.Rows != 12 {
		t.Fatalf("rows = %d, want 12", work.Rows)
	}
	minimum := uint64(len(bundle.RawBlock))
	for _, raw := range bundle.RawReceipts {
		minimum += uint64(len(raw))
	}
	if work.RawBytes <= minimum {
		t.Fatalf("raw bytes = %d, want independently owned nested slices above %d", work.RawBytes, minimum)
	}
	combined := work
	if err := combined.Add(work); err != nil {
		t.Fatal(err)
	}
	if combined.RawBytes != 2*work.RawBytes || combined.Rows != 2*work.Rows {
		t.Fatalf("combined work = %+v, single = %+v", combined, work)
	}
}
