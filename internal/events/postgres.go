package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const DefaultReplayLimit = 256

const (
	defaultStatusReporterLease = 10 * time.Second
	maximumStatusReporterLease = time.Hour
)

var errorCodePattern = regexp.MustCompile(`^[a-z0-9_]*$`)

type PostgresOptions struct {
	ReplayLimit int
}

type PostgresStore struct {
	db           dbaccess.Database
	chainID      string
	chainNumeric pgtype.Numeric
	replayLimit  int
}

type SyncStatus struct {
	Latest              uint64
	Indexed             uint64
	HighestCovered      uint64
	LatestKnown         bool
	IndexedKnown        bool
	HighestCoveredKnown bool
	BackfillComplete    bool
	Ready               bool
	PolledAt            time.Time
	ErrorCode           string
	ReporterID          string
	ReporterLease       time.Duration
	// SafetyHalt protects the observation for the active reporter lease. It is
	// not a permanent cluster fence; an expired reporter can be replaced.
	SafetyHalt bool
}

type statusEventPayload struct {
	Latest           *string   `json:"latest"`
	Indexed          *string   `json:"indexed"`
	HighestCovered   *string   `json:"highest_covered"`
	BackfillComplete bool      `json:"backfill_complete"`
	Ready            bool      `json:"ready"`
	PolledAt         time.Time `json:"polled_at"`
	ErrorCode        string    `json:"error_code,omitempty"`
}

func NewPostgresStore(db dbaccess.Database, chainID string, options PostgresOptions) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("runtime event database is nil")
	}
	if !validChainID(chainID) {
		return nil, errors.New("runtime event chain ID is invalid")
	}
	limit := options.ReplayLimit
	if limit <= 0 {
		limit = DefaultReplayLimit
	}
	if limit > 4096 {
		return nil, errors.New("runtime event replay limit exceeds 4096")
	}
	chainInteger, _ := new(big.Int).SetString(chainID, 10)
	return &PostgresStore{
		db: db, chainID: chainID,
		chainNumeric: pgtype.Numeric{Int: chainInteger, Valid: true},
		replayLimit:  limit,
	}, nil
}

// RecordStatus elects one reporter per chain, then atomically updates the
// split-role status snapshot and appends a replayable status event. A lagging
// non-writer returns a zero Event without changing either durable surface.
// Only a bounded, non-sensitive error code is stored.
func (s *PostgresStore) RecordStatus(ctx context.Context, status SyncStatus) (Event, error) {
	if status.ReporterID == "" {
		status.ReporterID = "legacy"
	}
	if status.ReporterLease <= 0 {
		status.ReporterLease = defaultStatusReporterLease
	}
	if err := validateSyncStatus(status); err != nil {
		return Event{}, err
	}
	if status.PolledAt.IsZero() {
		status.PolledAt = time.Now().UTC()
	} else {
		status.PolledAt = status.PolledAt.UTC()
	}
	payload := statusEventPayload{
		Latest:           nullableDecimal(status.Latest, status.LatestKnown),
		Indexed:          nullableDecimal(status.Indexed, status.IndexedKnown),
		HighestCovered:   nullableDecimal(status.HighestCovered, status.HighestCoveredKnown),
		BackfillComplete: status.BackfillComplete,
		Ready:            status.Ready, PolledAt: status.PolledAt, ErrorCode: status.ErrorCode,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode sync status event: %w", err)
	}
	if len(encoded) > maxEventPayloadBytes {
		return Event{}, errors.New("sync status event exceeds payload limit")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Event{}, fmt.Errorf("begin sync status update: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := dbgen.New(tx).EventsWriteRecordStatusStatement1(ctx, new(s.chainID)); err != nil {
		return Event{}, fmt.Errorf("lock sync status writer election: %w", err)
	}
	var reporter string
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(s.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if nullableNumber(status.Latest, status.LatestKnown) != nil {
			if err := queryValue1.Scan(*nullableNumber(status.Latest, status.LatestKnown)); err != nil {
				return err
			}
		}
		queryRow, err := dbgen.New(tx).EventsWriteRecordStatusStatement2(ctx, dbgen.EventsWriteRecordStatusStatement2Params{ChainID: queryValue0, ReporterID: status.ReporterID, ObservedLatestNumber: queryValue1, ObservedLatestKnown: status.LatestKnown, SafetyHalt: status.SafetyHalt, LastError: status.ErrorCode, LeaseMilliseconds: status.ReporterLease.Milliseconds()})
		if err != nil {
			return err
		}
		reporter = queryRow
		return nil
	}()
	if err == pgx.ErrNoRows {
		return Event{}, nil
	}
	if err != nil {
		return Event{}, fmt.Errorf("acquire sync status writer lease: %w", err)
	}
	if reporter != status.ReporterID {
		return Event{}, errors.New("sync status writer lease returned an unexpected reporter")
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(s.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if nullableNumber(status.Latest, status.LatestKnown) != nil {
			if err := queryValue1.Scan(*nullableNumber(status.Latest, status.LatestKnown)); err != nil {
				return err
			}
		}
		var queryValue2 pgtype.Numeric
		if nullableNumber(status.Indexed, status.IndexedKnown) != nil {
			if err := queryValue2.Scan(*nullableNumber(status.Indexed, status.IndexedKnown)); err != nil {
				return err
			}
		}
		var queryValue3 pgtype.Numeric
		if nullableNumber(status.HighestCovered, status.HighestCoveredKnown) != nil {
			if err := queryValue3.Scan(*nullableNumber(status.HighestCovered, status.HighestCoveredKnown)); err != nil {
				return err
			}
		}
		return dbgen.New(tx).EventsWriteRecordStatusStatement3(ctx, dbgen.EventsWriteRecordStatusStatement3Params{ChainID: queryValue0, LatestNumber: queryValue1, IndexedNumber: queryValue2, HighestCoveredNumber: queryValue3, BackfillComplete: status.BackfillComplete, Ready: status.Ready, LastPollAt: pgtype.Timestamptz{Time: status.PolledAt, Valid: true}, LastErrorCode: status.ErrorCode})
	}(); err != nil {
		return Event{}, fmt.Errorf("upsert sync runtime status: %w", err)
	}
	var id int64
	var createdAt time.Time
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(s.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EventsWriteRecordStatusStatement4(ctx, queryValue0, encoded)
		if err != nil {
			return err
		}
		id = queryRow.ID
		if !queryRow.CreatedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if queryRow.CreatedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		createdAt = queryRow.CreatedAt.Time
		return nil
	}(); err != nil {
		return Event{}, fmt.Errorf("insert sync status event: %w", err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(s.chainID); err != nil {
			return err
		}
		if s.replayLimit-1 < -2147483648 || s.replayLimit-1 > 2147483647 {
			return errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EventsWriteRecordStatusStatement5(ctx, queryValue0, int32(s.replayLimit-1))
	}(); err != nil {
		return Event{}, fmt.Errorf("prune runtime event replay window: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, fmt.Errorf("commit sync status update: %w", err)
	}
	if id <= 0 {
		return Event{}, errors.New("runtime event returned an invalid ID")
	}
	return Event{ID: uint64(id), Type: "status", Time: createdAt.UTC(), Data: encoded}, nil
}

func (s *PostgresStore) Status(ctx context.Context) (SyncStatus, bool, error) {
	var row dbgen.GetSyncRuntimeStatusRow
	err := dbaccess.WithQueries(ctx, s.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetSyncRuntimeStatus(ctx, s.chainNumeric)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncStatus{}, false, nil
	}
	if err != nil {
		return SyncStatus{}, false, fmt.Errorf("query sync runtime status: %w", err)
	}
	if !row.LastPollAt.Valid {
		return SyncStatus{}, false, errors.New("stored sync runtime status has no poll time")
	}
	status := SyncStatus{
		BackfillComplete: row.BackfillComplete, Ready: row.Ready,
		PolledAt: row.LastPollAt.Time.UTC(), ErrorCode: row.LastErrorCode,
	}
	for _, field := range []struct {
		raw   string
		name  string
		value *uint64
		known *bool
	}{
		{row.LatestNumber, "latest", &status.Latest, &status.LatestKnown},
		{row.IndexedNumber, "indexed", &status.Indexed, &status.IndexedKnown},
		{row.HighestCoveredNumber, "highest covered", &status.HighestCovered, &status.HighestCoveredKnown},
	} {
		if field.raw == "" {
			continue
		}
		value, decodeErr := strconv.ParseUint(field.raw, 10, 64)
		if decodeErr != nil {
			return SyncStatus{}, false, fmt.Errorf("stored %s block is outside uint64", field.name)
		}
		*field.value, *field.known = value, true
	}
	status.SafetyHalt = safetyHaltErrorCode(status.ErrorCode)
	if err := validateSyncStatus(status); err != nil {
		return SyncStatus{}, false, fmt.Errorf("stored sync runtime status is invalid: %w", err)
	}
	return status, true, nil
}

// Replay is the strict public-cursor path. Bounds and rows are read from one
// repeatable-read snapshot so retention cannot race cursor validation.
func (s *PostgresStore) Replay(ctx context.Context, after *uint64, limit int) ([]Event, error) {
	limit = boundedLimit(limit, s.replayLimit)
	var rows []dbgen.ListRuntimeEventsRow
	err := dbaccess.WithTransactionOptions(ctx, s.db, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(queries *dbgen.Queries) error {
		bounds, queryErr := queries.GetRuntimeEventReplayBounds(ctx, s.chainNumeric)
		if queryErr != nil {
			return fmt.Errorf("query runtime event replay bounds: %w", queryErr)
		}
		if bounds.MinimumID < 0 || bounds.MaximumID < 0 ||
			(bounds.MinimumID == 0) != (bounds.MaximumID == 0) {
			return errors.New("runtime event replay bounds are invalid")
		}
		afterID := int64(0)
		if after != nil {
			if bounds.MaximumID == 0 {
				if *after != 0 {
					return ErrFutureCursor
				}
			} else {
				if *after > uint64(bounds.MaximumID) {
					return ErrFutureCursor
				}
				oldest := uint64(bounds.MinimumID)
				if oldest > 0 && *after < oldest-1 {
					return ErrExpiredCursor
				}
			}
			afterID = int64(*after)
		}
		rows, queryErr = queries.ListRuntimeEvents(ctx, dbgen.ListRuntimeEventsParams{
			ChainID: s.chainNumeric, HasAfter: after != nil,
			AfterID: afterID, PageLimit: int32(limit),
		})
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("replay runtime events: %w", err)
	}
	return runtimeEventsFromRows(rows)
}

// Poll is used by every API replica independently. It intentionally clamps an
// old internal cursor to retained rows and performs no claim/delete operation.
func (s *PostgresStore) Poll(ctx context.Context, after uint64, limit int) ([]Event, error) {
	limit = boundedLimit(limit, s.replayLimit)
	if after > uint64(^uint64(0)>>1) {
		return nil, errors.New("runtime event poll cursor exceeds bigint")
	}
	var rows []dbgen.ListRuntimeEventsRow
	err := dbaccess.WithQueries(ctx, s.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListRuntimeEvents(ctx, dbgen.ListRuntimeEventsParams{
			ChainID: s.chainNumeric, HasAfter: true,
			AfterID: int64(after), PageLimit: int32(limit),
		})
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("poll runtime events: %w", err)
	}
	return runtimeEventsFromRows(rows)
}

func runtimeEventsFromRows(rows []dbgen.ListRuntimeEventsRow) ([]Event, error) {
	result := make([]Event, 0, len(rows))
	for _, row := range rows {
		if row.ID <= 0 || !row.CreatedAt.Valid {
			return nil, errors.New("stored runtime event ID is invalid")
		}
		event := Event{
			ID: uint64(row.ID), Type: row.EventType,
			Time: row.CreatedAt.Time.UTC(), Data: row.Payload,
		}
		if err := validateEvent(event); err != nil {
			return nil, fmt.Errorf("stored runtime event is invalid: %w", err)
		}
		if len(result) > 0 && result[len(result)-1].ID >= event.ID {
			return nil, errors.New("stored runtime events are not strictly ordered")
		}
		result = append(result, event)
	}
	return result, nil
}

func validateSyncStatus(status SyncStatus) error {
	if status.IndexedKnown && (!status.HighestCoveredKnown || status.Indexed > status.HighestCovered) {
		return errors.New("contiguous indexed status cannot exceed highest covered block")
	}
	if status.BackfillComplete && (!status.LatestKnown || !status.IndexedKnown ||
		!status.HighestCoveredKnown || status.Indexed < status.Latest) {
		return errors.New("complete backfill status must cover the known latest block")
	}
	if status.Ready && !status.BackfillComplete {
		return errors.New("ready sync status requires complete historical backfill")
	}
	if len(status.ErrorCode) > 64 || !errorCodePattern.MatchString(status.ErrorCode) {
		return errors.New("sync error code must be a bounded lowercase identifier")
	}
	if len(status.ReporterID) > 128 {
		return errors.New("sync status reporter ID exceeds 128 bytes")
	}
	if status.ReporterLease > maximumStatusReporterLease {
		return errors.New("sync status reporter lease exceeds one hour")
	}
	if status.SafetyHalt != safetyHaltErrorCode(status.ErrorCode) {
		return errors.New("sync safety halt must match a stable safety error code")
	}
	if status.SafetyHalt && status.Ready {
		return errors.New("sync safety halt cannot be ready")
	}
	return nil
}

func safetyHaltErrorCode(code string) bool {
	switch code {
	case "finalized_reorg", "reorg_too_deep", "no_common_ancestor", "source_inconsistent":
		return true
	default:
		return false
	}
}

func validChainID(value string) bool {
	parsed, ok := new(big.Int).SetString(value, 10)
	return ok && parsed.Sign() >= 0 && parsed.BitLen() <= 256 && parsed.String() == value
}

func nullableNumber(value uint64, known bool) *string {
	if !known {
		return nil
	}
	return new(strconv.FormatUint(value, 10))
}

func nullableDecimal(value uint64, known bool) *string {
	if !known {
		return nil
	}
	result := strconv.FormatUint(value, 10)
	return &result
}

func boundedLimit(requested, maximum int) int {
	if requested <= 0 || requested > maximum {
		return maximum
	}
	return requested
}
