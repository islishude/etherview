//go:build integration

package integration_test

import (
	"github.com/islishude/etherview/internal/enssync"
	"testing"
)

func TestENSOperatorImportReplayBoundsAndIsolation(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	scope := enssync.OperationScope{SourceID: f.source, ChainID: policy.TargetChainID, Policy: policy.Digest}
	values := []string{"Alice.eth", "alice.eth", testAddress(800).Hex(), "bob.eth"}
	for range 2 {
		if n, e := f.repo.ImportCandidates(ctx, scope, "batch-1", values); e != nil || n != 3 {
			t.Fatal(n, e)
		}
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE coin_type=60`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_imports`, 1)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE requested_generation=1 AND minimum_generation=1`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs`, 0)
	if _, e := f.repo.ImportCandidates(ctx, scope, "batch-1", []string{"changed.eth"}); e == nil {
		t.Fatal("changed idempotent batch accepted")
	}
	wrong := scope
	wrong.ChainID = "1"
	if _, e := f.repo.ImportCandidates(ctx, wrong, "wrong", values); e == nil {
		t.Fatal("wrong chain accepted")
	}
	if _, e := f.repo.ReplayCandidates(ctx, scope, "invalid", 0, 1001, "too many"); e == nil {
		t.Fatal("unbounded replay accepted")
	}
	first, e := f.repo.ReplayCandidates(ctx, scope, "page-one", 0, 2, "operator retry")
	if e != nil || first.Count != 2 || first.Through == 0 {
		t.Fatal(first, e)
	}
	again, e := f.repo.ReplayCandidates(ctx, scope, "page-one", 0, 2, "operator retry")
	if e != nil || again != first {
		t.Fatal(again, e)
	}
	if _, e = f.repo.ReplayCandidates(ctx, scope, "page-one", 0, 1, "operator retry"); e == nil {
		t.Fatal("changed replay batch accepted")
	}
	next, e := f.repo.ReplayCandidates(ctx, scope, "page-two", first.Through, 2, "operator retry")
	if e != nil || next.Count != 2 || next.Through <= first.Through {
		t.Fatal(next, e)
	}
	last, e := f.repo.ReplayCandidates(ctx, scope, "page-three", next.Through, 2, "operator retry")
	if e != nil || last.Count != 1 {
		t.Fatal(last, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE requested_generation=2 AND minimum_generation=2`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_replays`, 3)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='names' AND payload ? 'invalidated'`, 3)
}
