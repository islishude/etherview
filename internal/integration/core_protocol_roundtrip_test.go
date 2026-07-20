//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestPostgresCoreProtocolRoundTripAndReceiptMismatchAtomicity(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	blockHash := testHash(50_000)
	valid := coreProtocolBundle(t, 0, blockHash, testHash(0), 51_000)
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
	if err != nil || !found || !canonical.Hash.Equal(blockHash) {
		t.Fatalf("canonical protocol block = %+v, found=%t, err=%v", canonical, found, err)
	}
	checkpointBefore, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found || checkpointBefore.ContiguousThrough != 0 || !checkpointBefore.BlockHash.Equal(blockHash) {
		t.Fatalf("protocol checkpoint = %+v, found=%t, err=%v", checkpointBefore, found, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM blocks WHERE chain_id = 1 AND hash = $1`, 1, mustBytes(t, blockHash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transaction_inclusions WHERE chain_id = 1 AND block_hash = $1`, 6, mustBytes(t, blockHash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM receipts WHERE chain_id = 1 AND block_hash = $1`, 6, mustBytes(t, blockHash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM logs WHERE chain_id = 1 AND block_hash = $1`, 6, mustBytes(t, blockHash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM withdrawals WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, blockHash))

	maximum := coreProtocolMaximumQuantity(t)
	maximumInteger, err := maximum.Big()
	if err != nil {
		t.Fatalf("decode maximum uint256: %v", err)
	}
	var persistedWithdrawalAmount, persistedDifficulty, persistedTotalDifficulty string
	if err := db.QueryRowContext(ctx, `
		SELECT withdrawal.amount::text, block.raw->>'difficulty', block.raw->>'totalDifficulty'
		FROM withdrawals AS withdrawal
		JOIN blocks AS block
		  ON block.chain_id = withdrawal.chain_id
		 AND block.number = withdrawal.block_number
		 AND block.hash = withdrawal.block_hash
		WHERE withdrawal.chain_id = 1 AND withdrawal.block_hash = $1`,
		mustBytes(t, blockHash),
	).Scan(&persistedWithdrawalAmount, &persistedDifficulty, &persistedTotalDifficulty); err != nil {
		t.Fatalf("read persisted uint256 fields: %v", err)
	}
	if persistedWithdrawalAmount != maximumInteger.String() ||
		persistedDifficulty != maximum.String() || persistedTotalDifficulty != maximum.String() {
		t.Fatalf("persisted uint256 fields = amount %q difficulty %q totalDifficulty %q", persistedWithdrawalAmount, persistedDifficulty, persistedTotalDifficulty)
	}
	unknownHash := valid.Block.Transactions[5].Transaction.Hash
	var unknownType, unknownFutureField string
	if err := db.QueryRowContext(ctx, `
		SELECT tx_type::text, raw->'futureTransaction'->>'opaque'
		FROM transactions
		WHERE chain_id = 1 AND hash = $1`, mustBytes(t, unknownHash),
	).Scan(&unknownType, &unknownFutureField); err != nil {
		t.Fatalf("read persisted unknown transaction type: %v", err)
	}
	if unknownType != "127" || unknownFutureField != "type-127" {
		t.Fatalf("unknown transaction persisted as type=%q future=%q", unknownType, unknownFutureField)
	}

	badHash := testHash(60_000)
	bad := coreProtocolBundle(t, 1, badHash, blockHash, 61_000)
	bad.Receipts[2].TransactionHash = testHash(69_999)
	badReference := mustBlockRef(t, bad)
	err = repository.CommitCanonical(ctx, "1", bad, store.NewCoreCheckpoint(badReference))
	var validationError *ethrpc.ValidationError
	if !errors.As(err, &validationError) || validationError.Path != "receipts[2].transactionHash" {
		t.Fatalf("receipt identity mismatch error = %v, want receipts[2].transactionHash validation error", err)
	}

	if _, found, err := repository.BundleByHash(ctx, "1", badHash); err != nil || found {
		t.Fatalf("rejected block bundle found=%t err=%v", found, err)
	}
	if rejectedCanonical, found, err := repository.CanonicalBlock(ctx, "1", 1); err != nil || found {
		t.Fatalf("rejected canonical block = %+v, found=%t, err=%v", rejectedCanonical, found, err)
	}
	tip, found, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !found || tip.Number != 0 || !tip.Hash.Equal(blockHash) {
		t.Fatalf("canonical tip changed after rejected commit: %+v, found=%t, err=%v", tip, found, err)
	}
	checkpointAfter, found, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !found || checkpointAfter.ContiguousThrough != checkpointBefore.ContiguousThrough ||
		!checkpointAfter.BlockHash.Equal(checkpointBefore.BlockHash) || !checkpointAfter.UpdatedAt.Equal(checkpointBefore.UpdatedAt) {
		t.Fatalf("checkpoint changed after rejected commit: before=%+v after=%+v found=%t err=%v", checkpointBefore, checkpointAfter, found, err)
	}

	badHashBytes := mustBytes(t, badHash)
	for _, table := range []string{"blocks", "transaction_inclusions", "receipts", "logs", "withdrawals"} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND %s = $1`, table, coreProtocolBlockHashColumn(table)),
			0, badHashBytes,
		)
	}
	for _, transaction := range bad.Block.Transactions {
		assertRowCount(t, ctx, db, `SELECT count(*) FROM transactions WHERE chain_id = 1 AND hash = $1`, 0,
			mustBytes(t, transaction.Transaction.Hash))
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transactional_outbox WHERE chain_id = 1 AND message_key = $1`, 0, badHash.String())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM runtime_events WHERE chain_id = 1 AND payload->>'hash' = $1`, 0, badHash.String())
}

func coreProtocolBundle(
	t *testing.T,
	number uint64,
	blockHash ethrpc.Hash,
	parentHash ethrpc.Hash,
	seed uint64,
) ethrpc.Bundle {
	t.Helper()
	maximum := coreProtocolMaximumQuantity(t)
	blockNumber := ethrpc.QuantityFromUint64(number)
	miner := testAddress(seed + 1)
	recipient := testAddress(seed + 2)
	logsBloom := ethrpc.Data("0x" + strings.Repeat("00", 256))
	nonce := ethrpc.Data("0x0102030405060708")
	withdrawalsRoot := testHash(seed + 3)
	parentBeaconRoot := testHash(seed + 4)
	requestsHash := testHash(seed + 5)

	transactionTypes := []uint64{0, 1, 2, 3, 4, 0x7f}
	transactions := make([]ethrpc.TransactionRef, 0, len(transactionTypes))
	receipts := make([]ethrpc.Receipt, 0, len(transactionTypes))
	for index, typeNumber := range transactionTypes {
		transactionIndex := ethrpc.QuantityFromUint64(uint64(index))
		transactionType := ethrpc.QuantityFromUint64(typeNumber)
		transactionHash := testHash(seed + 100 + uint64(index))
		from := testAddress(seed + 200 + uint64(index))
		transaction := &ethrpc.Transaction{
			Hash:             transactionHash,
			Type:             coreProtocolQuantityPointer(transactionType),
			BlockHash:        coreProtocolHashPointer(blockHash),
			BlockNumber:      coreProtocolQuantityPointer(blockNumber),
			TransactionIndex: coreProtocolQuantityPointer(transactionIndex),
			From:             from,
			To:               coreProtocolAddressPointer(recipient),
			Nonce:            ethrpc.QuantityFromUint64(uint64(index)),
			Gas:              ethrpc.QuantityFromUint64(21_000),
			Value:            maximum,
			Input:            ethrpc.DataFromBytes([]byte{0xf0, byte(index)}),
			Extra: map[string]json.RawMessage{
				"futureTransaction": json.RawMessage(fmt.Sprintf(`{"opaque":"type-%d","quantity":"%s"}`, typeNumber, maximum)),
			},
		}
		switch index {
		case 0:
			transaction.GasPrice = coreProtocolQuantityPointer(maximum)
			transaction.V = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(37))
			transaction.R = coreProtocolQuantityPointer(maximum)
			transaction.S = coreProtocolQuantityPointer(maximum)
		case 1:
			transaction.ChainID = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.GasPrice = coreProtocolQuantityPointer(maximum)
			transaction.AccessList = json.RawMessage(fmt.Sprintf(
				`[{"address":"%s","storageKeys":["%s"]}]`, recipient, testHash(seed+301),
			))
			transaction.YParity = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.R = coreProtocolQuantityPointer(maximum)
			transaction.S = coreProtocolQuantityPointer(maximum)
		case 2:
			transaction.ChainID = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxPriorityFeePerGas = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxFeePerGas = coreProtocolQuantityPointer(maximum)
			transaction.AccessList = json.RawMessage(`[]`)
			transaction.YParity = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(0))
			transaction.R = coreProtocolQuantityPointer(maximum)
			transaction.S = coreProtocolQuantityPointer(maximum)
		case 3:
			transaction.ChainID = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxPriorityFeePerGas = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxFeePerGas = coreProtocolQuantityPointer(maximum)
			transaction.AccessList = json.RawMessage(`[]`)
			transaction.MaxFeePerBlobGas = coreProtocolQuantityPointer(maximum)
			transaction.BlobVersionedHashes = []ethrpc.Hash{testHash(seed + 401), testHash(seed + 402)}
			transaction.YParity = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.R = coreProtocolQuantityPointer(maximum)
			transaction.S = coreProtocolQuantityPointer(maximum)
		case 4:
			transaction.ChainID = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxPriorityFeePerGas = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(1))
			transaction.MaxFeePerGas = coreProtocolQuantityPointer(maximum)
			transaction.AccessList = json.RawMessage(`[]`)
			transaction.AuthorizationList = json.RawMessage(fmt.Sprintf(
				`[{"chainId":"0x1","address":"%s","nonce":"0x0","yParity":"0x1","r":"%s","s":"0x2"}]`,
				testAddress(seed+501), maximum,
			))
			transaction.YParity = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(0))
			transaction.R = coreProtocolQuantityPointer(maximum)
			transaction.S = coreProtocolQuantityPointer(maximum)
		}
		transactions = append(transactions, ethrpc.TransactionRef{Hash: transactionHash, Transaction: transaction})

		gasUsed := ethrpc.QuantityFromUint64(21_000)
		cumulativeGasUsed := ethrpc.QuantityFromUint64(21_000 * uint64(index+1))
		status := ethrpc.QuantityFromUint64(1)
		logIndex := ethrpc.QuantityFromUint64(uint64(index))
		logAddress := testAddress(seed + 600 + uint64(index))
		log := ethrpc.Log{
			LogIndex:         coreProtocolQuantityPointer(logIndex),
			TransactionIndex: coreProtocolQuantityPointer(transactionIndex),
			TransactionHash:  coreProtocolHashPointer(transactionHash),
			BlockHash:        coreProtocolHashPointer(blockHash),
			BlockNumber:      coreProtocolQuantityPointer(blockNumber),
			Address:          logAddress,
			Data:             ethrpc.DataFromBytes([]byte{0xa0, byte(index)}),
			Topics:           []ethrpc.Hash{testHash(seed + 700 + uint64(index))},
			Extra: map[string]json.RawMessage{
				"futureLog": json.RawMessage(fmt.Sprintf(`{"opaque":"log-%d"}`, index)),
			},
		}
		receipt := ethrpc.Receipt{
			TransactionHash:   transactionHash,
			TransactionIndex:  transactionIndex,
			BlockHash:         blockHash,
			BlockNumber:       blockNumber,
			From:              coreProtocolAddressPointer(from),
			To:                coreProtocolAddressPointer(recipient),
			CumulativeGasUsed: cumulativeGasUsed,
			GasUsed:           coreProtocolQuantityPointer(gasUsed),
			Logs:              []ethrpc.Log{log},
			LogsBloom:         logsBloom,
			Status:            coreProtocolQuantityPointer(status),
			Type:              coreProtocolQuantityPointer(transactionType),
			EffectiveGasPrice: coreProtocolQuantityPointer(maximum),
			Extra: map[string]json.RawMessage{
				"futureReceipt": json.RawMessage(fmt.Sprintf(`{"opaque":"receipt-%d"}`, index)),
			},
		}
		if index == 3 {
			receipt.BlobGasUsed = coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(131_072))
			receipt.BlobGasPrice = coreProtocolQuantityPointer(maximum)
		}
		receipts = append(receipts, receipt)
	}

	return ethrpc.Bundle{
		Block: ethrpc.Block{
			Number:           coreProtocolQuantityPointer(blockNumber),
			Hash:             coreProtocolHashPointer(blockHash),
			ParentHash:       parentHash,
			Nonce:            &nonce,
			Sha3Uncles:       testHash(seed + 6),
			LogsBloom:        &logsBloom,
			TransactionsRoot: testHash(seed + 7),
			StateRoot:        testHash(seed + 8),
			ReceiptsRoot:     testHash(seed + 9),
			Miner:            &miner,
			Difficulty:       coreProtocolQuantityPointer(maximum),
			TotalDifficulty:  coreProtocolQuantityPointer(maximum),
			ExtraData:        ethrpc.Data("0xfeed"),
			Size:             coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(4_096)),
			GasLimit:         ethrpc.QuantityFromUint64(30_000_000),
			GasUsed:          ethrpc.QuantityFromUint64(126_000),
			Timestamp:        ethrpc.QuantityFromUint64(1_700_000_000 + number),
			BaseFeePerGas:    coreProtocolQuantityPointer(maximum),
			WithdrawalsRoot:  &withdrawalsRoot,
			BlobGasUsed:      coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(131_072)),
			ExcessBlobGas:    coreProtocolQuantityPointer(ethrpc.QuantityFromUint64(262_144)),
			ParentBeaconRoot: &parentBeaconRoot,
			RequestsHash:     &requestsHash,
			Transactions:     transactions,
			Uncles:           []ethrpc.Hash{},
			Withdrawals: []ethrpc.Withdrawal{{
				Index:          ethrpc.QuantityFromUint64(number),
				ValidatorIndex: ethrpc.QuantityFromUint64(seed + 10),
				Address:        testAddress(seed + 11),
				Amount:         maximum,
				Extra: map[string]json.RawMessage{
					"futureWithdrawal": json.RawMessage(`{"opaque":"kept"}`),
				},
			}},
			Extra: map[string]json.RawMessage{
				"futureBlock": json.RawMessage(`{"opaque":true,"protocolVersion":6}`),
			},
		},
		Receipts: receipts,
	}
}

func assertCoreProtocolRoundTrip(t *testing.T, expected, actual ethrpc.Bundle) {
	t.Helper()
	assertCoreProtocolJSONEquivalent(t, "block", expected.Block, actual.Block)
	assertCoreProtocolJSONEquivalent(t, "receipts", expected.Receipts, actual.Receipts)

	maximum := coreProtocolMaximumQuantity(t)
	if actual.Block.Nonce == nil || actual.Block.Nonce.String() != "0x0102030405060708" ||
		actual.Block.Difficulty == nil || actual.Block.Difficulty.String() != maximum.String() ||
		actual.Block.TotalDifficulty == nil || actual.Block.TotalDifficulty.String() != maximum.String() {
		t.Fatalf("PoW wire fields were not preserved: nonce=%v difficulty=%v totalDifficulty=%v", actual.Block.Nonce, actual.Block.Difficulty, actual.Block.TotalDifficulty)
	}
	if len(actual.Block.Withdrawals) != 1 || actual.Block.Withdrawals[0].Amount.String() != maximum.String() {
		t.Fatalf("withdrawal uint256 was not preserved: %+v", actual.Block.Withdrawals)
	}
	assertCoreProtocolExtra(t, "block", expected.Block.Extra, actual.Block.Extra, "futureBlock")
	assertCoreProtocolExtra(t, "withdrawal", expected.Block.Withdrawals[0].Extra, actual.Block.Withdrawals[0].Extra, "futureWithdrawal")

	transactionTypes := []uint64{0, 1, 2, 3, 4, 0x7f}
	if len(actual.Block.Transactions) != len(transactionTypes) || len(actual.Receipts) != len(transactionTypes) {
		t.Fatalf("round-trip counts = transactions %d receipts %d", len(actual.Block.Transactions), len(actual.Receipts))
	}
	for index, typeNumber := range transactionTypes {
		transaction := actual.Block.Transactions[index].Transaction
		if transaction == nil || transaction.Type == nil || transaction.Type.String() != ethrpc.QuantityFromUint64(typeNumber).String() {
			t.Fatalf("transaction %d type was not preserved: %+v", index, transaction)
		}
		if transaction.Value.String() != maximum.String() {
			t.Fatalf("transaction %d uint256 value = %s", index, transaction.Value)
		}
		assertCoreProtocolExtra(t, fmt.Sprintf("transaction %d", index), expected.Block.Transactions[index].Transaction.Extra, transaction.Extra, "futureTransaction")
		assertCoreProtocolExtra(t, fmt.Sprintf("receipt %d", index), expected.Receipts[index].Extra, actual.Receipts[index].Extra, "futureReceipt")
		assertCoreProtocolExtra(t, fmt.Sprintf("log %d", index), expected.Receipts[index].Logs[0].Extra, actual.Receipts[index].Logs[0].Extra, "futureLog")
	}

	blob := actual.Block.Transactions[3].Transaction
	if blob.MaxFeePerBlobGas == nil || blob.MaxFeePerBlobGas.String() != maximum.String() || len(blob.BlobVersionedHashes) != 2 ||
		actual.Receipts[3].BlobGasPrice == nil || actual.Receipts[3].BlobGasPrice.String() != maximum.String() {
		t.Fatalf("blob transaction fields were not preserved: transaction=%+v receipt=%+v", blob, actual.Receipts[3])
	}
	setCode := actual.Block.Transactions[4].Transaction
	if len(setCode.AuthorizationList) == 0 {
		t.Fatal("EIP-7702 authorizationList was not preserved")
	}
	assertCoreProtocolJSONEquivalent(t, "authorizationList",
		expected.Block.Transactions[4].Transaction.AuthorizationList, setCode.AuthorizationList)
}

func assertCoreProtocolExtra(
	t *testing.T,
	label string,
	expected map[string]json.RawMessage,
	actual map[string]json.RawMessage,
	key string,
) {
	t.Helper()
	expectedValue, expectedExists := expected[key]
	actualValue, actualExists := actual[key]
	if !expectedExists || !actualExists {
		t.Fatalf("%s unknown field %q existence = expected %t actual %t", label, key, expectedExists, actualExists)
	}
	assertCoreProtocolJSONEquivalent(t, label+"."+key, expectedValue, actualValue)
}

func assertCoreProtocolJSONEquivalent(t *testing.T, label string, expected, actual any) {
	t.Helper()
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("marshal expected %s: %v", label, err)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("marshal actual %s: %v", label, err)
	}
	decode := func(encoded []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("decode %s JSON: %v", label, err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(expectedJSON), decode(actualJSON)) {
		t.Fatalf("%s wire JSON changed\nexpected: %s\nactual:   %s", label, expectedJSON, actualJSON)
	}
}

func coreProtocolMaximumQuantity(t *testing.T) ethrpc.Quantity {
	t.Helper()
	value, err := ethrpc.ParseQuantity("0x" + strings.Repeat("f", 64))
	if err != nil {
		t.Fatalf("parse maximum uint256: %v", err)
	}
	return value
}

func coreProtocolQuantityPointer(value ethrpc.Quantity) *ethrpc.Quantity { return &value }

func coreProtocolHashPointer(value ethrpc.Hash) *ethrpc.Hash { return &value }

func coreProtocolAddressPointer(value ethrpc.Address) *ethrpc.Address { return &value }

func coreProtocolBlockHashColumn(table string) string {
	if table == "blocks" {
		return "hash"
	}
	return "block_hash"
}
