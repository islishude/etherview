//go:build integration

package integration_test

import (
	"github.com/islishude/etherview/internal/store"
	"testing"
)

func TestAnalyticsReconciliationBoundsAndResumesWithoutOverwritingNewPublication(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	first := testBundle(0, testHash(170000), testHash(0), testHash(171000), "reconcile-first")
	commitCanonical(t, ctx, repository, first)
	// Simulate a consistent projection of identities that no longer exist. A
	// reconciliation batch must withdraw no more than its 1,000 identity budget.
	execFixture(t, ctx, db, `INSERT INTO chart_source_blocks(chain_id,block_number,block_hash,ready) SELECT 1,n,decode(repeat('aa',32),'hex'),false FROM generate_series(1,2000) n`)
	execFixture(t, ctx, db, `UPDATE chart_source_totals SET total_blocks=total_blocks+2000 WHERE chain_id=1`)
	assertProgress := func(count, cursor int) {
		t.Helper()
		var total, projected, next int
		if err := db.QueryRow(ctx, `SELECT total_blocks,(SELECT count(*) FROM chart_source_blocks WHERE chain_id=1),(SELECT next_block FROM chart_source_reconciliation WHERE chain_id=1) FROM chart_source_totals WHERE chain_id=1`).Scan(&total, &projected, &next); err != nil || total != count || projected != count || next != cursor {
			t.Fatalf("total=%d projected=%d cursor=%d expected=%d/%d err=%v", total, projected, next, count, cursor, err)
		}
	}
	execFixture(t, ctx, db, `SELECT reconcile_chart_sources(1)`)
	assertProgress(1002, 1000)
	execFixture(t, ctx, db, `SELECT reconcile_chart_sources(1)`)
	assertProgress(1002, 1000) // The next call before 15 minutes performs no scan.
	second := testBundle(1, testHash(170001), first.Block.Hash(), testHash(171001), "reconcile-next")
	commitCanonical(t, ctx, repository, second)
	execFixture(t, ctx, db, `UPDATE chart_source_reconciliation SET next_at=now()-interval '1 second' WHERE chain_id=1`)
	execFixture(t, ctx, db, `SELECT reconcile_chart_sources(1)`)
	assertProgress(3, 2000)
	execFixture(t, ctx, db, `UPDATE chart_source_reconciliation SET next_at=now()-interval '1 second' WHERE chain_id=1`)
	execFixture(t, ctx, db, `SELECT reconcile_chart_sources(1)`)
	assertProgress(2, 0)
	var hash []byte
	if err := db.QueryRow(ctx, `SELECT block_hash FROM chart_source_blocks WHERE chain_id=1 AND block_number=1`).Scan(&hash); err != nil || string(hash) != string(second.Block.Hash().Bytes()) {
		t.Fatalf("new publication overwritten: %v", err)
	}
}
