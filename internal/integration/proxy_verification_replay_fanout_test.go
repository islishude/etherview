//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestProxyReplayCarriesLargeUntouchedGenerationWithoutRPCFanout(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(99_500), testHash(0), testHash(99_600),
		"verification-replay-fanout-genesis",
	)
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number:     1,
		ParentHash: genesis.Block.Hash(),
		ExtraData:  []byte("verification-replay-carry-forward"),
		RawExtra:   map[string]any{"integrationVariant": "verification-replay-carry-forward"},
	})
	if err != nil {
		t.Fatalf("build replay carry-forward block: %v", err)
	}
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)
	publishProxyVerificationInteractionCoverage(t, ctx, db, blockRef)

	const (
		priorProxyCount  = 4097
		proxyAddressBase = 120_000
	)
	carryResolutionProxy := testAddress(proxyAddressBase)
	redetectedProxy := testAddress(proxyAddressBase + 1)
	beacon := testAddress(130_000)
	untouchedNegative := testAddress(130_001)
	implementation := testAddress(130_002)
	implementationCode := []byte{0x60, 0x7f, 0x60, 0x00}
	implementationHash := common.BytesToHash(crypto.Keccak256(implementationCode))
	proxyRuntime := []byte{0x60, 0x70, 0x60, 0x00}
	proxyHash := common.BytesToHash(crypto.Keccak256(proxyRuntime))
	redetectedRuntime := []byte{0x60, 0x71, 0x60, 0x00}
	redetectedHash := common.BytesToHash(crypto.Keccak256(redetectedRuntime))
	beaconRuntime := []byte{0x60, 0x72, 0x60, 0x00}
	beaconHash := common.BytesToHash(crypto.Keccak256(beaconRuntime))
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	proxyArtifactJob := insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, blockRef, generation, compilerDigest, executorDigest,
		carryResolutionProxy, proxyHash, proxyRuntime, "erc1967_proxy", nil,
	)
	insertProxyVerificationCode(
		t, ctx, db, blockRef, redetectedProxy, redetectedHash, redetectedRuntime,
	)

	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(blockRef.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: blockRef.Number,
	})
	if err != nil || !enqueued.Created {
		t.Fatalf("enqueue initial proxy generation: result=%+v error=%v", enqueued, err)
	}
	firstLease, found, err := queue.Claim(
		ctx, "proxy-carry-forward-generation-one", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || firstLease.Job.ID != enqueued.Job.ID || firstLease.Job.Generation != 1 {
		t.Fatalf("claim initial proxy generation: lease=%+v found=%t error=%v", firstLease, found, err)
	}
	seedProxyCarryForwardGeneration(
		t, ctx, db, blockRef, firstLease.Job.ID, firstLease.Job.Generation,
		proxyAddressBase, priorProxyCount, carryResolutionProxy, proxyHash,
		redetectedProxy, redetectedHash, implementation, implementationHash,
		beacon, beaconHash, untouchedNegative, proxyArtifactJob,
	)
	firstProcessor := proxyVerificationProcessor(
		t, db, blockRef, map[common.Address]proxyVerificationRPCState{},
	)
	firstResult, err := firstProcessor.ProcessLease(ctx, firstLease, queue)
	if err != nil || firstResult.State != enrich.ResultComplete ||
		firstResult.Details["candidates"] != "0" {
		t.Fatalf("publish initial proxy generation: result=%+v error=%v", firstResult, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observation_generations
		WHERE chain_id = 1 AND observation_block_hash = $1
		  AND durable_job_id = $2 AND job_generation = 1`,
		priorProxyCount, blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM beacon_observation_generations
		WHERE chain_id = 1 AND observation_block_hash = $1
		  AND durable_job_id = $2 AND job_generation = 1`,
		1, blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_artifact_resolutions
		WHERE chain_id = 1 AND observation_block_hash = $1
		  AND durable_job_id = $2 AND job_generation = 1`,
		1, blockRef.Hash.Bytes(), enqueued.Job.ID)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20,
		MaxResultBytes:  1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	verificationJob, created, err := repository.SubmitV2(ctx, verifierV2AddressSubmission(
		redetectedProxy.Bytes(), redetectedHash.Bytes(), blockRef.Hash.Bytes(),
		redetectedRuntime,
	))
	if err != nil || !created {
		t.Fatalf("submit direct replay verification: created=%t error=%v", created, err)
	}
	verificationLease, found, err := repository.Claim(ctx, "verification-replay-carry", time.Minute)
	if err != nil || !found || verificationLease.Job.ID != verificationJob.ID {
		t.Fatalf("claim direct replay verification: found=%t lease=%+v error=%v", found, verificationLease, err)
	}
	if err := repository.BindCompiler(
		ctx, verificationLease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatalf("bind direct replay verification compiler: %v", err)
	}
	if err := repository.CompleteV2(
		ctx, verificationLease, "verification_success", verifierV2SuccessOutcome(t, "full"),
	); err != nil {
		t.Fatalf("complete direct replay verification: %v", err)
	}

	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs
		WHERE id = $1::uuid AND status = 'succeeded'`, 1, verificationJob.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_replay_targets
		WHERE source_verification_job_id = $1::uuid`,
		1, verificationJob.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_replay_targets
		WHERE source_verification_job_id = $1::uuid
		  AND address = $2 AND target_kind = 'proxy'`,
		1, verificationJob.ID, redetectedProxy.Bytes())

	var (
		replayStatus                                                string
		requestedGeneration, claimedGeneration, completedGeneration int64
	)
	if err := db.QueryRowContext(ctx, `
		SELECT status, requested_generation, claimed_generation, completed_generation
		FROM durable_jobs
		WHERE id = $1::bigint`, enqueued.Job.ID,
	).Scan(
		&replayStatus, &requestedGeneration, &claimedGeneration, &completedGeneration,
	); err != nil {
		t.Fatalf("load durable proxy replay: %v", err)
	}
	if replayStatus != "queued" || requestedGeneration != 2 ||
		claimedGeneration != 1 || completedGeneration != 1 {
		t.Fatalf(
			"durable proxy replay = status %s requested %d claimed %d completed %d, want queued 2/1/1",
			replayStatus, requestedGeneration, claimedGeneration, completedGeneration,
		)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_job_replay_requests
		WHERE job_id = $1 AND source_kind = 'verification-publication'
		  AND source_key = $2 AND requested_generation = $3`,
		1, enqueued.Job.ID, verificationJob.ID, 2)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO durable_job_replay_requests (
			job_id, source_kind, source_key, requested_generation
		) VALUES ($1::bigint, 'verification-publication', 'malformed-legacy-source', 1)`,
		enqueued.Job.ID,
	); err != nil {
		t.Fatalf("insert malformed legacy replay source: %v", err)
	}

	publishPendingEmptyProxyVerificationCoverage(
		t, ctx, db, blockRef,
		map[common.Address]proxyVerificationRPCState{
			redetectedProxy: {code: redetectedRuntime},
		},
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_jobs
		WHERE id = $1::bigint AND status = 'succeeded'
		  AND requested_generation = 2
		  AND completed_generation = 2
		  AND last_error IS NULL`,
		1, enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE chain_id = 1 AND block_hash = $1
		  AND stage = 'proxy' AND stage_version = 2 AND state = 'complete'
		  AND durable_job_id = $2::bigint AND job_generation = 2`,
		1, blockRef.Hash.Bytes(), enqueued.Job.ID)
	var candidates, carriedProxies, carriedBeacons, carriedResolutions, carriedNegative int
	if err := db.QueryRowContext(ctx, `
		SELECT (details->>'candidates')::integer,
		       (details->>'carried_proxies')::integer,
		       (details->>'carried_beacons')::integer,
		       (details->>'carried_resolutions')::integer,
		       (details->>'carried_negative_evidence')::integer
		FROM published_block_stage_results
		WHERE durable_job_id = $1::bigint AND job_generation = 2`, enqueued.Job.ID,
	).Scan(
		&candidates, &carriedProxies, &carriedBeacons, &carriedResolutions, &carriedNegative,
	); err != nil {
		t.Fatalf("load carry-forward replay details: %v", err)
	}
	if candidates != 1 || carriedProxies != priorProxyCount-1 || carriedBeacons != 1 ||
		carriedResolutions != 1 || carriedNegative != 1 {
		t.Fatalf(
			"carry-forward details candidates=%d proxies=%d beacons=%d resolutions=%d negative=%d",
			candidates, carriedProxies, carriedBeacons, carriedResolutions, carriedNegative,
		)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observation_generations
		WHERE chain_id = 1 AND observation_block_hash = $1
		  AND durable_job_id = $2::bigint AND job_generation = 2`,
		priorProxyCount-1, blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observation_generations
		WHERE chain_id = 1 AND proxy_address = $1 AND observation_block_hash = $2
		  AND durable_job_id = $3::bigint AND job_generation = 2`,
		0, redetectedProxy.Bytes(), blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM beacon_observation_generations
		WHERE chain_id = 1 AND beacon_address = $1 AND observation_block_hash = $2
		  AND durable_job_id = $3::bigint AND job_generation = 2`,
		1, beacon.Bytes(), blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_artifact_resolutions
		WHERE chain_id = 1 AND proxy_address = $1 AND observation_block_hash = $2
		  AND durable_job_id = $3::bigint AND job_generation = 2`,
		1, carryResolutionProxy.Bytes(), blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2
		  AND durable_job_id = $3::bigint AND job_generation = 2
		  AND candidate_kind = 'proxy' AND detection_state = 'not_detected'
		  AND reason = 'not_proxy'`,
		1, redetectedProxy.Bytes(), blockRef.Hash.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2
		  AND durable_job_id = $3::bigint AND job_generation = 2
		  AND candidate_kind = 'beacon' AND detection_state = 'rejected'
		  AND reason = 'invalid_beacon_implementation'`,
		1, untouchedNegative.Bytes(), blockRef.Hash.Bytes(), enqueued.Job.ID)
}

func TestVerificationReplayPersistsGenerationOneSource(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(99_700), testHash(0), testHash(99_800),
		"verification-generation-one-genesis",
	)
	block := testBundle(
		1, testHash(99_701), genesis.Block.Hash(), testHash(99_801),
		"verification-generation-one-block",
	)
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	blockRef := mustBlockRef(t, block)
	target := testAddress(140_000)
	runtime := []byte{0x60, 0x01, 0x60, 0x02}
	codeHash := common.BytesToHash(crypto.Keccak256(runtime))
	insertProxyVerificationCode(t, ctx, db, blockRef, target, codeHash, runtime)

	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20,
		MaxResultBytes:  1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := repository.SubmitV2(ctx, verifierV2AddressSubmission(
		target.Bytes(), codeHash.Bytes(), blockRef.Hash.Bytes(), runtime,
	))
	if err != nil || !created {
		t.Fatalf("submit generation-one verification: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "verification-generation-one", time.Minute)
	if err != nil || !found || lease.Job.ID != job.ID {
		t.Fatalf("claim generation-one verification: found=%t lease=%+v error=%v", found, lease, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatalf("bind generation-one verification compiler: %v", err)
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "full"),
	); err != nil {
		t.Fatalf("complete generation-one verification: %v", err)
	}

	var proxyJobID int64
	if err := db.QueryRowContext(ctx, `
		SELECT id FROM durable_jobs
		WHERE chain_id = 1 AND stage = 'proxy' AND stage_version = 2
		  AND payload->>'block_hash' = $1
		  AND requested_generation = 1 AND status = 'queued'`,
		blockRef.Hash.String(),
	).Scan(&proxyJobID); err != nil {
		t.Fatalf("load generation-one proxy replay job: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM durable_job_replay_requests
		WHERE job_id = $1 AND source_kind = 'verification-publication'
		  AND source_key = $2 AND requested_generation = 1`,
		1, proxyJobID, job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_replay_targets
		WHERE source_verification_job_id = $1::uuid
		  AND address = $2 AND target_kind = 'proxy'`,
		1, job.ID, target.Bytes())
	if _, err := db.ExecContext(ctx, `
		INSERT INTO durable_job_replay_requests (
			job_id, source_kind, source_key, requested_generation
		) VALUES ($1, 'verification-publication', 'invalid-generation-zero', 0)`,
		proxyJobID,
	); err == nil {
		t.Fatal("durable replay source accepted generation zero")
	}
}

func proxyVerificationProcessor(
	t *testing.T,
	db *sql.DB,
	block store.BlockRef,
	states map[common.Address]proxyVerificationRPCState,
) *enrich.PostgresProxyProcessor {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "proxy-carry-forward-state",
		Client: newIntegrationRPCClient(t, "eth", &proxyVerificationRPCService{
			blockHash: block.Hash,
			states:    states,
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func seedProxyCarryForwardGeneration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	jobID string,
	jobGeneration uint64,
	proxyAddressBase uint64,
	proxyCount int,
	carryResolutionProxy common.Address,
	carryResolutionProxyHash common.Hash,
	redetectedProxy common.Address,
	redetectedProxyHash common.Hash,
	implementation common.Address,
	implementationHash common.Hash,
	beacon common.Address,
	beaconHash common.Hash,
	untouchedNegative common.Address,
	proxyArtifactJob string,
) {
	t.Helper()
	if carryResolutionProxy != testAddress(proxyAddressBase) ||
		redetectedProxy != testAddress(proxyAddressBase+1) {
		t.Fatal("carry-forward fixture proxy range is inconsistent")
	}
	if _, err := db.ExecContext(ctx, `
		WITH proxy_addresses AS (
			SELECT decode(
				lpad(to_hex($1::bigint + series.value), 40, '0'), 'hex'
			) AS address
			FROM generate_series(0, $2::integer - 1) AS series(value)
		)
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, stage_version,
			proxy_code_hash, proxy_kind, proxy_pattern, standard_version,
			implementation_address, implementation_code_hash,
			confidence, evidence_state, canonical, details
		)
		SELECT 1, address, $3::numeric, $4, 2,
		       CASE WHEN address = $5::bytea THEN $6::bytea ELSE $7::bytea END,
		       'eip1967', 'erc1967', '5.6.1', $8, $9,
		       'high', 'exact', TRUE, '{"fixture":"carry-forward"}'::jsonb
		FROM proxy_addresses`,
		proxyAddressBase, proxyCount, block.Number, block.Hash.Bytes(),
		carryResolutionProxy.Bytes(), carryResolutionProxyHash.Bytes(),
		redetectedProxyHash.Bytes(), implementation.Bytes(), implementationHash.Bytes(),
	); err != nil {
		t.Fatalf("insert carry-forward proxy observations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		WITH proxy_addresses AS (
			SELECT decode(
				lpad(to_hex($1::bigint + series.value), 40, '0'), 'hex'
			) AS address
			FROM generate_series(0, $2::integer - 1) AS series(value)
		)
		INSERT INTO proxy_observation_generations (
			chain_id, proxy_address, observation_block_hash,
			observation_stage_version, durable_job_id, job_generation
		)
		SELECT 1, address, $3, 2, $4::bigint, $5::bigint
		FROM proxy_addresses`,
		proxyAddressBase, proxyCount, block.Hash.Bytes(), jobID, jobGeneration,
	); err != nil {
		t.Fatalf("insert carry-forward proxy witnesses: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO beacon_implementation_observations (
			chain_id, beacon_address, block_number, block_hash, beacon_code_hash,
			implementation_address, implementation_code_hash, stage_version,
			confidence, canonical, details
		) VALUES (
			1, $1, $2::numeric, $3, $4,
			$5, $6, 2, 'high', TRUE, '{"fixture":"carry-forward"}'::jsonb
		)`, beacon.Bytes(), block.Number, block.Hash.Bytes(), beaconHash.Bytes(),
		implementation.Bytes(), implementationHash.Bytes(),
	); err != nil {
		t.Fatalf("insert carry-forward beacon observation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO beacon_observation_generations (
			chain_id, beacon_address, observation_block_hash,
			observation_stage_version, durable_job_id, job_generation
		) VALUES (1, $1, $2, 2, $3::bigint, $4::bigint)`,
		beacon.Bytes(), block.Hash.Bytes(), jobID, jobGeneration,
	); err != nil {
		t.Fatalf("insert carry-forward beacon witness: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO proxy_artifact_resolutions (
			chain_id, proxy_address, observation_block_hash,
			observation_stage_version, proxy_code_hash, proxy_kind,
			proxy_pattern, standard_version, implementation_address,
			implementation_code_hash, proxy_artifact_job_id,
			durable_job_id, job_generation, evidence
		) VALUES (
			1, $1, $2, 2, $3, 'eip1967',
			'erc1967', '5.6.1', $4, $5, $6::uuid,
			$7::bigint, $8::bigint, '{"fixture":"carry-forward"}'::jsonb
		)`, carryResolutionProxy.Bytes(), block.Hash.Bytes(), carryResolutionProxyHash.Bytes(),
		implementation.Bytes(), implementationHash.Bytes(), proxyArtifactJob, jobID, jobGeneration,
	); err != nil {
		t.Fatalf("insert carry-forward artifact resolution: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO proxy_detection_evidence (
			chain_id, address, block_number, block_hash, stage_version,
			code_hash, candidate_kind, detection_state, reason, canonical,
			durable_job_id, job_generation, details
		) VALUES (
			1, $1, $2::numeric, $3, 2,
			$4, 'beacon', 'rejected', 'invalid_beacon_implementation', TRUE,
			$5::bigint, $6::bigint, '{"fixture":"carry-forward"}'::jsonb
		)`, untouchedNegative.Bytes(), block.Number, block.Hash.Bytes(), beaconHash.Bytes(),
		jobID, jobGeneration,
	); err != nil {
		t.Fatalf("insert carry-forward negative evidence: %v", err)
	}
}
