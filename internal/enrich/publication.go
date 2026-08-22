package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"math"
	"strconv"
	"time"
)

// stagePublication binds a production processor invocation to the PostgreSQL
// queue lease that must atomically publish its successful output.
type stagePublication struct {
	queue *PostgresJobQueue
	lease Lease
}

type stagePublicationOutcome uint8

const (
	stagePublicationNone stagePublicationOutcome = iota
	stagePublicationSucceeded
	stagePublicationSuperseded
)

type stageWriter func(context.Context, *sql.Tx) (StageResult, error)

type durablePublicationIdentity struct {
	jobID      int64
	generation int64
}

func bindStagePublication(job Job, lease Lease, queue *PostgresJobQueue) Job {
	job.publication = &stagePublication{queue: queue, lease: lease}
	return job
}

// runStageTransaction preserves the direct Process(Job) fixture path while
// routing every lease-bound production success through the PostgreSQL queue's
// atomic publisher.
func runStageTransaction(
	ctx context.Context,
	db *sql.DB,
	job Job,
	writer stageWriter,
) (StageResult, error) {
	if db == nil || writer == nil {
		return StageResult{}, errors.New("stage transaction requires a database and writer")
	}
	if job.publication != nil {
		if job.publication.queue == nil || job.publication.queue.db != db {
			return StageResult{}, ErrAtomicPublicationRequired
		}
		return job.publication.queue.publishSuccess(ctx, job.publication.lease, writer)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return StageResult{}, fmt.Errorf("begin direct block stage: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := writer(ctx, tx)
	if err != nil {
		return StageResult{}, err
	}
	return commitStageResult(ctx, tx, job, result)
}

func (queue *PostgresJobQueue) publishSuccess(
	ctx context.Context,
	lease Lease,
	writer stageWriter,
) (StageResult, error) {
	if queue == nil || queue.db == nil || writer == nil {
		return StageResult{}, errors.New("atomic stage publication requires a PostgreSQL queue and writer")
	}
	identity, err := publicationIdentity(lease)
	if err != nil {
		return StageResult{}, err
	}
	tx, err := queue.db.BeginTx(ctx, nil)
	if err != nil {
		return StageResult{}, fmt.Errorf("begin atomic stage publication: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := enablePublicationProtocolTx(ctx, tx); err != nil {
		return StageResult{}, err
	}
	// Every transaction that can touch both a durable job and its publication
	// rows takes the same advisory lock before any output, marker, or job row.
	// This is the cross-process lock order shared with replay and exhaustion.
	if err := lockPublicationJobTx(ctx, tx, identity.jobID); err != nil {
		return StageResult{}, err
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichSavepointStageOutput); err != nil {
		return StageResult{}, fmt.Errorf("create atomic stage output savepoint: %w", err)
	}
	result, err := writer(ctx, tx)
	if err != nil {
		return StageResult{}, err
	}
	if err := result.validateForFinish(); err != nil {
		return StageResult{}, err
	}
	if result.State != ResultComplete {
		return StageResult{}, ErrAtomicPublicationRequired
	}
	if err := persistPublishedStageResultTx(ctx, tx, lease.Job, result, identity); err != nil {
		return StageResult{}, err
	}
	if err := persistPublishedJournalTx(ctx, tx, lease.Job, identity); err != nil {
		return StageResult{}, err
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return StageResult{}, fmt.Errorf("encode atomic stage result: %w", err)
	}

	guard := lease.heartbeat
	if guard == nil {
		guard = &leaseHeartbeatGuard{}
	}
	// Only the final job CAS and commit exclude heartbeat renewal. RPC and the
	// potentially long stage writer never hold this in-process guard or the job
	// row lock.
	guard.mu.Lock()
	defer guard.mu.Unlock()
	updated, err := tx.ExecContext(ctx, dbgen.EnrichLegacyAtomicPublishSuccess, identity.jobID, lease.Token, identity.generation, string(encodedResult),
		lease.Job.ChainID, lease.Job.Stage.Name, lease.Job.Stage.Version,
		lease.Job.BlockHash.String(), strconv.FormatUint(lease.Job.BlockNumber, 10),
	)
	if err != nil {
		return StageResult{}, fmt.Errorf("publish successful enrichment generation: %w", err)
	}
	affected, err := updated.RowsAffected()
	if err != nil {
		return StageResult{}, fmt.Errorf("read successful enrichment publication result: %w", err)
	}
	if affected == 1 {
		if err := persistDurablePublicationTx(ctx, tx, lease.Job, result, identity, string(ResultComplete)); err != nil {
			return StageResult{}, err
		}
		if err := tx.Commit(); err != nil {
			if !queue.confirmPublishedSuccess(ctx, identity) {
				return StageResult{}, fmt.Errorf("commit successful enrichment publication: %w", err)
			}
		}
		guard.finished = true
		result.publication = stagePublicationSucceeded
		return result, nil
	}

	// A newer replay request owns the next publication. Discard every write from
	// this generation, then consume the pending marker and old published state in
	// the same lease-fenced transaction. Any other CAS miss is a lost lease and
	// leaves no stage writes behind.
	if _, err := tx.ExecContext(ctx, dbgen.EnrichRollbackStageOutput); err != nil {
		return StageResult{}, fmt.Errorf("discard superseded stage output: %w", err)
	}
	consumed, err := tx.ExecContext(ctx, dbgen.EnrichLegacyAtomicConsumePendingReplay, identity.jobID, lease.Token, identity.generation,
		lease.Job.ChainID, lease.Job.Stage.Name, lease.Job.Stage.Version,
		lease.Job.BlockHash.String(), strconv.FormatUint(lease.Job.BlockNumber, 10),
	)
	if err != nil {
		return StageResult{}, fmt.Errorf("consume pending enrichment generation: %w", err)
	}
	affected, err = consumed.RowsAffected()
	if err != nil {
		return StageResult{}, fmt.Errorf("read pending enrichment consumption result: %w", err)
	}
	if affected != 1 {
		return StageResult{}, ErrLeaseLost
	}
	if err := clearStageReplayStateTx(ctx, tx, lease.Job); err != nil {
		return StageResult{}, err
	}
	if err := persistDurablePublicationTx(ctx, tx, lease.Job, result, identity, "superseded"); err != nil {
		return StageResult{}, err
	}
	if err := tx.Commit(); err != nil {
		if !queue.confirmSupersededPublication(ctx, identity) {
			return StageResult{}, fmt.Errorf("commit superseded enrichment publication: %w", err)
		}
	}
	guard.finished = true
	result.publication = stagePublicationSuperseded
	return result, nil
}

func publicationIdentity(lease Lease) (durablePublicationIdentity, error) {
	if err := lease.Job.Validate(); err != nil {
		return durablePublicationIdentity{}, err
	}
	if lease.Token == "" {
		return durablePublicationIdentity{}, errors.New("atomic stage publication lease token is empty")
	}
	id, err := strconv.ParseInt(lease.Job.ID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != lease.Job.ID {
		return durablePublicationIdentity{}, errors.New("atomic stage publication job ID must be a canonical positive BIGINT")
	}
	if lease.Job.Generation == 0 || lease.Job.Generation > uint64(math.MaxInt64) {
		return durablePublicationIdentity{}, errors.New("atomic stage publication generation is out of range")
	}
	return durablePublicationIdentity{jobID: id, generation: int64(lease.Job.Generation)}, nil
}

func persistPublishedStageResultTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	result StageResult,
	identity durablePublicationIdentity,
) error {
	details := result.Details
	if details == nil {
		details = map[string]string{}
	}
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode published stage details: %w", err)
	}
	var lastError any
	if result.Error != "" {
		lastError = result.Error
	}
	row := tx.QueryRowContext(ctx, dbgen.EnrichLegacyInsertPublishedStageResult, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		job.Stage.Name, job.Stage.Version, result.State, string(encodedDetails), lastError,
		identity.jobID, identity.generation,
	)
	var inserted int
	if err := row.Scan(&inserted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("persist lease-bound block stage result: %w", err)
	}
	return nil
}

func persistPublishedJournalTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	identity durablePublicationIdentity,
) error {
	journal, err := encodeDerivedJournal(job.Stage)
	if err != nil {
		return err
	}
	var inserted int
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacyUpsertPublishedDerivedJournal, job.ChainID, job.BlockHash[:], job.Stage.String(), derivedJournalSequence,
		string(journal), strconv.FormatUint(job.BlockNumber, 10),
		identity.jobID, identity.generation,
	).Scan(&inserted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("persist lease-bound block stage journal: %w", err)
	}
	return nil
}

func persistDurablePublicationTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	result StageResult,
	identity durablePublicationIdentity,
	state string,
) error {
	details := result.Details
	if details == nil {
		details = map[string]string{}
	}
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode durable publication details: %w", err)
	}
	var lastError any
	if state == string(ResultFailed) || state == string(ResultUnavailable) {
		lastError = result.Error
	}
	var inserted int
	err = tx.QueryRowContext(ctx, dbgen.EnrichLegacyInsertDurablePublication, identity.jobID, identity.generation,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		job.Stage.Name, job.Stage.Version, state, string(encodedDetails), lastError,
	).Scan(&inserted)
	if err != nil {
		return fmt.Errorf("persist durable stage publication: %w", err)
	}
	return nil
}

func (queue *PostgresJobQueue) confirmPublishedSuccess(ctx context.Context, identity durablePublicationIdentity) bool {
	confirmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var confirmed bool
	err := queue.db.QueryRowContext(confirmCtx, dbgen.EnrichLegacyConfirmPublishedSuccess, identity.jobID, identity.generation).Scan(&confirmed)
	return err == nil && confirmed
}

func (queue *PostgresJobQueue) confirmSupersededPublication(ctx context.Context, identity durablePublicationIdentity) bool {
	confirmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var confirmed bool
	err := queue.db.QueryRowContext(confirmCtx, dbgen.EnrichLegacyConfirmSupersededPublication, identity.jobID, identity.generation).Scan(&confirmed)
	return err == nil && confirmed
}
