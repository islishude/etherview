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
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	readonly := &ensReadOnlyDB{Pool: f.db}
	reader, e := ensread.New(readonly, ensread.Options{ChainID: 31337, Policy: policy.Digest, MaxBatch: 100, SnapshotTTL: 30 * time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	address := testAddress(102)
	missing, emptyToken, e := reader.ResolveAddressBatch(ctx, []common.Address{address}, "")
	if e != nil || missing[0].Outcome != enscontract.Unavailable || missing[0].Code != "not_indexed" || emptyToken == "" {
		t.Fatal(missing, e)
	}
	if detail, err := reader.NameDetail(ctx, "unknown.eth"); err != nil || detail.State != "not_indexed" {
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
	forward, e := reader.NameDetail(ctx, "ALICE.eth")
	if e != nil || forward.ResolvedAddress.State != "value" || forward.ResolvedAddress.Value != address.Hex() || forward.ResolutionObservation == nil {
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
	// Status compares progress with current core facts even before the next
	// collection round. A new head never turns a valid publication into coverage.
	core, e := store.NewPostgresRepository(f.db)
	if e != nil {
		t.Fatal(e)
	}
	if e = core.ConfigureIndex(ctx, "31337", 100); e != nil {
		t.Fatal(e)
	}
	nextBlock := testBundle(101, testHash(160022), f.block, testHash(160023), "ens-status-next")
	if _, e = core.CommitCanonicalSegment(ctx, "31337", []chainbundle.Bundle{nextBlock}); e != nil {
		t.Fatal(e)
	}
	statuses, e = reader.NameStatus(ctx)
	if e != nil || statuses[0].Coverage != "incomplete" || statuses[0].Target == nil || *statuses[0].Target != "101" || *statuses[0].Scanned != "100" {
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
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e = f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	reader, e := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337, NamePolicy: policy.Digest})
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
	// API assembly only supplies a name policy when ENS is enabled. Retained
	// database publications must not enable name search by themselves.
	disabled, e := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337})
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
	if e != nil || len(results) != 1 || results[0].Kind != "name" || results[0].NameDetails == nil || results[0].NameDetails.ResolvedAddress.State != "pending" {
		t.Fatalf("known name lost with stale resolution: %+v %v", results, e)
	}
	if results, _, e = reader.Search(ctx, "alice.eth", "", 10); e != nil || len(results) != 1 {
		t.Fatalf("exact known name: %+v %v", results, e)
	}
}
