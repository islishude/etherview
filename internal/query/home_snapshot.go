package query

import (
	"context"
	"errors"
	"fmt"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/publicquery"
)

const homeSnapshotLimit = 6

var _ publicquery.HomeSnapshotReader = (*PostgresReader)(nil)

func (r *PostgresReader) HomeSnapshot(ctx context.Context) (publicquery.HomeSnapshotState, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return publicquery.HomeSnapshotState{}, fmt.Errorf("begin home snapshot: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	eventID, err := r.homeEventID(ctx, tx)
	if err != nil {
		return publicquery.HomeSnapshotState{}, err
	}
	status, err := r.status(ctx, tx, r.transactionRuntimeStatus(tx), nil)
	if err != nil {
		return publicquery.HomeSnapshotState{}, fmt.Errorf("read home status: %w", err)
	}
	blocks, transactions, err := r.homeActivity(ctx, tx)
	if err != nil {
		return publicquery.HomeSnapshotState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return publicquery.HomeSnapshotState{}, fmt.Errorf("commit home snapshot: %w", err)
	}
	return publicquery.HomeSnapshotState{
		EventID: eventID, Status: status,
		Blocks: blocks, Transactions: transactions,
	}, nil
}

func (r *PostgresReader) homeEventID(ctx context.Context, tx pgx.Tx) (uint64, error) {
	var id pgtype.Int8
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetHomeRuntimeEventID(ctx, queryValue0)
		if err != nil {
			return err
		}
		id = pgtype.Int8{Int64: queryRow, Valid: true}
		return nil
	}(); err != nil {
		return 0, fmt.Errorf("query home runtime event identity: %w", err)
	}
	if !id.Valid {
		return 0, nil
	}
	if id.Int64 < 0 {
		return 0, errors.New("home runtime event identity is invalid")
	}
	return uint64(id.Int64), nil
}

func (r *PostgresReader) transactionRuntimeStatus(tx pgx.Tx) RuntimeStatusFunc {
	return func(ctx context.Context) (RuntimeStatus, bool, error) {
		var latest, indexed, highest pgtype.Text
		var status RuntimeStatus
		err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(r.chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).GetHomeRuntimeStatus(ctx, queryValue0)
			if err != nil {
				return err
			}
			resultValue0, err := dbaccess.NumericText(queryRow.LatestNumber)
			if err != nil {
				return err
			}
			latest = resultValue0
			resultValue2, err := dbaccess.NumericText(queryRow.IndexedNumber)
			if err != nil {
				return err
			}
			indexed = resultValue2
			resultValue4, err := dbaccess.NumericText(queryRow.HighestCoveredNumber)
			if err != nil {
				return err
			}
			highest = resultValue4
			status.BackfillComplete = queryRow.BackfillComplete
			status.Ready = queryRow.Ready
			return nil
		}()
		if errors.Is(err, pgx.ErrNoRows) {
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

func nullableRuntimeQuantity(value pgtype.Text) (uint64, bool, error) {
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
	tx pgx.Tx,
) ([]gen.Block, []gen.Transaction, error) {
	var tipNumberText string
	var tipHash []byte
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetCurrentQueryTip(ctx, queryValue0)
		if err != nil {
			return err
		}
		tipNumberText = queryRow.CanonicalNumber
		tipHash = queryRow.BlockHash
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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

	blockRows, err := func() ([]dbgen.QueryListBlocksFirstRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(tipNumberText); err != nil {
			return nil, err
		}
		if homeSnapshotLimit < -2147483648 || homeSnapshotLimit > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).QueryListBlocksFirst(ctx, queryValue0, queryValue1, int32(homeSnapshotLimit))
	}()
	if err != nil {
		return nil, nil, fmt.Errorf("query home blocks: %w", err)
	}
	blocks := make([]gen.Block, 0, homeSnapshotLimit)
	for _, storedRow := range blockRows {
		record, scanErr := r.decodeBlock(dbgen.QueryListBlocksFirstRow(storedRow), true)
		if scanErr != nil {

			return nil, nil, scanErr
		}
		blocks = append(blocks, record.Model)
	}

	transactionRows, err := func() ([]dbgen.QueryListTransactionsFirstRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(tipNumberText); err != nil {
			return nil, err
		}
		if homeSnapshotLimit < -2147483648 || homeSnapshotLimit > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).QueryListTransactionsFirst(ctx, queryValue0, queryValue1, int32(homeSnapshotLimit))
	}()
	if err != nil {
		return nil, nil, fmt.Errorf("query home transactions: %w", err)
	}

	transactions := make([]gen.Transaction, 0, homeSnapshotLimit)
	for _, storedRow := range transactionRows {
		record, scanErr := r.decodeTransaction(dbgen.ListBlockTransactionsRow(storedRow), tipNumber)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		if !record.Model.Canonical {
			return nil, nil, errors.New("home transaction query returned an orphan inclusion")
		}
		transactions = append(transactions, record.Model)
	}

	return blocks, transactions, nil
}
