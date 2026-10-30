//go:build integration

package integration_test

import (
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"
	"math/big"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
)

func TestENSEffectivePublicationAtomicSnapshotAndWithdrawal(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	lookup := enscontract.EffectiveLookup{TargetChainID: policy.TargetChainID, PolicyDigest: policy.Digest, Direction: enscontract.Forward, Key: f.lookup.Key, Snapshot: -1}
	before, e := f.repo.Snapshot(ctx, policy.TargetChainID, policy.Digest)
	if e != nil || before != 0 {
		t.Fatal(before, e)
	}
	f.enqueue(t, 3)
	job := f.claim(t)
	if _, e = f.repo.Publish(ctx, f.publication(job)); e != nil {
		t.Fatal(e)
	}
	snapshot, e := f.repo.Snapshot(ctx, policy.TargetChainID, policy.Digest)
	if e != nil || snapshot <= before {
		t.Fatal(snapshot, e)
	}
	database := &ensReadOnlyDB{Pool: f.db}
	reader, e := enssync.NewRepository(database)
	if e != nil {
		t.Fatal(e)
	}
	lookup.Snapshot = snapshot
	got, e := reader.LookupEffective(ctx, lookup)
	if e != nil || got.State != enscontract.Resolved || got.Version != snapshot || got.Name != f.lookup.Key {
		t.Fatalf("effective %+v %v", got, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_effective_publications`, 1)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM search_catalog_documents WHERE name_publication_id=$1 AND ens_effective_publication_current(name_publication_id)`, 1, snapshot)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='names' AND jsonb_typeof(payload->'publication')='string'`, 1)
	lookup.Snapshot = before
	empty, e := reader.LookupEffective(ctx, lookup)
	if e != nil || empty.State != enscontract.Unavailable {
		t.Fatalf("empty snapshot switched versions %+v %v", empty, e)
	}
	if _, e = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1); e != nil {
		t.Fatal(e)
	}
	lookup.Snapshot = snapshot
	got, e = reader.LookupEffective(ctx, lookup)
	if e != nil || got.State != enscontract.Unavailable || got.Name != "" {
		t.Fatalf("dirty snapshot revived %+v %v", got, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM search_catalog_documents WHERE name_publication_id=$1 AND ens_effective_publication_current(name_publication_id)`, 0, snapshot)
	// Force outbox failure: source observation, effective row and job completion
	// must all roll back, retaining the live lease for a safe retry.
	execFixture(t, ctx, f.db, `CREATE FUNCTION reject_ens_test_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='names' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$`)
	execFixture(t, ctx, f.db, `CREATE TRIGGER ens_test_notice BEFORE INSERT ON runtime_events FOR EACH ROW EXECUTE FUNCTION reject_ens_test_notice()`)
	f.enqueue(t, 3)
	job = f.claim(t)
	if _, e = f.repo.Publish(ctx, f.publication(job)); e == nil {
		t.Fatal("outbox failure accepted")
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_observations`, 1)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_effective_publications`, 1)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs WHERE id=$1 AND state='leased'`, 1, job.ID)
}

func TestENSEffectiveHousekeepingPreservesActiveDependenciesAndSnapshotFloor(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	var snapshots []int64
	for i := range 3 {
		if i > 0 {
			if _, e := f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, int64(i)); e != nil {
				t.Fatal(e)
			}
		}
		f.enqueue(t, 3)
		job := f.claim(t)
		if _, e := f.repo.Publish(ctx, f.publication(job)); e != nil {
			t.Fatal(e)
		}
		snapshot, e := f.repo.Snapshot(ctx, policy.TargetChainID, policy.Digest)
		if e != nil {
			t.Fatal(e)
		}
		snapshots = append(snapshots, snapshot)
	}
	// Catalog retention owns removal of closed search documents. ENS cleanup
	// must not break a still-retained document or active projection dependency.
	future := pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true}
	chain := pgtype.Numeric{Int: big.NewInt(31337), Valid: true}
	deleted, e := f.q.DeleteExpiredENSEffectivePublications(ctx, chain, future, 1)
	if e != nil || deleted != 0 {
		t.Fatal(deleted, e)
	}
	execFixture(t, ctx, f.db, `DELETE FROM search_catalog_documents WHERE valid_to_generation IS NOT NULL`)
	deleted, e = f.q.DeleteExpiredENSEffectivePublications(ctx, chain, future, 1)
	if e != nil || deleted != 1 {
		t.Fatal(deleted, e)
	}
	floor, e := f.q.GetENSEffectiveRetentionFloor(ctx, chain, policy.Digest.Bytes())
	if e != nil || floor != snapshots[0]+1 {
		t.Fatal(floor, e)
	}
	got, e := f.repo.LookupEffective(ctx, enscontract.EffectiveLookup{TargetChainID: "31337", PolicyDigest: policy.Digest, Direction: enscontract.Forward, Key: f.lookup.Key, Snapshot: snapshots[0]})
	if e != nil || got.Code != enscontract.Stale {
		t.Fatal(got, e)
	}
	deleted, e = f.q.DeleteExpiredENSSyncObservations(ctx, chain, future, 1)
	if e != nil || deleted != 1 {
		t.Fatal(deleted, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_observations`, 2)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_publications`, 2)
	got, e = f.repo.LookupEffective(ctx, enscontract.EffectiveLookup{TargetChainID: "31337", PolicyDigest: policy.Digest, Direction: enscontract.Forward, Key: f.lookup.Key, Snapshot: snapshots[2]})
	if e != nil || got.State != enscontract.Resolved {
		t.Fatal(got, e)
	}
}

func TestENSProgressEventsPreserveValidNamesAndRealInvalidationWithdraws(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if err := f.repo.RegisterPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	job := f.claim(t)
	if _, err := f.repo.Publish(ctx, f.publication(job)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.repo.Snapshot(ctx, policy.TargetChainID, policy.Digest)
	if err != nil {
		t.Fatal(err)
	}
	lookup := enscontract.EffectiveLookup{TargetChainID: policy.TargetChainID, PolicyDigest: policy.Digest, Direction: enscontract.Forward, Key: f.lookup.Key, Snapshot: snapshot}
	assertCurrent := func() {
		t.Helper()
		got, err := f.repo.LookupEffective(ctx, lookup)
		if err != nil || got.State != enscontract.Resolved || got.Version != snapshot || got.Name != f.lookup.Key {
			t.Fatalf("progress withdrew valid observation: %+v %v", got, err)
		}
	}
	parent := f.block
	for i := uint64(1); i <= 3; i++ {
		hash := testHash(100 + i)
		f.putBlock(t, fmt.Sprint(100+i), hash, parent)
		for range 2 { // Same-head health refresh must not emit extra notifications.
			n, err := f.q.SetENSSyncTarget(ctx, ensTime(time.Now().Add(time.Hour)), f.source, hash.Bytes())
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			assertCurrent()
		}
		f.cover(t, hash, 1)
		assertCurrent()
		parent = hash
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='names' AND payload ? 'invalidated'`, 0)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='status' AND payload->>'reason'='names_progress' AND payload->>'source'=$1`, 6, fmt.Sprint(f.source))
	if _, err := f.q.InvalidateENSSyncSource(ctx, f.source, 1); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.LookupEffective(ctx, lookup)
	if err != nil || got.State != enscontract.Unavailable || got.Name != "" {
		t.Fatalf("source invalidation retained name: %+v %v", got, err)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='names' AND payload->>'invalidated'='true'`, 1)
	execFixture(t, ctx, f.db, `UPDATE ens_sync_progress SET state='unsupported' WHERE source_id=$1`, f.source)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM runtime_events WHERE event_type='names' AND payload->>'invalidated'='true'`, 2)
}
