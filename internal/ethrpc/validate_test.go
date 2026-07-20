package ethrpc

import (
	"errors"
	"testing"
)

func TestValidateBundleAcceptsUnknownTransactionType(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 1)
	unknown := Quantity("0x7f")
	bundle.Block.Transactions[0].Transaction.Type = &unknown
	if err := ValidateBundle(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBundleRejectsReceiptHashMismatch(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 1)
	bundle.Receipts[0].TransactionHash = testHash(99)
	err := ValidateBundle(bundle)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Path != "receipts[0].transactionHash" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateBundleRejectsHashOnlyTransactions(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 1)
	bundle.Block.Transactions[0].Transaction = nil
	err := ValidateBundle(bundle)
	if !IsValidationError(err) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestValidateBundleAcceptsPreByzantiumReceipt(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 1)
	bundle.Receipts[0].Status = nil
	root := Data(testHash(77))
	bundle.Receipts[0].Root = &root
	if err := ValidateBundle(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBundleRejectsNonContiguousLogIndex(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 1)
	tx := bundle.Block.Transactions[0].Transaction
	index := QuantityFromUint64(4)
	bundle.Receipts[0].Logs = []Log{{
		LogIndex:         &index,
		TransactionIndex: tx.TransactionIndex,
		TransactionHash:  &tx.Hash,
		BlockHash:        bundle.Block.Hash,
		BlockNumber:      bundle.Block.Number,
		Address:          testAddress(1),
		Data:             Data("0x"),
	}}
	err := ValidateBundle(bundle)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Path != "receipts[0].logs[0].logIndex" {
		t.Fatalf("error = %v", err)
	}
}
