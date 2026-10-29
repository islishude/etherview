package enssync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

func dependencyExpiry(dependencies []dbgen.EnsSyncProjection) time.Time {
	var earliest time.Time
	for _, dep := range dependencies {
		if dep.Kind != "registration" && dep.Kind != "control" {
			continue
		}
		fields := map[string]any{}
		if json.Unmarshal(dep.Payload, &fields) != nil {
			continue
		}
		for _, key := range []string{"expiry", "expires"} {
			raw, ok := fields[key].(string)
			if !ok {
				continue
			}
			n, ok := new(big.Int).SetString(raw, 10)
			if !ok || !n.IsInt64() || n.Sign() <= 0 {
				continue
			}
			expiration := time.Unix(n.Int64(), 0)
			if earliest.IsZero() || expiration.Before(earliest) {
				earliest = expiration
			}
		}
	}
	return earliest
}
func (w *Worker) tryProjection(ctx context.Context, job dbgen.EnsSyncJob, blockTime time.Time) (bool, error) {
	q := dbgen.New(w.repo.db)
	previous, e := q.GetENSSyncPreviousProjectionObservation(ctx, job.SourceID, job.CandidateID)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, ErrStorage
	}
	if previous.SourceRevision != job.SourceRevision {
		return false, nil
	}
	dependencies, e := q.ListENSSyncJobDependencies(ctx, job.SourceID, previous.JobID)
	if e != nil {
		return false, ErrStorage
	}
	if len(dependencies) == 0 {
		return false, nil
	}
	now := time.Now()
	expiration := dependencyExpiry(dependencies)
	if !expiration.IsZero() && (!now.Before(expiration) || !blockTime.Before(expiration)) {
		return false, nil
	}
	e = dbaccess.WithTransaction(ctx, w.repo.db, func(q *dbgen.Queries) error {
		progress, e := q.LockENSSyncSource(ctx, job.SourceID)
		if e != nil {
			return e
		}
		if progress.Revision != job.SourceRevision || !bytes.Equal(progress.TargetHash, job.TargetHash) {
			return ErrFenceLost
		}
		for _, dep := range dependencies {
			n, e := q.BindENSSyncJobDependency(ctx, dbgen.BindENSSyncJobDependencyParams{SourceID: job.SourceID, JobID: job.ID, LeaseToken: job.LeaseToken, Kind: dep.Kind, Key: dep.Key, Revision: dep.Revision})
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrFenceLost
			}
		}
		return nil
	})
	if e != nil {
		return true, collectionError(e)
	}
	valid := now.Add(w.options.Freshness)
	if !expiration.IsZero() && expiration.Before(valid) {
		valid = expiration
	}
	observation := dbgen.PublishENSSyncObservationParams{SourceID: job.SourceID, CandidateID: job.CandidateID, JobID: job.ID, Generation: job.Generation, LeaseToken: job.LeaseToken, Deployment: previous.Deployment, Endpoint: "projection", Evidence: "projection", Outcome: previous.Outcome, Name: previous.Name, Address: previous.Address, Resolver: previous.Resolver, ReverseResolver: previous.ReverseResolver, ObservedAt: stamp(now), ValidUntil: stamp(valid), RefreshAt: stamp(now.Add(valid.Sub(now) / 2)), RetainUntil: stamp(now.Add(w.options.Retention))}
	_, e = w.repo.Publish(ctx, observation)
	return true, collectionError(e)
}
func (w *Worker) qualifyProjection(ctx context.Context, q *dbgen.Queries, job dbgen.EnsSyncJob, result *dbgen.PublishENSSyncObservationParams) error {
	if result.Outcome != "resolved" {
		return nil
	}
	addresses := [][]byte{result.Resolver}
	if len(result.ReverseResolver) > 0 {
		addresses = append(addresses, result.ReverseResolver)
	}
	rows, e := q.ListENSSyncEmitterDeployments(ctx, job.SourceID, addresses)
	if e != nil {
		return e
	}
	standard := map[common.Address]bool{}
	for _, row := range rows {
		for _, name := range []string{"resolver", "v2_resolver", "default_reverse"} {
			profile, e := loadProfile(name)
			if e != nil {
				return e
			}
			if bytes.Equal(row.AbiDigest, profile.digest[:]) {
				standard[common.BytesToAddress(row.Address)] = true
			}
		}
	}
	if !standard[common.BytesToAddress(result.Resolver)] || (len(result.ReverseResolver) > 0 && !standard[common.BytesToAddress(result.ReverseResolver)]) {
		return nil
	}
	dependencies, e := q.ListENSSyncJobDependencies(ctx, job.SourceID, job.ID)
	if e != nil {
		return e
	}
	registered := false
	recordMatches := false
	reverseMatches := len(result.ReverseResolver) == 0
	for _, dep := range dependencies {
		if dep.Kind == "resolver_record" && bytes.Equal(dep.Deployment, result.Resolver) {
			var fields struct {
				Address string
				Deleted bool
				Version string
			}
			if json.Unmarshal(dep.Payload, &fields) != nil {
				return ErrStorage
			}
			if common.IsHexAddress(fields.Address) && bytes.Equal(common.HexToAddress(fields.Address).Bytes(), result.Address) && !fields.Deleted {
				recordMatches = true
			}
		}
		if dep.Kind == "resolver_record" && bytes.Equal(dep.Deployment, result.ReverseResolver) && result.Name != nil {
			var fields struct {
				Name    string
				Deleted bool
			}
			if json.Unmarshal(dep.Payload, &fields) != nil {
				return ErrStorage
			}
			if fields.Name == *result.Name && !fields.Deleted {
				reverseMatches = true
			}
		}
		if dep.Kind == "registration" {
			registered = true
		}
	}
	if !registered || !recordMatches || !reverseMatches {
		return nil
	} // Unknown lifecycle/coverage remains dynamic.
	expiration := dependencyExpiry(dependencies)
	if !expiration.IsZero() {
		if !result.ObservedAt.Time.Before(expiration) {
			return nil
		}
		if expiration.Before(result.ValidUntil.Time) {
			result.ValidUntil = stamp(expiration)
			result.RefreshAt = stamp(result.ObservedAt.Time.Add(expiration.Sub(result.ObservedAt.Time) / 2))
		}
	}
	result.Evidence = "projection"
	return nil
}
