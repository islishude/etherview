package enssync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

func contractRevisionKey(address common.Address) string { return addressKey(address) + "/revision" }

// Dependencies are captured in a short fenced transaction after external work.
// The target must still match the fully scanned source before any fact is bound.
func (w *Worker) bindResultDependencies(ctx context.Context, job dbgen.EnsSyncJob, key string, observation *dbgen.PublishENSSyncObservationParams) error {
	stale := false
	err := dbaccess.WithTransaction(ctx, w.repo.db, func(q *dbgen.Queries) error {
		progress, e := q.LockENSSyncSource(ctx, job.SourceID)
		if e != nil {
			return e
		}
		if progress.Revision != job.SourceRevision {
			return ErrFenceLost
		}
		if e := w.watchExactResult(ctx, q, job, observation); e != nil {
			if errors.Is(e, ErrFenceLost) {
				// Retain the watched emitters even when newer core logs reject
				// this result. The scheduler needs that evidence to replace the
				// stale job; rolling it back would leave a claimable old target.
				stale = true
				return nil
			}
			return e
		}
		if observation.ProfileData != nil {
			return nil
		}
		if !bytes.Equal(progress.ScannedHash, job.TargetHash) || !bytes.Equal(progress.TargetHash, job.TargetHash) {
			return nil
		}
		return w.bindProjectionDependencies(ctx, q, job, key, observation, &progress)

	})
	if err == nil && stale {
		return ErrFenceLost
	}
	return collectionError(err)
}

func (w *Worker) watchExactResult(ctx context.Context, q *dbgen.Queries, job dbgen.EnsSyncJob, observation *dbgen.PublishENSSyncObservationParams) error {
	// Exact RPC evidence watches core logs without manufacturing projection
	// rows ahead of the collector. Old logs cannot invalidate a later read.
	addresses := [][]byte{w.options.Registry.Bytes(), w.options.UniversalResolver.Bytes()}
	for _, d := range w.options.Manifest.Deployments {
		addresses = append(addresses, d.Address.Bytes())
	}
	if len(observation.Resolver) > 0 {
		addresses = append(addresses, observation.Resolver)
	}
	if len(observation.ReverseResolver) > 0 {
		addresses = append(addresses, observation.ReverseResolver)
	}
	if _, e := q.WatchENSSyncJob(ctx, dbgen.WatchENSSyncJobParams{SourceID: job.SourceID, JobID: job.ID, LeaseToken: job.LeaseToken, Addresses: addresses}); e != nil {
		return e
	}
	valid, e := q.CheckENSSyncJobCurrent(ctx, job.ID)
	if e != nil {
		return e
	}
	if !valid {
		return ErrFenceLost
	}
	return nil
}

func (w *Worker) bindProjectionDependencies(ctx context.Context, q *dbgen.Queries, job dbgen.EnsSyncJob, key string, observation *dbgen.PublishENSSyncObservationParams, progress *dbgen.EnsSyncProgress) error {
	var e error
	bound := map[string]bool{}
	bind := func(kind, key string) (dbgen.EnsSyncProjection, error) {
		row, e := q.GetENSSyncLatestProjection(ctx, job.SourceID, kind, key)
		if e != nil {
			return row, e
		}
		identity := kind + "/" + key
		if bound[identity] {
			return row, nil
		}
		if len(bound) >= 256 {
			return row, ErrCollectionLimit
		}
		n, e := q.BindENSSyncJobDependency(ctx, dbgen.BindENSSyncJobDependencyParams{SourceID: job.SourceID, JobID: job.ID, LeaseToken: job.LeaseToken, Kind: kind, Key: key, Revision: row.Revision})
		if e != nil {
			return row, e
		}
		if n != 1 {
			return row, ErrFenceLost
		}
		bound[identity] = true
		return row, nil
	}
	// The entrypoint's reviewed identity is itself a dependency, including
	// definitive negatives for which no final resolver is returned.
	if _, e = bind("lifecycle", contractRevisionKey(w.options.UniversalResolver)); e != nil {
		return e
	}
	name := key
	if observation.Name != nil {
		name = *observation.Name
	}
	if _, valid := normalizedName(name); valid {
		// Bind every known registrar lifecycle on the suffix path so wall-clock
		// expiry cannot be extended merely because no new event was emitted.
		suffix := name
		for {
			for _, d := range w.options.Manifest.Deployments {
				if d.Profile == "registrar" || strings.HasPrefix(d.Profile, "controller") {
					if _, e = bind("registration", recordKey(d.Address, nameHash(suffix), "registration")); e != nil && !errors.Is(e, pgx.ErrNoRows) {
						return e
					}
				}
			}
			index := strings.IndexByte(suffix, '.')
			if index < 0 {
				break
			}
			suffix = suffix[index+1:]
		}
		for _, root := range w.options.Manifest.Roots {
			suffix := ""
			if root.Name != "" {
				suffix = "." + root.Name
			}
			if suffix != "" && !strings.HasSuffix(name, suffix) {
				continue
			}
			labels := strings.Split(strings.TrimSuffix(name, suffix), ".")
			registry := root.Address
			for i, labelName := range slices.Backward(labels) {
				label := crypto.Keccak256Hash([]byte(labelName))
				row, e := bind("registration", v2Key(registry, "label", label.Hex()))
				if errors.Is(e, pgx.ErrNoRows) {
					_, e = bind("lifecycle", contractRevisionKey(registry))
					if e != nil {
						return e
					}
					break
				}
				if e != nil {
					return e
				}
				var fields v2Registration
				if json.Unmarshal(row.Payload, &fields) != nil {
					return ErrStorage
				}
				if i > 0 {
					registry = common.HexToAddress(fields.Subregistry)
					if registry == (common.Address{}) {
						break
					}
				}
			}
		}
	}
	if err := w.bindRecordDependencies(ctx, q, job, name, observation, bind, &progress.ProjectionRevision); err != nil {
		return err
	}
	return w.qualifyProjection(ctx, q, job, observation)
}
