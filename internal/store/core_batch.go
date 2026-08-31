package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/db/gen"
)

const (
	coreWriteBatchMaxRows  = 512
	coreWriteBatchMaxBytes = 4 << 20
	coreWriteRowOverhead   = 512
)

type coreJSONBatchWriter[T any] struct {
	operation      string
	execute        func(json.RawMessage) error
	rows           []T
	estimatedBytes int
}

func newCoreJSONBatchWriter[T any](
	operation string,
	execute func(json.RawMessage) error,
) *coreJSONBatchWriter[T] {
	return &coreJSONBatchWriter[T]{
		operation: operation,
		execute:   execute,
		rows:      make([]T, 0, coreWriteBatchMaxRows),
	}
}

func newCoreSQLBatchWriter[T any](
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	query string,
	operation string,
) *coreJSONBatchWriter[T] {
	return newCoreJSONBatchWriter[T](operation, func(payload json.RawMessage) error {
		if _, err := tx.ExecContext(ctx, query, chainID, payload); err != nil {
			return fmt.Errorf("%s: %w", operation, err)
		}
		return nil
	})
}

func (w *coreJSONBatchWriter[T]) add(row T, estimatedBytes int) error {
	if estimatedBytes < 1 {
		estimatedBytes = 1
	}
	if len(w.rows) > 0 &&
		(len(w.rows) >= coreWriteBatchMaxRows ||
			w.estimatedBytes+estimatedBytes > coreWriteBatchMaxBytes) {
		if err := w.flush(); err != nil {
			return err
		}
	}
	w.rows = append(w.rows, row)
	w.estimatedBytes += estimatedBytes
	return nil
}

func (w *coreJSONBatchWriter[T]) close() error {
	return w.flush()
}

func (w *coreJSONBatchWriter[T]) flush() error {
	if len(w.rows) == 0 {
		return nil
	}
	if err := w.executeBounded(w.rows); err != nil {
		return err
	}
	clear(w.rows)
	w.rows = w.rows[:0]
	w.estimatedBytes = 0
	return nil
}

func (w *coreJSONBatchWriter[T]) executeBounded(rows []T) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("encode %s batch: %w", w.operation, err)
	}
	if len(payload) > coreWriteBatchMaxBytes && len(rows) > 1 {
		middle := len(rows) / 2
		if err := w.executeBounded(rows[:middle]); err != nil {
			return err
		}
		return w.executeBounded(rows[middle:])
	}
	return w.execute(payload)
}

type coreBlockWriteRow struct {
	Number     string          `json:"number"`
	Hash       string          `json:"hash"`
	ParentHash string          `json:"parent_hash"`
	Timestamp  string          `json:"timestamp"`
	Raw        json.RawMessage `json:"raw"`
}

type coreTransactionWriteRow struct {
	Hash string          `json:"hash"`
	Type string          `json:"tx_type"`
	Raw  json.RawMessage `json:"raw"`
}

type coreTransactionFactWriteRow struct {
	BlockNumber string          `json:"block_number"`
	BlockHash   string          `json:"block_hash"`
	TxIndex     string          `json:"tx_index"`
	TxHash      string          `json:"tx_hash"`
	Raw         json.RawMessage `json:"raw"`
}

type coreLogWriteRow struct {
	BlockNumber string          `json:"block_number"`
	BlockHash   string          `json:"block_hash"`
	LogIndex    string          `json:"log_index"`
	TxIndex     string          `json:"tx_index"`
	TxHash      string          `json:"tx_hash"`
	Address     string          `json:"address"`
	Topic0      *string         `json:"topic0"`
	Raw         json.RawMessage `json:"raw"`
}

type coreWithdrawalWriteRow struct {
	BlockNumber     string          `json:"block_number"`
	BlockHash       string          `json:"block_hash"`
	WithdrawalIndex string          `json:"withdrawal_index"`
	ValidatorIndex  string          `json:"validator_index"`
	Address         string          `json:"address"`
	Amount          string          `json:"amount"`
	Raw             json.RawMessage `json:"raw"`
}

func putBundlesTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
) error {
	if err := putBlockRowsTx(ctx, tx, chainID, bundles); err != nil {
		return err
	}
	if err := putTransactionRowsTx(ctx, tx, chainID, bundles); err != nil {
		return err
	}
	if err := putTransactionFactRowsTx(
		ctx, tx, chainID, bundles,
		dbgen.StorePutTransactionInclusionsBatch,
		"upsert transaction inclusions",
		func(bundle chainbundle.Bundle, index int) json.RawMessage {
			return bundle.RawTransactions[index]
		},
	); err != nil {
		return err
	}
	if err := putTransactionFactRowsTx(
		ctx, tx, chainID, bundles,
		dbgen.StorePutReceiptsBatch,
		"upsert receipts",
		func(bundle chainbundle.Bundle, index int) json.RawMessage {
			return bundle.RawReceipts[index]
		},
	); err != nil {
		return err
	}
	if err := putLogRowsTx(ctx, tx, chainID, bundles); err != nil {
		return err
	}
	return putWithdrawalRowsTx(ctx, tx, chainID, bundles)
}

func putBlockRowsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
) error {
	writer := newCoreSQLBatchWriter[coreBlockWriteRow](
		ctx, tx, chainID, dbgen.StorePutBlocksBatch, "upsert blocks",
	)
	for index, bundle := range bundles {
		reference, err := RefFromBundle(bundle)
		if err != nil {
			return fmt.Errorf("read owned block %d identity: %w", index, err)
		}
		blockJSON, err := chainbundle.EncodeOwnedStoredBlock(bundle)
		if err != nil {
			return fmt.Errorf("encode block %d persistence: %w", index, err)
		}
		if err := writer.add(coreBlockWriteRow{
			Number:     decimal(reference.Number),
			Hash:       encodeCoreBytes(reference.Hash[:]),
			ParentHash: encodeCoreBytes(reference.ParentHash[:]),
			Timestamp:  decimal(bundle.Block.Time()),
			Raw:        blockJSON,
		}, len(blockJSON)+coreWriteRowOverhead); err != nil {
			return err
		}
	}
	return writer.close()
}

func putTransactionRowsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
) error {
	writer := newCoreSQLBatchWriter[coreTransactionWriteRow](
		ctx, tx, chainID, dbgen.StorePutTransactionsBatch, "upsert transactions",
	)
	for _, bundle := range bundles {
		for index, transaction := range bundle.Block.Transactions() {
			raw := bundle.RawTransactions[index]
			if err := writer.add(coreTransactionWriteRow{
				Hash: encodeCoreBytes(transaction.Hash().Bytes()),
				Type: strconv.FormatUint(uint64(transaction.Type()), 10),
				Raw:  raw,
			}, len(raw)+coreWriteRowOverhead); err != nil {
				return err
			}
		}
	}
	return writer.close()
}

func putTransactionFactRowsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
	query string,
	operation string,
	rawAt func(chainbundle.Bundle, int) json.RawMessage,
) error {
	writer := newCoreSQLBatchWriter[coreTransactionFactWriteRow](
		ctx, tx, chainID, query, operation,
	)
	for _, bundle := range bundles {
		reference, err := RefFromBundle(bundle)
		if err != nil {
			return fmt.Errorf("read owned transaction fact block identity: %w", err)
		}
		for index, transaction := range bundle.Block.Transactions() {
			raw := rawAt(bundle, index)
			if err := writer.add(coreTransactionFactWriteRow{
				BlockNumber: decimal(reference.Number),
				BlockHash:   encodeCoreBytes(reference.Hash[:]),
				TxIndex:     strconv.Itoa(index),
				TxHash:      encodeCoreBytes(transaction.Hash().Bytes()),
				Raw:         raw,
			}, len(raw)+coreWriteRowOverhead); err != nil {
				return err
			}
		}
	}
	return writer.close()
}

func putLogRowsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
) error {
	writer := newCoreSQLBatchWriter[coreLogWriteRow](
		ctx, tx, chainID, dbgen.StorePutLogsBatch, "upsert logs",
	)
	for _, bundle := range bundles {
		reference, err := RefFromBundle(bundle)
		if err != nil {
			return fmt.Errorf("read owned log block identity: %w", err)
		}
		for transactionIndex, receipt := range bundle.Receipts {
			transactionHash := bundle.Block.Transactions()[transactionIndex].Hash()
			for logPosition, logEntry := range receipt.Logs {
				raw := bundle.RawLogs[transactionIndex][logPosition]
				var topic0 *string
				if len(logEntry.Topics) > 0 {
					encoded := encodeCoreBytes(logEntry.Topics[0][:])
					topic0 = &encoded
				}
				if err := writer.add(coreLogWriteRow{
					BlockNumber: decimal(reference.Number),
					BlockHash:   encodeCoreBytes(reference.Hash[:]),
					LogIndex:    strconv.FormatUint(uint64(logEntry.Index), 10),
					TxIndex:     strconv.Itoa(transactionIndex),
					TxHash:      encodeCoreBytes(transactionHash[:]),
					Address:     encodeCoreBytes(logEntry.Address[:]),
					Topic0:      topic0,
					Raw:         raw,
				}, len(raw)+coreWriteRowOverhead); err != nil {
					return err
				}
			}
		}
	}
	return writer.close()
}

func putWithdrawalRowsTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	bundles []chainbundle.Bundle,
) error {
	writer := newCoreSQLBatchWriter[coreWithdrawalWriteRow](
		ctx, tx, chainID, dbgen.StorePutWithdrawalsBatch, "upsert withdrawals",
	)
	for _, bundle := range bundles {
		reference, err := RefFromBundle(bundle)
		if err != nil {
			return fmt.Errorf("read owned withdrawal block identity: %w", err)
		}
		for index, withdrawal := range bundle.Block.Withdrawals() {
			raw := bundle.RawWithdrawals[index]
			if err := writer.add(coreWithdrawalWriteRow{
				BlockNumber:     decimal(reference.Number),
				BlockHash:       encodeCoreBytes(reference.Hash[:]),
				WithdrawalIndex: decimal(withdrawal.Index),
				ValidatorIndex:  decimal(withdrawal.Validator),
				Address:         encodeCoreBytes(withdrawal.Address[:]),
				Amount:          decimal(withdrawal.Amount),
				Raw:             raw,
			}, len(raw)+coreWriteRowOverhead); err != nil {
				return err
			}
		}
	}
	return writer.close()
}

type canonicalBlockWriteRow struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
}

func insertCanonicalBlocksTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	references []BlockRef,
) error {
	writer := newCoreSQLBatchWriter[canonicalBlockWriteRow](
		ctx, tx, chainID, dbgen.StoreInsertCanonicalBlocksBatch,
		"insert canonical blocks",
	)
	for _, reference := range references {
		if err := writer.add(canonicalBlockWriteRow{
			Number: decimal(reference.Number),
			Hash:   encodeCoreBytes(reference.Hash[:]),
		}, coreWriteRowOverhead); err != nil {
			return err
		}
	}
	return writer.close()
}

func deleteCanonicalBlocksTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	references []BlockRef,
) error {
	var affected int64
	writer := newCoreJSONBatchWriter[canonicalBlockWriteRow](
		"delete canonical blocks",
		func(payload json.RawMessage) error {
			result, err := tx.ExecContext(
				ctx, dbgen.StoreDeleteCanonicalBlocksBatch, chainID, payload,
			)
			if err != nil {
				return fmt.Errorf("delete canonical blocks: %w", err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("count deleted canonical blocks: %w", err)
			}
			affected += count
			return nil
		},
	)
	for _, reference := range references {
		if err := writer.add(canonicalBlockWriteRow{
			Number: decimal(reference.Number),
			Hash:   encodeCoreBytes(reference.Hash[:]),
		}, coreWriteRowOverhead); err != nil {
			return err
		}
	}
	if err := writer.close(); err != nil {
		return err
	}
	if affected != int64(len(references)) {
		return fmt.Errorf(
			"%w: delete canonical blocks affected %d rows, expected %d",
			ErrConflict, affected, len(references),
		)
	}
	return nil
}

func setBlockJournalsCanonicalBatchTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	references []BlockRef,
	canonical bool,
) error {
	return writeHashBatchTx(
		ctx, tx, chainID, references, canonical,
		dbgen.StoreSetBlockJournalsCanonicalBatch,
		"set block journal canonical state",
	)
}

func setDerivedCanonicalBatchTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	references []BlockRef,
	canonical bool,
) error {
	return writeHashBatchTx(
		ctx, tx, chainID, references, canonical,
		dbgen.StoreSetDerivedCanonicalBatch,
		"set derived canonical state",
	)
}

func writeHashBatchTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	references []BlockRef,
	canonical bool,
	query string,
	operation string,
) error {
	writer := newCoreJSONBatchWriter[string](operation, func(payload json.RawMessage) error {
		if _, err := tx.ExecContext(ctx, query, chainID, payload, canonical); err != nil {
			return fmt.Errorf("%s: %w", operation, err)
		}
		return nil
	})
	for _, reference := range references {
		if err := writer.add(encodeCoreBytes(reference.Hash[:]), common.HashLength*2+4); err != nil {
			return err
		}
	}
	return writer.close()
}

type coreOutboxMessage struct {
	Topic     string
	Reference BlockRef
}

type coreOutboxWriteRow struct {
	Topic      string            `json:"topic"`
	MessageKey string            `json:"message_key"`
	Payload    coreOutboxPayload `json:"payload"`
}

type coreOutboxPayload struct {
	BlockHash   string `json:"block_hash"`
	BlockNumber string `json:"block_number"`
}

func insertCoreOutboxBatchTx(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	messages []coreOutboxMessage,
) error {
	writer := newCoreSQLBatchWriter[coreOutboxWriteRow](
		ctx, tx, chainID, dbgen.StoreInsertCoreOutboxBatch,
		"insert core outbox messages",
	)
	for _, message := range messages {
		hash := message.Reference.Hash.String()
		if err := writer.add(coreOutboxWriteRow{
			Topic:      message.Topic,
			MessageKey: hash,
			Payload: coreOutboxPayload{
				BlockHash:   hash,
				BlockNumber: decimal(message.Reference.Number),
			},
		}, coreWriteRowOverhead); err != nil {
			return err
		}
	}
	return writer.close()
}

func encodeCoreBytes(value []byte) string {
	return hex.EncodeToString(value)
}
