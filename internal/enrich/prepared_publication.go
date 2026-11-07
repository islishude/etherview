package enrich

import (
	"context"
	"errors"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PreparedPublication holds the publication fence in its caller-owned transaction.
// Prepare it before acquiring any evidence or output row locks.
type PreparedPublication struct {
	tx      pgx.Tx
	job     Job
	created bool
}

// PreparePublicationTx finds or creates a job without requesting a replay or
// changing existing generations. No caller evidence locks may precede this call.
func (queue *PostgresJobQueue) PreparePublicationTx(ctx context.Context, tx pgx.Tx, request EnqueueRequest) (*PreparedPublication, error) {
	if queue == nil || queue.db == nil || tx == nil || request.Replay != (ReplaySource{}) || (request.Kind != "" && request.Kind != DefaultJobKind) {
		return nil, errors.New("invalid publication preparation")
	}
	if err := validateEnqueueRequest(request); err != nil {
		return nil, err
	}
	if request.Kind == "" {
		request.Kind = DefaultJobKind
	}
	identity := Job{ID: "prepare", ChainID: request.ChainID, Stage: request.Stage, BlockHash: request.BlockHash, BlockNumber: request.BlockNumber}
	key, err := identity.IdempotencyKey()
	if err != nil {
		return nil, err
	}
	var chain pgtype.Numeric
	if err := chain.Scan(request.ChainID); err != nil {
		return nil, err
	}
	job, err := decodeJob(dbgen.New(tx).EnrichLegacySelectExistingJob(ctx, chain, request.Kind, key))
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		var result EnqueueResult
		result, err = queue.enqueueTx(ctx, tx, request)
		job, created = result.Job, result.Created
	}
	if err != nil {
		return nil, err
	}
	lockedJobID := job.ID
	id, _, err := durableJobGeneration(job)
	if err != nil {
		return nil, err
	}
	if err := lockPublicationJobTx(ctx, tx, id); err != nil {
		return nil, err
	}
	job, err = decodeJob(dbgen.New(tx).EnrichLegacySelectExistingJob(ctx, chain, request.Kind, key))
	if err != nil {
		return nil, err
	}
	if job.ID != lockedJobID || job.ChainID != request.ChainID || job.Stage != request.Stage || job.BlockHash != request.BlockHash || job.BlockNumber != request.BlockNumber {
		return nil, errors.New("publication preparation identity changed")
	}
	return &PreparedPublication{tx: tx, job: job, created: created}, nil
}

// ReplayPreparedTx atomically records the publication's replay source. A job
// created by preparation keeps generation one, just like an initial enqueue.
func (queue *PostgresJobQueue) ReplayPreparedTx(ctx context.Context, tx pgx.Tx, prepared *PreparedPublication, request EnqueueRequest) error {
	if prepared == nil || prepared.tx != tx || tx == nil || queue == nil || queue.db == nil || request.Replay == (ReplaySource{}) {
		return errors.New("publication preparation transaction mismatch")
	}
	if err := validateEnqueueRequest(request); err != nil {
		return err
	}
	if err := request.Replay.Validate(); err != nil {
		return err
	}
	job := prepared.job
	if job.ChainID != request.ChainID || job.Stage != request.Stage || job.BlockHash != request.BlockHash || job.BlockNumber != request.BlockNumber || (request.Kind != "" && request.Kind != DefaultJobKind) {
		return errors.New("publication replay identity mismatch")
	}
	if !prepared.created {
		_, err := requestJobReplayTx(ctx, tx, job, request.Replay)
		return err
	}
	id, generation, err := durableJobGeneration(job)
	if err != nil {
		return err
	}
	_, err = dbgen.New(tx).EnrichLegacyInsertReplayRequest(ctx, dbgen.EnrichLegacyInsertReplayRequestParams{JobID: id, SourceKind: request.Replay.Kind, SourceKey: request.Replay.Key, RequestedGeneration: generation})
	return err
}
