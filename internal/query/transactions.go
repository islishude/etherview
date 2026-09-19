package query

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
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
	executionResolution pgtype.Text
	executionAddress    []byte
	executionCodeHash   []byte
	decodedSignature    pgtype.Text
	decodedSource       pgtype.Text
	decodedConfidence   pgtype.Text
}

func (r *PostgresReader) Transactions(ctx context.Context, encodedCursor string, limit int) ([]gen.Transaction, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("transaction limit %d is outside 1..100", limit)
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable transaction query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	var cursor transactionCursor
	if encodedCursor == "" {
		cursor, err = r.currentTransactionCursor(ctx, tx)
		if err != nil {
			return nil, "", err
		}
	} else {
		if err := publicquery.DecodeCursor(encodedCursor, &cursor); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateTransactionCursor(ctx, tx, cursor); err != nil {
			return nil, "", err
		}
	}

	chain, err := r.chainNumeric()
	if err != nil {
		return nil, "", err
	}
	queries := dbgen.New(r.db).WithTx(tx)
	var rows []dbgen.QueryListTransactionsWithMethodFirstRow
	if encodedCursor == "" {
		rows, err = queries.QueryListTransactionsWithMethodFirst(ctx, chain, numericUint64(cursor.SnapshotNumber), int32(limit+1))
	} else {
		var page []dbgen.QueryListTransactionsWithMethodRow
		page, err = queries.QueryListTransactionsWithMethod(ctx, dbgen.QueryListTransactionsWithMethodParams{ChainID: chain, MaxBlockNumber: numericUint64(cursor.BeforeBlockNumber), TxIndex: int64(cursor.BeforeTxIndex), Limit: int32(limit + 1)})
		rows = make([]dbgen.QueryListTransactionsWithMethodFirstRow, len(page))
		for index, row := range page {
			rows[index] = dbgen.QueryListTransactionsWithMethodFirstRow(row)
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("query canonical transaction page: %w", err)
	}
	records := make([]transactionRecord, 0, len(rows))
	for _, row := range rows {
		record, err := r.decodeTransactionWithMethod(row, cursor.SnapshotNumber)
		if err != nil {
			return nil, "", err
		}
		if !record.Model.Canonical {
			return nil, "", errors.New("canonical transaction query returned an orphan inclusion")
		}
		records = append(records, record)
	}

	if err := r.projectTransactionMethods(ctx, tx, records); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
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
	next, err := publicquery.EncodeCursor(transactionCursor{
		ChainID: r.chainID, SnapshotNumber: cursor.SnapshotNumber, SnapshotHash: cursor.SnapshotHash,
		BeforeBlockNumber: last.BlockNumber, BeforeBlockHash: last.BlockHash.String(),
		BeforeTxIndex: last.Index, BeforeTxHash: last.Hash.String(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode transaction cursor: %w", err)
	}
	return items, next, nil
}

func (r *PostgresReader) decodeTransactionWithMethod(row dbgen.QueryListTransactionsWithMethodFirstRow, tipNumber uint64) (transactionRecord, error) {
	record, err := r.decodeTransaction(dbgen.ListBlockTransactionsRow{Raw: row.Raw, ReceiptRaw: row.ReceiptRaw, BlockNumber: row.BlockNumber, BlockHash: row.BlockHash, TxIndex: row.TxIndex, TxHash: row.TxHash, Canonical: row.Canonical, SafeNumber: row.SafeNumber, FinalizedNumber: row.FinalizedNumber, BlockTimestamp: row.BlockTimestamp, BlockBaseFeePerGas: row.BlockBaseFeePerGas}, tipNumber)
	if err != nil {
		return transactionRecord{}, err
	}
	record.method = transactionMethodContext{stateDiffComplete: row.Exists, executionResolution: row.Resolution, executionAddress: row.ExecutionAddress, executionCodeHash: row.ExecutionCodeHash}
	if row.Signature != nil {
		record.method.decodedSignature = pgtype.Text{String: *row.Signature, Valid: true}
	}
	if row.Source != nil {
		record.method.decodedSource = pgtype.Text{String: *row.Source, Valid: true}
	}
	if row.Confidence != nil {
		record.method.decodedConfidence = pgtype.Text{String: *row.Confidence, Valid: true}
	}
	return record, nil
}

func projectTransactionMethod(
	model *gen.Transaction,
	executionResolution, methodSignature pgtype.Text,
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

func (r *PostgresReader) currentTransactionCursor(ctx context.Context, tx pgx.Tx) (transactionCursor, error) {
	var numberText string
	var hashBytes []byte
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetCurrentQueryTip(ctx, queryValue0)
		if err != nil {
			return err
		}
		numberText = queryRow.CanonicalNumber
		hashBytes = queryRow.BlockHash
		return nil
	}(); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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

func (r *PostgresReader) validateTransactionCursor(ctx context.Context, tx pgx.Tx, cursor transactionCursor) error {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(cursor.SnapshotNumber, 10)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(cursor.BeforeBlockNumber, 10)); err != nil {
			return err
		}
		if cursor.BeforeTxIndex > 9223372036854775807 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).QueryValidateTransactionCursor(ctx, dbgen.QueryValidateTransactionCursorParams{ChainID: queryValue0, Number: queryValue1, BlockHash: snapshotHash.Bytes(), BlockNumber: queryValue2, BlockHash2: beforeBlockHash.Bytes(), TxIndex: int64(cursor.BeforeTxIndex), TxHash: beforeTxHash.Bytes()})
		if err != nil {
			return err
		}
		if queryRow == nil {
			return errors.New("invalid stored query value")
		}
		valid = *queryRow
		return nil
	}(); err != nil {
		return fmt.Errorf("validate transaction cursor: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: canonical transaction branch changed", ErrInvalidCursor)
	}
	return nil
}

func (r *PostgresReader) decodeTransaction(row dbgen.ListBlockTransactionsRow, tipNumber uint64) (transactionRecord, error) {
	safeHeight, err := dbaccess.NumericText(row.SafeNumber)
	if err != nil {
		return transactionRecord{}, err
	}
	finalizedHeight, err := dbaccess.NumericText(row.FinalizedNumber)
	if err != nil {
		return transactionRecord{}, err
	}
	var blockBaseFeeText pgtype.Text
	if row.BlockBaseFeePerGas != nil {
		blockBaseFeeText = pgtype.Text{String: *row.BlockBaseFeePerGas, Valid: true}
	}

	model, err := r.transactionModel(
		row.Raw, row.ReceiptRaw, row.BlockTimestamp, blockBaseFeeText,
		row.BlockNumber, row.BlockHash, row.TxIndex,
		row.TxHash, row.Canonical, safeHeight, finalizedHeight, tipNumber,
	)
	if err != nil {
		return transactionRecord{}, err
	}
	blockNumber, err := parseDecimalUint64(row.BlockNumber)
	if err != nil {
		return transactionRecord{}, err
	}
	blockHash, err := decodeHashBytes(row.BlockHash)
	if err != nil {
		return transactionRecord{}, err
	}
	transactionHash, err := decodeHashBytes(row.TxHash)
	if err != nil {
		return transactionRecord{}, err
	}
	return transactionRecord{
		Model: model, BlockNumber: blockNumber, BlockHash: blockHash,
		Index: uint64(row.TxIndex), Hash: transactionHash,
	}, nil
}
