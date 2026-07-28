package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/httpapi"
)

const homeSnapshotLimit = 6

var _ httpapi.HomeSnapshotReader = (*PostgresReader)(nil)

func (r *PostgresReader) HomeSnapshot(ctx context.Context) (httpapi.HomeSnapshotState, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return httpapi.HomeSnapshotState{}, fmt.Errorf("begin home snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	eventID, err := r.homeEventID(ctx, tx)
	if err != nil {
		return httpapi.HomeSnapshotState{}, err
	}
	status, err := r.status(ctx, tx, r.transactionRuntimeStatus(tx), nil)
	if err != nil {
		return httpapi.HomeSnapshotState{}, fmt.Errorf("read home status: %w", err)
	}
	blocks, transactions, err := r.homeActivity(ctx, tx)
	if err != nil {
		return httpapi.HomeSnapshotState{}, err
	}
	if err := tx.Commit(); err != nil {
		return httpapi.HomeSnapshotState{}, fmt.Errorf("commit home snapshot: %w", err)
	}
	return httpapi.HomeSnapshotState{
		EventID: eventID, Status: status,
		Blocks: blocks, Transactions: transactions,
	}, nil
}

func (r *PostgresReader) homeEventID(ctx context.Context, tx *sql.Tx) (uint64, error) {
	var id sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(id)
		FROM runtime_events
		WHERE chain_id = $1::numeric`, r.chainID).Scan(&id); err != nil {
		return 0, fmt.Errorf("query home runtime event identity: %w", err)
	}
	if !id.Valid {
		return 0, nil
	}
	if id.Int64 <= 0 {
		return 0, errors.New("home runtime event identity is invalid")
	}
	return uint64(id.Int64), nil
}

func (r *PostgresReader) transactionRuntimeStatus(tx *sql.Tx) RuntimeStatusFunc {
	return func(ctx context.Context) (RuntimeStatus, bool, error) {
		var latest, indexed, highest sql.NullString
		var status RuntimeStatus
		err := tx.QueryRowContext(ctx, `
			SELECT latest_number::text, indexed_number::text,
			       highest_covered_number::text, backfill_complete, ready
			FROM sync_runtime_status
			WHERE chain_id = $1::numeric`, r.chainID).Scan(
			&latest, &indexed, &highest, &status.BackfillComplete, &status.Ready,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return RuntimeStatus{}, false, nil
		}
		if err != nil {
			return RuntimeStatus{}, false, fmt.Errorf("query home runtime status: %w", err)
		}
		var parseErr error
		status.Latest, status.LatestKnown, parseErr = nullableRuntimeQuantity(latest)
		if parseErr != nil {
			return RuntimeStatus{}, false, fmt.Errorf("decode home latest block: %w", parseErr)
		}
		status.Indexed, status.IndexedKnown, parseErr = nullableRuntimeQuantity(indexed)
		if parseErr != nil {
			return RuntimeStatus{}, false, fmt.Errorf("decode home indexed block: %w", parseErr)
		}
		status.HighestCovered, status.HighestCoveredKnown, parseErr = nullableRuntimeQuantity(highest)
		if parseErr != nil {
			return RuntimeStatus{}, false, fmt.Errorf("decode home highest covered block: %w", parseErr)
		}
		return status, true, nil
	}
}

func nullableRuntimeQuantity(value sql.NullString) (uint64, bool, error) {
	if !value.Valid {
		return 0, false, nil
	}
	parsed, err := parseDecimalUint64(value.String)
	if err != nil {
		return 0, false, err
	}
	return parsed, true, nil
}

func (r *PostgresReader) homeActivity(
	ctx context.Context,
	tx *sql.Tx,
) ([]gen.Block, []gen.Transaction, error) {
	var tipNumberText string
	var tipHash []byte
	err := tx.QueryRowContext(ctx, currentTipSQL, r.chainID).Scan(&tipNumberText, &tipHash)
	if errors.Is(err, sql.ErrNoRows) {
		return []gen.Block{}, []gen.Transaction{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("query home canonical tip: %w", err)
	}
	tipNumber, err := parseDecimalUint64(tipNumberText)
	if err != nil {
		return nil, nil, fmt.Errorf("decode home canonical tip: %w", err)
	}
	if _, err := decodeHashBytes(tipHash); err != nil {
		return nil, nil, fmt.Errorf("decode home canonical tip: %w", err)
	}

	blockRows, err := tx.QueryContext(
		ctx, listBlocksFirstSQL, r.chainID, tipNumberText, homeSnapshotLimit,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query home blocks: %w", err)
	}
	blocks := make([]gen.Block, 0, homeSnapshotLimit)
	for blockRows.Next() {
		record, scanErr := r.scanBlock(blockRows, true)
		if scanErr != nil {
			_ = blockRows.Close()
			return nil, nil, scanErr
		}
		blocks = append(blocks, record.Model)
	}
	if err := blockRows.Err(); err != nil {
		_ = blockRows.Close()
		return nil, nil, fmt.Errorf("iterate home blocks: %w", err)
	}
	if err := blockRows.Close(); err != nil {
		return nil, nil, fmt.Errorf("close home blocks: %w", err)
	}

	transactionRows, err := tx.QueryContext(
		ctx, listTransactionsFirstSQL, r.chainID, tipNumberText, homeSnapshotLimit,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query home transactions: %w", err)
	}
	defer transactionRows.Close() //nolint:errcheck
	transactions := make([]gen.Transaction, 0, homeSnapshotLimit)
	for transactionRows.Next() {
		record, scanErr := r.scanTransaction(transactionRows, tipNumber)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		if !record.Model.Canonical {
			return nil, nil, errors.New("home transaction query returned an orphan inclusion")
		}
		transactions = append(transactions, record.Model)
	}
	if err := transactionRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate home transactions: %w", err)
	}
	return blocks, transactions, nil
}
