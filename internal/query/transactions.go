package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type transactionCursor struct {
	ChainID           string `json:"chain_id"`
	SnapshotNumber    uint64 `json:"snapshot_number"`
	SnapshotHash      string `json:"snapshot_hash"`
	BeforeBlockNumber uint64 `json:"before_block_number"`
	BeforeBlockHash   string `json:"before_block_hash"`
	BeforeTxIndex     uint64 `json:"before_tx_index"`
	BeforeTxHash      string `json:"before_tx_hash"`
}

type transactionRecord struct {
	Model       gen.Transaction
	BlockNumber uint64
	BlockHash   common.Hash
	Index       uint64
	Hash        common.Hash
}

func (r *PostgresReader) Transactions(ctx context.Context, encodedCursor string, limit int) ([]gen.Transaction, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("transaction limit %d is outside 1..100", limit)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable transaction query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var cursor transactionCursor
	if encodedCursor == "" {
		cursor, err = r.currentTransactionCursor(ctx, tx)
		if err != nil {
			return nil, "", err
		}
	} else {
		if err := httpapi.DecodeCursor(encodedCursor, &cursor); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateTransactionCursor(ctx, tx, cursor); err != nil {
			return nil, "", err
		}
	}

	query, arguments := listTransactionsWithMethodSQL, []any{
		r.chainID, strconv.FormatUint(cursor.BeforeBlockNumber, 10), cursor.BeforeTxIndex, limit + 1,
	}
	if encodedCursor == "" {
		query = listTransactionsWithMethodFirstSQL
		arguments = []any{r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10), limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, "", fmt.Errorf("query canonical transaction page: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	records := make([]transactionRecord, 0, limit+1)
	for rows.Next() {
		record, err := r.scanTransactionWithMethod(rows, cursor.SnapshotNumber)
		if err != nil {
			return nil, "", err
		}
		if !record.Model.Canonical {
			return nil, "", errors.New("canonical transaction query returned an orphan inclusion")
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate canonical transaction page: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("commit stable transaction query: %w", err)
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	items := make([]gen.Transaction, len(records))
	for index := range records {
		items[index] = records[index].Model
	}
	if !hasMore || len(records) == 0 {
		return items, "", nil
	}
	last := records[len(records)-1]
	next, err := httpapi.EncodeCursor(transactionCursor{
		ChainID: r.chainID, SnapshotNumber: cursor.SnapshotNumber, SnapshotHash: cursor.SnapshotHash,
		BeforeBlockNumber: last.BlockNumber, BeforeBlockHash: last.BlockHash.String(),
		BeforeTxIndex: last.Index, BeforeTxHash: last.Hash.String(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode transaction cursor: %w", err)
	}
	return items, next, nil
}

func (r *PostgresReader) scanTransactionWithMethod(
	scanner rowScanner,
	tipNumber uint64,
) (transactionRecord, error) {
	var transactionJSON, receiptJSON, blockRaw []byte
	var blockNumberText string
	var blockHashBytes, transactionHashBytes []byte
	var transactionIndex int64
	var canonical bool
	var safeHeight, finalizedHeight, executionResolution, methodSignature sql.NullString
	if err := scanner.Scan(
		&transactionJSON, &receiptJSON, &blockNumberText, &blockHashBytes, &transactionIndex,
		&transactionHashBytes, &canonical, &safeHeight, &finalizedHeight, &blockRaw,
		&executionResolution, &methodSignature,
	); err != nil {
		return transactionRecord{}, fmt.Errorf("scan transaction with method: %w", err)
	}
	model, err := r.transactionModel(
		transactionJSON, receiptJSON, blockRaw, blockNumberText, blockHashBytes, transactionIndex,
		transactionHashBytes, canonical, safeHeight, finalizedHeight, tipNumber,
	)
	if err != nil {
		return transactionRecord{}, err
	}
	projectTransactionMethod(&model, executionResolution, methodSignature)
	blockNumber, err := parseDecimalUint64(blockNumberText)
	if err != nil {
		return transactionRecord{}, err
	}
	blockHash, err := decodeHashBytes(blockHashBytes)
	if err != nil {
		return transactionRecord{}, err
	}
	transactionHash, err := decodeHashBytes(transactionHashBytes)
	if err != nil {
		return transactionRecord{}, err
	}
	return transactionRecord{
		Model: model, BlockNumber: blockNumber, BlockHash: blockHash,
		Index: uint64(transactionIndex), Hash: transactionHash,
	}, nil
}

func projectTransactionMethod(
	model *gen.Transaction,
	executionResolution, methodSignature sql.NullString,
) {
	if model.To == nil {
		method := "Contract Creation"
		model.Method = &method
		return
	}
	if methodSignature.Valid {
		open := strings.IndexByte(methodSignature.String, '(')
		if open > 0 && len(methodSignature.String) <= 4096 {
			method := methodSignature.String[:open]
			model.Method = &method
			model.MethodSignature = &methodSignature.String
			return
		}
	}
	if executionResolution.Valid && executionResolution.String == "empty" && model.Input == "0x" {
		method := "Native Transfer"
		model.Method = &method
		return
	}
	method := model.Input
	if len(method) > 10 {
		method = method[:10]
	}
	method = strings.ToLower(method)
	model.Method = &method
}

func (r *PostgresReader) currentTransactionCursor(ctx context.Context, tx *sql.Tx) (transactionCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := tx.QueryRowContext(ctx, currentTipSQL, r.chainID).Scan(&numberText, &hashBytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return transactionCursor{}, httpUnavailableNotReady()
		}
		return transactionCursor{}, fmt.Errorf("query transaction cursor snapshot: %w", err)
	}
	number, err := parseDecimalUint64(numberText)
	if err != nil {
		return transactionCursor{}, fmt.Errorf("decode transaction cursor snapshot number: %w", err)
	}
	hash, err := decodeHashBytes(hashBytes)
	if err != nil {
		return transactionCursor{}, err
	}
	return transactionCursor{
		ChainID: r.chainID, SnapshotNumber: number, SnapshotHash: hash.String(),
		BeforeBlockNumber: number, BeforeBlockHash: hash.String(),
	}, nil
}

func (r *PostgresReader) validateTransactionCursor(ctx context.Context, tx *sql.Tx, cursor transactionCursor) error {
	if cursor.ChainID != r.chainID || cursor.BeforeBlockNumber > cursor.SnapshotNumber || cursor.BeforeTxIndex > math.MaxInt64 {
		return fmt.Errorf("%w: transaction cursor chain or ordering is invalid", ErrInvalidCursor)
	}
	snapshotHash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return fmt.Errorf("%w: invalid transaction snapshot hash", ErrInvalidCursor)
	}
	beforeBlockHash, err := ethrpc.ParseHash(cursor.BeforeBlockHash)
	if err != nil {
		return fmt.Errorf("%w: invalid transaction boundary block hash", ErrInvalidCursor)
	}
	beforeTxHash, err := ethrpc.ParseHash(cursor.BeforeTxHash)
	if err != nil {
		return fmt.Errorf("%w: invalid transaction boundary hash", ErrInvalidCursor)
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, validateTransactionCursorSQL,
		r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10), snapshotHash.Bytes(),
		strconv.FormatUint(cursor.BeforeBlockNumber, 10), beforeBlockHash.Bytes(),
		cursor.BeforeTxIndex, beforeTxHash.Bytes(),
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate transaction cursor: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: canonical transaction branch changed", ErrInvalidCursor)
	}
	return nil
}

func (r *PostgresReader) scanTransaction(scanner rowScanner, tipNumber uint64) (transactionRecord, error) {
	var transactionJSON, receiptJSON, blockRaw []byte
	var blockNumberText string
	var blockHashBytes, transactionHashBytes []byte
	var transactionIndex int64
	var canonical bool
	var safeHeight, finalizedHeight sql.NullString
	if err := scanner.Scan(
		&transactionJSON, &receiptJSON, &blockNumberText, &blockHashBytes, &transactionIndex,
		&transactionHashBytes, &canonical, &safeHeight, &finalizedHeight, &blockRaw,
	); err != nil {
		return transactionRecord{}, fmt.Errorf("scan transaction: %w", err)
	}
	model, err := r.transactionModel(
		transactionJSON, receiptJSON, blockRaw, blockNumberText, blockHashBytes, transactionIndex,
		transactionHashBytes, canonical, safeHeight, finalizedHeight, tipNumber,
	)
	if err != nil {
		return transactionRecord{}, err
	}
	blockNumber, err := parseDecimalUint64(blockNumberText)
	if err != nil {
		return transactionRecord{}, err
	}
	blockHash, err := decodeHashBytes(blockHashBytes)
	if err != nil {
		return transactionRecord{}, err
	}
	transactionHash, err := decodeHashBytes(transactionHashBytes)
	if err != nil {
		return transactionRecord{}, err
	}
	return transactionRecord{
		Model: model, BlockNumber: blockNumber, BlockHash: blockHash,
		Index: uint64(transactionIndex), Hash: transactionHash,
	}, nil
}

const listTransactionsFirstSQL = `
SELECT
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text,
    block.raw
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.block_number <= $2::numeric
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $3`

const transactionMethodJoinsSQL = `
LEFT JOIN transaction_execution_code_resolutions AS execution
  ON execution.chain_id = inclusion.chain_id
 AND execution.block_number = inclusion.block_number
 AND execution.block_hash = inclusion.block_hash
 AND execution.transaction_hash = inclusion.tx_hash
 AND execution.transaction_index = inclusion.tx_index
 AND execution.context_address = decode(substring(inclusion.raw->>'to' from 3), 'hex')
 AND execution.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published_state_diff
     WHERE published_state_diff.chain_id = execution.chain_id
       AND published_state_diff.block_number = execution.block_number
       AND published_state_diff.block_hash = execution.block_hash
       AND published_state_diff.stage = 'state_diff'
       AND published_state_diff.stage_version = 2
       AND published_state_diff.state = 'complete'
 )
LEFT JOIN abi_decodings AS decoding
  ON decoding.chain_id = inclusion.chain_id
 AND decoding.block_number = inclusion.block_number
 AND decoding.block_hash = inclusion.block_hash
 AND decoding.transaction_hash = inclusion.tx_hash
 AND decoding.object_kind = 'transaction_calldata'
 AND decoding.object_index = ''
 AND decoding.target_address = execution.execution_address
 AND decoding.target_code_hash = execution.execution_code_hash
 AND decoding.status = 'decoded'
 AND decoding.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published_abi
     WHERE published_abi.chain_id = decoding.chain_id
       AND published_abi.block_number = decoding.block_number
       AND published_abi.block_hash = decoding.block_hash
       AND published_abi.stage = 'abi'
       AND published_abi.stage_version = 3
       AND published_abi.state = 'complete'
 )`

const listTransactionsWithMethodColumnsSQL = `
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text,
    block.raw,
    execution.resolution,
    decoding.signature`

const listTransactionsWithMethodFirstSQL = `
SELECT ` + listTransactionsWithMethodColumnsSQL + `
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id
` + transactionMethodJoinsSQL + `
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.block_number <= $2::numeric
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $3`

const listTransactionsWithMethodSQL = `
SELECT ` + listTransactionsWithMethodColumnsSQL + `
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id
` + transactionMethodJoinsSQL + `
WHERE inclusion.chain_id = $1::numeric
  AND (
      inclusion.block_number < $2::numeric
      OR (inclusion.block_number = $2::numeric AND inclusion.tx_index < $3)
  )
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $4`

const validateTransactionCursorSQL = `
SELECT
    EXISTS (
        SELECT 1 FROM canonical_blocks
        WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
    )
AND EXISTS (
    SELECT 1
    FROM transaction_inclusions AS inclusion
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = inclusion.chain_id
     AND canonical.number = inclusion.block_number
     AND canonical.block_hash = inclusion.block_hash
    WHERE inclusion.chain_id = $1::numeric
      AND inclusion.block_number = $4::numeric
      AND inclusion.block_hash = $5
      AND inclusion.tx_index = $6
      AND inclusion.tx_hash = $7
)`
