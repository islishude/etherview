//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestAllDerivedStagesUseOneLeaseFencedPublicationProtocol(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block := testBundle(0, testHash(110_000), testHash(0), testHash(110_001), "atomic-all-stages")
	commitCanonical(t, ctx, repository, block)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())

	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []enrich.StageID{
		enrich.ProxyStage, enrich.ABIStage, enrich.TokenStage, enrich.StatsStage, enrich.TraceStage,
	} {
		if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
		}); err != nil {
			t.Fatalf("enqueue %s: %v", stage, err)
		}
	}
	derived := newDerivedProcessors(t, db)
	proxy := newEmptyProxyProcessor(t, db, reference.Hash.String())
	abi, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{
		proxy, abi, derived.token, derived.stats, derived.trace,
	}, enrich.WorkerOptions{ID: "atomic-all-stages", LeaseDuration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	processed := 0
	for ; processed < 12; processed++ {
		found, err := worker.ProcessOne(ctx)
		if err != nil {
			t.Fatalf("process derived stage %d: %v", processed+1, err)
		}
		if !found {
			break
		}
	}
	if processed < 5 || processed == 12 {
		t.Fatalf("processed derived attempts=%d, want a quiescent queue after at least five", processed)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM published_block_stage_results
		WHERE chain_id = 1 AND block_hash = $1
		  AND stage IN ('proxy', 'abi', 'token', 'stats', 'trace')
		  AND state = 'complete'`, 5, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM block_stage_results AS result
		JOIN block_journals AS journal
		  ON journal.durable_job_id = result.durable_job_id
		 AND journal.job_generation = result.job_generation
		WHERE result.chain_id = 1 AND result.block_hash = $1
		  AND result.durable_job_id IS NOT NULL
		  AND result.job_generation IS NOT NULL`, 5, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_jobs
		WHERE chain_id = 1 AND payload->>'block_hash' = $1 AND status = 'succeeded'
		  AND requested_generation = claimed_generation
		  AND claimed_generation = completed_generation`, 5, reference.Hash.String())
}

func TestExpiredWriterCannotPublishAfterReplacementLease(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(111_000), testHash(0), testHash(111_001), "atomic-expired-writer")
	commitCanonical(t, ctx, repository, block)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	oldLease, found, err := queue.Claim(ctx, "expired-publisher", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim expiring publisher=%+v found=%t err=%v", oldLease, found, err)
	}
	const outputBarrier = int64(714_119)
	lockConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, outputBarrier); err != nil {
		t.Fatal(err)
	}
	barrierHeld := true
	defer func() {
		if barrierHeld {
			_, _ = lockConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, outputBarrier)
		}
	}()
	execFixture(t, ctx, db, `
		CREATE FUNCTION block_atomic_stats_output() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(714119);
			RETURN NEW;
		END
		$$`)
	execFixture(t, ctx, db, `
		CREATE TRIGGER block_atomic_stats_output
		BEFORE INSERT OR UPDATE ON block_statistics
		FOR EACH ROW EXECUTE FUNCTION block_atomic_stats_output()`)
	oldResult := make(chan error, 1)
	go func() {
		_, runErr := processor.ProcessLease(ctx, oldLease, queue)
		oldResult <- runErr
	}()
	waitForAdvisoryWaiter(t, ctx, db, outputBarrier)
	execFixture(t, ctx, db, `
		UPDATE durable_jobs
		SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1`, enqueued.Job.ID)
	type claimResult struct {
		lease enrich.Lease
		found bool
		err   error
	}
	replacementResult := make(chan claimResult, 1)
	go func() {
		lease, claimFound, claimErr := queue.Claim(ctx, "replacement-publisher", []enrich.StageID{enrich.StatsStage}, time.Minute)
		replacementResult <- claimResult{lease: lease, found: claimFound, err: claimErr}
	}()
	waitForAdvisoryWaiterCount(t, ctx, db, 2)
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, outputBarrier); err != nil {
		t.Fatal(err)
	}
	barrierHeld = false
	if err := <-oldResult; !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("expired worker error=%v, want ErrLeaseLost", err)
	}
	replacementClaim := <-replacementResult
	replacement := replacementClaim.lease
	if replacementClaim.err != nil || !replacementClaim.found || replacement.Token == "" || replacement.Job.Attempt != 2 {
		t.Fatalf("replacement lease=%+v found=%t err=%v", replacement, replacementClaim.found, replacementClaim.err)
	}
	assertAtomicStageAbsent(t, ctx, db, reference, enrich.StatsStage)
	result, err := processor.ProcessLease(ctx, replacement, queue)
	if err != nil || result.State != enrich.ResultComplete {
		t.Fatalf("replacement publication result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1, enrich.ResultComplete)
}

func TestPendingReplayDiscardsOwnedWriterAndInvalidatesPublishedView(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(112_000), testHash(0), testHash(112_001), "atomic-pending-replay")
	commitCanonical(t, ctx, repository, block)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "pending-generation-one", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim generation one=%+v found=%t err=%v", lease, found, err)
	}
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	replay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "integration", Key: "pending-generation-two"},
	})
	if err != nil || !replay.Replayed {
		t.Fatalf("request pending replay=%+v err=%v", replay, err)
	}
	assertReplayGeneration(t, ctx, db, enqueued.Job.ID, replayGenerationState{
		Status: "leased", Requested: 2, Claimed: 1, Completed: 0, Leased: true,
	})
	if _, err := processor.ProcessLease(ctx, lease, queue); err != nil {
		t.Fatalf("consume pending publication: %v", err)
	}
	assertReplayGeneration(t, ctx, db, enqueued.Job.ID, replayGenerationState{
		Status: "queued", Requested: 2, Claimed: 1, Completed: 1,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_stage_publications
		WHERE job_id = $1 AND job_generation = 1 AND state = 'superseded'`,
		1, enqueued.Job.ID)
	assertAtomicStageAbsent(t, ctx, db, reference, enrich.StatsStage)

	second, found, err := queue.Claim(ctx, "pending-generation-two", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found || second.Job.Generation != 2 {
		t.Fatalf("claim generation two=%+v found=%t err=%v", second, found, err)
	}
	if _, err := processor.ProcessLease(ctx, second, queue); err != nil {
		t.Fatal(err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2, enrich.ResultComplete)

	thirdRequest, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "integration", Key: "published-generation-three"},
	})
	if err != nil || !thirdRequest.Replayed {
		t.Fatalf("request terminal replay=%+v err=%v", thirdRequest, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_statistics
		WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, reference.Hash))
}

func TestAtomicPublicationRollsBackDerivedOutputOnJournalFailure(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(113_000), testHash(0), testHash(113_001), "atomic-journal-failure")
	commitCanonical(t, ctx, repository, block)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	_, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `
		CREATE FUNCTION reject_atomic_stats_journal() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.stage = 'stats@2' THEN
				RAISE EXCEPTION 'reject atomic stats journal';
			END IF;
			RETURN NEW;
		END
		$$`)
	execFixture(t, ctx, db, `
		CREATE TRIGGER reject_atomic_stats_journal
		BEFORE INSERT OR UPDATE ON block_journals
		FOR EACH ROW EXECUTE FUNCTION reject_atomic_stats_journal()`)
	lease, found, err := queue.Claim(ctx, "journal-failure", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim journal failure=%+v found=%t err=%v", lease, found, err)
	}
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	if _, err := processor.ProcessLease(ctx, lease, queue); err == nil {
		t.Fatal("atomic publication unexpectedly survived journal failure")
	}
	assertAtomicStageAbsent(t, ctx, db, reference, enrich.StatsStage)
	assertReplayGeneration(t, ctx, db, lease.Job.ID, replayGenerationState{
		Status: "leased", Requested: 1, Claimed: 1, Completed: 0, Leased: true,
	})
}

func TestOlderGenerationAndDirectFixtureCannotOverwritePublishedGeneration(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(114_000), testHash(0), testHash(114_001), "atomic-generation-order")
	commitCanonical(t, ctx, repository, block)
	configureAtomicStatsStart(t, ctx, db)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	first, found, err := queue.Claim(ctx, "generation-one", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim generation one=%+v found=%t err=%v", first, found, err)
	}
	if _, err := processor.ProcessLease(ctx, first, queue); err != nil {
		t.Fatal(err)
	}
	replay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "integration", Key: "generation-two"},
	})
	if err != nil || !replay.Replayed {
		t.Fatalf("request generation two=%+v err=%v", replay, err)
	}
	second, found, err := queue.Claim(ctx, "generation-two", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found || second.Job.Generation != 2 {
		t.Fatalf("claim generation two=%+v found=%t err=%v", second, found, err)
	}
	if _, err := processor.ProcessLease(ctx, second, queue); err != nil {
		t.Fatal(err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2, enrich.ResultComplete)

	if _, err := processor.ProcessLease(ctx, first, queue); !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("generation-one republish error=%v, want ErrLeaseLost", err)
	}
	if _, err := processor.Process(ctx, first.Job); !errors.Is(err, enrich.ErrAtomicPublicationRequired) {
		t.Fatalf("direct fixture overwrite error=%v, want ErrAtomicPublicationRequired", err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2, enrich.ResultComplete)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE durable_job_id = $1 AND job_generation = 2`, 1, enqueued.Job.ID)
}

func TestDerivedTerminalMarkersArePublishedWithoutJournals(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(115_000), testHash(0), testHash(115_001), "atomic-terminal-markers")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)

	for _, test := range []struct {
		stage  enrich.StageID
		state  enrich.ResultState
		reason string
	}{
		{stage: enrich.ProxyStage, state: enrich.ResultUnavailable, reason: "exact state unavailable"},
		{stage: enrich.TokenStage, state: enrich.ResultFailed, reason: "permanent token data failure"},
	} {
		enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: test.stage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, found, err := queue.Claim(ctx, "terminal-"+test.stage.Name, []enrich.StageID{test.stage}, time.Minute)
		if err != nil || !found {
			t.Fatalf("claim %s=%+v found=%t err=%v", test.stage, lease, found, err)
		}
		if err := queue.Finish(ctx, lease, enrich.StageResult{State: test.state, Error: test.reason}); err != nil {
			t.Fatalf("finish %s: %v", test.stage, err)
		}
		assertPublishedTerminalNoJournal(t, ctx, db, enqueued.Job.ID, 1, test.state, test.reason)
	}

	retryJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	retryLease, found, err := queue.Claim(ctx, "retry-exhaustion", []enrich.StageID{enrich.TraceStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim retry exhaustion=%+v found=%t err=%v", retryLease, found, err)
	}
	if err := queue.Retry(ctx, retryLease, enrich.Retry{Reason: "trace retry budget exhausted"}); err != nil {
		t.Fatal(err)
	}
	assertPublishedTerminalNoJournal(t, ctx, db, retryJob.Job.ID, 1, enrich.ResultFailed, "trace retry budget exhausted")
}

func TestPublicationMigrationAndViewRequireExactDurableTerminalIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(116_000), testHash(0), testHash(116_001), "atomic-view-contract")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO block_stage_results (
			chain_id, block_number, block_hash, stage, stage_version,
			state, details, durable_job_id
		) VALUES (1, $1, $2, 'stats', $3, 'complete', '{}'::jsonb, $4)`,
		reference.Number, mustBytes(t, reference.Hash), enrich.StatsStage.Version, enqueued.Job.ID,
	); err == nil {
		t.Fatal("block_stage_results accepted a half-populated publication marker")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical, job_generation
		) VALUES (1, $1, $2, 1, '{}'::jsonb, TRUE, 1)`,
		mustBytes(t, reference.Hash), enrich.StatsStage.String(),
	); err == nil {
		t.Fatal("block_journals accepted a half-populated publication marker")
	}
	execFixture(t, ctx, db, `
		INSERT INTO block_stage_results (
			chain_id, block_number, block_hash, stage, stage_version,
			state, details, durable_job_id, job_generation
		) VALUES (1, $1, $2, 'stats', $3, 'complete', '{}'::jsonb, $4, 1)`,
		reference.Number, mustBytes(t, reference.Hash), enrich.StatsStage.Version, enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('etherview.enrichment_publication_protocol', '2', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical,
			durable_job_id, job_generation
		) VALUES (1, $1, $2, 1, '{"version":1,"operations":[]}'::jsonb, TRUE, $3, 1)`,
		mustBytes(t, reference.Hash), enrich.StatsStage.String(), enqueued.Job.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 1, 1, $2, $3, 'stats', $4, 'complete', '{}'::jsonb)`,
		enqueued.Job.ID, reference.Number, mustBytes(t, reference.Hash), enrich.StatsStage.Version,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE durable_jobs
		SET status = 'succeeded', attempts = 1,
			claimed_generation = 1, completed_generation = 1,
			result = '{"state":"complete"}'::jsonb,
			leased_by = NULL, lease_token = NULL,
			lease_expires_at = NULL, leased_generation = NULL
		WHERE id = $1`, enqueued.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 1, enqueued.Job.ID)
	execFixture(t, ctx, db, `
		UPDATE block_stage_results
		SET details = '{"mismatch":"true"}'::jsonb
		WHERE durable_job_id = $1 AND job_generation = 1`, enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
	execFixture(t, ctx, db, `
		UPDATE block_stage_results
		SET details = '{}'::jsonb
		WHERE durable_job_id = $1 AND job_generation = 1`, enqueued.Job.ID)
	replay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "integration", Key: "view-newer-generation"},
	})
	if err != nil || !replay.Replayed {
		t.Fatalf("request view replay=%+v err=%v", replay, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
}

func TestLeaseFencedPublicationMigrationReplaysLegacyTerminalsAndGuardsOldWorkers(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	migrations, err := store.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var publicationMigration string
	for _, migration := range migrations {
		if migration.Version == "0014_lease_fenced_stage_publication" {
			publicationMigration = migration.SQL
			break
		}
		if _, err := db.ExecContext(ctx, migration.SQL); err != nil {
			t.Fatalf("apply pre-publication migration %s: %v", migration.Version, err)
		}
	}
	if publicationMigration == "" {
		t.Fatal("missing lease-fenced publication migration")
	}
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(116_100), testHash(0), testHash(116_101), "legacy-publication-upgrade")
	commitCanonical(t, ctx, repository, block)
	reference := mustBlockRef(t, block)
	payload := `{"block_hash":"` + reference.Hash.String() + `","block_number":"0"}`
	insertLegacy := func(key, stage, status string, attempts, claimed, completed int, lease bool) int64 {
		t.Helper()
		var id int64
		var owner, token any
		var expires any
		var leasedGeneration any
		if lease {
			owner, token = "legacy-worker", "legacy-token-"+key
			expires = time.Now().Add(time.Minute)
			leasedGeneration = int64(1)
		}
		var result any
		if status == "succeeded" {
			result = `{"state":"complete"}`
		} else if status == "failed" {
			result = `{"state":"failed","error":"legacy failure"}`
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO durable_jobs (
				chain_id, kind, stage, stage_version, idempotency_key, payload,
				status, attempts, max_attempts, leased_by, lease_token,
				lease_expires_at, result, last_error,
				requested_generation, claimed_generation, leased_generation,
				completed_generation
			) VALUES (
				1, 'enrichment', $1, 1, $2, $3::jsonb,
				$4, $5, 10, $6, $7, $8, $9::jsonb,
				CASE WHEN $4 = 'failed' THEN 'legacy failure' ELSE NULL END,
				1, $10, $11, $12
			) RETURNING id`,
			stage, key, payload, status, attempts, owner, token, expires, result,
			claimed, leasedGeneration, completed,
		).Scan(&id); err != nil {
			t.Fatalf("insert legacy job %s: %v", key, err)
		}
		return id
	}
	succeededID := insertLegacy("legacy-success", "stats", "succeeded", 1, 1, 1, false)
	failedID := insertLegacy("legacy-failed", "token", "failed", 1, 1, 1, false)
	queuedID := insertLegacy("legacy-queued", "proxy", "queued", 0, 0, 0, false)
	leasedID := insertLegacy("legacy-leased", "trace", "leased", 2, 1, 0, true)
	execFixture(t, ctx, db, `
		INSERT INTO block_stage_results (
			chain_id, block_number, block_hash, stage, stage_version, state, details
		) VALUES
			(1, 0, $1, 'stats', 1, 'complete', '{}'::jsonb),
			(1, 0, $1, 'token', 1, 'failed', '{}'::jsonb)`, mustBytes(t, reference.Hash))
	execFixture(t, ctx, db, `
		INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical
		) VALUES (1, $1, 'stats@1', 1, '{"version":1,"operations":[]}'::jsonb, TRUE)`,
		mustBytes(t, reference.Hash))

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := db.ExecContext(ctx, publicationMigration); err != nil {
			t.Fatalf("apply publication migration attempt %d: %v", attempt, err)
		}
	}
	for _, terminalID := range []int64{succeededID, failedID} {
		assertReplayGeneration(t, ctx, db, fmt.Sprint(terminalID), replayGenerationState{
			Status: "queued", Requested: 2, Claimed: 1, Completed: 1,
		})
	}
	assertReplayGeneration(t, ctx, db, fmt.Sprint(queuedID), replayGenerationState{
		Status: "queued", Requested: 1, Claimed: 0, Completed: 0,
	})
	assertReplayGeneration(t, ctx, db, fmt.Sprint(leasedID), replayGenerationState{
		Status: "leased", Requested: 1, Claimed: 1, Completed: 0, Leased: true,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_job_replay_requests
		WHERE source_kind = 'schema-upgrade'
		  AND source_key = '0014-lease-fenced-stage-publication'`, 2)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage IN ('stats', 'token')`,
		0, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_stage_publications`, 0)

	if _, err := db.ExecContext(ctx, `
		UPDATE durable_jobs
		SET status = 'leased', leased_by = 'old-worker', lease_token = 'old-token',
			lease_expires_at = clock_timestamp() + INTERVAL '1 minute', leased_generation = 1
		WHERE id = $1`, queuedID); err == nil {
		t.Fatal("post-migration old worker acquired a derived lease without protocol 2")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE durable_jobs
		SET status = 'failed', result = '{"state":"failed","error":"old finish"}'::jsonb,
			last_error = 'old finish', completed_generation = 1,
			leased_by = NULL, lease_token = NULL, lease_expires_at = NULL, leased_generation = NULL
		WHERE id = $1`, leasedID); err == nil {
		t.Fatal("pre-migration lease committed an old two-transaction terminal result")
	}
	assertJobStatus(t, ctx, db, fmt.Sprint(leasedID), "leased")
	execFixture(t, ctx, db, `
		UPDATE durable_jobs SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1`, leasedID)
	queue, _ := enrich.NewPostgresJobQueue(db)
	reclaimed, found, err := queue.Claim(ctx, "protocol-two-worker", []enrich.StageID{enrich.TraceStage}, time.Minute)
	if err != nil || !found || reclaimed.Job.ID != fmt.Sprint(leasedID) || reclaimed.Job.Attempt != 3 {
		t.Fatalf("reclaim legacy lease=%+v found=%t err=%v", reclaimed, found, err)
	}
}

func TestOlderExhaustionCannotOverwriteNewerOrForeignPublicationMarker(t *testing.T) {
	for _, test := range []struct {
		name       string
		foreign    bool
		generation int64
	}{
		{name: "newer generation", generation: 2},
		{name: "foreign job", foreign: true, generation: 1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			repository, _ := store.NewPostgresRepository(db)
			block := testBundle(0, testHash(117_000), testHash(0), testHash(117_001), "atomic-old-exhaustion")
			commitCanonical(t, ctx, repository, block)
			reference := mustBlockRef(t, block)
			word, _ := enrich.ParseWord(reference.Hash.String())
			queue, _ := enrich.NewPostgresJobQueue(db)
			target, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
				Stage: enrich.StatsStage, ChainID: "1", BlockHash: word,
				BlockNumber: reference.Number, MaxAttempts: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			markerJobID := target.Job.ID
			if test.foreign {
				foreign, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
					Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
				})
				if err != nil {
					t.Fatal(err)
				}
				markerJobID = foreign.Job.ID
			}
			execFixture(t, ctx, db, `
				UPDATE durable_jobs
				SET status = 'queued', attempts = 1, max_attempts = 1,
					claimed_generation = 1, requested_generation = 1,
					completed_generation = 0, available_at = clock_timestamp() - INTERVAL '1 second',
					last_error = 'old exhaustion'
				WHERE id = $1`, target.Job.ID)
			execFixture(t, ctx, db, `
				INSERT INTO block_stage_results (
					chain_id, block_number, block_hash, stage, stage_version,
					state, details, durable_job_id, job_generation
				) VALUES (1, $1, $2, 'stats', $3, 'complete',
					'{"sentinel":"preserve"}'::jsonb, $4, $5)`,
				reference.Number, mustBytes(t, reference.Hash), enrich.StatsStage.Version,
				markerJobID, test.generation)

			lease, found, err := queue.Claim(ctx, "exhaustion-reaper", []enrich.StageID{enrich.StatsStage}, time.Minute)
			if !errors.Is(err, enrich.ErrStagePublicationConflict) || found {
				t.Fatalf("claim over protected marker=%+v found=%t err=%v, want publication conflict", lease, found, err)
			}
			var state, sentinel string
			var gotJob, gotGeneration int64
			if err := db.QueryRowContext(ctx, `
				SELECT state, details->>'sentinel', durable_job_id, job_generation
				FROM block_stage_results
				WHERE chain_id = 1 AND block_hash = $1
				  AND stage = 'stats' AND stage_version = $2`,
				mustBytes(t, reference.Hash), enrich.StatsStage.Version,
			).Scan(&state, &sentinel, &gotJob, &gotGeneration); err != nil {
				t.Fatal(err)
			}
			if state != "complete" || sentinel != "preserve" || gotGeneration != test.generation {
				t.Fatalf("protected marker state=%q sentinel=%q job/gen=%d/%d", state, sentinel, gotJob, gotGeneration)
			}
			assertJobStatus(t, ctx, db, target.Job.ID, "queued")
		})
	}
}

func TestReplayGenerationHandoffRejectsForeignJournal(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	block := testBundle(0, testHash(117_100), testHash(0), testHash(117_101), "atomic-foreign-journal")
	commitCanonical(t, ctx, repository, block)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	target, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `
		UPDATE durable_jobs
		SET requested_generation = 2, status = 'queued', available_at = clock_timestamp()
		WHERE id = $1`, target.Job.ID)
	execFixture(t, ctx, db, `
		INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical,
			durable_job_id, job_generation
		) VALUES (1, $1, $2, 1, '{"sentinel":"preserve"}'::jsonb, TRUE, $3, 1)`,
		mustBytes(t, reference.Hash), enrich.StatsStage.String(), foreign.Job.ID)

	lease, found, err := queue.Claim(ctx, "foreign-journal-handoff", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if !errors.Is(err, enrich.ErrStagePublicationConflict) || found {
		t.Fatalf("claim over foreign journal=%+v found=%t err=%v", lease, found, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = $2
		  AND durable_job_id = $3 AND job_generation = 1`,
		1, mustBytes(t, reference.Hash), enrich.StatsStage.String(), foreign.Job.ID)
	var status string
	var attempts, claimed int64
	if err := db.QueryRowContext(ctx, `
		SELECT status, attempts, claimed_generation FROM durable_jobs WHERE id = $1`, target.Job.ID,
	).Scan(&status, &attempts, &claimed); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || attempts != 0 || claimed != 0 {
		t.Fatalf("rolled-back handoff status=%q attempts=%d claimed=%d", status, attempts, claimed)
	}
}

func TestStaleCanonicalPublicationRemainsInvisibleAcrossSameHashReattach(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	genesis := testBundle(0, testHash(118_000), testHash(0), testHash(118_100), "stale-publish-genesis")
	original := testBundle(1, testHash(118_001), testHash(118_000), testHash(118_101), "stale-publish-original")
	replacement := testBundle(1, testHash(118_002), testHash(118_000), testHash(118_102), "stale-publish-replacement")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, original)
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{original}, []ethrpc.Bundle{replacement}, "orphan before stale stage")
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	originalRef := mustBlockRef(t, original)
	word, _ := enrich.ParseWord(originalRef.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: originalRef.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "stale-canonical-worker", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim stale generation=%+v found=%t err=%v", lease, found, err)
	}

	const advisoryKey = int64(714_118)
	lockConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lockConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	execFixture(t, ctx, db, `
		CREATE FUNCTION block_stale_stage_marker() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.stage = 'stats' AND NEW.details->>'outcome' = 'stale_canonical_skipped' THEN
				PERFORM pg_advisory_xact_lock(714118);
			END IF;
			RETURN NEW;
		END
		$$`)
	execFixture(t, ctx, db, `
		CREATE TRIGGER block_stale_stage_marker
		BEFORE INSERT OR UPDATE ON block_stage_results
		FOR EACH ROW EXECUTE FUNCTION block_stale_stage_marker()`)
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	published := make(chan error, 1)
	go func() {
		_, publishErr := processor.ProcessLease(ctx, lease, queue)
		published <- publishErr
	}()
	waitForAdvisoryWaiter(t, ctx, db, advisoryKey)
	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{replacement}, []ethrpc.Bundle{original}, "reattach during stale publication")
	if _, err := lockConnection.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := <-published; err != nil {
		t.Fatalf("commit stale audit publication: %v", err)
	}
	assertStageDetail(t, ctx, db, word, enrich.StatsStage.Name, "outcome", "stale_canonical_skipped")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)

	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
		Stages: []enrich.StageID{enrich.StatsStage},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 8; attempt++ {
		result, err := dispatcher.DispatchOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var requested int64
		if err := db.QueryRowContext(ctx, `SELECT requested_generation FROM durable_jobs WHERE id = $1`, enqueued.Job.ID).Scan(&requested); err != nil {
			t.Fatal(err)
		}
		if requested == 2 {
			break
		}
		if result.State == enrich.OutboxIdle {
			t.Fatal("reattach outbox became idle before requesting replay")
		}
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
	second, found, err := queue.Claim(ctx, "reattached-generation", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found || second.Job.Generation != 2 {
		t.Fatalf("claim reattached generation=%+v found=%t err=%v", second, found, err)
	}
	if _, err := processor.ProcessLease(ctx, second, queue); err != nil {
		t.Fatal(err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2, enrich.ResultComplete)
}

func TestCompletedPublicationRemainsInvisibleUntilSameHashReattachReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, _ := store.NewPostgresRepository(db)
	genesis := testBundle(0, testHash(118_200), testHash(0), testHash(118_210), "complete-reattach-genesis")
	original := testBundle(1, testHash(118_201), testHash(118_200), testHash(118_211), "complete-reattach-original")
	replacement := testBundle(1, testHash(118_202), testHash(118_200), testHash(118_212), "complete-reattach-replacement")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, original)
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	originalRef := mustBlockRef(t, original)
	word, _ := enrich.ParseWord(originalRef.Hash.String())
	queue, _ := enrich.NewPostgresJobQueue(db)
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: originalRef.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "complete-before-detach", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim initial complete=%+v found=%t err=%v", lease, found, err)
	}
	processor, _ := enrich.NewPostgresStatsProcessor(db)
	if _, err := processor.ProcessLease(ctx, lease, queue); err != nil {
		t.Fatal(err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1, enrich.ResultComplete)

	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{original}, []ethrpc.Bundle{replacement}, "detach completed publication")
	// Model the orphan notification having drained before the later reattach.
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{replacement}, []ethrpc.Bundle{original}, "reattach completed publication")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)

	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
		Stages: []enrich.StageID{enrich.StatsStage},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 8; attempt++ {
		result, err := dispatcher.DispatchOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var requested int64
		if err := db.QueryRowContext(ctx, `SELECT requested_generation FROM durable_jobs WHERE id = $1`, enqueued.Job.ID).Scan(&requested); err != nil {
			t.Fatal(err)
		}
		if requested == 2 {
			break
		}
		if result.State == enrich.OutboxIdle {
			t.Fatal("reattach outbox became idle before replay request")
		}
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1`, 0, enqueued.Job.ID)
	second, found, err := queue.Claim(ctx, "complete-after-reattach", []enrich.StageID{enrich.StatsStage}, time.Minute)
	if err != nil || !found || second.Job.Generation != 2 {
		t.Fatalf("claim replay complete=%+v found=%t err=%v", second, found, err)
	}
	if _, err := processor.ProcessLease(ctx, second, queue); err != nil {
		t.Fatal(err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2, enrich.ResultComplete)
}

func newEmptyProxyProcessor(t *testing.T, db *sql.DB, blockHash string) *enrich.PostgresProxyProcessor {
	t.Helper()
	states := map[string]map[string]proxyContractState{blockHash: {}}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint("atomic-publication-state", states, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func configureAtomicStatsStart(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO core_index_configuration (chain_id, configured_start)
		VALUES (1, 0)
		ON CONFLICT (chain_id) DO UPDATE SET configured_start = EXCLUDED.configured_start`)
}

func waitForDurableLeaseOwner(t *testing.T, ctx context.Context, db *sql.DB, jobID, worker string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		var owner sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT status, leased_by FROM durable_jobs WHERE id = $1`, jobID,
		).Scan(&status, &owner); err != nil {
			t.Fatal(err)
		}
		if status == "leased" && owner.Valid && owner.String == worker {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for durable lease owner %q: %v", worker, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForAdvisoryWaiter(t *testing.T, ctx context.Context, db *sql.DB, key int64) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_locks
				WHERE locktype = 'advisory' AND NOT granted
				  AND objid = $1
			)`, key).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for advisory lock %d: %v", key, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForAdvisoryWaiterCount(t *testing.T, ctx context.Context, db *sql.DB, minimum int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= minimum {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d advisory waiters: %v", minimum, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertAtomicStageAbsent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	reference store.BlockRef,
	stage enrich.StageID,
) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = $2 AND stage_version = $3`,
		`SELECT count(*) FROM published_block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = $2 AND stage_version = $3`,
	} {
		assertRowCount(t, ctx, db, query, 0, mustBytes(t, reference.Hash), stage.Name, stage.Version)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = $2`,
		0, mustBytes(t, reference.Hash), stage.String())
	if stage == enrich.StatsStage {
		assertRowCount(t, ctx, db, `
			SELECT count(*) FROM block_statistics
			WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, reference.Hash))
	}
}

func assertPublishedGeneration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	jobID string,
	generation int64,
	state enrich.ResultState,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM published_block_stage_results AS result
		JOIN block_journals AS journal
		  ON journal.durable_job_id = result.durable_job_id
		 AND journal.job_generation = result.job_generation
		JOIN durable_stage_publications AS publication
		  ON publication.job_id = result.durable_job_id
		 AND publication.job_generation = result.job_generation
		 AND publication.state = result.state
		WHERE result.durable_job_id = $1
		  AND result.job_generation = $2
		  AND result.state = $3`, 1, jobID, generation, state)
}

func assertPublishedTerminalNoJournal(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	jobID string,
	generation int64,
	state enrich.ResultState,
	reason string,
) {
	t.Helper()
	var gotState string
	var gotError sql.NullString
	var markerJob, markerGeneration int64
	if err := db.QueryRowContext(ctx, `
		SELECT state, last_error, durable_job_id, job_generation
		FROM published_block_stage_results
		WHERE durable_job_id = $1 AND job_generation = $2`, jobID, generation,
	).Scan(&gotState, &gotError, &markerJob, &markerGeneration); err != nil {
		t.Fatalf("read terminal publication: %v", err)
	}
	if gotState != string(state) || !gotError.Valid || gotError.String != reason ||
		markerGeneration != generation {
		t.Fatalf("terminal publication state=%q error=%+v marker=%d/%d", gotState, gotError, markerJob, markerGeneration)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE durable_job_id = $1 AND job_generation = $2`, 0, jobID, generation)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_stage_publications
		WHERE job_id = $1 AND job_generation = $2
		  AND state = $3 AND last_error = $4`, 1, jobID, generation, state, reason)
}
