package enssync

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type EffectivePolicy struct {
	TargetChainID, CoinType, CustomCoinType, Mode string
	Digest                                        common.Hash
	OfficialSourceID, CustomSourceID              int64
	UnsupportedCoin                               bool
}

func (r *Repository) RegisterPolicy(ctx context.Context, p EffectivePolicy) error {
	chain, valid := unsignedNumeric(p.TargetChainID)
	coin, coinValid := unsignedNumeric(p.CoinType)
	if p.CustomCoinType == "" {
		p.CustomCoinType = p.CoinType
	}
	customCoin, customValid := unsignedNumeric(p.CustomCoinType)
	if !valid || !coinValid || !customValid || p.TargetChainID == "0" || p.Digest == (common.Hash{}) || p.OfficialSourceID < 0 || p.CustomSourceID < 0 || (p.Mode != "official_first" && p.Mode != "custom_only") {
		return ErrInvalidInput
	}
	if p.UnsupportedCoin && chain.Int.BitLen() <= 31 {
		return ErrInvalidInput
	}
	if p.Mode == "custom_only" && (p.CustomSourceID == 0 || p.OfficialSourceID != 0 || p.UnsupportedCoin) {
		return ErrInvalidInput
	}
	if p.Mode == "official_first" && ((p.OfficialSourceID == 0 && !p.UnsupportedCoin) || (p.OfficialSourceID != 0 && p.UnsupportedCoin) || (p.UnsupportedCoin && p.CustomSourceID == 0)) {
		return ErrInvalidInput
	}
	return collectionError(dbaccess.WithTransaction(ctx, r.db, func(q *dbgen.Queries) error {
		for _, entry := range []struct {
			id        int64
			namespace string
		}{{p.OfficialSourceID, "ens"}, {p.CustomSourceID, "custom_ens"}} {
			if entry.id == 0 {
				continue
			}
			s, e := q.GetENSSyncSource(ctx, entry.id)
			if e != nil {
				return e
			}
			if s.TargetChainID.Int.Cmp(chain.Int) != 0 || s.Namespace != entry.namespace {
				return ErrSourceIdentity
			}
		}
		return q.RegisterENSEffectivePolicy(ctx, dbgen.RegisterENSEffectivePolicyParams{TargetChainID: chain, PolicyDigest: p.Digest.Bytes(), Mode: p.Mode, OfficialSourceID: optionalID(p.OfficialSourceID), CustomSourceID: optionalID(p.CustomSourceID), UnsupportedCoin: p.UnsupportedCoin, CoinType: coin, CustomCoinType: customCoin})
	}))
}
func optionalID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func sourcePublication(ctx context.Context, q *dbgen.Queries, source *int64, publication *int64, direction, key string, coin pgtype.Numeric) (enscontract.Result, error) {
	result := enscontract.Result{State: enscontract.Unavailable, Code: enscontract.NotIndexed}
	if source == nil {
		return result, nil
	}
	s, e := q.GetENSSyncSource(ctx, *source)
	if e != nil {
		return result, e
	}
	id := int64(0)
	if publication != nil {
		id = *publication
	}
	row, e := q.ReadENSSyncCandidate(ctx, dbgen.ReadENSSyncCandidateParams{SourceID: *source, PublicationID: id, TargetChainID: s.TargetChainID, PolicyDigest: s.PolicyDigest, Direction: direction, LookupKey: key, CoinType: coin})
	if errors.Is(e, pgx.ErrNoRows) {
		return result, nil
	}
	if e != nil {
		return result, e
	}
	return readResult(row, time.Now()), nil
}
func selectEffective(policy dbgen.EnsEffectivePolicy, official, custom enscontract.Result) enscontract.Result {
	if policy.Mode == "custom_only" || policy.UnsupportedCoin {
		return custom
	}
	if official.State != enscontract.NotFound || policy.CustomSourceID == nil {
		return official
	}
	if custom.State != enscontract.Unavailable && official.ValidUntil.Before(custom.ValidUntil) {
		custom.ValidUntil = official.ValidUntil
	}
	return custom
}
func publishEffective(ctx context.Context, q *dbgen.Queries, source, candidate int64, retention pgtype.Timestamptz) error {
	policies, e := q.ListENSEffectivePoliciesForSource(ctx, &source)
	if e != nil {
		return e
	}
	if len(policies) > 64 {
		return ErrCollectionLimit
	}
	if len(policies) == 0 {
		return nil
	}
	c, e := q.GetENSSyncCandidateByID(ctx, source, candidate)
	if e != nil {
		return e
	}
	for _, policy := range policies {
		expectedCoin := policy.CoinType
		if policy.CustomSourceID != nil && *policy.CustomSourceID == source {
			expectedCoin = policy.CustomCoinType
		}
		if c.CoinType.Int.Cmp(expectedCoin.Int) != 0 {
			continue
		}
		official, e := sourcePublication(ctx, q, policy.OfficialSourceID, nil, c.Direction, c.LookupKey, policy.CoinType)
		if e != nil {
			return e
		}
		custom, e := sourcePublication(ctx, q, policy.CustomSourceID, nil, c.Direction, c.LookupKey, policy.CustomCoinType)
		if e != nil {
			return e
		}
		result := selectEffective(policy, official, custom)
		input := dbgen.PutENSEffectivePublicationParams{TargetChainID: policy.TargetChainID, PolicyDigest: policy.PolicyDigest, Direction: c.Direction, LookupKey: c.LookupKey, OfficialPublicationID: optionalID(official.PublicationID), CustomPublicationID: optionalID(custom.PublicationID), State: string(result.State), RetainUntil: retention, Code: optionalText(string(result.Code))}
		if result.State != enscontract.Unavailable {
			input.SelectedPublicationID = optionalID(result.PublicationID)
			input.Name = optionalText(result.Name)
			input.Namespace = optionalText(string(result.Source))
			input.ValidUntil = stamp(result.ValidUntil)
			if result.State == enscontract.Resolved || c.Direction == "primary" {
				input.Address = result.Address.Bytes()
			}
			if input.ValidUntil.Time.After(input.RetainUntil.Time) {
				input.RetainUntil = input.ValidUntil
			}
		}
		version, e := q.PutENSEffectivePublication(ctx, input)
		if e != nil {
			return e
		}
		if e = q.EmitENSNamesEvent(ctx, policy.TargetChainID, strconv.FormatInt(version, 10)); e != nil {
			return e
		}
	}
	return nil
}
func (r *Repository) Snapshot(ctx context.Context, chainID string, digest common.Hash) (int64, error) {
	chain, ok := unsignedNumeric(chainID)
	if !ok || chainID == "0" || digest == (common.Hash{}) {
		return 0, ErrInvalidInput
	}
	var snapshot int64
	err := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		var e error
		snapshot, e = q.GetENSEffectiveSnapshot(ctx, chain, digest.Bytes())
		return e
	})
	return snapshot, collectionError(err)
}
func (r *Repository) LookupEffective(ctx context.Context, input enscontract.EffectiveLookup) (enscontract.EffectiveResult, error) {
	var result enscontract.EffectiveResult
	result.State, result.Code = enscontract.Unavailable, enscontract.NotIndexed
	chain, valid := unsignedNumeric(input.TargetChainID)
	if !valid || input.TargetChainID == "0" || input.Snapshot < -1 || len(input.Key) == 0 || len(input.Key) > 512 || input.PolicyDigest == (common.Hash{}) || (input.Direction != enscontract.Forward && input.Direction != enscontract.Primary) {
		return result, ErrInvalidInput
	}
	err := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		policy, e := q.GetENSEffectivePolicy(ctx, chain, input.PolicyDigest.Bytes())
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		floor, e := q.GetENSEffectiveRetentionFloor(ctx, chain, input.PolicyDigest.Bytes())
		if e != nil {
			return e
		}
		if input.Snapshot >= 0 && input.Snapshot < floor {
			result.Code = enscontract.Stale
			return nil
		}
		row, e := q.ReadENSEffectivePublication(ctx, dbgen.ReadENSEffectivePublicationParams{TargetChainID: chain, PolicyDigest: input.PolicyDigest.Bytes(), Direction: string(input.Direction), LookupKey: input.Key, Snapshot: input.Snapshot})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		result.Version = row.ID
		if !time.Now().Before(row.RetainUntil.Time) {
			result.Code = enscontract.Stale
			return nil
		}
		// A retained snapshot never selects newly published source results. Each
		// selected immutable version must still satisfy current dependency fences.
		official := enscontract.Result{State: enscontract.Unavailable, Code: enscontract.NotIndexed}
		custom := official
		if row.OfficialPublicationID != nil {
			official, e = sourcePublication(ctx, q, policy.OfficialSourceID, row.OfficialPublicationID, row.Direction, row.LookupKey, policy.CoinType)
			if e != nil {
				return e
			}
		}
		if row.CustomPublicationID != nil {
			custom, e = sourcePublication(ctx, q, policy.CustomSourceID, row.CustomPublicationID, row.Direction, row.LookupKey, policy.CustomCoinType)
			if e != nil {
				return e
			}
		}
		selected := selectEffective(policy, official, custom)
		if row.SelectedPublicationID == nil {
			result.State = enscontract.Unavailable
			if row.Code != nil {
				result.Code = enscontract.Code(*row.Code)
			}
			return nil
		}
		if selected.State == enscontract.Unavailable {
			result.Result = selected
			return nil
		}
		if selected.PublicationID != *row.SelectedPublicationID || !time.Now().Before(row.ValidUntil.Time) {
			result.Code = enscontract.Stale
			return nil
		}
		result.Result = selected
		if row.ValidUntil.Time.Before(result.ValidUntil) {
			result.ValidUntil = row.ValidUntil.Time
		}
		return nil
	})
	return result, collectionError(err)
}
