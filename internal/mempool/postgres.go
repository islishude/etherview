package mempool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	cursorVersion      = 1
	maximumCursorBytes = 2048
)

var errSnapshotExpired = errors.New("mempool snapshot expired")

type PostgresOptions struct {
	ChainID uint64
	Enabled bool
	Now     func() time.Time
}

type Postgres struct {
	db      dbaccess.Database
	chainID uint64
	chain   string
	enabled bool
	now     func() time.Time
}

var _ Store = (*Postgres)(nil)
var _ Reader = (*Postgres)(nil)

func NewPostgres(db dbaccess.Database, options PostgresOptions) (*Postgres, error) {
	if db == nil {
		return nil, errors.New("mempool database is nil")
	}
	if options.ChainID == 0 {
		return nil, errors.New("mempool chain ID must be greater than zero")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Postgres{
		db: db, chainID: options.ChainID, chain: strconv.FormatUint(options.ChainID, 10),
		enabled: options.Enabled, now: options.Now,
	}, nil
}

func (repository *Postgres) StoreSnapshot(ctx context.Context, snapshot Snapshot) (SnapshotInfo, error) {
	if err := validateSnapshotForStorage(snapshot); err != nil {
		return SnapshotInfo{}, err
	}
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("begin mempool snapshot transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := lockMempool(ctx, tx, repository.chain); err != nil {
		return SnapshotInfo{}, err
	}
	previousSnapshotID, replacementEvidence, err := repository.replacementPredecessor(ctx, tx, snapshot)
	if err != nil {
		return SnapshotInfo{}, err
	}

	var snapshotID int64
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		if len(snapshot.Transactions) < -2147483648 || len(snapshot.Transactions) > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).MempoolWriteStoreSnapshotStatement1(ctx, dbgen.MempoolWriteStoreSnapshotStatement1Params{ChainID: queryValue0, EndpointName: snapshot.Endpoint, ObservedAt: pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: snapshot.ExpiresAt, Valid: true}, TransactionCount: int32(len(snapshot.Transactions))})
		if err != nil {
			return err
		}
		snapshotID = queryRow
		return nil
	}()
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("insert mempool snapshot: %w", err)
	}

	for index, transaction := range snapshot.Transactions {
		values, err := transactionStorageValues(transaction)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("mempool transaction %d: %w", index, err)
		}
		result, err := func() (int64, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(repository.chain); err != nil {
				return 0, err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(transaction.Nonce); err != nil {
				return 0, err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(transaction.Value); err != nil {
				return 0, err
			}
			var queryValue3 pgtype.Numeric
			if err := queryValue3.Scan(transaction.Gas); err != nil {
				return 0, err
			}
			var queryValue4 pgtype.Numeric
			if nullableString(transaction.GasPrice) != nil {
				if err := queryValue4.Scan(*nullableString(transaction.GasPrice)); err != nil {
					return 0, err
				}
			}
			var queryValue5 pgtype.Numeric
			if nullableString(transaction.MaxFeePerGas) != nil {
				if err := queryValue5.Scan(*nullableString(transaction.MaxFeePerGas)); err != nil {
					return 0, err
				}
			}
			var queryValue6 pgtype.Numeric
			if nullableString(transaction.MaxPriorityFeePerGas) != nil {
				if err := queryValue6.Scan(*nullableString(transaction.MaxPriorityFeePerGas)); err != nil {
					return 0, err
				}
			}
			var queryValue7 pgtype.Numeric
			if nullableString(transaction.Type) != nil {
				if err := queryValue7.Scan(*nullableString(transaction.Type)); err != nil {
					return 0, err
				}
			}
			return dbgen.New(tx).MempoolWriteStoreSnapshotStatement2(ctx, dbgen.MempoolWriteStoreSnapshotStatement2Params{ChainID: queryValue0, TxHash: values.hash, FromAddress: values.from, ToAddress: values.to, Nonce: queryValue1, Value: queryValue2, Gas: queryValue3, GasPrice: queryValue4, MaxFeePerGas: queryValue5, MaxPriorityFeePerGas: queryValue6, TxType: queryValue7, Input: values.input, Raw: []byte(string(transaction.Raw)), FirstSeenAt: pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}, LastSeenAt: pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: snapshot.ExpiresAt, Valid: true}, LastEndpointName: snapshot.Endpoint})
		}()
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("upsert mempool transaction %d: %w", index, err)
		}
		rows := result
		if rows != 1 {
			return SnapshotInfo{}, fmt.Errorf("mempool transaction %d conflicts with an existing hash identity", index)
		}
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(repository.chain); err != nil {
				return err
			}
			return dbgen.New(tx).MempoolWriteStoreSnapshotStatement3(ctx, queryValue0, snapshotID, values.hash)
		}(); err != nil {
			return SnapshotInfo{}, fmt.Errorf("insert mempool snapshot membership %d: %w", index, err)
		}
	}

	if replacementEvidence {
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(repository.chain); err != nil {
				return err
			}
			return dbgen.New(tx).MempoolWriteStoreSnapshotStatement4(ctx, queryValue0, snapshotID, previousSnapshotID)
		}(); err != nil {
			return SnapshotInfo{}, fmt.Errorf("insert mempool replacement observations: %w", err)
		}
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(repository.chain); err != nil {
				return err
			}
			return dbgen.New(tx).MempoolWriteStoreSnapshotStatement5(ctx, pgtype.Timestamptz{Time: snapshot.ExpiresAt, Valid: true}, queryValue0, snapshotID)
		}(); err != nil {
			return SnapshotInfo{}, fmt.Errorf("extend replaced mempool transaction retention: %w", err)
		}
	}

	statusResult, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return 0, err
		}
		if len(snapshot.Transactions) < -2147483648 || len(snapshot.Transactions) > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).MempoolWriteStoreSnapshotStatement6(ctx, dbgen.MempoolWriteStoreSnapshotStatement6Params{ChainID: queryValue0, EndpointName: new(snapshot.Endpoint), LatestSnapshotID: new(snapshotID), TransactionCount: new(int32(len(snapshot.Transactions))), LastAttemptAt: pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}})
	}()
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("update complete mempool status: %w", err)
	}
	if replacementEvidence {
		rows := statusResult
		if rows != 1 {
			return SnapshotInfo{}, fmt.Errorf("%w: replacement snapshot did not become current", ErrCorruptData)
		}
	}
	markerResult, markerErr := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return 0, err
		}
		return dbgen.New(tx).MempoolWriteStoreSnapshotStatement7(ctx, new(snapshotID), queryValue0)
	}()
	if markerErr != nil {
		return SnapshotInfo{}, fmt.Errorf("record mempool snapshot write continuity: %w", markerErr)
	}
	if rows := markerResult; rows != 1 {
		return SnapshotInfo{}, fmt.Errorf("%w: mempool snapshot write continuity was not recorded", ErrCorruptData)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		return dbgen.New(tx).MempoolWriteStoreSnapshotStatement8(ctx, queryValue0, pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}, snapshotID)
	}(); err != nil {
		return SnapshotInfo{}, fmt.Errorf("expire mempool snapshots: %w", err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		return dbgen.New(tx).MempoolWriteStoreSnapshotStatement9(ctx, queryValue0, pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true})
	}(); err != nil {
		return SnapshotInfo{}, fmt.Errorf("expire mempool transactions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotInfo{}, fmt.Errorf("commit mempool snapshot: %w", err)
	}
	return SnapshotInfo{
		ID: snapshotID, Endpoint: snapshot.Endpoint, ObservedAt: snapshot.ObservedAt,
		ExpiresAt: snapshot.ExpiresAt, TransactionCount: len(snapshot.Transactions),
	}, nil
}

func (repository *Postgres) replacementPredecessor(
	ctx context.Context,
	tx pgx.Tx,
	snapshot Snapshot,
) (int64, bool, error) {
	var state string
	var endpoint pgtype.Text
	var snapshotID pgtype.Int8
	var lastSnapshotWriteID pgtype.Int8
	var lastAttempt time.Time
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MempoolReplacementPredecessorStatus(ctx, queryValue0)
		if err != nil {
			return err
		}
		state = queryRow.State
		var resultValue1 pgtype.Text
		if queryRow.EndpointName != nil {
			resultValue1 = pgtype.Text{String: *queryRow.EndpointName, Valid: true}
		}
		endpoint = resultValue1
		var resultValue3 pgtype.Int8
		if queryRow.LatestSnapshotID != nil {
			resultValue3 = pgtype.Int8{Int64: *queryRow.LatestSnapshotID, Valid: true}
		}
		snapshotID = resultValue3
		var resultValue5 pgtype.Int8
		if queryRow.LastSnapshotWriteID != nil {
			resultValue5 = pgtype.Int8{Int64: *queryRow.LastSnapshotWriteID, Valid: true}
		}
		lastSnapshotWriteID = resultValue5
		if !queryRow.LastAttemptAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.LastAttemptAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		lastAttempt = queryRow.LastAttemptAt.Time
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query replacement predecessor status: %w", err)
	}
	if state != string(StateComplete) || !endpoint.Valid || !snapshotID.Valid ||
		!lastSnapshotWriteID.Valid || lastSnapshotWriteID.Int64 != snapshotID.Int64 ||
		!snapshot.ObservedAt.After(lastAttempt) || endpoint.String != snapshot.Endpoint {
		return 0, false, nil
	}

	var previousEndpoint string
	var previousObservedAt, previousExpiresAt time.Time
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MempoolReplacementPredecessorSnapshot(ctx, queryValue0, snapshotID.Int64)
		if err != nil {
			return err
		}
		previousEndpoint = queryRow.EndpointName
		if !queryRow.ObservedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.ObservedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		previousObservedAt = queryRow.ObservedAt.Time
		if !queryRow.ExpiresAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.ExpiresAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		previousExpiresAt = queryRow.ExpiresAt.Time
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("%w: replacement predecessor snapshot is missing", ErrCorruptData)
	}
	if err != nil {
		return 0, false, fmt.Errorf("query replacement predecessor snapshot: %w", err)
	}
	if previousEndpoint != endpoint.String || !previousObservedAt.Equal(lastAttempt) {
		return 0, false, fmt.Errorf("%w: replacement predecessor status differs from its snapshot", ErrCorruptData)
	}
	if !previousExpiresAt.After(snapshot.ObservedAt) {
		return 0, false, nil
	}
	return snapshotID.Int64, true, nil
}

func (repository *Postgres) StoreFailure(ctx context.Context, failure Failure) error {
	if failure.State != StateUnavailable && failure.State != StateFailed {
		return errors.New("mempool failure state must be unavailable or failed")
	}
	if failure.Code == "" || len(failure.Code) > 64 || failure.ObservedAt.IsZero() {
		return errors.New("mempool failure code and observation time are required")
	}
	if len(failure.Endpoint) > 128 {
		return errors.New("mempool failure endpoint is too long")
	}
	failure.Message = boundedMessage(failure.Message)
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin mempool failure transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := lockMempool(ctx, tx, repository.chain); err != nil {
		return err
	}
	var endpoint *string
	if failure.Endpoint != "" {
		endpoint = new(failure.Endpoint)
	}
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		return dbgen.New(tx).MempoolWriteStoreFailureStatement1(ctx, dbgen.MempoolWriteStoreFailureStatement1Params{ChainID: queryValue0, State: string(failure.State), EndpointName: endpoint, LastAttemptAt: pgtype.Timestamptz{Time: failure.ObservedAt, Valid: true}, ErrorCode: new(failure.Code), ErrorMessage: new(failure.Message)})
	}()
	if err != nil {
		return fmt.Errorf("update failed mempool status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit mempool failure status: %w", err)
	}
	return nil
}

func (repository *Postgres) Pending(ctx context.Context, encodedCursor string, limit int) (Page, error) {
	if !repository.enabled {
		return Page{}, CapabilityError{State: StateUnavailable, Code: "feature_disabled"}
	}
	if limit <= 0 || limit > 100 {
		return Page{}, fmt.Errorf("pending transaction limit %d is outside 1..100", limit)
	}
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, fmt.Errorf("begin stable mempool query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	status, err := repository.readStatus(ctx, tx)
	if err != nil {
		return Page{}, err
	}
	if status.state != StateComplete {
		return Page{}, CapabilityError{State: status.state, Code: status.errorCode, LastAttemptAt: status.lastAttemptAt}
	}

	snapshotID := status.snapshotID
	var boundary *pendingCursor
	if encodedCursor != "" {
		cursor, err := decodePendingCursor(encodedCursor)
		if err != nil || cursor.ChainID != repository.chain || cursor.SnapshotID <= 0 {
			return Page{}, ErrInvalidCursor
		}
		if _, err := ethrpc.ParseHash(cursor.BeforeHash); err != nil || cursor.BeforeFirstSeen.IsZero() {
			return Page{}, ErrInvalidCursor
		}
		snapshotID = cursor.SnapshotID
		boundary = &cursor
	}
	snapshot, err := repository.readSnapshot(ctx, tx, snapshotID)
	if err != nil {
		if (errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errSnapshotExpired)) && encodedCursor != "" {
			return Page{}, ErrInvalidCursor
		}
		if errors.Is(err, errSnapshotExpired) {
			return Page{}, CapabilityError{
				State: StateUnavailable, Code: "snapshot_expired", LastAttemptAt: status.lastAttemptAt,
			}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Page{}, fmt.Errorf("%w: latest mempool snapshot is missing", ErrCorruptData)
		}
		return Page{}, err
	}
	rows, err := repository.pendingRows(ctx, tx, snapshot, boundary, limit+1)
	if err != nil {
		return Page{}, err
	}
	items := make([]Transaction, 0, len(rows))
	for _, row := range rows {
		transaction, err := repository.decodePending(row, snapshot)
		if err != nil {
			return Page{}, err
		}
		items = append(items, transaction)
	}

	if err := tx.Commit(ctx); err != nil {
		return Page{}, fmt.Errorf("commit stable mempool query: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := Page{Items: items, Snapshot: snapshot}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor, err = encodePendingCursor(pendingCursor{
			Version: cursorVersion, ChainID: repository.chain, SnapshotID: snapshot.ID,
			BeforeFirstSeen: last.FirstSeenAt, BeforeHash: last.Hash,
		})
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func (repository *Postgres) Lookup(ctx context.Context, value string) (Detail, error) {
	if !repository.enabled {
		return Detail{}, CapabilityError{State: StateUnavailable, Code: "feature_disabled"}
	}
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return Detail{}, errors.New("invalid mempool transaction hash")
	}
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Detail{}, fmt.Errorf("begin mempool detail query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	status, err := repository.readStatus(ctx, tx)
	if err != nil {
		return Detail{}, err
	}

	var currentCapability *CapabilityError
	if status.state == StateComplete {
		snapshot, snapshotErr := repository.readSnapshot(ctx, tx, status.snapshotID)
		switch {
		case snapshotErr == nil:
			transaction, lookupErr := repository.lookupPending(ctx, tx, snapshot, hash.Bytes())
			if lookupErr == nil {
				if err := tx.Commit(ctx); err != nil {
					return Detail{}, fmt.Errorf("commit pending transaction detail query: %w", err)
				}
				return Detail{Kind: DetailPending, Transaction: transaction}, nil
			}
			if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return Detail{}, lookupErr
			}
		case errors.Is(snapshotErr, errSnapshotExpired):
			currentCapability = &CapabilityError{
				State: StateUnavailable, Code: "snapshot_expired", LastAttemptAt: status.lastAttemptAt,
			}
		case errors.Is(snapshotErr, pgx.ErrNoRows):
			return Detail{}, fmt.Errorf("%w: latest mempool snapshot is missing", ErrCorruptData)
		default:
			return Detail{}, snapshotErr
		}
	} else {
		currentCapability = &CapabilityError{
			State: status.state, Code: status.errorCode, LastAttemptAt: status.lastAttemptAt,
		}
	}

	replaced, err := repository.lookupReplaced(ctx, tx, hash.Bytes())
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return Detail{}, fmt.Errorf("commit replaced transaction detail query: %w", commitErr)
		}
		return replaced, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, err
	}
	if currentCapability != nil {
		return Detail{}, *currentCapability
	}
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, fmt.Errorf("commit absent mempool transaction detail query: %w", err)
	}
	return Detail{}, ErrNotFound
}

func (repository *Postgres) lookupPending(
	ctx context.Context,
	tx pgx.Tx,
	snapshot SnapshotInfo,
	hash []byte,
) (Transaction, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(repository.chain); err != nil {
		return Transaction{}, err
	}
	row, err := dbgen.New(repository.db).WithTx(tx).MempoolLookupPending(ctx, dbgen.MempoolLookupPendingParams{ChainID: chain, SnapshotID: snapshot.ID, TxHash: hash, ObservedAt: pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: repository.now().UTC(), Valid: true}})
	if err != nil {
		return Transaction{}, err
	}
	return repository.decodePending(dbgen.MempoolListPendingFirstRow(row), snapshot)
}

func (repository *Postgres) lookupReplaced(ctx context.Context, tx pgx.Tx, hash []byte) (Detail, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(repository.chain); err != nil {
		return Detail{}, err
	}
	row, err := dbgen.New(repository.db).WithTx(tx).MempoolLookupReplaced(ctx, pgtype.Timestamptz{Time: repository.now().UTC(), Valid: true}, chain, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, pgx.ErrNoRows
	}
	if err != nil {
		return Detail{}, fmt.Errorf("query replaced mempool transaction: %w", err)
	}
	for _, value := range []pgtype.Timestamptz{row.ExpiresAt, row.ObservedAt, row.ExpiresAt_2} {
		if !value.Valid || value.InfinityModifier != pgtype.Finite {
			return Detail{}, ErrCorruptData
		}
	}
	if row.ExpiresAt.Time.Before(row.ExpiresAt_2.Time) {
		return Detail{}, fmt.Errorf("%w: replaced transaction retention is shorter than its evidence", ErrCorruptData)
	}
	transaction, err := repository.decodePending(dbgen.MempoolListPendingFirstRow{TxHash: row.TxHash, FromAddress: row.FromAddress, ToAddress: row.ToAddress, PendingNonce: row.PendingNonce, PendingValue: row.PendingValue, PendingGas: row.PendingGas, GasPrice: row.GasPrice, MaxFeePerGas: row.MaxFeePerGas, MaxPriorityFeePerGas: row.MaxPriorityFeePerGas, TxType: row.TxType, Input: row.Input, Raw: row.Raw, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt, ReplacedHash: row.ReplacedHash, ExpiresAt: row.ExpiresAt_2}, SnapshotInfo{Endpoint: row.EndpointName, ObservedAt: row.LastSeenAt.Time.UTC(), ExpiresAt: row.ExpiresAt_2.Time.UTC()})
	if err != nil {
		return Detail{}, err
	}
	replacement, err := fixedHash(row.ReplacementHash)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Kind: DetailReplaced, Transaction: transaction, ReplacementHash: replacement, ReplacedAt: row.ObservedAt.Time.UTC()}, nil
}

type statusRecord struct {
	state         State
	snapshotID    int64
	errorCode     string
	lastAttemptAt time.Time
}

func (repository *Postgres) readStatus(ctx context.Context, tx pgx.Tx) (statusRecord, error) {
	var state string
	var snapshotID pgtype.Int8
	var errorCode pgtype.Text
	var lastAttempt time.Time
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MempoolReadStatus(ctx, queryValue0)
		if err != nil {
			return err
		}
		state = queryRow.State
		var resultValue1 pgtype.Int8
		if queryRow.LatestSnapshotID != nil {
			resultValue1 = pgtype.Int8{Int64: *queryRow.LatestSnapshotID, Valid: true}
		}
		snapshotID = resultValue1
		var resultValue3 pgtype.Text
		if queryRow.ErrorCode != nil {
			resultValue3 = pgtype.Text{String: *queryRow.ErrorCode, Valid: true}
		}
		errorCode = resultValue3
		if !queryRow.LastAttemptAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.LastAttemptAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		lastAttempt = queryRow.LastAttemptAt.Time
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return statusRecord{state: StatePending, errorCode: "not_observed"}, nil
	}
	if err != nil {
		return statusRecord{}, fmt.Errorf("query mempool capability status: %w", err)
	}
	parsed := State(state)
	if parsed != StatePending && parsed != StateComplete && parsed != StateUnavailable && parsed != StateFailed {
		return statusRecord{}, fmt.Errorf("%w: invalid mempool state %q", ErrCorruptData, state)
	}
	if parsed == StateComplete && !snapshotID.Valid {
		return statusRecord{}, fmt.Errorf("%w: complete mempool status has no snapshot", ErrCorruptData)
	}
	if (parsed == StateUnavailable || parsed == StateFailed) && !errorCode.Valid {
		return statusRecord{}, fmt.Errorf("%w: failed mempool status has no error code", ErrCorruptData)
	}
	return statusRecord{
		state: parsed, snapshotID: snapshotID.Int64, errorCode: errorCode.String,
		lastAttemptAt: lastAttempt.UTC(),
	}, nil
}

func (repository *Postgres) readSnapshot(ctx context.Context, tx pgx.Tx, snapshotID int64) (SnapshotInfo, error) {
	var snapshot SnapshotInfo
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MempoolReadSnapshot(ctx, queryValue0, snapshotID)
		if err != nil {
			return err
		}
		snapshot.ID = queryRow.ID
		snapshot.Endpoint = queryRow.EndpointName
		if !queryRow.ObservedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.ObservedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		snapshot.ObservedAt = queryRow.ObservedAt.Time
		if !queryRow.ExpiresAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.ExpiresAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		snapshot.ExpiresAt = queryRow.ExpiresAt.Time
		snapshot.TransactionCount = int(queryRow.TransactionCount)
		return nil
	}()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotInfo{}, pgx.ErrNoRows
		}
		return SnapshotInfo{}, fmt.Errorf("query mempool snapshot: %w", err)
	}
	if snapshot.ID <= 0 || snapshot.Endpoint == "" || snapshot.TransactionCount < 0 || !snapshot.ExpiresAt.After(snapshot.ObservedAt) {
		return SnapshotInfo{}, fmt.Errorf("%w: invalid mempool snapshot metadata", ErrCorruptData)
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	snapshot.ExpiresAt = snapshot.ExpiresAt.UTC()
	if !snapshot.ExpiresAt.After(repository.now().UTC()) {
		return SnapshotInfo{}, errSnapshotExpired
	}
	return snapshot, nil
}

func (repository *Postgres) pendingRows(ctx context.Context, tx pgx.Tx, snapshot SnapshotInfo, cursor *pendingCursor, limit int) ([]dbgen.MempoolListPendingFirstRow, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(repository.chain); err != nil {
		return nil, err
	}
	queries := dbgen.New(repository.db).WithTx(tx)
	observed := pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}
	now := pgtype.Timestamptz{Time: repository.now().UTC(), Valid: true}
	if cursor == nil {
		return queries.MempoolListPendingFirst(ctx, dbgen.MempoolListPendingFirstParams{ChainID: chain, SnapshotID: snapshot.ID, ObservedAt: observed, ExpiresAt: now, Limit: int32(limit)})
	}
	hash, err := ethrpc.ParseHash(cursor.BeforeHash)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	page, err := queries.MempoolListPendingAfter(ctx, dbgen.MempoolListPendingAfterParams{ChainID: chain, SnapshotID: snapshot.ID, ObservedAt: observed, ExpiresAt: now, CursorFirstSeenAt: pgtype.Timestamptz{Time: cursor.BeforeFirstSeen, Valid: true}, CursorTxHash: hash.Bytes(), Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("query pending transaction page: %w", err)
	}
	rows := make([]dbgen.MempoolListPendingFirstRow, len(page))
	for index, row := range page {
		rows[index] = dbgen.MempoolListPendingFirstRow(row)
	}
	return rows, nil
}

func (repository *Postgres) decodePending(row dbgen.MempoolListPendingFirstRow, snapshot SnapshotInfo) (Transaction, error) {
	for _, value := range []pgtype.Timestamptz{row.FirstSeenAt, row.LastSeenAt, row.ExpiresAt} {
		if !value.Valid || value.InfinityModifier != pgtype.Finite {
			return Transaction{}, ErrCorruptData
		}
	}
	gasPrice, err := dbaccess.NumericText(row.GasPrice)
	if err != nil {
		return Transaction{}, err
	}
	maxFee, err := dbaccess.NumericText(row.MaxFeePerGas)
	if err != nil {
		return Transaction{}, err
	}
	priorityFee, err := dbaccess.NumericText(row.MaxPriorityFeePerGas)
	if err != nil {
		return Transaction{}, err
	}
	txType, err := dbaccess.NumericText(row.TxType)
	if err != nil {
		return Transaction{}, err
	}
	transaction, err := repository.decodeStoredTransaction(row.TxHash, row.FromAddress, row.ToAddress, row.Input, row.Raw, row.PendingNonce, row.PendingValue, row.PendingGas, gasPrice, maxFee, priorityFee, txType, row.FirstSeenAt.Time.UTC(), row.LastSeenAt.Time.UTC(), row.ExpiresAt.Time.UTC(), snapshot)
	if err != nil {
		return Transaction{}, err
	}
	// Global retention may advance after this immutable membership snapshot.
	transaction.LastSeenAt = snapshot.ObservedAt
	transaction.ExpiresAt = snapshot.ExpiresAt
	if row.ReplacedHash != nil {
		hash, err := fixedHash(row.ReplacedHash)
		if err != nil {
			return Transaction{}, err
		}
		transaction.ReplacesHash = &hash
	}
	return transaction, nil
}

func (repository *Postgres) decodeStoredTransaction(
	hashBytes, fromBytes, toBytes, inputBytes, raw []byte,
	nonce, value, gas string,
	gasPrice, maxFee, priorityFee, txType pgtype.Text,
	firstSeen, lastSeen, expires time.Time,
	snapshot SnapshotInfo,
) (Transaction, error) {
	hash, err := fixedHash(hashBytes)
	if err != nil {
		return Transaction{}, err
	}
	from, err := fixedAddress(fromBytes)
	if err != nil {
		return Transaction{}, err
	}
	var to *string
	if toBytes != nil {
		value, err := fixedAddress(toBytes)
		if err != nil {
			return Transaction{}, err
		}
		to = &value
	}
	for _, quantity := range []string{nonce, value, gas} {
		if !canonicalDecimal(quantity) {
			return Transaction{}, fmt.Errorf("%w: invalid pending quantity", ErrCorruptData)
		}
	}
	for _, quantity := range []pgtype.Text{gasPrice, maxFee, priorityFee, txType} {
		if quantity.Valid && !canonicalDecimal(quantity.String) {
			return Transaction{}, fmt.Errorf("%w: invalid optional pending quantity", ErrCorruptData)
		}
	}
	if firstSeen.IsZero() || lastSeen.Before(firstSeen) || !expires.After(lastSeen) {
		return Transaction{}, fmt.Errorf("%w: invalid pending observation timestamps", ErrCorruptData)
	}
	input := hexutil.Encode(inputBytes)
	normalized, err := pendingTransaction(raw, repository.chainID, snapshot.Endpoint, firstSeen, snapshot.ExpiresAt)
	if err != nil {
		return Transaction{}, fmt.Errorf("%w: %v", ErrCorruptData, err)
	}
	if !strings.EqualFold(normalized.Hash, hash) || normalized.From != from || !equalOptionalString(normalized.To, to) ||
		normalized.Nonce != nonce || normalized.Value != value || normalized.Gas != gas || normalized.Input != input ||
		!equalOptionalNull(normalized.GasPrice, gasPrice) || !equalOptionalNull(normalized.MaxFeePerGas, maxFee) ||
		!equalOptionalNull(normalized.MaxPriorityFeePerGas, priorityFee) || !equalOptionalNull(normalized.Type, txType) {
		return Transaction{}, fmt.Errorf("%w: raw pending transaction differs from indexed fields", ErrCorruptData)
	}
	normalized.Raw = append(json.RawMessage(nil), raw...)
	normalized.FirstSeenAt = firstSeen
	normalized.LastSeenAt = lastSeen
	normalized.ExpiresAt = expires
	return normalized, nil
}

type transactionValues struct {
	hash  []byte
	from  []byte
	to    []byte
	input []byte
}

func transactionStorageValues(transaction Transaction) (transactionValues, error) {
	hash, err := ethrpc.ParseHash(transaction.Hash)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction hash")
	}
	hashBytes := hash.Bytes()
	from, err := ethrpc.ParseAddress(transaction.From)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction sender")
	}
	fromBytes := from.Bytes()
	var toBytes []byte
	if transaction.To != nil {
		to, err := ethrpc.ParseAddress(*transaction.To)
		if err != nil {
			return transactionValues{}, errors.New("invalid transaction recipient")
		}
		toBytes = to.Bytes()
	}
	input, err := ethrpc.ParseData(transaction.Input)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction input")
	}
	inputBytes := []byte(input)
	return transactionValues{hash: hashBytes, from: fromBytes, to: toBytes, input: inputBytes}, nil
}

func validateSnapshotForStorage(snapshot Snapshot) error {
	if snapshot.Endpoint == "" || len(snapshot.Endpoint) > 128 || snapshot.ObservedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.ObservedAt) {
		return errors.New("mempool snapshot metadata is invalid")
	}
	seen := make(map[string]struct{}, len(snapshot.Transactions))
	seenSlots := make(map[string]struct{}, len(snapshot.Transactions))
	for index, transaction := range snapshot.Transactions {
		values, err := transactionStorageValues(transaction)
		if err != nil {
			return fmt.Errorf("transaction %d: %w", index, err)
		}
		for _, quantity := range []string{transaction.Nonce, transaction.Value, transaction.Gas} {
			if !canonicalDecimal(quantity) {
				return fmt.Errorf("transaction %d has an invalid quantity", index)
			}
		}
		for _, quantity := range []*string{transaction.GasPrice, transaction.MaxFeePerGas, transaction.MaxPriorityFeePerGas, transaction.Type} {
			if quantity != nil && !canonicalDecimal(*quantity) {
				return fmt.Errorf("transaction %d has an invalid optional quantity", index)
			}
		}
		if !json.Valid(transaction.Raw) {
			return fmt.Errorf("transaction %d raw JSON is invalid", index)
		}
		key := strings.ToLower(transaction.Hash)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("transaction %d duplicates hash", index)
		}
		seen[key] = struct{}{}
		slot := string(values.from) + "\x00" + transaction.Nonce
		if _, duplicate := seenSlots[slot]; duplicate {
			return fmt.Errorf("transaction %d conflicts with another transaction in sender and nonce slot", index)
		}
		seenSlots[slot] = struct{}{}
	}
	return nil
}

func lockMempool(ctx context.Context, tx pgx.Tx, chain string) error {
	if err := dbgen.New(tx).MempoolWriteLockMempoolStatement1(ctx, new(chain)); err != nil {
		return fmt.Errorf("lock mempool snapshot state: %w", err)
	}
	return nil
}

type pendingCursor struct {
	Version         int       `json:"v"`
	ChainID         string    `json:"chain_id"`
	SnapshotID      int64     `json:"snapshot_id"`
	BeforeFirstSeen time.Time `json:"before_first_seen"`
	BeforeHash      string    `json:"before_hash"`
}

func encodePendingCursor(cursor pendingCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode mempool cursor: %w", err)
	}
	if len(encoded) > maximumCursorBytes {
		return "", ErrInvalidCursor
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePendingCursor(encoded string) (pendingCursor, error) {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maximumCursorBytes) {
		return pendingCursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maximumCursorBytes {
		return pendingCursor{}, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor pendingCursor
	if err := decoder.Decode(&cursor); err != nil {
		return pendingCursor{}, ErrInvalidCursor
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return pendingCursor{}, ErrInvalidCursor
	}
	if cursor.Version != cursorVersion {
		return pendingCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func fixedHash(value []byte) (string, error) {
	if len(value) != common.HashLength {
		return "", fmt.Errorf("%w: pending hash has %d bytes", ErrCorruptData, len(value))
	}
	return common.BytesToHash(value).Hex(), nil
}

func fixedAddress(value []byte) (string, error) {
	if len(value) != common.AddressLength {
		return "", fmt.Errorf("%w: pending address has %d bytes", ErrCorruptData, len(value))
	}
	return checksumAddress(common.BytesToAddress(value)), nil
}

func nullableString(value *string) *string {
	if value == nil {
		return nil
	}
	return new(*value)
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalNull(value *string, stored pgtype.Text) bool {
	return value == nil && !stored.Valid || value != nil && stored.Valid && *value == stored.String
}
