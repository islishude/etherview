package chainbundle

import (
	"encoding/json"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"os"
	"testing"
)

func TestAmsterdamGasAccounting(t *testing.T) {
	for _, tc := range []struct {
		name              string
		amsterdam         bool
		gross, net, limit uint64
		invalid           bool
	}{
		{"legacy equal", false, 21000, 21000, 30000, false},
		{"legacy mismatch", false, 25000, 21000, 30000, true},
		{"Amsterdam refunded", true, 25000, 21000, 30000, false},
		{"Amsterdam no refund", true, 21000, 21000, 30000, false},
		{"two gas dimensions", true, 21000, 25000, 30000, false},
		{"net exceeds both dimensions", true, 21000, 42001, 30000, true},
		{"two dimension upper bound", true, 21000, 42000, 30000, false},
		{"gross exceeds limit", true, 31000, 21000, 30000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &types.Header{GasUsed: tc.gross, GasLimit: tc.limit}
			if tc.amsterdam {
				slot := uint64(0)
				hash := common.Hash{}
				h.SlotNumber = &slot
				h.BlockAccessListHash = &hash
			}
			if err := ValidateBlockGas(h, tc.net); (err != nil) != tc.invalid {
				t.Fatalf("err=%v", err)
			}
		})
	}
	for _, h := range []*types.Header{{SlotNumber: new(uint64)}, {BlockAccessListHash: new(common.Hash)}} {
		if ValidateBlockGas(h, 0) == nil {
			t.Fatal("accepted incomplete Amsterdam header")
		}
	}
}

// This fixture was mined by pinned Geth 1.17.7 in the disposable Preview
// network. Contract creation charges state gas, so receipt gas exceeds the
// header's maximum dimension. Raw receipts still authenticate to its root.
func TestAmsterdamRealGethTwoDimensionBlock(t *testing.T) {
	blockRaw, err := os.ReadFile("testdata/amsterdam-block.json")
	if err != nil {
		t.Fatal(err)
	}
	receiptRaw, err := os.ReadFile("testdata/amsterdam-receipts.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipts []json.RawMessage
	if err := json.Unmarshal(receiptRaw, &receipts); err != nil {
		t.Fatal(err)
	}
	block, err := DecodeBlock(blockRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := block.WithReceipts(receipts)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Block.Header().SlotNumber == nil || bundle.Receipts[0].GasUsed <= bundle.Block.GasUsed() {
		t.Fatal("fixture does not exercise two-dimensional gas")
	}
	for _, field := range []string{"slotNumber", "blockAccessListHash"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(blockRaw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeBlock(raw, nil); err == nil {
			t.Fatalf("accepted header missing %s", field)
		}
	}
}
