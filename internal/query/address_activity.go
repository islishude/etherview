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
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
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
		if err := publicquery.DecodeCursor(encodedCursor, &cursor); err != nil ||
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

	query := dbgen.QueryListAddressTransactions
	arguments := []any{
		r.chainID, strconv.FormatUint(cursor.BeforeBlockNumber, 10),
		cursor.BeforeTxIndex, normalizedAddress, limit + 1,
	}
	if encodedCursor == "" {
		query = dbgen.QueryListAddressTransactionsFirst
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
		record, scanErr := r.scanTransactionWithMethod(rows, cursor.SnapshotNumber)
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
	if err := rows.Close(); err != nil {
		return nil, "", fmt.Errorf("close canonical address transaction page: %w", err)
	}
	if err := r.projectTransactionMethods(ctx, tx, records); err != nil {
		return nil, "", err
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
	next, err := publicquery.EncodeCursor(addressTransactionCursor{
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
