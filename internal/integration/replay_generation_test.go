//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestWorkerConsumesTheOutboxDurableRetryBudget(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block := testBundle(0, testHash(79_000), testHash(0), testHash(79_001), "durable-retry-budget")
	commitCanonical(t, ctx, repository, block)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	stage := enrich.StageID{Name: "durable-budget", Version: 1}
	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{Stages: []enrich.StageID{stage}})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := dispatcher.DispatchOne(ctx); err != nil || result.State != enrich.OutboxPublished {
		t.Fatalf("dispatch durable-budget job: result=%+v err=%v", result, err)
	}
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	job := readEnrichmentJob(t, ctx, db, stage, word, reference.Number)
	var maximum int
	if err := db.QueryRowContext(ctx, `SELECT max_attempts FROM durable_jobs WHERE id = $1`, job.ID).Scan(&maximum); err != nil || maximum != int(enrich.DefaultEnrichmentMaxAttempts) {
		t.Fatalf("durable max attempts=%d err=%v", maximum, err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{enrich.ProcessorFunc{
		ID: stage,
		Fn: func(context.Context, enrich.Job) (enrich.StageResult, error) {
			return enrich.StageResult{}, errors.New("fixture remains retryable")
		},
	}}, enrich.WorkerOptions{
		ID: "durable-budget-worker", LeaseDuration: time.Second,
		RetryBase: time.Microsecond, RetryMax: time.Microsecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= maximum; attempt++ {
		if processed, err := worker.ProcessOne(ctx); err != nil || !processed {
			t.Fatalf("process durable attempt %d: processed=%t err=%v", attempt, processed, err)
		}
		if attempt < maximum {
			execFixture(t, ctx, db, `UPDATE durable_jobs SET available_at = clock_timestamp() WHERE id = $1`, job.ID)
		}
	}
	assertEnrichmentJobTerminal(t, ctx, db, job.ID, "failed", maximum)
	assertStageResult(t, ctx, db, job, enrich.ResultFailed, "fixture remains retryable", map[string]string{})
	assertReplayGeneration(t, ctx, db, job.ID, replayGenerationState{
		Status: "failed", Requested: 1, Claimed: 1, Completed: 1,
	})
}

func TestDurableReplayGenerationMigrationBackfillsExistingQueueRows(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	migrations, err := store.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var replayMigration string
	for _, migration := range migrations {
		if migration.Version == "0013_durable_replay_generations" {
			replayMigration = migration.SQL
			break
		}
		if _, err := db.ExecContext(ctx, migration.SQL); err != nil {
			t.Fatalf("apply pre-replay migration %s: %v", migration.Version, err)
		}
	}
	if replayMigration == "" {
		t.Fatal("missing durable replay generation migration")
	}
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	execFixture(t, ctx, db, `
		INSERT INTO transactional_outbox (
			chain_id, topic, message_key, payload, published_at, attempts
		) VALUES (1, 'core.block.canonical', 'legacy-hash', '{}'::jsonb, now(), 2)`)
	execFixture(t, ctx, db, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, payload,
			status, attempts, max_attempts
		) VALUES (1, 'enrichment', 'token', 1, 'legacy-queued', '{}'::jsonb,
			'queued', 0, 10)`)
	execFixture(t, ctx, db, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, payload,
			status, attempts, max_attempts, leased_by, lease_token, lease_expires_at
		) VALUES (1, 'enrichment', 'trace', 1, 'legacy-leased', '{}'::jsonb,
			'leased', 2, 10, 'legacy-worker', 'legacy-token', now() + INTERVAL '1 minute')`)
	execFixture(t, ctx, db, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, payload,
			status, attempts, max_attempts, result
		) VALUES (1, 'enrichment', 'abi', 1, 'legacy-succeeded', '{}'::jsonb,
			'succeeded', 1, 10, '{"state":"complete"}'::jsonb)`)
	// Applying the additive migration to live pre-generation rows must preserve
	// ownership/terminal state, and its guards keep a repeated operator run safe.
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := db.ExecContext(ctx, replayMigration); err != nil {
			t.Fatalf("apply durable replay migration attempt %d: %v", attempt, err)
		}
	}
	for key, want := range map[string]replayGenerationState{
		"legacy-queued":    {Status: "queued", Requested: 1, Claimed: 0, Completed: 0},
		"legacy-leased":    {Status: "leased", Requested: 1, Claimed: 1, Completed: 0, Leased: true},
		"legacy-succeeded": {Status: "succeeded", Requested: 1, Claimed: 1, Completed: 1},
	} {
		var jobID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM durable_jobs WHERE idempotency_key = $1`, key).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		assertReplayGeneration(t, ctx, db, jobID, want)
	}
	var outboxGeneration int64
	if err := db.QueryRowContext(ctx, `
		SELECT generation FROM transactional_outbox
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = 'legacy-hash'`,
	).Scan(&outboxGeneration); err != nil || outboxGeneration != 1 {
		t.Fatalf("legacy outbox generation=%d err=%v, want 1", outboxGeneration, err)
	}
	var replayTable string
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('durable_job_replay_requests')::text`).Scan(&replayTable); err != nil || replayTable != "durable_job_replay_requests" {
		t.Fatalf("replay request table=%q err=%v", replayTable, err)
	}
}

func TestCanonicalSameHashReattachReplaysTerminalStaleGeneration(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(80_000), testHash(0), testHash(81_000), "replay-genesis")
	original := testBundle(1, testHash(80_001), testHash(80_000), testHash(81_001), "replay-original")
	replacement := testBundle(1, testHash(90_001), testHash(80_000), testHash(91_001), "replay-replacement")
	commitCanonical(t, ctx, repository, genesis)
	// This test owns the height-one lifecycle; do not let the genesis wake create
	// an unrelated lower-ID token job.
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	commitCanonical(t, ctx, repository, original)

	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
		Stages: []enrich.StageID{enrich.TokenStage},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dispatched, err := dispatcher.DispatchOne(ctx); err != nil || dispatched.State != enrich.OutboxPublished {
		t.Fatalf("publish original canonical generation: result=%+v err=%v", dispatched, err)
	}
	originalRef := mustBlockRef(t, original)
	originalWord, _ := enrich.ParseWord(originalRef.Hash.String())
	job := readEnrichmentJob(t, ctx, db, enrich.TokenStage, originalWord, originalRef.Number)
	// An idempotent rewrite of an already-canonical identity is not a new
	// canonical lifetime and must not reopen its published outbox generation.
	commitCanonical(t, ctx, repository, original)
	var originalOutboxGeneration int64
	var originalPublished bool
	if err := db.QueryRowContext(ctx, `
		SELECT generation, published_at IS NOT NULL
		FROM transactional_outbox
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		originalRef.Hash.String(),
	).Scan(&originalOutboxGeneration, &originalPublished); err != nil || originalOutboxGeneration != 1 || !originalPublished {
		t.Fatalf("duplicate canonical outbox generation=%d published=%t err=%v", originalOutboxGeneration, originalPublished, err)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{original}, []ethrpc.Bundle{replacement}, "detach replay fixture")
	tokenProcessor, err := enrich.NewPostgresTokenProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{tokenProcessor}, enrich.WorkerOptions{
		ID: "reattach-stale-worker", LeaseDuration: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertStageDetail(t, ctx, db, originalWord, enrich.TokenStage.Name, "outcome", "stale_canonical_skipped")
	assertReplayGeneration(t, ctx, db, job.ID, replayGenerationState{
		Status: "succeeded", Requested: 1, Claimed: 1, Completed: 1,
	})

	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{replacement}, []ethrpc.Bundle{original}, "reattach exact old hash")
	for attempts := 0; attempts < 6; attempts++ {
		dispatched, err := dispatcher.DispatchOne(ctx)
		if err != nil || dispatched.State != enrich.OutboxPublished {
			t.Fatalf("publish reattached canonical generation: result=%+v err=%v", dispatched, err)
		}
		var requested int64
		if err := db.QueryRowContext(ctx, `SELECT requested_generation FROM durable_jobs WHERE id = $1`, job.ID).Scan(&requested); err != nil {
			t.Fatal(err)
		}
		if requested == 2 {
			break
		}
	}
	assertReplayGeneration(t, ctx, db, job.ID, replayGenerationState{
		Status: "queued", Requested: 2, Claimed: 1, Completed: 1,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'token' AND stage_version = 1`,
		0, mustBytes(t, originalRef.Hash))

	processOne(t, ctx, worker)
	assertReplayGeneration(t, ctx, db, job.ID, replayGenerationState{
		Status: "succeeded", Requested: 2, Claimed: 2, Completed: 2,
	})
	var stale bool
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(details->>'outcome' = 'stale_canonical_skipped', FALSE)
		FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'token' AND stage_version = 1`,
		mustBytes(t, originalRef.Hash),
	).Scan(&stale); err != nil {
		t.Fatalf("read reattached token result: %v", err)
	}
	if stale {
		t.Fatal("reattached canonical hash retained its terminal stale skip")
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_job_replay_requests
		WHERE job_id = $1 AND source_kind = 'canonical-attach'`, 1, job.ID)
	var outboxGeneration int64
	if err := db.QueryRowContext(ctx, `
		SELECT generation FROM transactional_outbox
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		originalRef.Hash.String(),
	).Scan(&outboxGeneration); err != nil || outboxGeneration != 2 {
		t.Fatalf("reattach outbox generation=%d err=%v, want 2", outboxGeneration, err)
	}

	// The older orphan wake is now stale because this exact hash reattached. It
	// must be publishable rather than retrying forever against canonical journals.
	for attempts := 0; attempts < 8; attempts++ {
		result, err := dispatcher.DispatchOne(ctx)
		if err != nil {
			t.Fatalf("drain reattach outbox: %v", err)
		}
		if result.State == enrich.OutboxIdle {
			return
		}
		if result.State != enrich.OutboxPublished {
			t.Fatalf("drain reattach outbox result=%+v", result)
		}
	}
	t.Fatal("reattach outbox did not quiesce")
}

func TestLateTraceReplayRacingActiveABILeaseIsConsumedByNextGeneration(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block := testBundle(0, testHash(82_000), testHash(0), testHash(83_000), "active-abi-replay")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	proxyJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	abiJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ABIStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyLease, found, err := queue.Claim(ctx, "proxy-generation-one", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim proxy generation one: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	proxyProcessor := newEmptyProxyProcessor(t, db, reference.Hash.String())
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish proxy generation one: %v", err)
	}
	abiLease, found, err := queue.Claim(ctx, "abi-after-proxy-generation-one", []enrich.StageID{enrich.ABIStage}, time.Minute)
	if err != nil || !found || abiLease.Job.Generation != 2 {
		t.Fatalf("claim ABI generation two: lease=%+v found=%t err=%v", abiLease, found, err)
	}
	abiProcessor, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
	if err != nil {
		t.Fatal(err)
	}
	traceProcessor := newDerivedProcessors(t, db).trace
	traceJob := derivedJob(t, block, enrich.TraceStage)
	traceJob.Generation = 1
	traceResult, err := traceProcessor.Process(ctx, traceJob)
	if err != nil || traceResult.Details["proxy_requeued"] != "true" || traceResult.Details["abi_requeued"] != "true" {
		t.Fatalf("late Trace replay request: result=%+v err=%v", traceResult, err)
	}
	assertReplayGeneration(t, ctx, db, proxyJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 2, Claimed: 1, Completed: 1,
	})
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "leased", Requested: 3, Claimed: 2, Completed: 0, Leased: true,
	})
	// Trace also invalidates the exact proxy dependency. The active ABI owner
	// therefore cannot enter publication; its production retry transition
	// consumes generation two and preserves the pending generation atomically.
	if _, err := abiProcessor.ProcessLease(ctx, abiLease, queue); err == nil {
		t.Fatal("ABI generation two ignored its replayed proxy dependency")
	}
	if err := queue.Retry(ctx, abiLease, enrich.Retry{Reason: "proxy dependency replay pending"}); err != nil {
		t.Fatalf("consume ABI generation two with replay pending: %v", err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 3, Claimed: 2, Completed: 2,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi' AND stage_version = 1`,
		0, mustBytes(t, reference.Hash))

	proxyLease, found, err = queue.Claim(ctx, "proxy-generation-two", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found || proxyLease.Job.Generation != 2 || proxyLease.Job.Attempt != 1 {
		t.Fatalf("claim proxy generation two: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish proxy generation two: %v", err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 4, Claimed: 2, Completed: 2,
	})
	abiLease, found, err = queue.Claim(ctx, "abi-after-proxy-generation-two", []enrich.StageID{enrich.ABIStage}, time.Minute)
	if err != nil || !found || abiLease.Job.Generation != 4 || abiLease.Job.Attempt != 1 {
		t.Fatalf("claim ABI generation four: lease=%+v found=%t err=%v", abiLease, found, err)
	}
	abiResult, err := abiProcessor.ProcessLease(ctx, abiLease, queue)
	if err != nil || abiResult.State != enrich.ResultComplete {
		t.Fatalf("publish ABI generation four: result=%+v err=%v", abiResult, err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "succeeded", Requested: 4, Claimed: 4, Completed: 4,
	})

	duplicate, err := traceProcessor.Process(ctx, traceJob)
	if err != nil || duplicate.Details["proxy_requeued"] != "false" {
		t.Fatalf("duplicate source generation did not quiesce: result=%+v err=%v", duplicate, err)
	}
	assertReplayGeneration(t, ctx, db, proxyJob.Job.ID, replayGenerationState{
		Status: "succeeded", Requested: 2, Claimed: 2, Completed: 2,
	})
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "succeeded", Requested: 4, Claimed: 4, Completed: 4,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_job_replay_requests
		WHERE job_id IN ($1, $2) AND source_kind = 'stage-completion'`, 4, proxyJob.Job.ID, abiJob.Job.ID)
}

func TestExpiredABILeaseReclaimAtomicallyClearsPersistedPreviousGeneration(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(70_000), testHash(0), testHash(71_000), "abi-replay-genesis")
	commitCanonical(t, ctx, repository, genesis)
	direct, proxy, implementation := testAddress(700), testAddress(701), testAddress(702)
	recipient, caller := testAddress(703), testAddress(704)
	block := abiFixtureBundle(t, direct, proxy, recipient, caller)
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
	reference := mustBlockRef(t, block)
	word, _ := enrich.ParseWord(reference.Hash.String())
	directCode, proxyCode, implementationCode := testHash(72_000), testHash(72_001), testHash(72_002)
	insertABICodeObservation(t, ctx, db, reference, direct, directCode)
	insertABICodeObservation(t, ctx, db, reference, proxy, proxyCode)
	insertABIVerifiedContract(t, ctx, db, direct, directCode)
	insertABIVerifiedContract(t, ctx, db, implementation, implementationCode)
	insertABISignatureCandidates(t, ctx, db)
	insertABITrace(t, ctx, db, reference, block, proxy, recipient, caller)

	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	proxyJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	abiJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ABIStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyLease, found, err := queue.Claim(ctx, "crash-proxy-generation-one", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim crash fixture proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	proxyProcessor := newEmptyProxyProcessor(t, db, reference.Hash.String())
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish crash fixture proxy: %v", err)
	}
	// The proxy stage sees exact code history and therefore has no RPC candidate;
	// add the historical implementation fixture only after its production
	// publication so replay discovery cannot conflict with the synthetic hash.
	insertABIProxyObservation(t, ctx, db, reference, proxy, proxyCode, implementation, implementationCode)
	abiLease, found, err := queue.Claim(ctx, "crash-abi-generation-two", []enrich.StageID{enrich.ABIStage}, time.Minute)
	if err != nil || !found || abiLease.Job.Generation != 2 {
		t.Fatalf("claim crash fixture ABI generation two: lease=%+v found=%t err=%v", abiLease, found, err)
	}
	abiProcessor, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
	if err != nil {
		t.Fatal(err)
	}
	abiResult, err := abiProcessor.Process(ctx, abiLease.Job)
	if err != nil || abiResult.State != enrich.ResultComplete {
		t.Fatalf("persist crash fixture ABI generation two: result=%+v err=%v", abiResult, err)
	}
	for table, count := range map[string]int{"contract_abis": 4, "abi_decodings": 5} {
		assertRowCount(t, ctx, db,
			"SELECT count(*) FROM "+table+" WHERE chain_id = 1 AND block_hash = $1",
			count, mustBytes(t, reference.Hash),
		)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi' AND stage_version = 1`,
		1, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi@1'`,
		1, mustBytes(t, reference.Hash))

	// Model a late Trace/Proxy completion through the public enqueue contract:
	// the active owner keeps its token while the durable source advances the
	// requested generation. It then crashes before Finish consumes that marker.
	replay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ABIStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		MaxAttempts: 3, Replay: enrich.ReplaySource{Kind: "stage-completion", Key: "trace-crash-generation-one"},
	})
	if err != nil || replay.Created || !replay.Replayed {
		t.Fatalf("request ABI replay during active lease: result=%+v err=%v", replay, err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "leased", Requested: 3, Claimed: 2, Completed: 0, Leased: true,
	})
	execFixture(t, ctx, db, `
		UPDATE durable_jobs
		SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1`, abiJob.Job.ID)

	// Reclaiming generation three must clear every generation-two observable in
	// the same transaction that publishes the new lease, even though the old
	// worker never reached Finish or Retry.
	reclaimed, found, err := queue.Claim(ctx, "crash-abi-generation-three", []enrich.StageID{enrich.ABIStage}, time.Minute)
	if err != nil || !found || reclaimed.Job.Generation != 3 || reclaimed.Job.Attempt != 1 {
		t.Fatalf("reclaim ABI generation three: lease=%+v found=%t err=%v", reclaimed, found, err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "leased", Requested: 3, Claimed: 3, Completed: 0, Leased: true,
	})
	for _, table := range []string{"contract_abis", "abi_decodings"} {
		assertRowCount(t, ctx, db,
			"SELECT count(*) FROM "+table+" WHERE chain_id = 1 AND block_hash = $1",
			0, mustBytes(t, reference.Hash),
		)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi' AND stage_version = 1`,
		0, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi@1'`,
		0, mustBytes(t, reference.Hash))
	if err := queue.Finish(ctx, abiLease, enrich.StageResult{State: enrich.ResultFailed, Error: "expired writer"}); !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("expired generation-two terminal write err=%v, want ErrLeaseLost", err)
	}

	abiResult, err = abiProcessor.ProcessLease(ctx, reclaimed, queue)
	if err != nil || abiResult.State != enrich.ResultComplete {
		t.Fatalf("publish rebuilt ABI generation three: result=%+v err=%v", abiResult, err)
	}
	assertReplayGeneration(t, ctx, db, abiJob.Job.ID, replayGenerationState{
		Status: "succeeded", Requested: 3, Claimed: 3, Completed: 3,
	})
	_ = proxyJob
}

type replayGenerationState struct {
	Status    string
	Requested int64
	Claimed   int64
	Completed int64
	Leased    bool
}

func assertReplayGeneration(t *testing.T, ctx context.Context, db *sql.DB, jobID string, want replayGenerationState) {
	t.Helper()
	var got replayGenerationState
	var leasedGeneration sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT status, requested_generation, claimed_generation,
		       completed_generation, leased_generation
		FROM durable_jobs WHERE id = $1`, jobID,
	).Scan(&got.Status, &got.Requested, &got.Claimed, &got.Completed, &leasedGeneration); err != nil {
		t.Fatalf("read replay generation for job %s: %v", jobID, err)
	}
	got.Leased = leasedGeneration.Valid
	if got != want {
		t.Fatalf("job %s replay generation=%+v want=%+v (leased_generation=%v)", jobID, got, want, leasedGeneration)
	}
}
