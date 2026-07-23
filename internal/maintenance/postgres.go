package maintenance

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	claimBatchSize      = 64
	maximumFailureBytes = 4096
)

// PostgresRepository uses a dedicated PostgreSQL session per active request.
// The session holds a negative-key advisory lock until completion, failure, or
// release. This supplies crash-safe lease ownership without pretending that
// repair_requests has token/expiry columns that do not exist in the schema.
type PostgresRepository struct {
	db *sql.DB
}

type leaseSession struct {
	mu       sync.Mutex
	owner    *PostgresRepository
	conn     *sql.Conn
	request  Request
	workerID string
	key      int64
	released bool
	terminal string
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(db *sql.DB) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("maintenance PostgreSQL repository requires a database")
	}
	return &PostgresRepository{db: db}, nil
}

type claimCandidate struct {
	id             int64
	chainID        string
	operation      string
	stage          string
	fromBlock      string
	toBlock        string
	allowFinalized bool
	reason         string
	status         string
	finalized      sql.NullString
	statusRank     int64
	requestedAt    time.Time
}

func (candidate claimCandidate) request() (Request, error) {
	fromBlock, err := strconv.ParseUint(candidate.fromBlock, 10, 64)
	if err != nil || strconv.FormatUint(fromBlock, 10) != candidate.fromBlock {
		return Request{}, fmt.Errorf("%w: from_block is not a canonical uint64", ErrInvalidRequest)
	}
	toBlock, err := strconv.ParseUint(candidate.toBlock, 10, 64)
	if err != nil || strconv.FormatUint(toBlock, 10) != candidate.toBlock {
		return Request{}, fmt.Errorf("%w: to_block is not a canonical uint64", ErrInvalidRequest)
	}
	request := Request{
		ID: candidate.id, ChainID: candidate.chainID,
		Operation: Operation(candidate.operation), Stage: candidate.stage,
		FromBlock: fromBlock, ToBlock: toBlock,
		AllowFinalized: candidate.allowFinalized, Reason: candidate.reason,
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	if candidate.status != "queued" && candidate.status != "running" {
		return Request{}, fmt.Errorf("%w: claim returned status %q", ErrInvalidRequest, candidate.status)
	}
	if candidate.status == "queued" && candidate.statusRank != 0 || candidate.status == "running" && candidate.statusRank != 1 {
		return Request{}, fmt.Errorf("%w: claim returned inconsistent status rank", ErrInvalidRequest)
	}
	if candidate.requestedAt.IsZero() {
		return Request{}, fmt.Errorf("%w: requested_at is zero", ErrInvalidRequest)
	}
	return request, nil
}

type claimCursor struct {
	set         bool
	statusRank  int64
	requestedAt time.Time
	id          int64
}

func queryCandidateBatch(ctx context.Context, tx *sql.Tx, cursor claimCursor) ([]claimCandidate, error) {
	requestedAt := cursor.requestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Unix(0, 0).UTC()
	}
	rows, err := tx.QueryContext(
		ctx, claimCandidatesSQL, claimBatchSize, cursor.set,
		cursor.statusRank, requestedAt, cursor.id,
	)
	if err != nil {
		return nil, fmt.Errorf("query maintenance candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	candidates := make([]claimCandidate, 0, claimBatchSize)
	for rows.Next() {
		var candidate claimCandidate
		if err := rows.Scan(
			&candidate.id, &candidate.chainID, &candidate.operation, &candidate.stage,
			&candidate.fromBlock, &candidate.toBlock, &candidate.allowFinalized,
			&candidate.reason, &candidate.status, &candidate.finalized,
			&candidate.statusRank, &candidate.requestedAt,
		); err != nil {
			return nil, fmt.Errorf("scan maintenance candidate: %w", err)
		}
		candidate.requestedAt = candidate.requestedAt.UTC()
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate maintenance candidates: %w", err)
	}
	return candidates, nil
}

func (repository *PostgresRepository) Claim(ctx context.Context, workerID string) (Lease, bool, error) {
	if repository == nil || repository.db == nil {
		return Lease{}, false, errors.New("claim using nil maintenance repository")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 128 {
		return Lease{}, false, errors.New("maintenance worker ID must contain 1 to 128 bytes")
	}
	conn, err := repository.db.Conn(ctx)
	if err != nil {
		return Lease{}, false, fmt.Errorf("reserve maintenance lease connection: %w", err)
	}
	keepConnection := false
	defer func() {
		if !keepConnection {
			_ = conn.Close()
		}
	}()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Lease{}, false, fmt.Errorf("begin maintenance claim: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var heldKey int64
	advisoryHeld := false
	defer func() {
		if advisoryHeld {
			cleanupCtx, cancel := leaseCleanupContext(ctx)
			defer cancel()
			_, _ = unlockAdvisory(cleanupCtx, conn, heldKey)
		}
	}()
	cursor := claimCursor{}
	for {
		candidates, err := queryCandidateBatch(ctx, tx, cursor)
		if err != nil {
			return Lease{}, false, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			key, err := repairAdvisoryKey(candidate.id)
			if err != nil {
				return Lease{}, false, err
			}
			var acquired bool
			if err := tx.QueryRowContext(ctx, tryAdvisoryLockSQL, key).Scan(&acquired); err != nil {
				return Lease{}, false, fmt.Errorf("acquire maintenance advisory lease: %w", err)
			}
			if !acquired {
				continue
			}
			heldKey, advisoryHeld = key, true

			request, requestErr := candidate.request()
			if requestErr != nil {
				if err := failCandidate(ctx, tx, candidate.id, "invalid persisted request: "+requestErr.Error()); err != nil {
					return Lease{}, false, err
				}
				if err := unlockAdvisoryTx(ctx, tx, key); err != nil {
					return Lease{}, false, err
				}
				advisoryHeld = false
				continue
			}
			if violation, err := finalizedViolation(request, candidate.finalized); err != nil {
				return Lease{}, false, err
			} else if violation != nil {
				if err := failCandidate(ctx, tx, request.ID, violation.Error()); err != nil {
					return Lease{}, false, err
				}
				if err := unlockAdvisoryTx(ctx, tx, key); err != nil {
					return Lease{}, false, err
				}
				advisoryHeld = false
				continue
			}

			result, err := tx.ExecContext(ctx, markRunningSQL, request.ID)
			if err != nil {
				return Lease{}, false, fmt.Errorf("mark maintenance request running: %w", err)
			}
			if err := requireSingleRow(result, ErrLeaseLost); err != nil {
				return Lease{}, false, err
			}
			if err := tx.Commit(); err != nil {
				return Lease{}, false, fmt.Errorf("commit maintenance claim: %w", err)
			}
			keepConnection = true
			advisoryHeld = false
			session := &leaseSession{
				owner: repository, conn: conn, request: request,
				workerID: workerID, key: key,
			}
			return Lease{Request: request, session: session}, true, nil
		}
		if len(candidates) < claimBatchSize {
			break
		}
		last := candidates[len(candidates)-1]
		cursor = claimCursor{
			set: true, statusRank: last.statusRank,
			requestedAt: last.requestedAt, id: last.id,
		}
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, false, fmt.Errorf("commit empty maintenance claim: %w", err)
	}
	return Lease{}, false, nil
}

func (repository *PostgresRepository) GuardFinalized(ctx context.Context, lease Lease) error {
	session, err := repository.validateLease(lease)
	if err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.released {
		return ErrLeaseLost
	}
	var status string
	var finalized sql.NullString
	err = session.conn.QueryRowContext(ctx, currentFinalitySQL, lease.Request.ID, lease.Request.ChainID).Scan(&status, &finalized)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("refresh maintenance finality guard: %w", err)
	}
	if status != "running" {
		return ErrLeaseLost
	}
	if lease.Request.AllowFinalized {
		return nil
	}
	violation, err := finalizedViolation(lease.Request, finalized)
	if err != nil {
		return err
	}
	return violation
}

func (repository *PostgresRepository) Complete(ctx context.Context, lease Lease) error {
	return repository.transition(ctx, lease, "done", "")
}

func (repository *PostgresRepository) Fail(ctx context.Context, lease Lease, cause error) error {
	if cause == nil {
		return errors.New("maintenance failure cause is nil")
	}
	return repository.transition(ctx, lease, "failed", normalizeFailure(cause.Error()))
}

func (repository *PostgresRepository) Release(ctx context.Context, lease Lease) error {
	session, err := repository.validateLease(lease)
	if err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.released {
		return nil
	}
	return session.releaseLocked(ctx, "")
}

func (repository *PostgresRepository) transition(ctx context.Context, lease Lease, target, lastError string) error {
	session, err := repository.validateLease(lease)
	if err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.released {
		if session.terminal == target {
			return nil
		}
		return ErrLeaseLost
	}
	query := completeRequestSQL
	arguments := []any{lease.Request.ID}
	if target == "failed" {
		query = failRequestSQL
		arguments = append(arguments, lastError)
	}
	result, err := session.conn.ExecContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("mark maintenance request %s: %w", target, err)
	}
	if err := requireSingleRow(result, ErrLeaseLost); err != nil {
		return err
	}
	return session.releaseLocked(ctx, target)
}

func (repository *PostgresRepository) validateLease(lease Lease) (*leaseSession, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("use nil maintenance repository")
	}
	if err := lease.Request.Validate(); err != nil {
		return nil, err
	}
	if lease.session == nil || lease.session.owner != repository || lease.session.request != lease.Request {
		return nil, ErrLeaseLost
	}
	return lease.session, nil
}

func (session *leaseSession) releaseLocked(ctx context.Context, terminal string) error {
	cleanupCtx, cancel := leaseCleanupContext(ctx)
	defer cancel()
	unlocked, err := unlockAdvisory(cleanupCtx, session.conn, session.key)
	conn := session.conn
	session.conn = nil
	session.released = true
	session.terminal = terminal
	if err != nil {
		discardSQLConnection(conn)
		return fmt.Errorf("release maintenance advisory lease: %w", err)
	}
	closeErr := conn.Close()
	if !unlocked {
		if closeErr != nil {
			return errors.Join(ErrLeaseLost, closeErr)
		}
		return ErrLeaseLost
	}
	if closeErr != nil {
		return fmt.Errorf("return maintenance lease connection: %w", closeErr)
	}
	return nil
}

func finalizedViolation(request Request, finalized sql.NullString) (error, error) {
	if request.AllowFinalized || !finalized.Valid {
		return nil, nil
	}
	height, err := strconv.ParseUint(finalized.String, 10, 64)
	if err != nil || strconv.FormatUint(height, 10) != finalized.String {
		return nil, errors.New("stored finalized height is not a canonical uint64")
	}
	if request.FromBlock > height {
		return nil, nil
	}
	return fmt.Errorf(
		"%w: request %d range %d..%d intersects finalized height %d; explicit allow_finalized is required",
		ErrFinalizedRange, request.ID, request.FromBlock, request.ToBlock, height,
	), nil
}

func failCandidate(ctx context.Context, tx *sql.Tx, id int64, reason string) error {
	result, err := tx.ExecContext(ctx, rejectCandidateSQL, id, normalizeFailure(reason))
	if err != nil {
		return fmt.Errorf("reject maintenance request: %w", err)
	}
	if err := requireSingleRow(result, ErrLeaseLost); err != nil {
		return fmt.Errorf("reject maintenance request: %w", err)
	}
	return nil
}

func unlockAdvisoryTx(ctx context.Context, tx *sql.Tx, key int64) error {
	var unlocked bool
	if err := tx.QueryRowContext(ctx, unlockAdvisorySQL, key).Scan(&unlocked); err != nil {
		return fmt.Errorf("release rejected maintenance advisory lease: %w", err)
	}
	if !unlocked {
		return ErrLeaseLost
	}
	return nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func unlockAdvisory(ctx context.Context, queryer queryRower, key int64) (bool, error) {
	var unlocked bool
	err := queryer.QueryRowContext(ctx, unlockAdvisorySQL, key).Scan(&unlocked)
	return unlocked, err
}

func repairAdvisoryKey(id int64) (int64, error) {
	if id <= 0 {
		return 0, fmt.Errorf("%w: request ID must be positive", ErrInvalidRequest)
	}
	// Generated repair IDs are positive. Mapping them into the negative int64
	// half creates a one-to-one namespace distinct from ordinary positive locks.
	return id | math.MinInt64, nil
}

func requireSingleRow(result sql.Result, missing error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read maintenance update count: %w", err)
	}
	if affected != 1 {
		return missing
	}
	return nil
}

func normalizeFailure(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if value == "" {
		value = "maintenance operation failed"
	}
	for len(value) > maximumFailureBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func leaseCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(ctx, 5*time.Second)
}

func discardSQLConnection(conn *sql.Conn) {
	if conn == nil {
		return
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

const claimCandidatesSQL = `
SELECT request.id, request.chain_id::text, request.operation, request.stage,
       request.from_block::text, request.to_block::text,
       request.allow_finalized, request.reason, request.status,
       finality.finalized_number::text,
       CASE request.status WHEN 'queued' THEN 0 ELSE 1 END AS status_rank,
       request.requested_at
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.status IN ('queued', 'running')
  AND (
      $2 = FALSE
      OR (
          CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
          request.requested_at,
          request.id
      ) > ($3::integer, $4::timestamptz, $5::bigint)
  )
ORDER BY CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
         request.requested_at, request.id
FOR UPDATE OF request SKIP LOCKED
LIMIT $1`

const tryAdvisoryLockSQL = `SELECT pg_try_advisory_lock($1)`

const unlockAdvisorySQL = `SELECT pg_advisory_unlock($1)`

const markRunningSQL = `
UPDATE repair_requests
SET status = 'running',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = NULL,
    last_error = NULL
WHERE id = $1
  AND status IN ('queued', 'running')`

const rejectCandidateSQL = `
UPDATE repair_requests
SET status = 'failed',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = clock_timestamp(),
    last_error = $2
WHERE id = $1
  AND status IN ('queued', 'running')`

const currentFinalitySQL = `
SELECT request.status, finality.finalized_number::text
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.id = $1
  AND request.chain_id = $2::numeric`

const completeRequestSQL = `
UPDATE repair_requests
SET status = 'done', completed_at = clock_timestamp(), last_error = NULL
WHERE id = $1 AND status = 'running'`

const failRequestSQL = `
UPDATE repair_requests
SET status = 'failed', completed_at = clock_timestamp(), last_error = $2
WHERE id = $1 AND status = 'running'`
