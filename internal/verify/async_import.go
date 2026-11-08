package verify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func localAddressJob(kind JobKind) bool { return kind == JobAddress || kind == JobSourcifyImport }

type SourceImporter interface {
	ImportV2(context.Context, uint64, VerificationTarget) (SubmissionV2, error)
}

type importInputRepository interface {
	SaveImportInput(context.Context, VerificationLease, SubmissionV2) (VerificationJob, error)
	AwaitImportCompiler(context.Context, VerificationLease) error
}

// SaveImportInput commits the first authenticated source input without changing
// the original request or deduplication identity. The job lock fences takeover.
func (repository *PostgresRepository) SaveImportInput(ctx context.Context, lease VerificationLease, input SubmissionV2) (VerificationJob, error) {
	if err := validateVerificationLease(lease); err != nil {
		return VerificationJob{}, err
	}
	if input.Kind != JobAddress {
		return VerificationJob{}, ErrSourcifyInvalidResponse
	}
	service := Service{maxInputBytes: repository.options.MaxRequestBytes}
	if err := service.prepareV2(ctx, &input); err != nil {
		return VerificationJob{}, ErrSourcifyInvalidResponse
	}
	normalizeSolidityAnalysisVersion(&input)
	payload, err := json.Marshal(input)
	if err != nil || len(payload) > repository.options.MaxRequestBytes {
		return VerificationJob{}, ErrSourcifyInvalidResponse
	}
	digest := sha256.Sum256(payload)
	var id pgtype.UUID
	if err := id.Scan(lease.Job.ID); err != nil {
		return VerificationJob{}, err
	}
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return VerificationJob{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	q := dbgen.New(tx)
	_, err = q.VerifyV2LockRunningJob(ctx, id, new(lease.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationJob{}, ErrLeaseLost
	}
	if err != nil {
		return VerificationJob{}, err
	}
	// A lock wait can outlive the statement snapshot used by the import-input
	// subquery. Read again after acquiring the job lock so a competing writer's
	// committed input is visible before deciding whether to bind it.
	current, err := q.VerifyV2GetJob(ctx, id)
	if err != nil {
		return VerificationJob{}, err
	}
	job, err := repository.decodeV2Job(current)
	if err != nil {
		return VerificationJob{}, err
	}
	if job.Kind != JobSourcifyImport || !reflect.DeepEqual(job.RequestV2.Target, input.Target) {
		return VerificationJob{}, ErrVerificationTargetInvalid
	}
	if job.RequestV2.Kind == JobSourcifyImport {
		count, err := q.VerifyImportSaveInput(ctx, dbgen.VerifyImportSaveInputParams{ID: id, Token: new(lease.Token), Request: payload, Payload: payload, Digest: digest[:]})
		if err != nil {
			return VerificationJob{}, err
		}
		if count != 1 {
			return VerificationJob{}, ErrLeaseLost
		}
		count, err = q.VerifyImportBindInput(ctx, id, new(lease.Token))
		if err != nil {
			return VerificationJob{}, err
		}
		if count != 1 {
			return VerificationJob{}, ErrLeaseLost
		}
	}
	row, err := q.VerifyV2LockRunningJob(ctx, id, new(lease.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationJob{}, ErrLeaseLost
	}
	if err != nil {
		return VerificationJob{}, err
	}
	job, err = repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
	if err != nil {
		return VerificationJob{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VerificationJob{}, err
	}
	return job, nil
}

func (repository *PostgresRepository) AwaitImportCompiler(ctx context.Context, lease VerificationLease) error {
	var id pgtype.UUID
	if err := id.Scan(lease.Job.ID); err != nil {
		return err
	}
	count, err := dbgen.New(repository.db).VerifyImportAwaitCompiler(ctx, id, new(lease.Token))
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

var errImportRetry = errors.New("source import awaits retry")

func (worker *Worker) prepareImport(ctx context.Context, lease VerificationLease) (VerificationLease, error) {
	repository, ok := worker.repository.(importInputRepository)
	if !ok || worker.options.Importer == nil {
		return lease, errors.New("source import worker is not configured")
	}
	job, err := runWithLeaseHeartbeat(ctx, worker, lease, func(ctx context.Context) (VerificationJob, error) {
		input, err := worker.options.Importer.ImportV2(ctx, lease.Job.RequestV2.SourceChainID, *lease.Job.RequestV2.Target)
		if err != nil {
			return VerificationJob{}, err
		}
		return repository.SaveImportInput(ctx, lease, input)
	})
	if err == nil {
		lease.Job = job
		availability := worker.compilerAvailability(ctx)
		language := job.RequestV2.Language
		available := (language == LanguageVyper && availability.Vyper) || ((language == LanguageSolidity || language == LanguageYul) && availability.SolcJS)
		if !available {
			if err := repository.AwaitImportCompiler(ctx, lease); err != nil {
				return lease, err
			}
			return lease, errImportFinished
		}
		return lease, nil
	}
	if ctx.Err() != nil {
		return lease, ctx.Err()
	}
	if errors.Is(err, ErrLeaseLost) {
		return lease, ErrLeaseLost
	}
	code := ErrorSourcifyUnavailable
	switch {
	case errors.Is(err, ErrSourcifyNotFound):
		code = ErrorSourcifyNotFound
	case errors.Is(err, ErrSourcifyTargetMismatch):
		code = ErrorSourcifyMismatch
	case errors.Is(err, ErrSourcifyInvalidResponse):
		code = ErrorSourcifyInvalid
	default:
		// Leave the lease to expire; no hot retry loop and takeover remains fenced.
		if lease.Job.AttemptCount < lease.Job.MaxAttempts {
			return lease, errImportRetry
		}
	}
	if err := worker.failLease(ctx, lease, code); err != nil {
		return lease, err
	}
	return lease, errImportFinished
}

var errImportFinished = errors.New("source import completed with a terminal failure")
