package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

type blockTransactionCursor struct {
	ChainID     string `json:"chain_id"`
	BlockNumber uint64 `json:"block_number"`
	BlockHash   string `json:"block_hash"`
	AfterIndex  int64  `json:"after_index"`
}

// BlockTransactions lists the transactions attached to one exact block
// identity. A height is resolved through the current canonical mapping; a
// hash is resolved directly so retained orphan blocks remain inspectable.
func (r *PostgresReader) BlockTransactions(
	ctx context.Context,
	identifier string,
	encodedCursor string,
	limit int,
) ([]gen.Transaction, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("block transaction limit %d is outside 1..100", limit)
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable block transaction query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	tip, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	blockNumber, blockHash, err := r.resolveBlockTransactionTarget(ctx, tx, identifier)
	if err != nil {
		return nil, "", err
	}

	cursor := blockTransactionCursor{
		ChainID: r.chainID, BlockNumber: blockNumber,
		BlockHash: blockHash.String(), AfterIndex: -1,
	}
	if encodedCursor != "" {
		if err := publicquery.DecodeCursor(encodedCursor, &cursor); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateBlockTransactionCursor(ctx, tx, cursor, blockNumber, blockHash); err != nil {
			return nil, "", err
		}
	}

	rows, err := func() ([]dbgen.ListBlockTransactionsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(blockNumber, 10)); err != nil {
			return nil, err
		}
		if limit+1 < -2147483648 || limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).ListBlockTransactions(ctx, dbgen.ListBlockTransactionsParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: blockHash.Bytes(), TxIndex: cursor.AfterIndex, Limit: int32(limit + 1)})
	}()
	if err != nil {
		return nil, "", fmt.Errorf("query block transaction page: %w", err)
	}

	records := make([]transactionRecord, 0, limit+1)
	for _, storedRow := range rows {
		record, err := r.decodeTransaction(dbgen.ListBlockTransactionsRow(storedRow), tip.SnapshotNumber)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit stable block transaction query: %w", err)
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
	next, err := publicquery.EncodeCursor(blockTransactionCursor{
		ChainID: r.chainID, BlockNumber: blockNumber,
		BlockHash: blockHash.String(), AfterIndex: int64(last.Index),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode block transaction cursor: %w", err)
	}
	return items, next, nil
}

func (r *PostgresReader) resolveBlockTransactionTarget(
	ctx context.Context,
	tx pgx.Tx,
	identifier string,
) (uint64, common.Hash, error) {
	if hash, isHash, err := parseHashIdentifier(identifier); err != nil {
		return 0, common.Hash{}, err
	} else if isHash {
		var numberText string
		var hashBytes []byte
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(r.chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).GetBlockTransactionTargetByHash(ctx, queryValue0, hash.Bytes())
			if err != nil {
				return err
			}
			numberText = queryRow.BlockNumber
			hashBytes = queryRow.BlockHash
			return nil
		}(); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, common.Hash{}, publicquery.ErrNotFound
			}
			return 0, common.Hash{}, fmt.Errorf("query block transaction target by hash: %w", err)
		}
		number, err := parseDecimalUint64(numberText)
		if err != nil {
			return 0, common.Hash{}, fmt.Errorf("decode block transaction target number: %w", err)
		}
		resolved, err := decodeHashBytes(hashBytes)
		if err != nil {
			return 0, common.Hash{}, err
		}
		if resolved != hash {
			return 0, common.Hash{}, errors.New("block transaction target hash does not match indexed identity")
		}
		return number, resolved, nil
	}

	number, err := parseBlockNumber(identifier)
	if err != nil {
		return 0, common.Hash{}, err
	}
	var numberText string
	var hashBytes []byte
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(number, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetBlockTransactionTargetByNumber(ctx, queryValue0, queryValue1)
		if err != nil {
			return err
		}
		numberText = queryRow.BlockNumber
		hashBytes = queryRow.BlockHash
		return nil
	}(); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, common.Hash{}, publicquery.ErrNotFound
		}
		return 0, common.Hash{}, fmt.Errorf("query block transaction target by number: %w", err)
	}
	resolvedNumber, err := parseDecimalUint64(numberText)
	if err != nil {
		return 0, common.Hash{}, fmt.Errorf("decode block transaction target canonical number: %w", err)
	}
	resolvedHash, err := decodeHashBytes(hashBytes)
	if err != nil {
		return 0, common.Hash{}, err
	}
	if resolvedNumber != number {
		return 0, common.Hash{}, errors.New("block transaction target number does not match indexed identity")
	}
	return resolvedNumber, resolvedHash, nil
}

func (r *PostgresReader) validateBlockTransactionCursor(
	ctx context.Context,
	tx pgx.Tx,
	cursor blockTransactionCursor,
	blockNumber uint64,
	blockHash common.Hash,
) error {
	if cursor.ChainID != r.chainID || cursor.BlockNumber != blockNumber || cursor.AfterIndex < 0 {
		return fmt.Errorf("%w: block transaction cursor identity or ordering is invalid", ErrInvalidCursor)
	}
	cursorHash, err := ethrpc.ParseHash(cursor.BlockHash)
	if err != nil || cursorHash != blockHash {
		return fmt.Errorf("%w: block transaction cursor block hash is invalid", ErrInvalidCursor)
	}
	var exists bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(blockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ValidateBlockTransactionCursor(ctx, queryValue0, queryValue1, blockHash.Bytes())
		if err != nil {
			return err
		}
		exists = queryRow
		return nil
	}(); err != nil {
		return fmt.Errorf("validate block transaction cursor: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: block transaction branch changed", ErrInvalidCursor)
	}
	return nil
}
