package enrich

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
)

var (
	ErrLeaseLost                = errors.New("enrichment job lease is no longer owned")
	ErrJobBusy                  = errors.New("enrichment job is currently leased")
	ErrStagePublicationConflict = errors.New("enrichment stage publication belongs to a foreign or newer generation")
)

const (
	DefaultJobKind               = "enrichment"
	DefaultEnrichmentMaxAttempts = uint32(10)
	maxPostgresInteger           = int64(1<<31 - 1)
	minPostgresInteger           = -maxPostgresInteger - 1
)

// ReplaySource identifies one durable reason to replay a job. Repeating the
// same source after a crash is idempotent; a new source advances the target
// generation exactly once.
type ReplaySource struct {
	Kind string
	Key  string
}

type EnqueueRequest struct {
	Kind        string
	Stage       StageID
	ChainID     string
	BlockHash   common.Hash
	BlockNumber uint64
	Payload     json.RawMessage
	Priority    int
	MaxAttempts uint32
	Replay      ReplaySource
}

type EnqueueResult struct {
	Job      Job
	Created  bool
	Replayed bool
}

type JobEnqueuer interface {
	Enqueue(context.Context, EnqueueRequest) (EnqueueResult, error)
}

// PostgresJobQueue implements durable enrichment scheduling using database/sql.
// Cross-process publication paths select candidates without a row lock, take a
// per-job advisory lock, and only then revalidate/lock the durable row. This
// single order prevents publisher/replay/reaper deadlocks.
type PostgresJobQueue struct {
	db     *sql.DB
	random io.Reader
}

func NewPostgresJobQueue(db *sql.DB) (*PostgresJobQueue, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL enrichment queue requires a database")
	}
	return &PostgresJobQueue{db: db, random: rand.Reader}, nil
}

type durableJobPayload struct {
	BlockHash   string          `json:"block_hash"`
	BlockNumber string          `json:"block_number"`
	Input       json.RawMessage `json:"input,omitempty"`
}

func (queue *PostgresJobQueue) Enqueue(ctx context.Context, request EnqueueRequest) (EnqueueResult, error) {
	if queue == nil || queue.db == nil {
		return EnqueueResult{}, errors.New("enqueue using nil PostgreSQL enrichment queue")
	}
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin enqueue enrichment job: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := queue.enqueueTx(ctx, tx, request)
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit enqueue enrichment job: %w", err)
	}
	return result, nil
}

// EnqueueTx schedules work in a caller-owned transaction. The caller must
// commit or roll back tx; this makes a durable source fact and its wake visible
// atomically.
func (queue *PostgresJobQueue) EnqueueTx(
	ctx context.Context,
	tx *sql.Tx,
	request EnqueueRequest,
) (EnqueueResult, error) {
	return queue.enqueueTx(ctx, tx, request)
}

// Requeue makes an existing immutable-block job eligible for explicit
// operator-requested replay. It never steals an active lease.
func (queue *PostgresJobQueue) Requeue(ctx context.Context, job Job) error {
	if queue == nil || queue.db == nil {
		return errors.New("requeue using nil PostgreSQL enrichment queue")
	}
	if err := job.Validate(); err != nil {
		return err
	}
	id, err := strconv.ParseInt(job.ID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != job.ID {
		return errors.New("enrichment job ID must be a positive canonical BIGINT")
	}
	idempotencyKey, err := job.IdempotencyKey()
	if err != nil {
		return err
	}
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enrichment replay: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockPublicationJobTx(ctx, tx, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, dbgen.EnrichLegacyRequeueJob, id, job.ChainID, job.Stage.Name, job.Stage.Version, idempotencyKey)
	if err != nil {
		return fmt.Errorf("requeue enrichment job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("requeue enrichment job: read affected rows: %w", err)
	}
	if affected == 1 {
		if job.Generation >= uint64(math.MaxInt64) {
			return errors.New("enrichment replay generation is out of range")
		}
		replayed := job
		replayed.Generation++
		if err := clearStageReplayStateTx(ctx, tx, replayed); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit enrichment replay: %w", err)
		}
		return nil
	}
	var status string
	if err := tx.QueryRowContext(ctx, dbgen.EnrichLegacyEnrichmentJobStatus, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("enrichment job disappeared before replay")
		}
		return fmt.Errorf("read enrichment replay status: %w", err)
	}
	if status == "leased" || status == "queued" {
		return ErrJobBusy
	}
	return errors.New("enrichment job identity changed before replay")
}

// requestDependentStageReplayTx records a replay demanded by one completed
// source-stage generation. A repeated source generation is a no-op. A leased
// target keeps its token and records pending work; its Finish/Retry transition
// consumes the pending marker without ever letting the request disappear.
func requestDependentStageReplayTx(ctx context.Context, tx *sql.Tx, source Job, dependent StageID) (bool, error) {
	return requestDependentStageReplayForKindTx(ctx, tx, source, dependent, "stage-completion")
}

func requestDependentStageReplayForKindTx(
	ctx context.Context,
	tx *sql.Tx,
	source Job,
	dependent StageID,
	sourceKind string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("request dependent replay using nil transaction")
	}
	if err := source.Validate(); err != nil {
		return false, err
	}
	if err := validateDatabaseStage(dependent); err != nil {
		return false, err
	}
	replaySource, err := replaySourceForStageKind(source, sourceKind)
	if err != nil {
		return false, err
	}
	var targetID int64
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectDependentReplayTargetID, source.ChainID, source.BlockHash.String(), dependent.Name, dependent.Version).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find dependent stage %s for replay: %w", dependent, err)
	}
	if err := lockPublicationJobTx(ctx, tx, targetID); err != nil {
		return false, err
	}
	target, status, err := scanReplayTarget(tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectReplayTargetByID, targetID))
	if err != nil {
		return false, fmt.Errorf("lock dependent stage %s for replay: %w", dependent, err)
	}
	if target.ChainID != source.ChainID || target.Stage != dependent || target.BlockHash != source.BlockHash {
		return false, errors.New("dependent enrichment replay target identity changed")
	}
	return requestLockedJobReplayTx(ctx, tx, target, status, replaySource)
}

func requestJobReplayTx(ctx context.Context, tx *sql.Tx, target Job, source ReplaySource) (bool, error) {
	if tx == nil {
		return false, errors.New("request job replay using nil transaction")
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	if err := source.validate(); err != nil {
		return false, err
	}
	id, err := strconv.ParseInt(target.ID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != target.ID {
		return false, errors.New("replay target job ID must be a positive canonical BIGINT")
	}
	if err := lockPublicationJobTx(ctx, tx, id); err != nil {
		return false, err
	}
	locked, status, err := scanReplayTarget(tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectReplayTargetByID, id))
	if err != nil {
		return false, fmt.Errorf("lock enrichment replay target: %w", err)
	}
	if locked.ChainID != target.ChainID || locked.Stage != target.Stage || locked.BlockHash != target.BlockHash || locked.BlockNumber != target.BlockNumber {
		return false, errors.New("enrichment replay target identity changed")
	}
	return requestLockedJobReplayTx(ctx, tx, locked, status, source)
}

func requestLockedJobReplayTx(ctx context.Context, tx *sql.Tx, target Job, status string, source ReplaySource) (bool, error) {
	if err := source.validate(); err != nil {
		return false, err
	}
	if status == "cancelled" {
		return false, nil
	}
	if status != "queued" && status != "leased" && status != "succeeded" && status != "failed" {
		return false, fmt.Errorf("cannot replay enrichment job in status %q", status)
	}
	if target.Generation == 0 || target.Generation >= uint64(math.MaxInt64) {
		return false, errors.New("enrichment replay generation is out of range")
	}
	nextGeneration := int64(target.Generation + 1)
	inserted, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertReplayRequest, target.ID, source.Kind, source.Key, nextGeneration)
	if err != nil {
		return false, fmt.Errorf("record enrichment replay source: %w", err)
	}
	affected, err := inserted.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read enrichment replay source result: %w", err)
	}
	if affected == 0 {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, dbgen.EnrichLegacyRequestReplayJob, target.ID, nextGeneration)
	if err != nil {
		return false, fmt.Errorf("advance enrichment replay generation: %w", err)
	}
	if err := requireSingleUpdate(result, "advance enrichment replay generation"); err != nil {
		return false, err
	}
	if status != "leased" {
		invalidated := target
		invalidated.Generation = uint64(nextGeneration)
		if err := clearStageReplayStateTx(ctx, tx, invalidated); err != nil {
			return false, err
		}
	}
	return true, nil
}

func replaySourceForStageKind(source Job, kind string) (ReplaySource, error) {
	key, err := source.IdempotencyKey()
	if err != nil {
		return ReplaySource{}, err
	}
	generation := source.Generation
	if generation == 0 {
		generation = 1
	}
	replay := ReplaySource{
		Kind: kind,
		Key:  fmt.Sprintf("%s:%s:%d", source.Stage, key, generation),
	}
	if err := replay.validate(); err != nil {
		return ReplaySource{}, err
	}
	return replay, nil
}

func clearStageReplayStateTx(ctx context.Context, tx *sql.Tx, job Job) error {
	jobID, generation, err := durableJobGeneration(job)
	if err != nil {
		return err
	}
	var resultJobID, resultGeneration sql.NullInt64
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectStageResultPublication, job.ChainID, job.BlockHash[:], job.Stage.Name, job.Stage.Version).Scan(&resultJobID, &resultGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lock enrichment stage result for replay: %w", err)
	}
	if err == nil && !replayMarkerOwned(resultJobID, resultGeneration, jobID, generation) {
		return ErrStagePublicationConflict
	}

	rows, err := tx.QueryContext(ctx, dbgen.EnrichLegacySelectStageJournalPublications, job.ChainID, job.BlockHash[:], job.Stage.String())
	if err != nil {
		return fmt.Errorf("lock enrichment stage journal for replay: %w", err)
	}
	for rows.Next() {
		var journalJobID, journalGeneration sql.NullInt64
		if err := rows.Scan(&journalJobID, &journalGeneration); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan enrichment stage journal publication: %w", err)
		}
		if !replayMarkerOwned(journalJobID, journalGeneration, jobID, generation) {
			_ = rows.Close()
			return ErrStagePublicationConflict
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate enrichment stage journal publications: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close enrichment stage journal publications: %w", err)
	}

	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyDeleteStageResult, job.ChainID, job.BlockHash[:], job.Stage.Name, job.Stage.Version); err != nil {
		return fmt.Errorf("clear enrichment stage result for replay: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyDeleteStageJournal, job.ChainID, job.BlockHash[:], job.Stage.String()); err != nil {
		return fmt.Errorf("clear enrichment stage journal for replay: %w", err)
	}
	if job.Stage == ABIStage {
		if _, err := tx.ExecContext(ctx, dbgen.EnrichClearABIReplayOutputs,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
			return fmt.Errorf("clear replayed ABI output: %w", err)
		}
	}
	if err := requestInvalidatedEvidenceDependentsTx(ctx, tx, job); err != nil {
		return err
	}
	return nil
}

// requestInvalidatedEvidenceDependentsTx withdraws downstream publications in
// the same transaction that withdraws an evidence-producing stage generation.
// Trace is a direct source for both immutable-args Clone authentication and ABI
// observations. StateDiff owns the transaction-time EIP-7702 execution-code
// evidence consumed by Trace, so withdrawing it first withdraws Trace; Trace
// then fans out to Proxy and ABI. Repeated invalidation for the same source
// generation is idempotent.
func requestInvalidatedEvidenceDependentsTx(ctx context.Context, tx *sql.Tx, source Job) error {
	var dependents []StageID
	switch source.Stage {
	case StateDiffStage:
		dependents = []StageID{TraceStage}
	case TraceStage:
		dependents = []StageID{ProxyStage, ABIStage}
	default:
		return nil
	}
	for _, dependent := range dependents {
		if _, err := requestDependentStageReplayForKindTx(
			ctx, tx, source, dependent, "stage-invalidation",
		); err != nil {
			return fmt.Errorf("invalidate %s after %s evidence withdrawal: %w", dependent, source.Stage, err)
		}
	}
	return nil
}

func replayMarkerOwned(markerJobID, markerGeneration sql.NullInt64, jobID, generation int64) bool {
	if !markerJobID.Valid && !markerGeneration.Valid {
		return true
	}
	return markerJobID.Valid && markerGeneration.Valid &&
		markerJobID.Int64 == jobID && markerGeneration.Int64 <= generation
}

func durableJobGeneration(job Job) (int64, int64, error) {
	id, err := strconv.ParseInt(job.ID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != job.ID {
		return 0, 0, errors.New("enrichment job ID must be a positive canonical BIGINT")
	}
	if job.Generation == 0 || job.Generation > uint64(math.MaxInt64) {
		return 0, 0, errors.New("enrichment job generation must be a positive BIGINT")
	}
	return id, int64(job.Generation), nil
}

func lockPublicationJobTx(ctx context.Context, tx *sql.Tx, jobID int64) error {
	if tx == nil || jobID <= 0 {
		return errors.New("publication advisory lock requires a transaction and positive job ID")
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyLockPublicationJob, jobID); err != nil {
		return fmt.Errorf("lock enrichment publication job: %w", err)
	}
	return nil
}

func enablePublicationProtocolTx(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return errors.New("publication protocol requires a transaction")
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyEnablePublicationProtocol); err != nil {
		return fmt.Errorf("enable enrichment publication protocol: %w", err)
	}
	return nil
}

func (queue *PostgresJobQueue) enqueueTx(ctx context.Context, tx *sql.Tx, request EnqueueRequest) (EnqueueResult, error) {
	if queue == nil || queue.db == nil || tx == nil {
		return EnqueueResult{}, errors.New("enqueue using nil PostgreSQL enrichment transaction")
	}
	if err := validateEnqueueRequest(request); err != nil {
		return EnqueueResult{}, err
	}
	if request.Kind == "" {
		request.Kind = DefaultJobKind
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = DefaultEnrichmentMaxAttempts
	}
	payload, err := json.Marshal(durableJobPayload{
		BlockHash:   request.BlockHash.String(),
		BlockNumber: strconv.FormatUint(request.BlockNumber, 10),
		Input:       request.Payload,
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("encode enrichment job payload: %w", err)
	}
	identity := Job{
		ID: "enqueue", Stage: request.Stage, ChainID: request.ChainID,
		BlockHash: request.BlockHash, BlockNumber: request.BlockNumber,
	}
	idempotencyKey, err := identity.IdempotencyKey()
	if err != nil {
		return EnqueueResult{}, err
	}
	// A replay of an existing job must take the per-job publication lock before
	// it takes the durable job row lock or clears stage markers. Avoid entering
	// INSERT .. ON CONFLICT first: PostgreSQL may wait on the conflicting job
	// tuple, which would invert the publisher's advisory-lock-first order.
	if request.Replay != (ReplaySource{}) {
		existing, existingErr := scanJob(tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectExistingJob, request.ChainID, request.Kind, idempotencyKey))
		if existingErr == nil {
			replayed, replayErr := requestJobReplayTx(ctx, tx, existing, request.Replay)
			if replayErr != nil {
				return EnqueueResult{}, replayErr
			}
			return EnqueueResult{Job: existing, Replayed: replayed}, nil
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return EnqueueResult{}, fmt.Errorf("find existing enrichment replay job: %w", existingErr)
		}
	}

	row := tx.QueryRowContext(ctx, dbgen.EnrichLegacyEnqueueJob, request.ChainID,
		request.Kind,
		request.Stage.Name,
		request.Stage.Version,
		idempotencyKey,
		string(payload),
		request.Priority,
		request.MaxAttempts,
	)
	job, scanErr := scanJob(row)
	created := scanErr == nil
	if errors.Is(scanErr, sql.ErrNoRows) {
		job, scanErr = scanJob(tx.QueryRowContext(ctx, dbgen.EnrichLegacySelectExistingJob, request.ChainID, request.Kind, idempotencyKey))
	}
	if scanErr != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue enrichment job: %w", scanErr)
	}
	replayed := false
	if created && request.Replay != (ReplaySource{}) {
		jobID, generation, identityErr := durableJobGeneration(job)
		if identityErr != nil {
			return EnqueueResult{}, identityErr
		}
		inserted, insertErr := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertReplayRequest, jobID, request.Replay.Kind, request.Replay.Key, generation)
		if insertErr != nil {
			return EnqueueResult{}, fmt.Errorf("record initial enrichment replay source: %w", insertErr)
		}
		if err := requireSingleUpdate(inserted, "record initial enrichment replay source"); err != nil {
			return EnqueueResult{}, err
		}
	} else if !created && request.Replay != (ReplaySource{}) {
		replayed, err = requestJobReplayTx(ctx, tx, job, request.Replay)
		if err != nil {
			return EnqueueResult{}, err
		}
	}
	return EnqueueResult{Job: job, Created: created, Replayed: replayed}, nil
}

func validateEnqueueRequest(request EnqueueRequest) error {
	if err := validateDatabaseStage(request.Stage); err != nil {
		return err
	}
	if err := validateChainID(request.ChainID); err != nil {
		return err
	}
	if request.BlockHash == (common.Hash{}) {
		return errors.New("enrichment job block hash is zero")
	}
	if request.Kind != "" && request.Kind != DefaultJobKind {
		return fmt.Errorf("enrichment job kind must be %q", DefaultJobKind)
	}
	if int64(request.MaxAttempts) > maxPostgresInteger {
		return errors.New("enrichment job max attempts exceeds PostgreSQL INTEGER")
	}
	if int64(request.Priority) < minPostgresInteger || int64(request.Priority) > maxPostgresInteger {
		return errors.New("enrichment job priority exceeds PostgreSQL INTEGER")
	}
	if len(request.Payload) > 0 && !json.Valid(request.Payload) {
		return errors.New("enrichment job payload is not valid JSON")
	}
	if err := request.Replay.validate(); err != nil {
		return err
	}
	return nil
}

func (source ReplaySource) validate() error {
	if source == (ReplaySource{}) {
		return nil
	}
	if strings.TrimSpace(source.Kind) == "" || len(source.Kind) > 64 {
		return errors.New("enrichment replay source kind must contain between 1 and 64 bytes")
	}
	if strings.TrimSpace(source.Key) == "" || len(source.Key) > 256 {
		return errors.New("enrichment replay source key must contain between 1 and 256 bytes")
	}
	return nil
}

func validateChainID(chainID string) error {
	if chainID == "" {
		return errors.New("enrichment job chain ID is empty")
	}
	value, ok := new(big.Int).SetString(chainID, 10)
	if !ok || value.Sign() < 0 || value.String() != chainID {
		return errors.New("enrichment job chain ID must be a canonical non-negative decimal integer")
	}
	if len(chainID) > 78 {
		return errors.New("enrichment job chain ID exceeds NUMERIC(78,0)")
	}
	return nil
}

func validateDatabaseStage(stage StageID) error {
	if err := stage.Validate(); err != nil {
		return err
	}
	if int64(stage.Version) > maxPostgresInteger {
		return errors.New("enrichment stage version exceeds PostgreSQL INTEGER")
	}
	return nil
}

func (queue *PostgresJobQueue) Claim(ctx context.Context, workerID string, stages []StageID, leaseFor time.Duration) (Lease, bool, error) {
	if queue == nil || queue.db == nil {
		return Lease{}, false, errors.New("claim using nil PostgreSQL enrichment queue")
	}
	if strings.TrimSpace(workerID) == "" || len(workerID) > 128 {
		return Lease{}, false, errors.New("worker ID must contain between 1 and 128 bytes")
	}
	if len(stages) == 0 {
		return Lease{}, false, errors.New("claim requires at least one stage")
	}
	stageKeys, err := databaseStageKeySet(stages)
	if err != nil {
		return Lease{}, false, err
	}
	leaseMicros, err := durationMicroseconds(leaseFor)
	if err != nil {
		return Lease{}, false, fmt.Errorf("lease duration: %w", err)
	}

	// Bound each call's reaping work so an old exhausted backlog cannot starve
	// ready work. Each terminal transition is its own atomic marker+job commit.
	for range 32 {
		terminalized, terminalErr := queue.terminalizeOneExhausted(ctx, stageKeys)
		if terminalErr != nil {
			return Lease{}, false, terminalErr
		}
		if !terminalized {
			break
		}
	}

	for range 32 {
		tx, beginErr := queue.db.BeginTx(ctx, nil)
		if beginErr != nil {
			return Lease{}, false, fmt.Errorf("begin claim enrichment job: %w", beginErr)
		}
		if setErr := enablePublicationProtocolTx(ctx, tx); setErr != nil {
			_ = tx.Rollback()
			return Lease{}, false, setErr
		}
		candidate, selectErr := scanJob(tx.QueryRowContext(ctx,
			dbgen.EnrichSelectClaimCandidate,
			stageKeys, int64(ProxyStage.Version),
		))
		if selectErr != nil {
			_ = tx.Rollback()
			if errors.Is(selectErr, sql.ErrNoRows) {
				return Lease{}, false, nil
			}
			return Lease{}, false, fmt.Errorf("select enrichment claim candidate: %w", selectErr)
		}
		candidateID, _, identityErr := durableJobGeneration(candidate)
		if identityErr != nil {
			_ = tx.Rollback()
			return Lease{}, false, identityErr
		}
		if lockErr := lockPublicationJobTx(ctx, tx, candidateID); lockErr != nil {
			_ = tx.Rollback()
			return Lease{}, false, lockErr
		}
		token, tokenErr := randomLeaseToken(queue.random)
		if tokenErr != nil {
			_ = tx.Rollback()
			return Lease{}, false, fmt.Errorf("generate lease token: %w", tokenErr)
		}
		arguments := []any{
			workerID, token, leaseMicros, candidateID,
			candidate.ChainID, candidate.Stage.Name, candidate.Stage.Version,
			candidate.BlockHash.String(), strconv.FormatUint(candidate.BlockNumber, 10),
		}
		arguments = append(arguments, stageKeys, int64(ProxyStage.Version))
		job, claimErr := scanJob(tx.QueryRowContext(ctx, dbgen.EnrichClaimCandidate, arguments...))
		if errors.Is(claimErr, sql.ErrNoRows) {
			_ = tx.Rollback()
			continue
		}
		if claimErr != nil {
			_ = tx.Rollback()
			return Lease{}, false, fmt.Errorf("claim enrichment job: %w", claimErr)
		}
		// A replay requested while the previous generation was leased leaves that
		// owner's state until generation handoff. Cleanup is guarded against any
		// foreign/newer marker and shares the claim transaction.
		if job.Generation > 1 && job.Attempt == 1 {
			if clearErr := clearStageReplayStateTx(ctx, tx, job); clearErr != nil {
				_ = tx.Rollback()
				return Lease{}, false, clearErr
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return Lease{}, false, fmt.Errorf("commit claimed enrichment job: %w", commitErr)
		}
		return Lease{Job: job, Token: token}, true, nil
	}
	// Another replica repeatedly won the same no-lock candidate. Treat that as
	// transient idle work, never as a role-fatal queue failure.
	return Lease{}, false, nil
}

func (queue *PostgresJobQueue) terminalizeOneExhausted(
	ctx context.Context,
	stageKeys string,
) (bool, error) {
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin exhausted enrichment transition: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := enablePublicationProtocolTx(ctx, tx); err != nil {
		return false, err
	}
	var candidateID int64
	if err := tx.QueryRowContext(ctx, dbgen.EnrichSelectExhaustedCandidate,
		stageKeys, int64(ProxyStage.Version),
	).Scan(&candidateID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("select exhausted enrichment job: %w", err)
	}
	if err := lockPublicationJobTx(ctx, tx, candidateID); err != nil {
		return false, err
	}
	job, reason, err := scanExhaustedJob(tx.QueryRowContext(ctx,
		dbgen.EnrichLockExhaustedJob,
		candidateID, stageKeys, int64(ProxyStage.Version),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock exhausted enrichment job: %w", err)
	}
	identity, err := durableIdentityForJob(job)
	if err != nil {
		return false, err
	}
	result := StageResult{State: ResultFailed, Error: reason}
	if isKnownDerivedStage(job.Stage) {
		if err := clearStageReplayStateTx(ctx, tx, job); err != nil {
			return false, err
		}
		if err := persistPublishedStageResultTx(ctx, tx, job, result, identity); err != nil {
			return false, err
		}
		if err := persistDurablePublicationTx(ctx, tx, job, result, identity, string(ResultFailed)); err != nil {
			return false, err
		}
	} else if err := persistPublishedStageResultTx(ctx, tx, job, result, identity); err != nil {
		return false, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("encode exhausted enrichment result: %w", err)
	}
	updated, err := tx.ExecContext(ctx, dbgen.EnrichLegacyTerminalizeExhaustedJob, candidateID, job.Generation, string(encoded), reason,
		job.ChainID, job.Stage.Name, job.Stage.Version,
		job.BlockHash.String(), strconv.FormatUint(job.BlockNumber, 10),
	)
	if err != nil {
		return false, fmt.Errorf("terminalize exhausted enrichment job: %w", err)
	}
	if err := requireSingleUpdate(updated, "terminalize exhausted enrichment job"); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit exhausted enrichment job: %w", err)
	}
	return true, nil
}

func durableIdentityForJob(job Job) (durablePublicationIdentity, error) {
	jobID, generation, err := durableJobGeneration(job)
	if err != nil {
		return durablePublicationIdentity{}, err
	}
	return durablePublicationIdentity{jobID: jobID, generation: generation}, nil
}

func (queue *PostgresJobQueue) Renew(ctx context.Context, lease Lease, leaseFor time.Duration) error {
	if queue == nil || queue.db == nil {
		return errors.New("renew using nil PostgreSQL enrichment queue")
	}
	identity, err := publicationIdentity(lease)
	if err != nil {
		return err
	}
	leaseMicros, err := durationMicroseconds(leaseFor)
	if err != nil {
		return fmt.Errorf("lease duration: %w", err)
	}
	result, err := queue.db.ExecContext(ctx, dbgen.EnrichLegacyRenewJob, identity.jobID, lease.Token, leaseMicros, identity.generation,
		lease.Job.ChainID, lease.Job.Stage.Name, lease.Job.Stage.Version,
		lease.Job.BlockHash.String(), strconv.FormatUint(lease.Job.BlockNumber, 10),
	)
	if err != nil {
		return fmt.Errorf("renew enrichment job: %w", err)
	}
	return requireLeaseUpdate(result)
}

func (queue *PostgresJobQueue) Finish(ctx context.Context, lease Lease, stageResult StageResult) error {
	if queue == nil || queue.db == nil {
		return errors.New("finish using nil PostgreSQL enrichment queue")
	}
	identity, err := publicationIdentity(lease)
	if err != nil {
		return err
	}
	if err := stageResult.validateForFinish(); err != nil {
		return err
	}
	derived := isKnownDerivedStage(lease.Job.Stage)
	if derived && stageResult.State == ResultComplete {
		return ErrAtomicPublicationRequired
	}
	status := "succeeded"
	var lastError any
	if stageResult.State != ResultComplete {
		status = "failed"
		lastError = stageResult.Error
	}
	encodedResult, err := json.Marshal(stageResult)
	if err != nil {
		return fmt.Errorf("encode enrichment job result: %w", err)
	}
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish enrichment job: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := enablePublicationProtocolTx(ctx, tx); err != nil {
		return err
	}
	if err := lockPublicationJobTx(ctx, tx, identity.jobID); err != nil {
		return err
	}
	var replayPending bool
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacyFinishJob, identity.jobID, lease.Token, status, string(encodedResult), lastError, identity.generation,
		lease.Job.ChainID, lease.Job.Stage.Name, lease.Job.Stage.Version,
		lease.Job.BlockHash.String(), strconv.FormatUint(lease.Job.BlockNumber, 10),
	).Scan(&replayPending)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("finish enrichment job: %w", err)
	}
	if replayPending {
		if err := clearStageReplayStateTx(ctx, tx, lease.Job); err != nil {
			return err
		}
		if derived {
			if err := persistDurablePublicationTx(ctx, tx, lease.Job, stageResult, identity, "superseded"); err != nil {
				return err
			}
		}
	} else if derived {
		if err := persistPublishedStageResultTx(ctx, tx, lease.Job, stageResult, identity); err != nil {
			return err
		}
		if err := persistDurablePublicationTx(ctx, tx, lease.Job, stageResult, identity, string(stageResult.State)); err != nil {
			return err
		}
	} else {
		if err := persistPublishedStageResultTx(ctx, tx, lease.Job, stageResult, identity); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finished enrichment job: %w", err)
	}
	return nil
}

func (queue *PostgresJobQueue) Retry(ctx context.Context, lease Lease, retry Retry) error {
	if queue == nil || queue.db == nil {
		return errors.New("retry using nil PostgreSQL enrichment queue")
	}
	identity, err := publicationIdentity(lease)
	if err != nil {
		return err
	}
	if strings.TrimSpace(retry.Reason) == "" {
		return errors.New("retry reason is empty")
	}
	retryMicros, err := durationMicrosecondsAllowZero(retry.After)
	if err != nil {
		return fmt.Errorf("retry delay: %w", err)
	}
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retry enrichment job: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := enablePublicationProtocolTx(ctx, tx); err != nil {
		return err
	}
	if err := lockPublicationJobTx(ctx, tx, identity.jobID); err != nil {
		return err
	}
	var status string
	var replayPending bool
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacyRetryJob, identity.jobID, lease.Token, retry.Reason, retryMicros, identity.generation,
		lease.Job.ChainID, lease.Job.Stage.Name, lease.Job.Stage.Version,
		lease.Job.BlockHash.String(), strconv.FormatUint(lease.Job.BlockNumber, 10),
	).Scan(&status, &replayPending)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("retry enrichment job: %w", err)
	}
	if replayPending {
		if err := clearStageReplayStateTx(ctx, tx, lease.Job); err != nil {
			return err
		}
		if isKnownDerivedStage(lease.Job.Stage) {
			if err := persistDurablePublicationTx(ctx, tx, lease.Job, StageResult{State: ResultComplete}, identity, "superseded"); err != nil {
				return err
			}
		}
	} else if status == "failed" {
		result := StageResult{State: ResultFailed, Error: retry.Reason}
		if isKnownDerivedStage(lease.Job.Stage) {
			if err := persistPublishedStageResultTx(ctx, tx, lease.Job, result, identity); err != nil {
				return err
			}
			if err := persistDurablePublicationTx(ctx, tx, lease.Job, result, identity, string(ResultFailed)); err != nil {
				return err
			}
		} else if err := persistPublishedStageResultTx(ctx, tx, lease.Job, result, identity); err != nil {
			return err
		}
	} else if status != "queued" {
		return fmt.Errorf("retry enrichment job returned invalid status %q", status)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enrichment job retry: %w", err)
	}
	return nil
}

func durationMicroseconds(duration time.Duration) (int64, error) {
	if duration <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return durationMicrosecondsAllowZero(duration)
}

func durationMicrosecondsAllowZero(duration time.Duration) (int64, error) {
	if duration < 0 {
		return 0, errors.New("duration must not be negative")
	}
	if duration == 0 {
		return 0, nil
	}
	microseconds := duration / time.Microsecond
	if duration%time.Microsecond != 0 {
		microseconds++
	}
	return int64(microseconds), nil
}

func randomLeaseToken(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("random source is nil")
	}
	random := make([]byte, 32)
	if _, err := io.ReadFull(source, random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func databaseStageKeySet(stages []StageID) (string, error) {
	seen := make(map[string]struct{}, len(stages))
	keys := make(map[string]bool, len(stages))
	for _, stage := range stages {
		if err := validateDatabaseStage(stage); err != nil {
			return "", err
		}
		if _, exists := seen[stage.String()]; exists {
			continue
		}
		seen[stage.String()] = struct{}{}
		keys[stage.String()] = true
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return "", fmt.Errorf("encode database stage keys: %w", err)
	}
	return string(encoded), nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var (
		id           int64
		chainID      string
		stageName    string
		stageVersion int64
		attempt      int64
		maxAttempts  int64
		payload      []byte
		generation   int64
	)
	if err := row.Scan(&id, &chainID, &stageName, &stageVersion, &attempt, &maxAttempts, &payload, &generation); err != nil {
		return Job{}, err
	}
	return decodeScannedJob(id, chainID, stageName, stageVersion, attempt, maxAttempts, payload, generation)
}

func scanReplayTarget(row rowScanner) (Job, string, error) {
	var (
		id           int64
		chainID      string
		stageName    string
		stageVersion int64
		attempt      int64
		maxAttempts  int64
		payload      []byte
		generation   int64
		status       string
	)
	if err := row.Scan(&id, &chainID, &stageName, &stageVersion, &attempt, &maxAttempts, &payload, &generation, &status); err != nil {
		return Job{}, "", err
	}
	job, err := decodeScannedJob(id, chainID, stageName, stageVersion, attempt, maxAttempts, payload, generation)
	return job, status, err
}

func scanExhaustedJob(row rowScanner) (Job, string, error) {
	var (
		id           int64
		chainID      string
		stageName    string
		stageVersion int64
		attempt      int64
		maxAttempts  int64
		payload      []byte
		generation   int64
		reason       string
	)
	if err := row.Scan(&id, &chainID, &stageName, &stageVersion, &attempt, &maxAttempts, &payload, &generation, &reason); err != nil {
		return Job{}, "", err
	}
	job, err := decodeScannedJob(id, chainID, stageName, stageVersion, attempt, maxAttempts, payload, generation)
	return job, reason, err
}

func decodeScannedJob(
	id int64,
	chainID, stageName string,
	stageVersion, attempt, maxAttempts int64,
	payload []byte,
	generation int64,
) (Job, error) {
	if id <= 0 || stageVersion <= 0 || stageVersion > maxPostgresInteger ||
		attempt < 0 || attempt > maxPostgresInteger || maxAttempts <= 0 ||
		maxAttempts > maxPostgresInteger || generation <= 0 {
		return Job{}, errors.New("durable job contains out-of-range identity or counters")
	}
	var decoded durableJobPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return Job{}, fmt.Errorf("decode durable job payload: %w", err)
	}
	blockHash, err := ParseWord(decoded.BlockHash)
	if err != nil {
		return Job{}, fmt.Errorf("decode durable job block hash: %w", err)
	}
	blockNumber, err := strconv.ParseUint(decoded.BlockNumber, 10, 64)
	if err != nil || strconv.FormatUint(blockNumber, 10) != decoded.BlockNumber {
		return Job{}, errors.New("decode durable job block number: not a canonical uint64")
	}
	job := Job{
		ID: strconv.FormatInt(id, 10), Stage: StageID{Name: stageName, Version: uint32(stageVersion)},
		ChainID: chainID, BlockHash: blockHash, BlockNumber: blockNumber,
		Attempt: uint32(attempt), MaxAttempts: uint32(maxAttempts), Generation: uint64(generation),
	}
	if err := validateChainID(job.ChainID); err != nil {
		return Job{}, fmt.Errorf("decode durable job: %w", err)
	}
	if err := job.Validate(); err != nil {
		return Job{}, fmt.Errorf("decode durable job: %w", err)
	}
	return job, nil
}

func requireLeaseUpdate(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read enrichment job update count: %w", err)
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}
