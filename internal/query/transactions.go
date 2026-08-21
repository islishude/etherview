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
	"github.com/islishude/etherview/internal/db/gen"
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
	method      transactionMethodContext
}

type transactionMethodContext struct {
	stateDiffComplete   bool
	executionResolution sql.NullString
	executionAddress    []byte
	executionCodeHash   []byte
	decodedSignature    sql.NullString
	decodedSource       sql.NullString
	decodedConfidence   sql.NullString
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

	query, arguments := dbgen.QueryListTransactionsWithMethod, []any{
		r.chainID, strconv.FormatUint(cursor.BeforeBlockNumber, 10), cursor.BeforeTxIndex, limit + 1,
	}
	if encodedCursor == "" {
		query = dbgen.QueryListTransactionsWithMethodFirst
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
	if err := rows.Close(); err != nil {
		return nil, "", fmt.Errorf("close canonical transaction page: %w", err)
	}
	if err := r.projectTransactionMethods(ctx, tx, records); err != nil {
		return nil, "", err
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
	var safeHeight, finalizedHeight sql.NullString
	var method transactionMethodContext
	if err := scanner.Scan(
		&transactionJSON, &receiptJSON, &blockNumberText, &blockHashBytes, &transactionIndex,
		&transactionHashBytes, &canonical, &safeHeight, &finalizedHeight, &blockRaw,
		&method.stateDiffComplete, &method.executionResolution,
		&method.executionAddress, &method.executionCodeHash,
		&method.decodedSignature, &method.decodedSource, &method.decodedConfidence,
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
		Index: uint64(transactionIndex), Hash: transactionHash, method: method,
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
		if validTransactionMethodSignature(methodSignature.String) {
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

func validTransactionMethodSignature(signature string) bool {
	open := strings.IndexByte(signature, '(')
	return open > 0 && len(signature) <= 4096 && strings.HasSuffix(signature, ")")
}

func (r *PostgresReader) currentTransactionCursor(ctx context.Context, tx *sql.Tx) (transactionCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := tx.QueryRowContext(ctx, dbgen.GetCurrentQueryTip, r.chainID).Scan(&numberText, &hashBytes); err != nil {
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
	if err := tx.QueryRowContext(ctx, dbgen.QueryValidateTransactionCursor, r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10), snapshotHash.Bytes(),
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
