//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ensread"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestENSSyncReverseResultDiscoveryAndSearch(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.cover(t, f.block, 1)
	f.lookup.Direction = enscontract.Primary
	f.lookup.Key = strings.ToLower(testAddress(102).Hex())
	f.discover(t)
	f.enqueue(t, 3)
	job := f.claim(t)
	input := f.publication(job)
	name := "offchain.eth" // No plaintext event or explicit import for this name.
	input.Name = &name
	input.ReverseResolver = f.deployment.Bytes()
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Mode: "official_first", Digest: testHash(160090), OfficialSourceID: f.source}
	if err := f.repo.RegisterPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	core, err := store.NewPostgresRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.ConfigureIndex(ctx, "31337", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(160091), common.Hash{}, testHash(160092), "reverse-discovery")
	if _, err = core.CommitCanonicalSegment(ctx, "31337", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	names, err := ensread.New(&ensReadOnlyDB{Pool: f.db}, ensread.Options{ChainID: 31337, Policy: policy.Digest, MaxBatch: 100, SnapshotTTL: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337, NameResolver: names, NamePolicy: policy.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Publish(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE source_id=$1 AND direction='forward' AND lookup_key=$2 AND coin_type=60`, 1, f.source, name)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidate_origins WHERE kind='reverse_result'`, 1)
	// Discovery does not turn a primary publication into a forward answer.
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_publications p JOIN ens_sync_candidates c ON c.id=p.candidate_id WHERE c.direction='forward'`, 0)
	if _, err = names.ResolveForward(ctx, name); err == nil {
		t.Fatal("reverse discovery published an unverified forward answer")
	}
	var forwardID, generation int64
	if err = f.db.QueryRow(ctx, `SELECT id,requested_generation FROM ens_sync_candidates WHERE direction='forward' AND lookup_key=$1`, name).Scan(&forwardID, &generation); err != nil {
		t.Fatal(err)
	}
	// A new primary publication for the same name coalesces into the same
	// discovery origin, preserving the forward job's generation and budget.
	if _, err = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, job.Generation); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	input = f.publication(f.claim(t))
	input.Name, input.ReverseResolver = &name, f.deployment.Bytes()
	if _, err = f.repo.Publish(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE id=$1 AND requested_generation=$2`, 1, forwardID, generation)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidate_origins WHERE kind='reverse_result'`, 1)
	// Only the separately leased forward job may populate exact and prefix search.
	f.candidate = forwardID
	f.enqueue(t, 3)
	input = f.publication(f.claim(t))
	input.Name = &name
	if _, err = f.repo.Publish(ctx, input); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{name, "offc"} {
		results, _, err := reader.Search(ctx, term, "", 10)
		if err != nil || len(results) != 1 || results[0].Label != name {
			t.Fatalf("search %s: %+v %v", term, results, err)
		}
	}
}

func TestENSSyncReverseDiscoveryRejectsUnpublishedResults(t *testing.T) {
	for _, scenario := range []string{"stale_lease", "negative", "invalid_name", "different_coin"} {
		t.Run(scenario, func(t *testing.T) {
			f := newENSSyncFixture(t)
			ctx := t.Context()
			f.cover(t, f.block, 1)
			f.lookup.Direction = enscontract.Primary
			f.lookup.Key = strings.ToLower(testAddress(102).Hex())
			if scenario == "different_coin" {
				f.lookup.CoinType = "2147514985"
			}
			f.discover(t)
			f.enqueue(t, 3)
			job := f.claim(t)
			input := f.publication(job)
			input.ReverseResolver = f.deployment.Bytes()
			switch scenario {
			case "stale_lease":
				execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, job.ID)
			case "negative":
				input.Outcome, input.Name = "not_found", nil
				input.Resolver, input.ReverseResolver = nil, nil
			case "invalid_name":
				name := "INVALID.eth"
				input.Name = &name
			}
			_, err := f.repo.Publish(ctx, input)
			switch scenario {
			case "stale_lease":
				if !errors.Is(err, enssync.ErrFenceLost) {
					t.Fatal(err)
				}
			case "invalid_name":
				if !errors.Is(err, enssync.ErrInvalidInput) {
					t.Fatal(err)
				}
				assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_publications`, 0)
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "different_coin" {
				assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND source_id=$1 AND coin_type=2147514985`, 1, f.source)
			} else {
				assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward'`, 0)
				assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidate_origins WHERE kind='reverse_result'`, 0)
			}
		})
	}
}
