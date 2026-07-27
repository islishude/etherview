//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
)

func TestPostgresCoreProtocolRoundTripAndMalformedBundlesAreAtomic(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	valid := coreProtocolBundle(t, 0, testHash(0), 51_000)
	blockHash := valid.Block.Hash()
	reference := mustBlockRef(t, valid)
	if err := repository.CommitCanonical(ctx, "1", valid, store.NewCoreCheckpoint(reference)); err != nil {
		t.Fatalf("commit protocol fixture: %v", err)
	}

	stored, found, err := repository.BundleByHash(ctx, "1", blockHash)
	if err != nil || !found {
		t.Fatalf("read protocol fixture: found=%t err=%v", found, err)
	}
	assertCoreProtocolRoundTrip(t, valid, stored)

	canonical, found, err := repository.CanonicalBlock(ctx, "1", 0)
	if err != nil || !found || canonical.Hash != blockHash {
		t.Fatalf("canonical protocol block = %+v, found=%t, err=%v", canonical, found, err)
	}
	checkpointBefore, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found || checkpointBefore.ContiguousThrough != 0 || checkpointBefore.BlockHash != blockHash {
		t.Fatalf("protocol checkpoint = %+v, found=%t, err=%v", checkpointBefore, found, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM blocks WHERE chain_id = 1 AND hash = $1`, 1, blockHash.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transaction_inclusions WHERE chain_id = 1 AND block_hash = $1`, 5, blockHash.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM receipts WHERE chain_id = 1 AND block_hash = $1`, 5, blockHash.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM logs WHERE chain_id = 1 AND block_hash = $1`, 5, blockHash.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM withdrawals WHERE chain_id = 1 AND block_hash = $1`, 1, blockHash.Bytes())

	t.Run("unsupported future transaction type", func(t *testing.T) {
		bad := coreProtocolBundle(t, 1, blockHash, 61_000)
		setRawTransactionField(t, &bad, 2, "type", "0x7f")
		assertRejectedBundleAtomic(t, ctx, db, repository, checkpointBefore, bad, chainbundle.ErrUnsupportedTransactionType)
	})

	t.Run("receipt transaction identity mismatch", func(t *testing.T) {
		bad := coreProtocolBundle(t, 1, blockHash, 62_000)
		setRawReceiptField(t, &bad, 2, "transactionHash", testHash(69_999))
		err := repository.CommitCanonical(ctx, "1", bad, store.NewCoreCheckpoint(mustBlockRef(t, bad)))
		var validationError *chainbundle.ValidationError
		if !errors.As(err, &validationError) || validationError.Path != "receipts[2].transactionHash" {
			t.Fatalf("receipt identity mismatch error = %v, want receipts[2].transactionHash validation error", err)
		}
		assertBundleRowsAbsent(t, ctx, db, bad.Block)
		assertCheckpointUnchanged(t, ctx, repository, checkpointBefore)
	})
}

func coreProtocolBundle(t *testing.T, number uint64, parentHash common.Hash, seed uint64) chainbundle.Bundle {
	t.Helper()
	transactionTypes := []uint8{
		types.LegacyTxType,
		types.AccessListTxType,
		types.DynamicFeeTxType,
		types.BlobTxType,
		types.SetCodeTxType,
	}
	transactions := make([]integrationTransactionOptions, len(transactionTypes))
	recipient := testAddress(seed + 2)
	for index, transactionType := range transactionTypes {
		transactions[index] = integrationTransactionOptions{
			Type:  transactionType,
			To:    &recipient,
			Data:  []byte{0xf0, byte(index)},
			Value: big.NewInt(int64(seed + uint64(index) + 1)),
			Logs: []*types.Log{{
				Address: testAddress(seed + 600 + uint64(index)),
				Data:    []byte{0xa0, byte(index)},
				Topics:  []common.Hash{testHash(seed + 700 + uint64(index))},
			}},
			RawExtra: map[string]any{
				"futureTransaction": map[string]any{"opaque": fmt.Sprintf("type-%d", transactionType)},
			},
			ReceiptRawExtra: map[string]any{
				"futureReceipt": map[string]any{"opaque": fmt.Sprintf("receipt-%d", index)},
			},
		}
	}
	blobGasUsed, excessBlobGas := uint64(131_072), uint64(262_144)
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number:        number,
		ParentHash:    parentHash,
		ExtraData:     []byte{0xfe, 0xed},
		Coinbase:      testAddress(seed + 1),
		Transactions:  transactions,
		BlobGasUsed:   &blobGasUsed,
		ExcessBlobGas: &excessBlobGas,
		Withdrawals: []*types.Withdrawal{{
			Index:     number,
			Validator: seed + 10,
			Address:   testAddress(seed + 11),
			Amount:    ^uint64(0),
		}},
		RawExtra: map[string]any{
			"futureBlock":     map[string]any{"opaque": true, "protocolVersion": 5},
			"totalDifficulty": "0xffff",
		},
	})
	if err != nil {
		t.Fatalf("build core protocol fixture: %v", err)
	}
	return bundle
}

func assertCoreProtocolRoundTrip(t *testing.T, expected, actual chainbundle.Bundle) {
	t.Helper()
	if expected.Block.Hash() != actual.Block.Hash() ||
		expected.Block.ParentHash() != actual.Block.ParentHash() ||
		expected.Block.NumberU64() != actual.Block.NumberU64() {
		t.Fatalf("block identity changed: expected=%s actual=%s", expected.Block.Hash(), actual.Block.Hash())
	}
	if len(actual.Block.Transactions()) != 5 || len(actual.Receipts) != 5 {
		t.Fatalf("round-trip counts = transactions %d receipts %d", len(actual.Block.Transactions()), len(actual.Receipts))
	}
	for index, wantType := range []uint8{0, 1, 2, 3, 4} {
		if got := actual.Block.Transactions()[index].Type(); got != wantType {
			t.Fatalf("transaction %d type=%d want=%d", index, got, wantType)
		}
		if actual.Block.Transactions()[index].Hash() != expected.Block.Transactions()[index].Hash() {
			t.Fatalf("transaction %d hash changed", index)
		}
		if actual.Receipts[index].TxHash != expected.Receipts[index].TxHash {
			t.Fatalf("receipt %d transaction hash changed", index)
		}
	}
	if actual.Block.Withdrawals()[0].Amount != ^uint64(0) {
		t.Fatalf("withdrawal amount=%d want max uint64", actual.Block.Withdrawals()[0].Amount)
	}
	assertRawEqual(t, "block", expected.RawBlock, actual.RawBlock)
	assertRawSliceEqual(t, "transactions", expected.RawTransactions, actual.RawTransactions)
	assertRawSliceEqual(t, "receipts", expected.RawReceipts, actual.RawReceipts)
	assertRawNestedSliceEqual(t, "logs", expected.RawLogs, actual.RawLogs)
	assertRawSliceEqual(t, "withdrawals", expected.RawWithdrawals, actual.RawWithdrawals)
	assertRawObjectField(t, actual.RawBlock, "futureBlock")
	for index := range actual.RawTransactions {
		assertRawObjectField(t, actual.RawTransactions[index], "futureTransaction")
		assertRawObjectField(t, actual.RawReceipts[index], "futureReceipt")
	}
}

func assertRejectedBundleAtomic(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	repository *store.PostgresRepository,
	checkpointBefore store.Checkpoint,
	bad chainbundle.Bundle,
	want error,
) {
	t.Helper()
	err := repository.CommitCanonical(ctx, "1", bad, store.NewCoreCheckpoint(mustBlockRef(t, bad)))
	if !errors.Is(err, want) || !chainbundle.IsPermanent(err) {
		t.Fatalf("rejected bundle error=%v, want permanent %v", err, want)
	}
	assertBundleRowsAbsent(t, ctx, db, bad.Block)
	assertCheckpointUnchanged(t, ctx, repository, checkpointBefore)
}

func assertBundleRowsAbsent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block *types.Block,
) {
	t.Helper()
	blockHash := block.Hash()
	for _, table := range []string{"blocks", "transaction_inclusions", "receipts", "logs", "withdrawals"} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND %s = $1`, table, coreProtocolBlockHashColumn(table)),
			0, blockHash.Bytes(),
		)
	}
	for _, transaction := range block.Transactions() {
		assertRowCount(t, ctx, db, `SELECT count(*) FROM transactions WHERE chain_id = 1 AND hash = $1`, 0, transaction.Hash().Bytes())
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transactional_outbox WHERE chain_id = 1 AND message_key = $1`, 0, blockHash.String())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM runtime_events WHERE chain_id = 1 AND payload->>'hash' = $1`, 0, blockHash.String())
}

func assertCheckpointUnchanged(
	t *testing.T,
	ctx context.Context,
	repository *store.PostgresRepository,
	before store.Checkpoint,
) {
	t.Helper()
	after, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found || after.ContiguousThrough != before.ContiguousThrough ||
		after.BlockHash != before.BlockHash || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("checkpoint changed after rejected commit: before=%+v after=%+v found=%t err=%v", before, after, found, err)
	}
}

func setRawTransactionField(t *testing.T, bundle *chainbundle.Bundle, index int, name string, value any) {
	t.Helper()
	var blockFields map[string]json.RawMessage
	if err := json.Unmarshal(bundle.RawBlock, &blockFields); err != nil {
		t.Fatal(err)
	}
	var transactions []map[string]json.RawMessage
	if err := json.Unmarshal(blockFields["transactions"], &transactions); err != nil {
		t.Fatal(err)
	}
	setIntegrationJSON(transactions[index], name, value)
	setIntegrationJSON(blockFields, "transactions", transactions)
	raw, err := json.Marshal(blockFields)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RawBlock = raw
	bundle.RawTransactions[index], err = json.Marshal(transactions[index])
	if err != nil {
		t.Fatal(err)
	}
}

func setRawReceiptField(t *testing.T, bundle *chainbundle.Bundle, index int, name string, value any) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(bundle.RawReceipts[index], &fields); err != nil {
		t.Fatal(err)
	}
	setIntegrationJSON(fields, name, value)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RawReceipts[index] = raw
}

func assertRawObjectField(t *testing.T, raw json.RawMessage, name string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode raw object: %v", err)
	}
	if _, exists := fields[name]; !exists {
		t.Fatalf("raw object lost field %q: %s", name, raw)
	}
}

func assertRawEqual(t *testing.T, label string, expected, actual json.RawMessage) {
	t.Helper()
	expectedValue := decodeRawJSON(t, label+" expected", expected)
	actualValue := decodeRawJSON(t, label+" actual", actual)
	if !reflect.DeepEqual(expectedValue, actualValue) {
		t.Fatalf("%s raw JSON changed\nexpected: %s\nactual:   %s", label, expected, actual)
	}
}

func decodeRawJSON(t *testing.T, label string, raw json.RawMessage) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s raw JSON: %v", label, err)
	}
	return value
}

func assertRawSliceEqual(t *testing.T, label string, expected, actual []json.RawMessage) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatalf("%s raw count=%d want=%d", label, len(actual), len(expected))
	}
	for index := range expected {
		assertRawEqual(t, fmt.Sprintf("%s[%d]", label, index), expected[index], actual[index])
	}
}

func assertRawNestedSliceEqual(t *testing.T, label string, expected, actual [][]json.RawMessage) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatalf("%s raw count=%d want=%d", label, len(actual), len(expected))
	}
	for index := range expected {
		assertRawSliceEqual(t, fmt.Sprintf("%s[%d]", label, index), expected[index], actual[index])
	}
}

func coreProtocolBlockHashColumn(table string) string {
	if table == "blocks" {
		return "hash"
	}
	return "block_hash"
}
