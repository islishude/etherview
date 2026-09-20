package query

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable address transaction query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

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

	chain, err := r.chainNumeric()
	if err != nil {
		return nil, "", err
	}
	queries := dbgen.New(r.db).WithTx(tx)
	var rows []dbgen.QueryListTransactionsWithMethodFirstRow
	if encodedCursor == "" {
		page, queryErr := queries.QueryListAddressTransactionsFirst(ctx, dbgen.QueryListAddressTransactionsFirstParams{ChainID: chain, MaxBlockNumber: numericUint64(cursor.SnapshotNumber), AddressHex: normalizedAddress, Limit: int32(limit + 1)})
		err = queryErr
		rows = make([]dbgen.QueryListTransactionsWithMethodFirstRow, len(page))
		for index, row := range page {
			rows[index] = dbgen.QueryListTransactionsWithMethodFirstRow(row)
		}
	} else {
		page, queryErr := queries.QueryListAddressTransactions(ctx, dbgen.QueryListAddressTransactionsParams{ChainID: chain, MaxBlockNumber: numericUint64(cursor.BeforeBlockNumber), MaxTxIndex: int64(cursor.BeforeTxIndex), AddressHex: normalizedAddress, Limit: int32(limit + 1)})
		err = queryErr
		rows = make([]dbgen.QueryListTransactionsWithMethodFirstRow, len(page))
		for index, row := range page {
			rows[index] = dbgen.QueryListTransactionsWithMethodFirstRow(row)
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("query canonical address transaction page: %w", err)
	}
	records := make([]transactionRecord, 0, len(rows))
	for _, row := range rows {
		record, err := r.decodeTransactionWithMethod(row, cursor.SnapshotNumber)
		if err != nil {
			return nil, "", err
		}
		if !record.Model.Canonical {
			return nil, "", errors.New("canonical address transaction query returned an orphan inclusion")
		}
		records = append(records, record)
	}

	if err := r.projectTransactionMethods(ctx, tx, records); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
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
