package query

import (
	"database/sql"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
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
	reader := &PostgresReader{}
	model, err := reader.transactionModel(
		bundle.RawTransactions[0], bundle.RawReceipts[0], bundle.RawBlock,
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
	record, err := reader.scanBlock(&singleRowScanner{values: []any{
		[]byte(bundle.RawBlock), "12", bundle.Block.Hash().Bytes(), true, nil, nil,
	}}, false)
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
	emptyRecord, err := reader.scanBlock(&singleRowScanner{values: []any{
		[]byte(emptyBundle.RawBlock), "12", emptyBundle.Block.Hash().Bytes(), true, nil, nil,
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if emptyRecord.Model.Withdrawals == nil || len(*emptyRecord.Model.Withdrawals) != 0 {
		t.Fatalf("empty withdrawals=%v", emptyRecord.Model.Withdrawals)
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
