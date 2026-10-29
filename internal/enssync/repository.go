// Package enssync owns bounded ENS collection and PostgreSQL synchronization.
// Resolution and effective-name publication use the same durable source fences.
package enssync

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrInvalidInput = errors.New("invalid ENS synchronization input")
	ErrFenceLost    = errors.New("ENS publication fence lost")
	ErrStorage      = errors.New("ENS synchronization storage unavailable")
)

type Repository struct{ db dbaccess.Database }

func NewRepository(database dbaccess.Database) (*Repository, error) {
	if database == nil {
		return nil, ErrInvalidInput
	}
	return &Repository{db: database}, nil
}

var _ enscontract.Reader = (*Repository)(nil)

func (r *Repository) CreateSource(ctx context.Context, input dbgen.CreateENSSyncSourceParams) (int64, error) {
	var id int64
	err := dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		var err error
		id, err = q.CreateENSSyncSource(ctx, input)
		if err != nil {
			return err
		}
		return q.InitializeENSSyncProgress(ctx, id)
	})
	return id, err
}

// Claim releases its short source/job transaction before returning work. Lease
// retries reuse the same durable generation and cannot reset its attempt budget.
func (r *Repository) Claim(ctx context.Context, sourceID int64, lease time.Duration) (dbgen.EnsSyncJob, error) {
	var job dbgen.EnsSyncJob
	if sourceID < 1 || lease < time.Second || lease > 5*time.Minute {
		return job, ErrInvalidInput
	}
	err := dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		if _, err := q.LockENSSyncSource(ctx, sourceID); err != nil {
			return err
		}
		if _, err := q.ExpireENSSyncJobs(ctx, sourceID); err != nil {
			return err
		}
		var err error
		job, err = q.ClaimENSSyncJob(ctx, pgtype.UUID{Bytes: uuid.New(), Valid: true}, int32(lease/time.Second), sourceID)
		// Commit terminalization even when no claimable work remains.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err == nil && job.ID == 0 {
		return job, pgx.ErrNoRows
	}
	return job, err
}

// Publish is the only source-publication writer. Its result, job completion and
// refresh deadline commit together. Callers must finish external work first.
// Effective namespace selection, catalog and SSE publication belong to T26.
func (r *Repository) Publish(ctx context.Context, input dbgen.PublishENSSyncObservationParams) (int64, error) {
	var id int64
	err := dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		if _, err := q.LockENSSyncSource(ctx, input.SourceID); err != nil {
			return err
		}
		if _, err := q.LockENSSyncCandidate(ctx, input.SourceID, input.CandidateID); err != nil {
			return err
		}
		var err error
		id, err = q.PublishENSSyncObservation(ctx, input)
		if err != nil {
			return err
		}
		return publishEffective(ctx, q, input.SourceID, input.CandidateID, input.RetainUntil)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrFenceLost
	}
	return id, err
}

// Lookup uses a writer-backed read-only snapshot. It performs no discovery,
// enqueue, refresh or snapshot writes. Absent rows never prove a negative.
func (r *Repository) Lookup(ctx context.Context, input enscontract.Lookup) (enscontract.Result, error) {
	result := enscontract.Result{State: enscontract.Unavailable, Code: enscontract.NotIndexed}
	chain, ok := unsignedNumeric(input.TargetChainID)
	coin, coinOK := unsignedNumeric(input.CoinType)
	if !ok || input.TargetChainID == "0" || !coinOK || input.SourceID < 1 || input.PublicationID < 0 || len(input.Key) == 0 || len(input.Key) > 512 ||
		(input.Direction != enscontract.Forward && input.Direction != enscontract.Primary) {
		return result, ErrInvalidInput
	}
	var row dbgen.ReadENSSyncCandidateRow
	err := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		var err error
		row, err = q.ReadENSSyncCandidate(ctx, dbgen.ReadENSSyncCandidateParams{
			SourceID: input.SourceID, PublicationID: input.PublicationID, TargetChainID: chain, PolicyDigest: input.PolicyDigest.Bytes(),
			Direction: string(input.Direction), LookupKey: input.Key, CoinType: coin,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, ErrStorage
	}
	return readResult(row, time.Now()), nil
}

func readResult(row dbgen.ReadENSSyncCandidateRow, now time.Time) enscontract.Result {
	result := enscontract.Result{State: enscontract.Unavailable, Source: enscontract.Namespace(row.Namespace)}
	switch {
	case row.SourceState == "unsupported" || row.SourceState == "halted":
		result.Code = enscontract.UnsupportedSource
	case row.SourceState != "active" || !row.CoverageCanonical || len(row.TargetHash) == 0 || !bytes.Equal(row.TargetHash, row.ScannedHash):
		result.Code = enscontract.SyncPending
	case !row.HealthyUntil.Valid || !now.Before(row.HealthyUntil.Time):
		result.Code = enscontract.Stale
	case row.PublicationID == 0:
		result.Code = enscontract.ResolutionPending
	case row.Generation == nil || *row.Generation < row.MinimumGeneration || row.SourceRevision == nil || *row.SourceRevision != row.Revision || !row.Canonical:
		result.Code = enscontract.ResolutionPending
	case !row.ValidUntil.Valid || !now.Before(row.ValidUntil.Time) || !row.RetainUntil.Valid || !now.Before(row.RetainUntil.Time):
		result.Code = enscontract.Stale
	case row.Outcome == nil:
		result.Code = enscontract.ResolutionPending
	default:
		result.State = enscontract.State(*row.Outcome)
		result.PublicationID = row.PublicationID
		result.Generation = *row.Generation
		if row.Name != nil {
			result.Name = *row.Name
		}
		result.Address = common.BytesToAddress(row.Address)
		result.Resolver = common.BytesToAddress(row.Resolver)
		result.ReverseResolver = common.BytesToAddress(row.ReverseResolver)
		result.BlockHash = common.BytesToHash(row.BlockHash)
		result.ValidUntil = row.ValidUntil.Time
		if row.HealthyUntil.Time.Before(result.ValidUntil) {
			result.ValidUntil = row.HealthyUntil.Time
		}
		if row.RetainUntil.Time.Before(result.ValidUntil) {
			result.ValidUntil = row.RetainUntil.Time
		}
	}
	return result
}

func unsignedNumeric(value string) (pgtype.Numeric, bool) {
	if len(value) == 0 || len(value) > 78 {
		return pgtype.Numeric{}, false
	}
	number, ok := new(big.Int).SetString(value, 10)
	if !ok || number.Sign() < 0 || number.BitLen() > 256 || number.String() != value {
		return pgtype.Numeric{}, false
	}
	return pgtype.Numeric{Int: number, Valid: true}, true
}
