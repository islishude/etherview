package query

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"strconv"
	"testing"

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
		strconv.FormatUint(bundle.Block.Time(), 10), sql.NullString{String: "0x1", Valid: true},
		"12", bundle.Block.Hash().Bytes(), 0, transaction.Hash().Bytes(), true,
		sql.NullString{String: "12", Valid: true}, sql.NullString{String: "10", Valid: true}, 12,
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
	record, err := reader.scanBlock(&singleRowScanner{values: projectedBlockRow(t, bundle)}, false)
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
	emptyRecord, err := reader.scanBlock(
		&singleRowScanner{values: projectedBlockRow(t, emptyBundle)}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if emptyRecord.Model.Withdrawals == nil || len(*emptyRecord.Model.Withdrawals) != 0 {
		t.Fatalf("empty withdrawals=%v", emptyRecord.Model.Withdrawals)
	}
}

func projectedBlockRow(t *testing.T, bundle chainbundle.Bundle) []any {
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
	var baseFee any
	if bundle.Block.BaseFee() != nil {
		baseFee = hexutil.EncodeBig(bundle.Block.BaseFee())
	}
	withdrawalsPresent := bundle.Block.Withdrawals() != nil
	var withdrawalCount any
	if withdrawalsPresent {
		withdrawalCount = int64(len(withdrawals))
	}
	return []any{
		strconv.FormatUint(bundle.Block.NumberU64(), 10),
		bundle.Block.Hash().Bytes(), bundle.Block.ParentHash().Bytes(),
		strconv.FormatUint(bundle.Block.Time(), 10), bundle.Block.Coinbase().Hex(),
		hexutil.EncodeUint64(bundle.Block.GasUsed()),
		hexutil.EncodeUint64(bundle.Block.GasLimit()), baseFee,
		int64(len(bundle.Block.Transactions())), int64(len(bundle.Block.Transactions())),
		withdrawalsPresent, withdrawalCount, withdrawalsJSON,
		true, nil, nil,
	}
}

type singleRowScanner struct {
	values []any
}

func (row *singleRowScanner) Scan(dest ...any) error {
	if len(dest) != len(row.values) {
		return &scanArityError{want: len(row.values), got: len(dest)}
	}
	for index, value := range row.values {
		switch target := dest[index].(type) {
		case *[]byte:
			targetValue, ok := value.([]byte)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = targetValue
		case *string:
			targetValue, ok := value.(string)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = targetValue
		case *bool:
			targetValue, ok := value.(bool)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = targetValue
		case *int64:
			targetValue, ok := value.(int64)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = targetValue
		case *sql.NullString:
			if value == nil {
				*target = sql.NullString{}
				continue
			}
			targetValue, ok := value.(string)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = sql.NullString{String: targetValue, Valid: true}
		case *sql.NullInt64:
			if value == nil {
				*target = sql.NullInt64{}
				continue
			}
			targetValue, ok := value.(int64)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = sql.NullInt64{Int64: targetValue, Valid: true}
		case *sql.NullBool:
			if value == nil {
				*target = sql.NullBool{}
				continue
			}
			targetValue, ok := value.(bool)
			if !ok {
				return &scanTypeError{index: index}
			}
			*target = sql.NullBool{Bool: targetValue, Valid: true}
		default:
			return &scanTypeError{index: index}
		}
	}
	return nil
}

type scanArityError struct{ want, got int }

func (err *scanArityError) Error() string { return "scan arity mismatch" }

type scanTypeError struct{ index int }

func (err *scanTypeError) Error() string { return "scan type mismatch" }
