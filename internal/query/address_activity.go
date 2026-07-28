package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type addressTransactionCursor struct {
	Version           int    `json:"v"`
	ChainID           string `json:"chain_id"`
	Address           string `json:"address"`
	SnapshotNumber    uint64 `json:"snapshot_number"`
	SnapshotHash      string `json:"snapshot_hash"`
	BeforeBlockNumber uint64 `json:"before_block_number"`
	BeforeBlockHash   string `json:"before_block_hash"`
	BeforeTxIndex     uint64 `json:"before_tx_index"`
	BeforeTxHash      string `json:"before_tx_hash"`
}

func (r *PostgresReader) AddressTransactions(
	ctx context.Context,
	rawAddress string,
	encodedCursor string,
	limit int,
) ([]gen.Transaction, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("address transaction limit %d is outside 1..100", limit)
	}
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return nil, "", fmt.Errorf("invalid address: %w", err)
	}
	normalizedAddress := strings.ToLower(address.Hex())
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable address transaction query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var cursor addressTransactionCursor
	if encodedCursor == "" {
		snapshot, snapshotErr := r.currentTransactionCursor(ctx, tx)
		if snapshotErr != nil {
			return nil, "", snapshotErr
		}
		cursor = addressTransactionCursor{
			Version: 1, ChainID: r.chainID, Address: normalizedAddress,
			SnapshotNumber: snapshot.SnapshotNumber, SnapshotHash: snapshot.SnapshotHash,
			BeforeBlockNumber: snapshot.BeforeBlockNumber, BeforeBlockHash: snapshot.BeforeBlockHash,
		}
	} else {
		if err := httpapi.DecodeCursor(encodedCursor, &cursor); err != nil ||
			cursor.Version != 1 || cursor.ChainID != r.chainID || cursor.Address != normalizedAddress ||
			cursor.BeforeBlockNumber > cursor.SnapshotNumber || cursor.BeforeTxIndex > math.MaxInt64 {
			return nil, "", ErrInvalidCursor
		}
		if err := r.validateTransactionCursor(ctx, tx, transactionCursor{
			ChainID: cursor.ChainID, SnapshotNumber: cursor.SnapshotNumber, SnapshotHash: cursor.SnapshotHash,
			BeforeBlockNumber: cursor.BeforeBlockNumber, BeforeBlockHash: cursor.BeforeBlockHash,
			BeforeTxIndex: cursor.BeforeTxIndex, BeforeTxHash: cursor.BeforeTxHash,
		}); err != nil {
			return nil, "", err
		}
	}

	query := listAddressTransactionsSQL
	arguments := []any{
		r.chainID, strconv.FormatUint(cursor.BeforeBlockNumber, 10),
		cursor.BeforeTxIndex, normalizedAddress, limit + 1,
	}
	if encodedCursor == "" {
		query = listAddressTransactionsFirstSQL
		arguments = []any{
			r.chainID, strconv.FormatUint(cursor.SnapshotNumber, 10),
			normalizedAddress, limit + 1,
		}
	}
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, "", fmt.Errorf("query canonical address transaction page: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	records := make([]transactionRecord, 0, limit+1)
	for rows.Next() {
		record, scanErr := r.scanTransaction(rows, cursor.SnapshotNumber)
		if scanErr != nil {
			return nil, "", scanErr
		}
		if !record.Model.Canonical {
			return nil, "", errors.New("canonical address transaction query returned an orphan inclusion")
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate canonical address transaction page: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("commit stable address transaction query: %w", err)
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
	next, err := httpapi.EncodeCursor(addressTransactionCursor{
		Version: 1, ChainID: r.chainID, Address: normalizedAddress,
		SnapshotNumber: cursor.SnapshotNumber, SnapshotHash: cursor.SnapshotHash,
		BeforeBlockNumber: last.BlockNumber, BeforeBlockHash: last.BlockHash.String(),
		BeforeTxIndex: last.Index, BeforeTxHash: last.Hash.String(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode address transaction cursor: %w", err)
	}
	return items, next, nil
}

const addressTransactionColumns = `
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text,
    block.raw`

const addressTransactionCandidatesFirst = `
    SELECT candidate.block_number, candidate.block_hash, candidate.tx_index, candidate.tx_hash
    FROM (
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'from') = $3
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'to') = $3
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM receipts
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'contractAddress') = $3
    ) AS candidate`

const addressTransactionCandidatesAfter = `
    SELECT candidate.block_number, candidate.block_hash, candidate.tx_index, candidate.tx_hash
    FROM (
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3))
          AND lower(raw->>'from') = $4
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3))
          AND lower(raw->>'to') = $4
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM receipts
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3))
          AND lower(raw->>'contractAddress') = $4
    ) AS candidate`

const listAddressTransactionsFirstSQL = `
WITH candidates AS (` + addressTransactionCandidatesFirst + `)
SELECT ` + addressTransactionColumns + `
FROM candidates
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = $1::numeric
 AND inclusion.block_number = candidates.block_number
 AND inclusion.block_hash = candidates.block_hash
 AND inclusion.tx_index = candidates.tx_index
 AND inclusion.tx_hash = candidates.tx_hash
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
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC
LIMIT $4`

const listAddressTransactionsSQL = `
WITH candidates AS (` + addressTransactionCandidatesAfter + `)
SELECT ` + addressTransactionColumns + `
FROM candidates
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = $1::numeric
 AND inclusion.block_number = candidates.block_number
 AND inclusion.block_hash = candidates.block_hash
 AND inclusion.tx_index = candidates.tx_index
 AND inclusion.tx_hash = candidates.tx_hash
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
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC
LIMIT $5`
