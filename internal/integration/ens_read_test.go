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

func TestENSDatabaseOnlyReadSnapshotAndMissingName(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.cover(t, f.block, 1)
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Mode: "official_first", Digest: testHash(160020), OfficialSourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	readonly := &ensReadOnlyDB{Pool: f.db}
	reader, e := ensread.New(readonly, ensread.Options{ChainID: 31337, Policy: policy.Digest, MaxBatch: 100, SnapshotTTL: 30 * time.Minute, Sources: []enscontract.Namespace{enscontract.Official}})
	if e != nil {
		t.Fatal(e)
	}
	address := testAddress(102)
	missing, emptyToken, e := reader.ResolveAddressBatch(ctx, []common.Address{address}, "")
	if e != nil || missing[0].Outcome != enscontract.Unavailable || missing[0].Code != "not_indexed" || emptyToken == "" {
		t.Fatal(missing, e)
	}
	if _, e = reader.ResolveForward(ctx, "unknown.eth"); e == nil {
		t.Fatal("unknown name became negative")
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates`, 0)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs`, 0)
	f.discover(t)
	f.enqueue(t, 3)
	job := f.claim(t)
	if _, e = f.repo.Publish(ctx, f.publication(job)); e != nil {
		t.Fatal(e)
	}
	forward, e := reader.ResolveForward(ctx, "ALICE.eth")
	if e != nil || forward.Outcome != enscontract.Resolved || forward.Address != address || forward.ValidUntil.IsZero() {
		t.Fatal(forward, e)
	}
	f.candidate = 0
	f.lookup.Direction = enscontract.Primary
	f.lookup.Key = strings.ToLower(address.Hex())
	f.discover(t)
	f.enqueue(t, 3)
	job = f.claim(t)
	publication := f.publication(job)
	publication.ReverseResolver = f.deployment.Bytes()
	if _, e = f.repo.Publish(ctx, publication); e != nil {
		t.Fatal(e)
	}
	current, token, e := reader.ResolveAddressBatch(ctx, []common.Address{address, testAddress(160021)}, "")
	if e != nil || current[0].Outcome != enscontract.Resolved || current[0].ValidUntil.IsZero() || current[1].Outcome != enscontract.Unavailable || current[0].SnapshotVersion != current[1].SnapshotVersion {
		t.Fatal(current, e)
	}
	empty, _, e := reader.ResolveAddressBatch(ctx, []common.Address{address}, emptyToken)
	if e != nil || empty[0].Outcome != enscontract.Unavailable {
		t.Fatalf("empty snapshot switched: %+v %v", empty, e)
	}
	reused, next, e := reader.ResolveAddressBatch(ctx, []common.Address{address}, token)
	if e != nil || next != token || reused[0].ObservationID != current[0].ObservationID {
		t.Fatal(reused, e)
	}
	if _, e = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1); e != nil {
		t.Fatal(e)
	}
	if _, _, e = reader.ResolveAddressBatch(ctx, []common.Address{address}, token); !errors.Is(e, enscontract.ErrSnapshotInvalid) {
		t.Fatalf("dirty snapshot survived: %v", e)
	}
	statuses, e := reader.NameStatus(ctx)
	if e != nil || len(statuses) != 1 || statuses[0].Scanned == nil || *statuses[0].Scanned != "100" || statuses[0].State != "resolution_pending" {
		t.Fatal(statuses, e)
	}
}

func TestENSPublishedSearchFiltersDirtyVersions(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.cover(t, f.block, 1)
	core, e := store.NewPostgresRepository(f.db)
	if e != nil {
		t.Fatal(e)
	}
	if e = core.ConfigureIndex(ctx, "31337", 0); e != nil {
		t.Fatal(e)
	}
	genesis := testBundle(0, testHash(160030), common.Hash{}, testHash(160031), "ens-search-genesis")
	if _, e = core.CommitCanonicalSegment(ctx, "31337", []chainbundle.Bundle{genesis}); e != nil {
		t.Fatal(e)
	}
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Mode: "official_first", Digest: testHash(160032), OfficialSourceID: f.source}
	if e = f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	names, e := ensread.New(&ensReadOnlyDB{Pool: f.db}, ensread.Options{ChainID: 31337, Policy: policy.Digest, MaxBatch: 100, SnapshotTTL: 30 * time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	reader, e := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337, NameResolver: names, NamePolicy: policy.Digest})
	if e != nil {
		t.Fatal(e)
	}
	f.discover(t)
	f.enqueue(t, 3)
	job := f.claim(t)
	if _, e = f.repo.Publish(ctx, f.publication(job)); e != nil {
		t.Fatal(e)
	}
	for _, term := range []string{"alice.eth", "ali"} {
		results, _, e := reader.Search(ctx, term, "", 10)
		if e != nil || len(results) != 1 || results[0].Label != "alice.eth" {
			t.Fatalf("%s: %+v %v", term, results, e)
		}
	}
	// Disabling ENS removes the name reader but may retain the configured policy
	// and already-published data. Partial search must respect the same boundary.
	disabled, e := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337, NamePolicy: policy.Digest})
	if e != nil {
		t.Fatal(e)
	}
	if results, _, e := disabled.Search(ctx, "ali", "", 10); e != nil || len(results) != 0 {
		t.Fatalf("disabled ENS exposed published names: %+v %v", results, e)
	}
	if _, e = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1); e != nil {
		t.Fatal(e)
	}
	results, _, e := reader.Search(ctx, "ali", "", 10)
	if e != nil || len(results) != 0 {
		t.Fatal(results, e)
	}
	if _, _, e = reader.Search(ctx, "alice.eth", "", 10); e == nil {
		t.Fatal("dirty dotted name became empty success")
	}
}
