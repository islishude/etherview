package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
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
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable block transaction query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

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

	rows, err := tx.QueryContext(ctx, dbgen.ListBlockTransactions,
		r.chainID, strconv.FormatUint(blockNumber, 10), blockHash.Bytes(), cursor.AfterIndex, limit+1,
	)
	if err != nil {
		return nil, "", fmt.Errorf("query block transaction page: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	records := make([]transactionRecord, 0, limit+1)
	for rows.Next() {
		record, err := r.scanTransaction(rows, tip.SnapshotNumber)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate block transaction page: %w", err)
	}
	if err := tx.Commit(); err != nil {
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
	tx *sql.Tx,
	identifier string,
) (uint64, common.Hash, error) {
	if hash, isHash, err := parseHashIdentifier(identifier); err != nil {
		return 0, common.Hash{}, err
	} else if isHash {
		var numberText string
		var hashBytes []byte
		if err := tx.QueryRowContext(ctx, dbgen.GetBlockTransactionTargetByHash, r.chainID, hash.Bytes()).Scan(&numberText, &hashBytes); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
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
	if err := tx.QueryRowContext(ctx, dbgen.GetBlockTransactionTargetByNumber,
		r.chainID, strconv.FormatUint(number, 10),
	).Scan(&numberText, &hashBytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	tx *sql.Tx,
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
	if err := tx.QueryRowContext(ctx, dbgen.ValidateBlockTransactionCursor,
		r.chainID, strconv.FormatUint(blockNumber, 10), blockHash.Bytes(),
	).Scan(&exists); err != nil {
		return fmt.Errorf("validate block transaction cursor: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: block transaction branch changed", ErrInvalidCursor)
	}
	return nil
}
