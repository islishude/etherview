package verify

import (
	"context"
	"encoding/json"
	"errors"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"time"
)

// JobSummary is the status projection. Reading it never fetches source bodies.
type JobSummary struct {
	ID                   string
	Kind                 JobKind
	Status               JobStatus
	Outcome              json.RawMessage
	ErrorCode            ErrorCode
	CreatedAt, UpdatedAt time.Time
	ChainID, Address     string
}

func (repository *PostgresRepository) JobSummary(ctx context.Context, id string) (JobSummary, bool, error) {
	var key pgtype.UUID
	if err := key.Scan(id); err != nil {
		return JobSummary{}, false, err
	}
	row, err := dbgen.New(repository.db).VerifyV2GetJobSummary(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobSummary{}, false, nil
	}
	if err != nil {
		return JobSummary{}, false, err
	}
	result := JobSummary{ID: row.ID, Kind: JobKind(row.Kind), Status: JobStatus(row.Status), Outcome: row.Outcome, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.ErrorCode != nil {
		result.ErrorCode = ErrorCode(*row.ErrorCode)
	}
	if row.SummaryChainID != nil {
		result.ChainID = *row.SummaryChainID
	}
	if row.SummaryAddress != nil {
		result.Address = *row.SummaryAddress
	}
	return result, true, nil
}

func (summary JobSummary) statusJob() VerificationJob {
	job := VerificationJob{ID: summary.ID, Kind: summary.Kind, Status: summary.Status, Outcome: summary.Outcome, ErrorCode: summary.ErrorCode, CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt}
	if summary.Address != "" {
		chainID, _ := strconv.ParseUint(summary.ChainID, 10, 64)
		job.RequestV2 = &SubmissionV2{Kind: summary.Kind, Target: &VerificationTarget{ChainID: chainID, Address: summary.Address}}
		if summary.Kind == JobSourcify || summary.Kind == JobSourcifyFromEtherscan {
			job.RequestV2.SourcifyRequest, _ = json.Marshal(map[string]string{"chain_id": summary.ChainID, "address": summary.Address})
		}
	}
	return job
}

// JobOutcome reads full immutable evidence only for callers whose response
// contract includes sources and compiler artifacts.
func (repository *PostgresRepository) JobOutcome(ctx context.Context, id string) (json.RawMessage, error) {
	var key pgtype.UUID
	if err := key.Scan(id); err != nil {
		return nil, err
	}
	return dbgen.New(repository.db).VerifyV2GetJobOutcome(ctx, key)
}

// JobWithOutcome preserves the native API's complete result response. Ordinary
// browser and compatibility polling use Job and its materialized summary.
func (service *Service) JobWithOutcome(ctx context.Context, id string) (VerificationJob, bool, error) {
	job, found, err := service.Job(ctx, id)
	if err != nil || !found || len(job.Outcome) == 0 {
		return job, found, err
	}
	if reader, ok := service.repository.(interface {
		JobOutcome(context.Context, string) (json.RawMessage, error)
	}); ok {
		job.Outcome, err = reader.JobOutcome(ctx, id)
		if err != nil {
			return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
		}
	}
	return job, found, nil
}
