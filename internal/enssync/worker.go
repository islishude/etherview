package enssync

import (
	"context"
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type WorkerOptions struct {
	SourceID                                    int64
	Manifest                                    Manifest
	Registry, UniversalResolver                 common.Address
	Pool                                        *ethrpc.Pool
	Resolver                                    *ensresolver.Resolver
	Gateways                                    []string
	Freshness, Retention, Timeout, FailureDelay time.Duration
	Observer                                    ensresolver.Observer
	MaxAttempts                                 int32
}
type Worker struct {
	repo    *Repository
	options WorkerOptions
	digest  common.Hash
}

var endpointLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func NewWorker(repo *Repository, options WorkerOptions) (*Worker, error) {
	if repo == nil || options.SourceID < 1 || options.Resolver == nil || options.Pool == nil || options.Registry == (common.Address{}) || options.UniversalResolver == (common.Address{}) || len(options.Gateways) > 4 {
		return nil, ErrInvalidInput
	}
	m, digest, _, e := validateManifest(options.Manifest)
	if e != nil {
		return nil, e
	}
	options.Manifest = m
	options.Gateways = append([]string(nil), options.Gateways...)
	found := false
	for _, d := range m.Deployments {
		if d.Address == options.UniversalResolver && d.Profile == "universal" {
			found = true
		}
	}
	if !found {
		return nil, ErrUnsupportedDeployment
	}
	if options.Freshness == 0 {
		options.Freshness = 15 * time.Minute
	}
	if options.Retention == 0 {
		options.Retention = 30 * time.Minute
	}
	if options.Timeout == 0 {
		options.Timeout = 8 * time.Second
	}
	if options.FailureDelay == 0 {
		options.FailureDelay = 30 * time.Second
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 5
	}
	if options.Freshness < time.Second || options.Freshness > 24*time.Hour || options.Retention < options.Freshness || options.Retention > 7*24*time.Hour || options.Timeout < 100*time.Millisecond || options.Timeout > time.Minute || options.FailureDelay < time.Second || options.FailureDelay > time.Hour || options.MaxAttempts < 1 || options.MaxAttempts > 100 {
		return nil, ErrInvalidInput
	}
	return &Worker{repo, options, digest}, nil
}

// Schedule coalesces due and invalidated identities. Exhausted work stays terminal
// until a source/dependency generation changes or an explicit replay requests it.
func (w *Worker) Schedule(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return ErrInvalidInput
	}
	if e := w.verifySource(ctx); e != nil {
		return e
	}
	return collectionError(dbaccess.WithTransaction(ctx, w.repo.db, func(q *dbgen.Queries) error {
		if _, e := q.LockENSSyncSource(ctx, w.options.SourceID); e != nil {
			return e
		}
		candidates, e := q.ListENSSyncSchedulableCandidates(ctx, w.options.SourceID, int32(limit))
		if e != nil {
			return e
		}
		invalidated := false
		for _, c := range candidates {
			if c.PreviousJobID != nil {
				if c.RefreshOnly {
					_, e = q.RefreshENSSyncCandidate(ctx, c.SourceID, c.ID, c.RequestedGeneration)
				} else {
					_, e = q.InvalidateENSSyncCandidate(ctx, c.SourceID, c.ID, c.RequestedGeneration)
					invalidated = true
				}
				if e != nil {
					return e
				}
			}
			if c.PreviousJobID != nil && c.PreserveBudget {
				if _, e = q.EnqueueENSSyncSuccessor(ctx, *c.PreviousJobID, c.SourceID, c.ID); e != nil {
					return e
				}
				continue
			}
			if _, e = q.EnqueueENSSyncJob(ctx, dbgen.EnqueueENSSyncJobParams{SourceID: c.SourceID, CandidateID: c.ID, MaxAttempts: w.options.MaxAttempts, Deadline: stamp(time.Now().Add(time.Duration(w.options.MaxAttempts) * (w.options.FailureDelay + w.options.Timeout)))}); e != nil {
				return e
			}
		}
		if invalidated {
			return q.EmitENSInvalidation(ctx, strconv.FormatInt(w.options.SourceID, 10))
		}
		return nil
	}))
}

// Step claims one generation and closes its transaction before resolver/CCIP
// calls. Claim fencing, not process identity, authorizes the final publication.
func (w *Worker) Step(ctx context.Context) (worked bool, failure error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, w.options.Timeout)
	defer cancel()
	if e := w.verifySource(ctx); e != nil {
		return false, e
	}
	job, e := w.repo.Claim(ctx, w.options.SourceID, w.options.Timeout+time.Second)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, collectionError(e)
	}
	data, e := dbgen.New(w.repo.db).GetENSSyncWorkerJob(ctx, job.SourceID, job.ID)
	if e != nil {
		return true, ErrStorage
	}
	outcome := "resolved"
	defer func() {
		if w.options.Observer != nil {
			if failure != nil {
				outcome = "error"
			}
			w.options.Observer.RecordENS(ensresolver.MetricObservation{Direction: data.Direction, Outcome: outcome, Duration: time.Since(started)})
		}
	}()
	target, e := uintNumber(data.Number)
	if e != nil {
		return true, e
	}
	coin, e := uint256Value(data.CoinType)
	if e != nil {
		return true, e
	}
	if data.Direction != "profile" {
		if reused, err := w.tryProjection(ctx, job, data.BlockTime.Time); reused || err != nil {
			return true, err
		}
	}
	endpoint, e := w.options.Pool.Acquire(ethrpc.PurposeState)
	if e != nil {
		return true, w.retry(ctx, job, ensresolver.CodeRPCUnavailable)
	}
	if !endpointLabel.MatchString(endpoint.Name) {
		return true, w.retry(ctx, job, ensresolver.CodeInvalidResponse)
	}
	if e = w.verifyEndpoint(ctx, endpoint, job, target); e != nil {
		return true, w.retry(ctx, job, ensresolver.CodeSourceIdentity)
	}
	if data.Direction == "profile" {
		return true, w.publishProfile(ctx, endpoint, job, data, target)
	}
	profile := ensresolver.Profile{Registry: w.options.Registry, UniversalResolver: w.options.UniversalResolver, CoinType: coin, Gateways: w.options.Gateways, Block: ensresolver.BlockRef{Number: target, Hash: common.BytesToHash(job.TargetHash)}}
	observation := dbgen.PublishENSSyncObservationParams{SourceID: job.SourceID, CandidateID: job.CandidateID, JobID: job.ID, Generation: job.Generation, LeaseToken: job.LeaseToken, Deployment: w.options.UniversalResolver.Bytes(), Endpoint: endpoint.Name, Evidence: "resolver"}
	if data.Direction == string(enscontract.Forward) {
		result, err := w.options.Resolver.Forward(ctx, endpoint, profile, data.LookupKey)
		e = err
		if e == nil {
			observation.Outcome = string(result.Outcome)
			observation.Name = &result.Name
			if result.Resolver != (common.Address{}) {
				observation.Resolver = result.Resolver.Bytes()
			}
			if result.Outcome == ensresolver.OutcomeResolved {
				observation.Address = result.Address.Bytes()
			}
		}
	} else {
		if !common.IsHexAddress(data.LookupKey) || data.LookupKey != addressKey(common.HexToAddress(data.LookupKey)) {
			return true, w.retry(ctx, job, ensresolver.CodeInvalidResponse)
		}
		address := common.HexToAddress(data.LookupKey)
		result, err := w.options.Resolver.Reverse(ctx, endpoint, profile, address)
		e = err
		if e == nil {
			observation.Outcome = string(result.Outcome)
			observation.Address = address.Bytes()
			if result.ReverseResolver != (common.Address{}) {
				observation.ReverseResolver = result.ReverseResolver.Bytes()
			}
			if result.Outcome == ensresolver.OutcomeResolved {
				observation.Name = &result.Name
				observation.Resolver = result.Resolver.Bytes()
				observation.ReverseResolver = result.ReverseResolver.Bytes()
			}
		}
	}
	if e != nil {
		code := ensresolver.CodeResolverFailure
		if typed, ok := errors.AsType[*ensresolver.ResolutionError](e); ok {
			code = typed.Code
		}
		return true, w.retry(ctx, job, code)
	}
	outcome = observation.Outcome
	now := time.Now()
	observation.ObservedAt = stamp(now)
	observation.ValidUntil = stamp(now.Add(w.options.Freshness))
	observation.RetainUntil = stamp(now.Add(w.options.Retention))
	observation.RefreshAt = stamp(now.Add(w.options.Freshness / 2))
	if e = w.bindResultDependencies(ctx, job, data.LookupKey, &observation); e != nil {
		return true, e
	}
	if observation.Outcome == string(ensresolver.OutcomeNoRecord) {
		// Resolver identities qualify dependency invalidation, not a negative
		// publication's public record fields.
		observation.Resolver = nil
		observation.ReverseResolver = nil
	}
	_, e = w.repo.Publish(ctx, observation)
	return true, collectionError(e)
}
func (w *Worker) retry(ctx context.Context, job dbgen.EnsSyncJob, code string) error {
	_, e := dbgen.New(w.repo.db).RetryENSSyncJob(ctx, dbgen.RetryENSSyncJobParams{SourceID: job.SourceID, JobID: job.ID, LeaseToken: job.LeaseToken, FailureCode: &code, RetryAt: stamp(time.Now().Add(w.options.FailureDelay))})
	if e != nil {
		return ErrStorage
	}
	return &ensresolver.ResolutionError{Code: code}
}
func uint256Value(value pgtype.Numeric) (*big.Int, error) {
	text, e := dbaccess.NumericText(value)
	if e != nil || !text.Valid {
		return nil, ErrStorage
	}
	number, ok := new(big.Int).SetString(text.String, 10)
	if !ok || number.Sign() < 0 || number.BitLen() > 256 {
		return nil, ErrStorage
	}
	return number, nil
}

// PostgreSQL may encode 60 as a coefficient of 6 with exponent 1. Compare
// complete numeric values, never the coefficient alone, for chain/coin identity.
func equalUnsignedNumeric(a, b pgtype.Numeric) bool {
	left, le := uint256Value(a)
	right, re := uint256Value(b)
	return le == nil && re == nil && left.Cmp(right) == 0
}
