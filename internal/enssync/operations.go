package enssync

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
)

type OperationScope struct {
	SourceID int64
	ChainID  string
	Policy   common.Hash
}

func (r *Repository) operationScope(ctx context.Context, q *dbgen.Queries, scope OperationScope) (string, error) {
	if scope.SourceID < 1 || scope.Policy == (common.Hash{}) {
		return "", ErrInvalidInput
	}
	source, e := q.GetENSSyncSource(ctx, scope.SourceID)
	if e != nil {
		return "", e
	}
	chain, ok := unsignedNumeric(scope.ChainID)
	if !ok || !equalUnsignedNumeric(chain, source.TargetChainID) || common.BytesToHash(source.PolicyDigest) != scope.Policy {
		return "", ErrSourceIdentity
	}
	policy, e := q.GetENSEffectivePolicy(ctx, chain, scope.Policy.Bytes())
	if e != nil {
		return "", e
	}
	coin := policy.CoinType
	if policy.SourceID != scope.SourceID {
		return "", ErrSourceIdentity
	}
	if _, e = q.LockENSSyncSource(ctx, scope.SourceID); e != nil {
		return "", e
	}
	value, e := uint256Value(coin)
	if e != nil {
		return "", e
	}
	return value.String(), nil
}
func (r *Repository) ImportCandidates(ctx context.Context, scope OperationScope, batch string, values []string) (int, error) {
	if !endpointLabel.MatchString(batch) || len(values) < 1 || len(values) > 1000 {
		return 0, ErrInvalidInput
	}
	normalizer, _ := ensresolver.NewNormalizer()
	candidates := make([]candidate, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		value := candidate{Direction: "forward"}
		if common.IsHexAddress(raw) {
			value.Direction = "primary"
			value.Key = addressKey(common.HexToAddress(raw))
		} else {
			var e error
			value.Key, e = normalizer.Normalize(ctx, raw)
			if e != nil {
				return 0, ErrInvalidInput
			}
		}
		key := value.Direction + ":" + value.Key
		if !seen[key] {
			candidates = append(candidates, value)
			seen[key] = true
		}
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return strings.Compare(a.Direction+":"+a.Key, b.Direction+":"+b.Key) })
	raw, e := json.Marshal(candidates)
	if e != nil {
		return 0, ErrInvalidInput
	}
	digest := sha256.Sum256(raw)
	e = dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		coin, e := r.operationScope(ctx, q, scope)
		if e != nil {
			return e
		}
		if e = q.RecordENSSyncImport(ctx, dbgen.RecordENSSyncImportParams{SourceID: scope.SourceID, BatchID: batch, Digest: digest[:], Count: int32(len(candidates))}); e != nil {
			return e
		}
		for _, value := range candidates {
			if e = candidateOrigin(ctx, q, scope.SourceID, coin, value, "import", batch); e != nil {
				return e
			}
		}
		return nil
	})
	return len(candidates), collectionError(e)
}

type ReplayResult struct {
	ID      int64
	Through int64
	Count   int
}

func (r *Repository) ReplayCandidates(ctx context.Context, scope OperationScope, batch string, after int64, limit int, reason string) (ReplayResult, error) {
	var result ReplayResult
	reason = strings.TrimSpace(reason)
	if !endpointLabel.MatchString(batch) || after < 0 || limit < 1 || limit > 1000 || len(reason) < 1 || len(reason) > 128 {
		return result, ErrInvalidInput
	}
	result.Through = after
	e := dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		if _, e := r.operationScope(ctx, q, scope); e != nil {
			return e
		}
		prior, e := q.GetENSSyncReplay(ctx, scope.SourceID, batch)
		if e == nil {
			if prior.AfterCandidate != after || prior.RowLimit != int32(limit) || prior.Reason != reason {
				return ErrInvalidInput
			}
			result = ReplayResult{ID: prior.ID, Through: prior.ThroughCandidate, Count: int(prior.Count)}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		rows, e := q.ListENSSyncReplayCandidates(ctx, scope.SourceID, after, int32(limit))
		if e != nil {
			return e
		}
		for _, row := range rows {
			if _, e = q.InvalidateENSSyncCandidate(ctx, scope.SourceID, row.ID, row.RequestedGeneration); e != nil {
				return e
			}
			result.Through = row.ID
			result.Count++
		}
		result.ID, e = q.RecordENSSyncReplay(ctx, dbgen.RecordENSSyncReplayParams{SourceID: scope.SourceID, BatchID: batch, RowLimit: int32(limit), AfterCandidate: after, ThroughCandidate: result.Through, Count: int32(result.Count), Reason: reason})
		if e != nil {
			return e
		}
		if len(rows) > 0 {
			return q.EmitENSInvalidation(ctx, strconv.FormatInt(scope.SourceID, 10))
		}
		return nil
	})
	return result, collectionError(e)
}
