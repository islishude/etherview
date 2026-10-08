package query

import (
	"encoding/json"
	"math/big"
	"strconv"
	"testing"

	dbgen "github.com/islishude/etherview/internal/db/gen"

	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestTransactionModelPublishesBlobTypedFields(t *testing.T) {
	t.Parallel()
	blobGasUsed := uint64(131072)
	excessBlobGas := uint64(0)
	bundle, err := testfixture.New(testfixture.Options{
		Number:           12,
		TransactionTypes: []uint8{types.BlobTxType},
		BaseFee:          big.NewInt(1),
		BlobGasUsed:      &blobGasUsed,
		ExcessBlobGas:    &excessBlobGas,
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction := bundle.Block.Transactions()[0]
	var receipt map[string]any
	if err := json.Unmarshal(bundle.RawReceipts[0], &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["blobGasUsed"] = "0x20000"
	receipt["blobGasPrice"] = "0x5"
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	reader := &PostgresReader{}
	model, err := reader.transactionModel(
		bundle.RawTransactions[0], rawReceipt,
		strconv.FormatUint(bundle.Block.Time(), 10), pgtype.Text{String: "0x1", Valid: true},
		"12", bundle.Block.Hash().Bytes(), 0, transaction.Hash().Bytes(), true,
		pgtype.Text{String: "12", Valid: true}, pgtype.Text{String: "10", Valid: true}, 12,
	)
	if err != nil {
		t.Fatal(err)
	}
	if model.Type == nil || *model.Type != "3" {
		t.Fatalf("type=%v", model.Type)
	}
	if model.AccessList == nil || len(*model.AccessList) != 0 {
		t.Fatalf("access list=%v", model.AccessList)
	}
	if model.BlobVersionedHashes == nil || len(*model.BlobVersionedHashes) != 1 ||
		(*model.BlobVersionedHashes)[0] != transaction.BlobHashes()[0].Hex() {
		t.Fatalf("blob hashes=%v", model.BlobVersionedHashes)
	}
	if model.MaxFeePerBlobGas == nil || *model.MaxFeePerBlobGas != "3" {
		t.Fatalf("max fee per blob gas=%v", model.MaxFeePerBlobGas)
	}
	if model.BaseFeePerGas == nil || *model.BaseFeePerGas != "1" {
		t.Fatalf("base fee per gas=%v", model.BaseFeePerGas)
	}
	if model.BlobBaseFeePerGas == nil || *model.BlobBaseFeePerGas != "5" {
		t.Fatalf("blob base fee per gas=%v", model.BlobBaseFeePerGas)
	}
}

func TestBlockModelPublishesPresentAndEmptyWithdrawals(t *testing.T) {
	t.Parallel()
	withdrawals := []*types.Withdrawal{{
		Index:     7,
		Validator: 42,
		Address:   testTransactionSender(),
		Amount:    3200000000,
	}}
	bundle, err := testfixture.New(testfixture.Options{Number: 12, Withdrawals: withdrawals})
	if err != nil {
		t.Fatal(err)
	}
	reader := &PostgresReader{}
	record, err := reader.decodeBlock(projectedBlockRow(t, bundle), false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Model.Withdrawals == nil || len(*record.Model.Withdrawals) != 1 ||
		(*record.Model.Withdrawals)[0].Index != "7" || (*record.Model.Withdrawals)[0].Amount != "3200000000" {
		t.Fatalf("withdrawals=%v", record.Model.Withdrawals)
	}

	emptyBundle, err := testfixture.New(testfixture.Options{Number: 12, Withdrawals: []*types.Withdrawal{}})
	if err != nil {
		t.Fatal(err)
	}
	emptyRecord, err := reader.decodeBlock(
		projectedBlockRow(t, emptyBundle), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if emptyRecord.Model.Withdrawals == nil || len(*emptyRecord.Model.Withdrawals) != 0 {
		t.Fatalf("empty withdrawals=%v", emptyRecord.Model.Withdrawals)
	}
}

func projectedBlockRow(t *testing.T, bundle chainbundle.Bundle) dbgen.QueryListBlocksFirstRow {
	t.Helper()
	withdrawals := make([]storedWithdrawalProjection, len(bundle.Block.Withdrawals()))
	for index, withdrawal := range bundle.Block.Withdrawals() {
		withdrawals[index] = storedWithdrawalProjection{
			Index:          strconv.FormatUint(withdrawal.Index, 10),
			ValidatorIndex: strconv.FormatUint(withdrawal.Validator, 10),
			Address:        withdrawal.Address.Hex(),
			Amount:         strconv.FormatUint(withdrawal.Amount, 10),
		}
	}
	withdrawalsJSON, err := json.Marshal(withdrawals)
	if err != nil {
		t.Fatal(err)
	}
	var baseFee *string
	if bundle.Block.BaseFee() != nil {
		baseFee = new(hexutil.EncodeBig(bundle.Block.BaseFee()))
	}
	withdrawalsPresent := bundle.Block.Withdrawals() != nil
	var withdrawalCount *int64
	if withdrawalsPresent {
		withdrawalCount = new(int64(len(withdrawals)))
	}
	return dbgen.QueryListBlocksFirstRow{
		BlockNumber: strconv.FormatUint(bundle.Block.NumberU64(), 10), Hash: bundle.Block.Hash().Bytes(), ParentHash: bundle.Block.ParentHash().Bytes(),
		BlockTimestamp: strconv.FormatUint(bundle.Block.Time(), 10), MinerText: new(bundle.Block.Coinbase().Hex()),
		GasUsedQuantity: new(hexutil.EncodeUint64(bundle.Block.GasUsed())), GasLimitQuantity: new(hexutil.EncodeUint64(bundle.Block.GasLimit())), BaseFeePerGasQuantity: baseFee,
		TransactionCount: new(int64(len(bundle.Block.Transactions()))), NormalizedTransactionCount: int64(len(bundle.Block.Transactions())),
		WithdrawalsPresent: new(withdrawalsPresent), WithdrawalCount: withdrawalCount, Withdrawals: withdrawalsJSON, Canonical: true,
	}
}

func TestBlockSlotNumberProjection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		raw     string
		want    *string
		invalid bool
	}{
		{name: "absent"},
		{name: "null", raw: "null"},
		{name: "zero", raw: `"0x0"`, want: new("0")},
		{name: "ordinary", raw: `"0x2a"`, want: new("42")},
		{name: "uint64 maximum", raw: `"0xffffffffffffffff"`, want: new("18446744073709551615")},
		{name: "overflow", raw: `"0x10000000000000000"`, invalid: true},
		{name: "number", raw: "42", invalid: true},
		{name: "decimal string", raw: `"42"`, invalid: true},
		{name: "leading zero", raw: `"0x00"`, invalid: true},
		{name: "malformed", raw: `"0xgg"`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := testBlockProjectionRow(2, 3, 2, 0, true, nil, nil)
			row[8] = []byte(test.raw)
			db := testDatabase(t, queryExpectation{
				contains: "canonical.number = $2::numeric", columns: columns(18), rows: [][]any{row},
			})
			reader := testReader(t, db, Options{ChainID: 1})
			model, err := reader.Block(t.Context(), "2")
			if test.invalid {
				if err == nil {
					t.Fatal("invalid slot accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (model.SlotNumber == nil) != (test.want == nil) || test.want != nil && *model.SlotNumber != *test.want {
				t.Fatalf("slot number = %v, want %v", model.SlotNumber, test.want)
			}
			encoded, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if _, exists := fields["slot_number"]; exists {
					t.Fatal("absent slot was serialized")
				}
			} else if string(fields["slot_number"]) != strconv.Quote(*test.want) {
				t.Fatalf("serialized slot = %s", fields["slot_number"])
			}
		})
	}
}

func TestBlockAccessListHashProjection(t *testing.T) {
	for _, raw := range []string{`null`, `"0x0000000000000000000000000000000000000000000000000000000000000000"`, `42`, `"0x1234"`} {
		t.Run(raw, func(t *testing.T) {
			row := testBlockProjectionRow(2, 3, 2, 0, true, nil, nil)
			row[9] = []byte(raw)
			db := testDatabase(t, queryExpectation{contains: "canonical.number = $2::numeric", columns: columns(18), rows: [][]any{row}})
			model, err := testReader(t, db, Options{ChainID: 1}).Block(t.Context(), "2")
			valid := raw == `null` || len(raw) == 68
			if (err == nil) != valid {
				t.Fatalf("err=%v", err)
			}
			if valid && raw != `null` && (model.BlockAccessListHash == nil || strconv.Quote(*model.BlockAccessListHash) != raw) {
				t.Fatalf("hash=%v", model.BlockAccessListHash)
			}
		})
	}
}
