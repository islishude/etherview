package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"time"
)

const DefaultReplayLimit = 256

var errorCodePattern = regexp.MustCompile(`^[a-z0-9_]*$`)

type PostgresOptions struct {
	ReplayLimit int
}

type PostgresStore struct {
	db          *sql.DB
	chainID     string
	replayLimit int
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

func NewPostgresStore(db *sql.DB, chainID string, options PostgresOptions) (*PostgresStore, error) {
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
	return &PostgresStore{db: db, chainID: chainID, replayLimit: limit}, nil
}

// RecordStatus atomically updates the split-role status snapshot and appends a
// replayable status event. Only a bounded, non-sensitive error code is stored.
func (s *PostgresStore) RecordStatus(ctx context.Context, status SyncStatus) (Event, error) {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("begin sync status update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_runtime_status (
			chain_id, latest_number, indexed_number, highest_covered_number,
			backfill_complete, ready,
			last_poll_at, last_error_code, updated_at
		) VALUES ($1::numeric, $2::numeric, $3::numeric, $4::numeric, $5, $6, $7, $8, clock_timestamp())
		ON CONFLICT (chain_id) DO UPDATE SET
			latest_number = EXCLUDED.latest_number,
			indexed_number = EXCLUDED.indexed_number,
			highest_covered_number = EXCLUDED.highest_covered_number,
			backfill_complete = EXCLUDED.backfill_complete,
			ready = EXCLUDED.ready,
			last_poll_at = EXCLUDED.last_poll_at,
			last_error_code = EXCLUDED.last_error_code,
			updated_at = clock_timestamp()`,
		s.chainID, nullableNumber(status.Latest, status.LatestKnown),
		nullableNumber(status.Indexed, status.IndexedKnown),
		nullableNumber(status.HighestCovered, status.HighestCoveredKnown),
		status.BackfillComplete, status.Ready, status.PolledAt, status.ErrorCode,
	); err != nil {
		return Event{}, fmt.Errorf("upsert sync runtime status: %w", err)
	}
	var id int64
	var createdAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES ($1::numeric, 'status', $2::jsonb)
		RETURNING id, created_at`, s.chainID, encoded).Scan(&id, &createdAt); err != nil {
		return Event{}, fmt.Errorf("insert sync status event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM runtime_events
		WHERE chain_id = $1::numeric
		  AND id < COALESCE((
			SELECT id
			FROM runtime_events
			WHERE chain_id = $1::numeric
			ORDER BY id DESC
			OFFSET $2 LIMIT 1
		  ), 0)`, s.chainID, s.replayLimit-1); err != nil {
		return Event{}, fmt.Errorf("prune runtime event replay window: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit sync status update: %w", err)
	}
	if id <= 0 {
		return Event{}, errors.New("runtime event returned an invalid ID")
	}
	return Event{ID: uint64(id), Type: "status", Time: createdAt.UTC(), Data: encoded}, nil
}

func (s *PostgresStore) Status(ctx context.Context) (SyncStatus, bool, error) {
	var latest, indexed, highest sql.NullString
	var status SyncStatus
	err := s.db.QueryRowContext(ctx, `
		SELECT latest_number::text, indexed_number::text, highest_covered_number::text,
		       backfill_complete, ready,
		       last_poll_at, last_error_code
		FROM sync_runtime_status
		WHERE chain_id = $1::numeric`, s.chainID).Scan(
		&latest, &indexed, &highest, &status.BackfillComplete,
		&status.Ready, &status.PolledAt, &status.ErrorCode,
	)
	if err == sql.ErrNoRows {
		return SyncStatus{}, false, nil
	}
	if err != nil {
		return SyncStatus{}, false, fmt.Errorf("query sync runtime status: %w", err)
	}
	var decodeErr error
	if latest.Valid {
		status.Latest, decodeErr = strconv.ParseUint(latest.String, 10, 64)
		if decodeErr != nil {
			return SyncStatus{}, false, errors.New("stored latest block is outside uint64")
		}
		status.LatestKnown = true
	}
	if indexed.Valid {
		status.Indexed, decodeErr = strconv.ParseUint(indexed.String, 10, 64)
		if decodeErr != nil {
			return SyncStatus{}, false, errors.New("stored indexed block is outside uint64")
		}
		status.IndexedKnown = true
	}
	if highest.Valid {
		status.HighestCovered, decodeErr = strconv.ParseUint(highest.String, 10, 64)
		if decodeErr != nil {
			return SyncStatus{}, false, errors.New("stored highest covered block is outside uint64")
		}
		status.HighestCoveredKnown = true
	}
	status.PolledAt = status.PolledAt.UTC()
	if err := validateSyncStatus(status); err != nil {
		return SyncStatus{}, false, fmt.Errorf("stored sync runtime status is invalid: %w", err)
	}
	return status, true, nil
}

// Replay is the strict public-cursor path. Bounds and rows are read from one
// repeatable-read snapshot so retention cannot race cursor validation.
func (s *PostgresStore) Replay(ctx context.Context, after *uint64, limit int) ([]Event, error) {
	limit = boundedLimit(limit, s.replayLimit)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin runtime event replay: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var minimum, maximum sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MIN(id), MAX(id)
		FROM runtime_events
		WHERE chain_id = $1::numeric`, s.chainID).Scan(&minimum, &maximum); err != nil {
		return nil, fmt.Errorf("query runtime event replay bounds: %w", err)
	}
	if after != nil {
		if !maximum.Valid {
			if *after != 0 {
				return nil, ErrFutureCursor
			}
		} else {
			if maximum.Int64 <= 0 || minimum.Int64 <= 0 {
				return nil, errors.New("runtime event replay bounds are invalid")
			}
			if *after > uint64(maximum.Int64) {
				return nil, ErrFutureCursor
			}
			oldest := uint64(minimum.Int64)
			if oldest > 0 && *after < oldest-1 {
				return nil, ErrExpiredCursor
			}
		}
	}
	events, err := queryReplay(ctx, tx, s.chainID, after, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit runtime event replay: %w", err)
	}
	return events, nil
}

// Poll is used by every API replica independently. It intentionally clamps an
// old internal cursor to retained rows and performs no claim/delete operation.
func (s *PostgresStore) Poll(ctx context.Context, after uint64, limit int) ([]Event, error) {
	limit = boundedLimit(limit, s.replayLimit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type, payload, created_at
		FROM runtime_events
		WHERE chain_id = $1::numeric AND id > $2
		ORDER BY id
		LIMIT $3`, s.chainID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("poll runtime events: %w", err)
	}
	return scanEvents(rows)
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryReplay(ctx context.Context, query queryer, chainID string, after *uint64, limit int) ([]Event, error) {
	var rows *sql.Rows
	var err error
	if after == nil {
		rows, err = query.QueryContext(ctx, `
			SELECT id, event_type, payload, created_at
			FROM (
				SELECT id, event_type, payload, created_at
				FROM runtime_events
				WHERE chain_id = $1::numeric
				ORDER BY id DESC
				LIMIT $2
			) recent
			ORDER BY id`, chainID, limit)
	} else {
		rows, err = query.QueryContext(ctx, `
			SELECT id, event_type, payload, created_at
			FROM runtime_events
			WHERE chain_id = $1::numeric AND id > $2
			ORDER BY id
			LIMIT $3`, chainID, *after, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query runtime event replay: %w", err)
	}
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	defer rows.Close() //nolint:errcheck
	result := make([]Event, 0)
	for rows.Next() {
		var id int64
		var event Event
		if err := rows.Scan(&id, &event.Type, &event.Data, &event.Time); err != nil {
			return nil, fmt.Errorf("scan runtime event: %w", err)
		}
		if id <= 0 {
			return nil, errors.New("stored runtime event ID is invalid")
		}
		event.ID = uint64(id)
		event.Time = event.Time.UTC()
		if err := validateEvent(event); err != nil {
			return nil, fmt.Errorf("stored runtime event is invalid: %w", err)
		}
		if len(result) > 0 && result[len(result)-1].ID >= event.ID {
			return nil, errors.New("stored runtime events are not strictly ordered")
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime events: %w", err)
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
	return nil
}

func validChainID(value string) bool {
	parsed, ok := new(big.Int).SetString(value, 10)
	return ok && parsed.Sign() >= 0 && parsed.BitLen() <= 256 && parsed.String() == value
}

func nullableNumber(value uint64, known bool) any {
	if !known {
		return nil
	}
	return strconv.FormatUint(value, 10)
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
