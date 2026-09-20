package maintenance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
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
	acquire func(context.Context) (sessionConnection, error)
}

// sessionConnection keeps query execution and lock ownership on one session.
type sessionConnection interface {
	dbgen.DBTX
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	Release()
	Discard(context.Context)
}
type nativeSession struct{ *pgxpool.Conn }

func (session nativeSession) Discard(ctx context.Context) { dbaccess.Discard(ctx, session.Conn) }

type leaseSession struct {
	mu       sync.Mutex
	owner    *PostgresRepository
	conn     sessionConnection
	request  Request
	workerID string
	key      int64
	released bool
	terminal string
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(db *pgxpool.Pool) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("maintenance PostgreSQL repository requires a database")
	}
	return &PostgresRepository{acquire: func(ctx context.Context) (sessionConnection, error) {
		conn, err := db.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		return nativeSession{Conn: conn}, nil
	}}, nil
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
	finalized      pgtype.Text
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

func queryCandidateBatch(ctx context.Context, tx pgx.Tx, cursor claimCursor) ([]claimCandidate, error) {
	requestedAt := cursor.requestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Unix(0, 0).UTC()
	}
	rows, err := func() ([]dbgen.MaintenanceLegacyClaimCandidatesRow, error) {
		if claimBatchSize < -2147483648 || claimBatchSize > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if cursor.statusRank < -2147483648 || cursor.statusRank > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).MaintenanceLegacyClaimCandidates(ctx, dbgen.MaintenanceLegacyClaimCandidatesParams{Limit: int32(claimBatchSize), HasCursor: cursor.set, CursorStatusRank: int32(cursor.statusRank), CursorRequestedAt: pgtype.Timestamptz{Time: requestedAt, Valid: true}, CursorID: cursor.id})
	}()
	if err != nil {
		return nil, fmt.Errorf("query maintenance candidates: %w", err)
	}

	candidates := make([]claimCandidate, 0, claimBatchSize)
	for _, storedRow := range rows {
		var candidate claimCandidate
		if err := func() error {
			candidate.id = storedRow.ID
			candidate.chainID = storedRow.RequestChainID
			candidate.operation = storedRow.Operation
			candidate.stage = storedRow.Stage
			candidate.fromBlock = storedRow.RequestFromBlock
			candidate.toBlock = storedRow.RequestToBlock
			candidate.allowFinalized = storedRow.AllowFinalized
			candidate.reason = storedRow.Reason
			candidate.status = storedRow.Status
			queryValue9, err := dbaccess.NumericText(storedRow.FinalizedNumber)
			if err != nil {
				return err
			}
			candidate.finalized = queryValue9
			candidate.statusRank = int64(storedRow.StatusRank)
			if !storedRow.RequestedAt.Valid {
				return errors.New("invalid stored query value")
			}
			if storedRow.RequestedAt.InfinityModifier != pgtype.Finite {
				return errors.New("invalid stored query value")
			}
			candidate.requestedAt = storedRow.RequestedAt.Time
			return nil
		}(); err != nil {
			return nil, fmt.Errorf("scan maintenance candidate: %w", err)
		}
		candidate.requestedAt = candidate.requestedAt.UTC()
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func (repository *PostgresRepository) Claim(ctx context.Context, workerID string) (Lease, bool, error) {
	if repository == nil || repository.acquire == nil {
		return Lease{}, false, errors.New("claim using nil maintenance repository")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 128 {
		return Lease{}, false, errors.New("maintenance worker ID must contain 1 to 128 bytes")
	}
	conn, err := repository.acquire(ctx)
	if err != nil {
		return Lease{}, false, fmt.Errorf("reserve maintenance lease connection: %w", err)
	}
	keepConnection := false
	uncertain := false
	defer func() {
		if !keepConnection {
			if uncertain {
				conn.Discard(ctx)
			} else {
				conn.Release()
			}
		}
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Lease{}, false, fmt.Errorf("begin maintenance claim: %w", err)
	}
	var heldKey int64
	advisoryHeld := false
	defer func() {
		dbaccess.Rollback(ctx, tx)
		if advisoryHeld {
			cleanupCtx, cancel := leaseCleanupContext(ctx)
			defer cancel()
			unlocked, err := unlockAdvisory(cleanupCtx, conn, heldKey)
			if err != nil || !unlocked {
				uncertain = true
			}
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
			if err := func() error {

				queryRow, err := dbgen.New(tx).MaintenanceLegacyTryAdvisoryLock(ctx, key)
				if err != nil {
					return err
				}
				acquired = queryRow
				return nil
			}(); err != nil {
				uncertain = true
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

			result, err := dbgen.New(tx).MaintenanceLegacyMarkRunning(ctx, request.ID)
			if err != nil {
				return Lease{}, false, fmt.Errorf("mark maintenance request running: %w", err)
			}
			if err := requireSingleRow(result, ErrLeaseLost); err != nil {
				return Lease{}, false, err
			}
			if err := tx.Commit(ctx); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
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
	var finalized pgtype.Text
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(lease.Request.ChainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(session.conn).MaintenanceLegacyCurrentFinality(ctx, lease.Request.ID, queryValue0)
		if err != nil {
			return err
		}
		status = queryRow.Status
		resultValue1, err := dbaccess.NumericText(queryRow.FinalizedNumber)
		if err != nil {
			return err
		}
		finalized = resultValue1
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	queries := dbgen.New(session.conn)
	var result int64
	if target == "failed" {
		result, err = queries.MaintenanceLegacyFailRequest(ctx, lease.Request.ID, new(lastError))
	} else {
		result, err = queries.MaintenanceLegacyCompleteRequest(ctx, lease.Request.ID)
	}
	if err != nil {
		return fmt.Errorf("mark maintenance request %s: %w", target, err)
	}
	if err := requireSingleRow(result, ErrLeaseLost); err != nil {
		return err
	}
	return session.releaseLocked(ctx, target)
}

func (repository *PostgresRepository) validateLease(lease Lease) (*leaseSession, error) {
	if repository == nil || repository.acquire == nil {
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
	if !unlocked {
		conn.Discard(ctx)
		return ErrLeaseLost
	}
	conn.Release()
	return nil
}

func finalizedViolation(request Request, finalized pgtype.Text) (error, error) {
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

func failCandidate(ctx context.Context, tx pgx.Tx, id int64, reason string) error {
	result, err := dbgen.New(tx).MaintenanceLegacyRejectCandidate(ctx, id, new(normalizeFailure(reason)))
	if err != nil {
		return fmt.Errorf("reject maintenance request: %w", err)
	}
	if err := requireSingleRow(result, ErrLeaseLost); err != nil {
		return fmt.Errorf("reject maintenance request: %w", err)
	}
	return nil
}

func unlockAdvisoryTx(ctx context.Context, tx pgx.Tx, key int64) error {
	var unlocked bool
	if err := func() error {

		queryRow, err := dbgen.New(tx).MaintenanceLegacyUnlockAdvisory(ctx, key)
		if err != nil {
			return err
		}
		unlocked = queryRow
		return nil
	}(); err != nil {
		return fmt.Errorf("release rejected maintenance advisory lease: %w", err)
	}
	if !unlocked {
		return ErrLeaseLost
	}
	return nil
}

type queryRower = dbgen.DBTX

func unlockAdvisory(ctx context.Context, queryer queryRower, key int64) (bool, error) {
	var unlocked bool
	err := func() error {

		queryRow, err := dbgen.New(queryer).MaintenanceLegacyUnlockAdvisory(ctx, key)
		if err != nil {
			return err
		}
		unlocked = queryRow
		return nil
	}()
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

func requireSingleRow(result int64, missing error) error {
	affected := result
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

func discardSQLConnection(conn sessionConnection) {
	if conn == nil {
		return
	}
	conn.Discard(context.Background())
}
