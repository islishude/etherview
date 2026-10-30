//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ensSyncFixture struct {
	db         *pgxpool.Pool
	q          *dbgen.Queries
	repo       *enssync.Repository
	source     int64
	candidate  int64
	input      dbgen.CreateENSSyncSourceParams
	lookup     enscontract.Lookup
	block      common.Hash
	deployment common.Address
}

func ensNumber(value string) pgtype.Numeric      { var n pgtype.Numeric; _ = n.Scan(value); return n }
func ensTime(value time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: value, Valid: true} }

func newENSSyncFixture(t *testing.T) *ensSyncFixture {
	t.Helper()
	db := newMigratedPostgres(t)
	execFixture(t, t.Context(), db, `INSERT INTO chains(chain_id) VALUES (31337), (31338)`)
	repo, err := enssync.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	f := &ensSyncFixture{db: db, q: dbgen.New(db), repo: repo, block: testHash(100), deployment: testAddress(101)}
	f.input = dbgen.CreateENSSyncSourceParams{TargetChainID: ensNumber("31337"), SourceChainID: ensNumber("31337"),
		GenesisHash: testHash(1).Bytes(), PolicyDigest: testHash(2).Bytes(), ManifestDigest: testHash(3).Bytes(),
		StartNumber: ensNumber("100"), StartHash: f.block.Bytes()}
	f.source, err = repo.CreateSource(t.Context(), f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.putBlock(t, "100", f.block, testHash(99))
	execFixture(t, t.Context(), f.db, `UPDATE chains SET genesis_hash=$2 WHERE chain_id=$1`, f.input.SourceChainID, f.input.GenesisHash)

	if err := f.q.PutENSSyncDeployment(t.Context(), dbgen.PutENSSyncDeploymentParams{SourceID: f.source,
		Address: f.deployment.Bytes(), Protocol: "v1", BlockHash: f.block.Bytes(), CodeHash: testHash(4).Bytes(), AbiDigest: testHash(5).Bytes()}); err != nil {
		t.Fatal(err)
	}
	n, err := f.q.SetENSSyncTarget(t.Context(), ensTime(time.Now().Add(time.Hour)), f.source, f.block.Bytes())
	if err != nil || n != 1 {
		t.Fatalf("target: %d %v", n, err)
	}
	f.lookup = enscontract.Lookup{SourceID: f.source, TargetChainID: "31337", PolicyDigest: testHash(2), Direction: enscontract.Forward, Key: "alice.eth", CoinType: "60"}
	return f
}

func (f *ensSyncFixture) putBlock(t *testing.T, number string, hash, parent common.Hash) {
	t.Helper()
	execFixture(t, t.Context(), f.db, `INSERT INTO blocks(chain_id,number,hash,parent_hash,timestamp,raw) VALUES($1,$2,$3,$4,100,'{}') ON CONFLICT DO NOTHING`, f.input.SourceChainID, number, hash.Bytes(), parent.Bytes())
	execFixture(t, t.Context(), f.db, `INSERT INTO canonical_blocks(chain_id,number,block_hash) VALUES($1,$2,$3) ON CONFLICT(chain_id,number) DO UPDATE SET block_hash=EXCLUDED.block_hash`, f.input.SourceChainID, number, hash.Bytes())
	var timestamp int64
	if err := f.db.QueryRow(t.Context(), `SELECT timestamp::bigint FROM blocks WHERE chain_id=$1 AND hash=$2`, f.input.SourceChainID, hash.Bytes()).Scan(&timestamp); err != nil {
		t.Fatal(err)
	}
	n, err := f.q.PutENSSyncBlock(t.Context(), dbgen.PutENSSyncBlockParams{SourceID: f.source,
		Number: ensNumber(number), Hash: hash.Bytes(), ParentHash: parent.Bytes(), BlockTime: ensTime(time.Unix(timestamp, 0)), Canonical: true})
	if err != nil || n != 1 {
		t.Fatalf("block: %d %v", n, err)
	}
}

func (f *ensSyncFixture) discover(t *testing.T) {
	t.Helper()
	c, err := f.q.EnsureENSSyncCandidate(t.Context(), dbgen.EnsureENSSyncCandidateParams{SourceID: f.source,
		Direction: string(f.lookup.Direction), LookupKey: f.lookup.Key, CoinType: ensNumber(f.lookup.CoinType), OriginKind: "import", OriginIdentity: "batch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if f.candidate != 0 && f.candidate != c.ID {
		t.Fatal("candidate identity changed")
	}
	f.candidate = c.ID
}

func (f *ensSyncFixture) cover(t *testing.T, hash common.Hash, expected int64) {
	t.Helper()
	n, err := f.q.AdvanceENSSyncCoverage(t.Context(), f.source, hash.Bytes(), 1)
	if err != nil || n != expected {
		t.Fatalf("coverage: %d want %d: %v", n, expected, err)
	}
}

func (f *ensSyncFixture) enqueue(t *testing.T, attempts int32) int64 {
	t.Helper()
	n, err := f.q.EnqueueENSSyncJob(t.Context(), dbgen.EnqueueENSSyncJobParams{SourceID: f.source, CandidateID: f.candidate, MaxAttempts: attempts, Deadline: ensTime(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *ensSyncFixture) claim(t *testing.T) dbgen.EnsSyncJob {
	t.Helper()
	job, err := f.repo.Claim(t.Context(), f.source, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *ensSyncFixture) publication(job dbgen.EnsSyncJob) dbgen.PublishENSSyncObservationParams {
	now := time.Now().Add(-time.Second)
	name := "alice.eth"
	return dbgen.PublishENSSyncObservationParams{JobID: job.ID, SourceID: f.source, CandidateID: f.candidate,
		LeaseToken: job.LeaseToken, Generation: job.Generation, Deployment: f.deployment.Bytes(),
		Endpoint: "mainnet_a", Evidence: "resolver", Outcome: "resolved", Name: &name,
		Address: testAddress(102).Bytes(), Resolver: f.deployment.Bytes(), ObservedAt: ensTime(now),
		ValidUntil: ensTime(now.Add(time.Minute)), RefreshAt: ensTime(now.Add(30 * time.Second)), RetainUntil: ensTime(now.Add(time.Hour))}
}

func (f *ensSyncFixture) requireCode(t *testing.T, code enscontract.Code) {
	t.Helper()
	got, err := f.repo.Lookup(t.Context(), f.lookup)
	if err != nil || got.State != enscontract.Unavailable || got.Code != code || got.Name != "" || got.PublicationID != 0 {
		t.Fatalf("read: %+v %v; want unavailable/%s", got, err, code)
	}
}

func TestENSSyncIdentityCoverageAndIdempotency(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	id, err := f.repo.CreateSource(ctx, f.input)
	if err != nil || id != f.source {
		t.Fatalf("source retry: %d %v", id, err)
	}
	changed := f.input
	changed.ManifestDigest = testHash(55).Bytes()
	if _, err := f.repo.CreateSource(ctx, changed); err == nil {
		t.Fatal("manifest changed without new policy")
	}
	for _, mutate := range []func(*dbgen.CreateENSSyncSourceParams){
		func(p *dbgen.CreateENSSyncSourceParams) {
			p.TargetChainID = ensNumber("31338")
			p.SourceChainID = ensNumber("31338")
		},
		func(p *dbgen.CreateENSSyncSourceParams) { p.GenesisHash = testHash(56).Bytes() },
		func(p *dbgen.CreateENSSyncSourceParams) { p.PolicyDigest = testHash(57).Bytes() },
		func(p *dbgen.CreateENSSyncSourceParams) {
			p.PolicyDigest = testHash(58).Bytes()
		},
	} {
		input := f.input
		mutate(&input)
		other, err := f.repo.CreateSource(ctx, input)
		if err != nil || other == f.source {
			t.Fatalf("source isolation: %d %v", other, err)
		}
		if n, err := f.q.PutENSSyncEvent(ctx, other, f.block.Bytes(), 0); err == nil && n != 0 {
			t.Fatal("invented event accepted")
		}
	}
	f.requireCode(t, enscontract.NotIndexed)
	f.discover(t)
	f.discover(t)
	f.requireCode(t, enscontract.ResolutionPending)
	f.putBlock(t, "102", testHash(102), testHash(101))
	_, err = f.q.SetENSSyncTarget(ctx, ensTime(time.Now().Add(time.Hour)), f.source, testHash(102).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	f.cover(t, testHash(102), 0) // Missing initial history cannot become coverage.
	f.cover(t, f.block, 1)
	f.cover(t, f.block, 1)
	f.cover(t, testHash(102), 0) // Cannot skip a middle block.
	f.discover(t)
	f.enqueue(t, 3)
	job := f.claim(t)
	negative := f.publication(job)
	negative.Outcome = "not_found"
	negative.Address = nil
	negative.Resolver = nil
	publication, err := f.repo.Publish(ctx, negative)
	if err != nil {
		t.Fatalf("exact negative blocked by coverage: %v", err)
	}
	f.putBlock(t, "101", testHash(101), f.block)
	f.cover(t, testHash(101), 1)
	f.cover(t, testHash(102), 1)
	got, err := f.repo.Lookup(ctx, f.lookup)
	if err != nil || got.State != enscontract.NotFound || got.PublicationID != publication {
		t.Fatalf("definitive negative: %+v %v", got, err)
	}
	if n, err := f.q.PutENSSyncEvent(ctx, f.source, f.block.Bytes(), 0); err != nil || n != 0 {
		t.Fatalf("ENS must not invent a core log: %d %v", n, err)
	}
	execFixture(t, ctx, f.db, `DELETE FROM canonical_blocks WHERE chain_id=$1 AND block_hash=$2`, f.input.SourceChainID, testHash(102).Bytes())
	f.requireCode(t, enscontract.ResolutionPending)
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM ens_sync_events WHERE source_id=$1`, f.source).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained events: %d %v", count, err)
	}
}

func TestENSSyncPublicationLeaseReplayAndProjectionFences(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	if f.enqueue(t, 3) != 1 || f.enqueue(t, 100) != 0 {
		t.Fatal("enqueue not idempotent")
	}
	first := f.claim(t)
	execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID)
	second := f.claim(t)
	if second.ID != first.ID || second.Attempts != 2 || second.MaxAttempts != 3 || second.LeaseToken == first.LeaseToken {
		t.Fatal("lease takeover changed retry identity")
	}
	if _, err := f.repo.Publish(ctx, f.publication(first)); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("stale lease: %v", err)
	}
	publication, err := f.repo.Publish(ctx, f.publication(second))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.Lookup(ctx, f.lookup)
	if err != nil || got.State != enscontract.Resolved || got.PublicationID != publication || got.Name != "alice.eth" {
		t.Fatalf("publication: %+v %v", got, err)
	}
	for _, statement := range []string{
		`UPDATE ens_sync_observations SET name='evil.eth' WHERE source_id=$1`,
		`UPDATE ens_sync_publications SET retain_until=retain_until+interval '1 hour' WHERE source_id=$1`,
		`UPDATE ens_sync_blocks SET parent_hash=decode(repeat('ff',32),'hex') WHERE source_id=$1`,
	} {
		if _, err := f.db.Exec(ctx, statement, f.source); err == nil {
			t.Fatal("immutable evidence changed")
		}
	}
	// An exact snapshot never falls through to a newer or unrelated publication.
	f.lookup.PublicationID = publication + 999
	f.requireCode(t, enscontract.ResolutionPending)
	f.lookup.PublicationID = publication
	f.lookup.PolicyDigest = testHash(999)
	f.requireCode(t, enscontract.NotIndexed)
	f.lookup.PolicyDigest = testHash(2)
	f.lookup.TargetChainID = "31338"
	f.requireCode(t, enscontract.NotIndexed)
	f.lookup.TargetChainID = "31337"
	generation, err := f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1)
	if err != nil || generation != 2 {
		t.Fatalf("invalidate: %d %v", generation, err)
	}
	f.requireCode(t, enscontract.ResolutionPending)
	f.enqueue(t, 3)
	third := f.claim(t)
	f.bindProjection(t, third, "resolver_record", "shared-bundle")
	revision, err := f.q.PutENSSyncProjection(ctx, dbgen.PutENSSyncProjectionParams{SourceID: f.source, ExpectedRevision: 2,
		Kind: "resolver_record", Key: "shared-bundle", Deployment: f.deployment.Bytes(), BlockHash: f.block.Bytes(), Payload: []byte(`{"deleted":true}`)})
	if err != nil || revision != 3 {
		t.Fatalf("projection: %d %v", revision, err)
	}
	if _, err := f.repo.Publish(ctx, f.publication(third)); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("stale projection: %v", err)
	}
	// A new authorized generation may observe different dynamic data at the same block.
	_, err = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	fourth := f.claim(t)
	changed := f.publication(fourth)
	changed.Address = testAddress(103).Bytes()
	next, err := f.repo.Publish(ctx, changed)
	if err != nil || next == publication {
		t.Fatalf("version: %d %v", next, err)
	}
	f.requireCode(t, enscontract.ResolutionPending) // Old exact snapshot remains invalid.
	confirmed, err := f.q.ConfirmENSSyncPublication(ctx, dbgen.ConfirmENSSyncPublicationParams{SourceID: f.source,
		JobID: second.ID, Generation: second.Generation, LeaseToken: second.LeaseToken})
	if err != nil || confirmed != publication {
		t.Fatalf("superseded commit confirmation: %d %v", confirmed, err)
	}
	f.lookup.PublicationID = 0
	got, err = f.repo.Lookup(ctx, f.lookup)
	if err != nil || got.Address != testAddress(103) {
		t.Fatalf("new observation: %+v %v", got, err)
	}
	execFixture(t, ctx, f.db, `UPDATE ens_sync_progress SET healthy_until=clock_timestamp()-interval '1 second' WHERE source_id=$1`, f.source)
	got, err = f.repo.Lookup(ctx, f.lookup)
	if err != nil || got.State != enscontract.Resolved {
		t.Fatalf("collection health revoked exact result: %+v %v", got, err)
	}
}

func TestENSSyncExhaustionAndAtomicFailure(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	f.enqueue(t, 1)
	job := f.claim(t)
	bad := f.publication(job)
	bad.Endpoint = "https://secret.invalid"
	if _, err := f.repo.Publish(ctx, bad); err == nil {
		t.Fatal("unsafe endpoint label accepted")
	}
	var state string
	if err := f.db.QueryRow(ctx, `SELECT state FROM ens_sync_jobs WHERE id=$1`, job.ID).Scan(&state); err != nil || state != "leased" {
		t.Fatalf("failed publication was not atomic: %s %v", state, err)
	}
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM ens_sync_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial observation: %d %v", count, err)
	}
	execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
	if _, err := f.repo.Claim(ctx, f.source, time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("exhausted claim: %v", err)
	}
	if f.enqueue(t, 100) != 0 {
		t.Fatal("repeated enqueue bypassed exhaustion")
	}
	if err := f.db.QueryRow(ctx, `SELECT state FROM ens_sync_jobs WHERE id=$1`, job.ID).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("durable exhaustion: %s %v", state, err)
	}
	if _, err := f.repo.Publish(ctx, f.publication(job)); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("exhausted publication: %v", err)
	}
}

func TestENSSyncConcurrentPublicationAndInvalidation(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	f.enqueue(t, 3)
	job := f.claim(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := f.repo.Publish(ctx, f.publication(job)); results <- err })
	}
	wg.Wait()
	close(results)
	successes, fenced := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, enssync.ErrFenceLost):
			fenced++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || fenced != 1 {
		t.Fatalf("concurrent publication: success=%d fenced=%d", successes, fenced)
	}
	// A projection transaction wins the source lock before a worker resumes.
	_, err := f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	job = f.claim(t)
	f.bindProjection(t, job, "registry_link", "ancestor")
	tx, err := f.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if _, err := q.LockENSSyncSource(ctx, f.source); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() { close(started); _, err := f.repo.Publish(ctx, f.publication(job)); finished <- err }()
	<-started
	_, err = q.PutENSSyncProjection(ctx, dbgen.PutENSSyncProjectionParams{SourceID: f.source, ExpectedRevision: 2,
		Kind: "registry_link", Key: "ancestor", Deployment: f.deployment.Bytes(), BlockHash: f.block.Bytes(), Payload: []byte(`{"detached":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("blocked worker escaped invalidation: %v", err)
	}
	f.requireCode(t, enscontract.ResolutionPending)
	var block []byte
	if err := f.db.QueryRow(ctx, `SELECT block_hash FROM ens_sync_observations WHERE source_id=$1`, f.source).Scan(&block); err != nil || !bytes.Equal(block, f.block.Bytes()) {
		t.Fatalf("historical observation lost: %x %v", block, err)
	}
}

func TestENSSyncCustomCanonicalityWithoutCollector(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	execFixture(t, ctx, f.db, `DELETE FROM canonical_blocks WHERE chain_id=31337`)
	core, err := store.NewPostgresRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.ConfigureIndex(ctx, "31337", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(9900), testHash(0), testHash(9901), "ens-sync-genesis")
	original := testBundle(1, testHash(9910), genesis.Block.Hash(), testHash(9911), "ens-sync-original")
	replacement := testBundle(1, testHash(9920), genesis.Block.Hash(), testHash(9921), "ens-sync-replacement")
	if _, err := core.CommitCanonicalSegment(ctx, "31337", []chainbundle.Bundle{genesis, original}); err != nil {
		t.Fatal(err)
	}
	input := f.input
	input.SourceChainID = ensNumber("31337")
	input.GenesisHash = genesis.Block.Hash().Bytes()
	input.StartNumber = ensNumber("0")
	input.StartHash = input.GenesisHash
	f.source, err = f.repo.CreateSource(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	f.block = original.Block.Hash()
	f.putBlock(t, "0", genesis.Block.Hash(), genesis.Block.ParentHash())
	f.putBlock(t, "1", f.block, genesis.Block.Hash())
	if err := f.q.PutENSSyncDeployment(ctx, dbgen.PutENSSyncDeploymentParams{SourceID: f.source,
		Address: f.deployment.Bytes(), Protocol: "v1", BlockHash: genesis.Block.Hash().Bytes(), CodeHash: testHash(4).Bytes(), AbiDigest: testHash(5).Bytes()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.SetENSSyncTarget(ctx, ensTime(time.Now().Add(time.Hour)), f.source, f.block.Bytes()); err != nil {
		t.Fatal(err)
	}
	f.cover(t, genesis.Block.Hash(), 1)
	f.cover(t, f.block, 1)
	f.lookup.SourceID = f.source
	f.lookup.Direction = enscontract.Primary
	f.lookup.Key = "0x" + common.Bytes2Hex(testAddress(102).Bytes())
	f.lookup.CoinType = "2147514985"
	f.discover(t)
	f.enqueue(t, 3)
	if _, err := f.repo.Claim(ctx, f.source, time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unbound core genesis claimed: %v", err)
	}
	execFixture(t, ctx, f.db, `UPDATE chains SET genesis_hash=NULL WHERE chain_id=31337`)
	if err := store.BindChainIdentity(ctx, f.db, "31337", genesis.Block.Hash()); err != nil {
		t.Fatal(err)
	}
	job := f.claim(t)
	publication := f.publication(job)
	publication.ReverseResolver = testAddress(103).Bytes()
	if _, err := f.repo.Publish(ctx, publication); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.Lookup(ctx, f.lookup)
	if err != nil || got.State != enscontract.Resolved {
		t.Fatalf("custom primary: %+v %v", got, err)
	}
	_, err = f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	job = f.claim(t)
	// Core detaches while ENS is disabled. The core-backed view immediately
	// invalidates its references without a second canonicality writer.
	if err := core.ApplyReorg(ctx, "31337", store.Reorg{Ancestor: mustBlockRef(t, genesis),
		Detached: []store.BlockRef{mustBlockRef(t, original)}, Attached: []chainbundle.Bundle{replacement},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, replacement)), Reason: "ENS collector disabled"}); err != nil {
		t.Fatal(err)
	}
	f.requireCode(t, enscontract.ResolutionPending)
	publication = f.publication(job)
	publication.ReverseResolver = testAddress(103).Bytes()
	if _, err := f.repo.Publish(ctx, publication); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("detached custom publication: %v", err)
	}
	if err := core.ApplyReorg(ctx, "31337", store.Reorg{Ancestor: mustBlockRef(t, genesis),
		Detached: []store.BlockRef{mustBlockRef(t, replacement)}, Attached: []chainbundle.Bundle{original},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, original)), Reason: "same hash reattached"}); err != nil {
		t.Fatal(err)
	}
	f.requireCode(t, enscontract.ResolutionPending)
	if _, err := f.repo.Publish(ctx, publication); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("reattached job escaped attachment fence: %v", err)
	}
}

func TestENSSyncRetryDeadlineAndCandidateFence(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	f.enqueue(t, 3)
	job := f.claim(t)
	code := "resolution_timeout"
	retry := dbgen.RetryENSSyncJobParams{SourceID: f.source, JobID: job.ID, LeaseToken: job.LeaseToken,
		FailureCode: &code, RetryAt: ensTime(time.Now().Add(time.Minute))}
	n, err := f.q.RetryENSSyncJob(ctx, retry)
	if err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if _, err := f.repo.Claim(ctx, f.source, time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("retry deadline ignored: %v", err)
	}
	execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET retry_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
	next := f.claim(t)
	if n, err := f.q.RetryENSSyncJob(ctx, retry); err != nil || n != 0 {
		t.Fatalf("stale retry token: %d %v", n, err)
	}
	if _, err := f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Publish(ctx, f.publication(next)); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("candidate generation not fenced: %v", err)
	}
	f.enqueue(t, 1)
	current := f.claim(t)
	retry.JobID = current.ID
	retry.LeaseToken = current.LeaseToken
	n, err = f.q.RetryENSSyncJob(ctx, retry)
	if err != nil || n != 1 {
		t.Fatalf("terminal retry: %d %v", n, err)
	}
	var state string
	if err := f.db.QueryRow(ctx, `SELECT state FROM ens_sync_jobs WHERE id=$1`, current.ID).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("exhaustion: %s %v", state, err)
	}
}

type ensReadOnlyDB struct {
	*pgxpool.Pool
	reads int
}

func (d *ensReadOnlyDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	if opts.AccessMode != pgx.ReadOnly || opts.IsoLevel != pgx.RepeatableRead {
		return nil, errors.New("ENS read attempted a writable or unstable snapshot")
	}
	d.reads++
	return d.Pool.BeginTx(ctx, opts)
}

func TestENSSyncUnknownReadsDoNotDiscoverOrWrite(t *testing.T) {
	f := newENSSyncFixture(t)
	database := &ensReadOnlyDB{Pool: f.db}
	repo, err := enssync.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := repo.Lookup(t.Context(), f.lookup)
		if err != nil || result.Code != enscontract.NotIndexed {
			t.Fatalf("unknown name: %+v %v", result, err)
		}
	}
	var count int
	if err := f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ens_sync_candidates) +
        (SELECT count(*) FROM ens_sync_jobs) + (SELECT count(*) FROM ens_sync_observations) +
        (SELECT count(*) FROM ens_sync_publications)`).Scan(&count); err != nil || count != 0 || database.reads != 2 {
		t.Fatalf("unknown reads changed state: count=%d snapshots=%d err=%v", count, database.reads, err)
	}
}

func (f *ensSyncFixture) bindProjection(t *testing.T, job dbgen.EnsSyncJob, kind, key string) {
	t.Helper()
	revision, err := f.q.PutENSSyncProjection(t.Context(), dbgen.PutENSSyncProjectionParams{SourceID: f.source,
		ExpectedRevision: 1, Kind: kind, Key: key, Deployment: f.deployment.Bytes(), BlockHash: f.block.Bytes(), Payload: []byte(`{"version":1}`)})
	if err != nil || revision != 2 {
		t.Fatalf("initial dependency: %d %v", revision, err)
	}
	n, err := f.q.BindENSSyncJobDependency(t.Context(), dbgen.BindENSSyncJobDependencyParams{SourceID: f.source,
		JobID: job.ID, LeaseToken: job.LeaseToken, Kind: kind, Key: key, Revision: revision})
	if err != nil || n != 1 {
		t.Fatalf("bind dependency: %d %v", n, err)
	}
}

func TestENSSyncDependencyIsolationAndProjectionReplay(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.cover(t, f.block, 1)
	f.enqueue(t, 3)
	job := f.claim(t)
	publication := f.publication(job)
	publication.Evidence = "projection"
	if _, err := f.repo.Publish(ctx, publication); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("projection without dependency proof: %v", err)
	}
	f.bindProjection(t, job, "resolver_record", "shared")
	input := dbgen.PutENSSyncProjectionParams{SourceID: f.source, ExpectedRevision: 2, Kind: "resolver_record", Key: "unrelated",
		Deployment: f.deployment.Bytes(), BlockHash: f.block.Bytes(), Payload: []byte(`{"version":1}`)}
	revision, err := f.q.PutENSSyncProjection(ctx, input)
	if err != nil || revision != 3 {
		t.Fatalf("unrelated projection: %d %v", revision, err)
	}
	// Same-key, same-fact replay does not allocate another version, even if
	// another worker already consumed the expected projection counter.
	revision, err = f.q.PutENSSyncProjection(ctx, input)
	if err != nil || revision != 3 {
		t.Fatalf("idempotent projection: %d %v", revision, err)
	}
	if _, err := f.repo.Publish(ctx, publication); err != nil {
		t.Fatalf("unrelated change invalidated publication: %v", err)
	}
	result, err := f.repo.Lookup(ctx, f.lookup)
	if err != nil || result.State != enscontract.Resolved {
		t.Fatalf("unrelated dependency hid name: %+v %v", result, err)
	}
	n, err := f.q.BindENSSyncJobDependency(ctx, dbgen.BindENSSyncJobDependencyParams{SourceID: f.source,
		JobID: job.ID, LeaseToken: job.LeaseToken, Kind: "resolver_record", Key: "unrelated", Revision: 3})
	if err != nil || n != 0 {
		t.Fatalf("completed dependency set mutated: %d %v", n, err)
	}
	input.Key = "shared"
	input.ExpectedRevision = 3
	input.Payload = []byte(`{"deleted":true}`)
	revision, err = f.q.PutENSSyncProjection(ctx, input)
	if err != nil || revision != 4 {
		t.Fatalf("shared change: %d %v", revision, err)
	}
	f.requireCode(t, enscontract.ResolutionPending)
	progress, err := f.q.LockENSSyncSource(ctx, f.source)
	if err != nil || progress.Revision != 1 || progress.ProjectionRevision != 4 {
		t.Fatalf("projection changed source epoch: %+v %v", progress, err)
	}
	if _, err := f.q.InvalidateENSSyncCandidate(ctx, f.source, f.candidate, 1); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 3)
	next := f.claim(t)
	if _, err := f.q.InvalidateENSSyncSource(ctx, f.source, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Publish(ctx, f.publication(next)); !errors.Is(err, enssync.ErrFenceLost) {
		t.Fatalf("source epoch not fenced: %v", err)
	}
}

func TestENSSyncOrphanDependencyCannotRemainReadable(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.putBlock(t, "101", testHash(101), f.block)
	if _, err := f.q.SetENSSyncTarget(ctx, ensTime(time.Now().Add(time.Hour)), f.source, testHash(101).Bytes()); err != nil {
		t.Fatal(err)
	}
	f.cover(t, f.block, 1)
	f.cover(t, testHash(101), 1)
	f.discover(t)
	f.enqueue(t, 3)
	job := f.claim(t)
	f.bindProjection(t, job, "resolver_record", "ancestor-record")
	if _, err := f.repo.Publish(ctx, f.publication(job)); err != nil {
		t.Fatal(err)
	}
	// Withdrawal of a dependency must hide the observation even before the
	// collector advances the source epoch or rebuilds that record's projection.
	execFixture(t, ctx, f.db, `DELETE FROM canonical_blocks WHERE chain_id=$1 AND block_hash=$2`, f.input.SourceChainID, f.block.Bytes())
	f.requireCode(t, enscontract.ResolutionPending)
}

func TestENSSyncExactPublicationSurvivesHeadAndHistoricalProjection(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	// No historical coverage is claimed. The job's exact block is core canonical.
	f.enqueue(t, 3)
	job := f.claim(t)
	f.putBlock(t, "101", testHash(101), f.block)
	if _, e := f.q.SetENSSyncTarget(ctx, ensTime(time.Now().Add(time.Hour)), f.source, testHash(101).Bytes()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.repo.Publish(ctx, f.publication(job)); e != nil {
		t.Fatalf("moving head starved job: %v", e)
	}
	// A newly processed old event is not evidence of a change after observation.
	if _, e := f.q.PutENSSyncProjection(ctx, dbgen.PutENSSyncProjectionParams{SourceID: f.source, ExpectedRevision: 1, Kind: "registration", Key: "older", Deployment: f.deployment.Bytes(), BlockHash: f.block.Bytes(), Payload: []byte(`{"owner":"old"}`)}); e != nil {
		t.Fatal(e)
	}
	got, e := f.repo.Lookup(ctx, f.lookup)
	if e != nil || got.State != enscontract.Resolved || got.BlockHash != f.block {
		t.Fatalf("old projection hid observation: %+v %v", got, e)
	}
	execFixture(t, ctx, f.db, `DELETE FROM canonical_blocks WHERE chain_id=31337 AND block_hash=$1`, f.block.Bytes())
	f.requireCode(t, enscontract.ResolutionPending)
}

func TestENSSyncQueuedBacklogDoesNotSpendExecutionBudget(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	f.enqueue(t, 3)
	// Simulate two hours waiting for a bounded worker pool, preserving the
	// original one-hour budget without sleeping or altering retry attempts.
	execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET queued_at=queued_at-interval '2 hours',deadline=deadline-interval '2 hours'`)
	job := f.claim(t)
	if job.Attempts != 1 || time.Until(job.Deadline.Time) < 59*time.Minute {
		t.Fatalf("queue consumed budget: %+v", job)
	}
	execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET lease_until=clock_timestamp()-interval '1 second',deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
	if _, err := f.repo.Claim(ctx, f.source, time.Minute); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("started execution renewed its deadline: %v", err)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs WHERE state='failed' AND failure_code='retry_exhausted'`, 1)
}
