//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/contractartifact"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/derivedverify"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestFactoryVerificationBackfillsUniquelyMatchedCreatedContract(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(7_500), testHash(0), testHash(8_500), "derived-genesis")
	factoryBlock := testBundle(1, testHash(7_501), testHash(7_500), testHash(8_501), "derived-factory")
	childBlock := testBundle(2, testHash(7_502), testHash(7_501), testHash(8_502), "derived-child")
	replacementBlock := testBundle(3, testHash(7_503), testHash(7_502), testHash(8_503), "derived-replaced-factory")
	forwardBlock := testBundle(4, testHash(7_504), testHash(7_503), testHash(8_504), "derived-forward-child")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, factoryBlock)
	commitCanonical(t, ctx, core, childBlock)
	commitCanonical(t, ctx, core, replacementBlock)

	factoryAddress := mustBytes(t, testAddress(750))
	childAddress := mustBytes(t, testAddress(751))
	wrongEpochChildAddress := mustBytes(t, testAddress(752))
	grandchildAddress := mustBytes(t, testAddress(753))
	pendingGrandchildAddress := mustBytes(t, testAddress(754))
	factoryRuntime := []byte{0x60, 0xaa}
	childCreation := []byte{0x60, 0x10}
	childRuntime := []byte{0x60, 0x11}
	grandchildCreation := []byte{0x60, 0x12}
	grandchildRuntime := []byte{0x60, 0x13}
	factoryCodeHash := keccak256(factoryRuntime)
	childCodeHash := keccak256(childRuntime)
	replacementFactoryRuntime := []byte{0x60, 0xbb}
	replacementFactoryCodeHash := keccak256(replacementFactoryRuntime)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES
			(1, $1, 1, $2, $3, $4, TRUE),
			(1, $1, 2, $5, $3, $4, TRUE),
			(1, $6, 1, $2, $7, $8, TRUE),
			(1, $1, 3, $9, $10, $11, TRUE),
			(1, $12, 3, $9, $7, $8, TRUE)`,
		factoryAddress, factoryBlock.Block.Hash().Bytes(), factoryCodeHash, factoryRuntime,
		childBlock.Block.Hash().Bytes(), childAddress, childCodeHash, childRuntime,
		replacementBlock.Block.Hash().Bytes(), replacementFactoryCodeHash,
		replacementFactoryRuntime, wrongEpochChildAddress,
	)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (
			1, 1, $1, $2, 0, '1', '', 1, 'CREATE', $3, $4,
			0, 50000, 21000, $5, $6, NULL, FALSE, TRUE
		)`,
		factoryBlock.Block.Hash().Bytes(), factoryBlock.Block.Transactions()[0].Hash().Bytes(),
		factoryAddress, childAddress, childCreation, childRuntime,
	)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (
			1, 3, $1, $2, 0, '0', '', 1, 'CREATE', $3, $4,
			0, 50000, 21000, $5, $6, NULL, FALSE, TRUE
		)`,
		replacementBlock.Block.Hash().Bytes(), replacementBlock.Block.Transactions()[0].Hash().Bytes(),
		factoryAddress, wrongEpochChildAddress, childCreation, childRuntime,
	)

	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	submission := verifierV2AddressSubmission(
		factoryAddress, factoryCodeHash, childBlock.Block.Hash().Bytes(), factoryRuntime,
	)
	submission.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {} contract Child {}"}},"settings":{}}`)
	submission.StandardJSONVariants = []json.RawMessage{submission.StandardJSON}
	_, created, err := repository.SubmitV2(ctx, submission)
	if err != nil || !created {
		t.Fatalf("submit factory verification: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "derived-factory-verifier", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim factory verification: found=%t error=%v", found, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatal(err)
	}
	unit := verify.AuthenticatedCompilation{
		StandardJSON: submission.StandardJSON,
		Candidates: []verify.CandidateArtifact{
			derivedCandidate("A", "0x6000", "0x"+hex.EncodeToString(factoryRuntime)),
			derivedCandidate("Child", "0x"+hex.EncodeToString(childCreation), "0x"+hex.EncodeToString(childRuntime)),
			derivedCandidate("Grandchild", "0x"+hex.EncodeToString(grandchildCreation), "0x"+hex.EncodeToString(grandchildRuntime)),
		},
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "partial"), unit,
	); err != nil {
		t.Fatalf("complete factory verification: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE chain_id = 1 AND creator_address = $1 AND valid_from_block = 1
		  AND cursor_block_number = 1 AND status = 'queued'`, 1,
		factoryAddress,
	)
	worker, err := derivedverify.NewWorker(db, repository, derivedverify.Options{
		WorkerID: "derived-history", LeaseDuration: time.Minute,
		PollInterval: time.Millisecond, MaxTraces: 10, PublishMatches: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process historical derived scan: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM verified_contracts AS verified
		JOIN verification_jobs AS job ON job.id = verified.verification_job_id
		WHERE verified.chain_id = 1 AND verified.address = $1
		  AND verified.code_hash = $2 AND verified.valid_from_block = 1
		  AND verified.file_name = 'A.sol' AND verified.contract_name = 'Child'
		  AND job.kind = 'derived' AND job.status = 'succeeded'`, 1,
		childAddress, childCodeHash,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE chain_id = 1 AND created_address = $1 AND call_type = 'CREATE'
		  AND status = 'matched' AND file_name = 'A.sol'
		  AND contract_name = 'Child' AND verification_job_id IS NOT NULL`, 1,
		childAddress,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE chain_id = 1 AND creator_address = $1 AND status = 'succeeded'`, 1,
		factoryAddress,
	)
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("settle transitive child scan: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE chain_id = 1 AND creator_address = $1 AND status = 'succeeded'`, 1,
		childAddress,
	)

	commitCanonical(t, ctx, core, forwardBlock)
	grandchildCodeHash := keccak256(grandchildRuntime)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES
			(1, $1, 4, $2, $3, $4, TRUE),
			(1, $5, 4, $2, $6, $7, TRUE)`,
		grandchildAddress, forwardBlock.Block.Hash().Bytes(), grandchildCodeHash, grandchildRuntime,
		factoryAddress, factoryCodeHash, factoryRuntime,
	)
	var reattachedEpoch string
	if err := db.QueryRowContext(ctx, dbgen.DerivedVerifyCreatorCodeEpochStart,
		"1", factoryAddress, factoryCodeHash, "4", forwardBlock.Block.Hash().Bytes(),
	).Scan(&reattachedEpoch); err != nil || reattachedEpoch != "4" {
		t.Fatalf("A-to-B-to-A creator epoch = %q, error = %v", reattachedEpoch, err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (
			1, 4, $1, $2, 0, '0', '', 1, 'CREATE', $3, $4,
			0, 50000, 21000, $5, $6, NULL, FALSE, TRUE
		)`,
		forwardBlock.Block.Hash().Bytes(), forwardBlock.Block.Transactions()[0].Hash().Bytes(),
		childAddress, grandchildAddress, grandchildCreation, grandchildRuntime,
	)
	var traceJobID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, payload
		) VALUES (1, 'enrichment', 'trace', 3, 'derived-forward-fixture',
			jsonb_build_object('block_number', '4', 'block_hash', $1::text))
		RETURNING id`, forwardBlock.Block.Hash().Hex()).Scan(&traceJobID); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 1, 1, 4, $2, 'trace', 3, 'complete', '{"frames":1}')`,
		traceJobID, forwardBlock.Block.Hash().Bytes(),
	)
	forwardWorker, err := derivedverify.NewForwardWorker(db, derivedverify.ForwardOptions{
		WorkerID: "derived-forward", LeaseDuration: time.Minute,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err = forwardWorker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("dispatch forward derived block: processed=%t error=%v", processed, err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process forward derived scan: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM verified_contracts AS verified
		JOIN verification_jobs AS job ON job.id = verified.verification_job_id
		WHERE verified.chain_id = 1 AND verified.address = $1
		  AND verified.code_hash = $2 AND verified.valid_from_block = 4
		  AND verified.contract_name = 'Grandchild' AND job.kind = 'derived'`, 1,
		grandchildAddress, grandchildCodeHash,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_forward_blocks
		WHERE chain_id = 1 AND block_hash = $1 AND status = 'succeeded'`, 1,
		forwardBlock.Block.Hash().Bytes(),
	)

	// The same block hash at a newer published trace generation is distinct
	// forward evidence. It revives a recoverable failed scan and rewinds a cursor
	// that has already advanced beyond the source block.
	execFixture(t, ctx, db, `
		UPDATE derived_verification_scans
		SET status = 'failed', cursor_block_number = 10,
			cursor_transaction_hash = decode(repeat('ff', 32), 'hex'),
			cursor_trace_path = 'z', attempt_count = max_attempts,
			last_error = 'attempts_exhausted'
		WHERE creator_address = $1`, childAddress)
	execFixture(t, ctx, db, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 2, 1, 4, $2, 'trace', 3, 'complete', '{"frames":1}')`,
		traceJobID, forwardBlock.Block.Hash().Bytes(),
	)
	processed, err = forwardWorker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("dispatch replayed trace generation: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE creator_address = $1 AND status = 'queued'
		  AND cursor_block_number = 4
		  AND cursor_transaction_hash = decode(repeat('00', 32), 'hex')
		  AND cursor_trace_path = '' AND attempt_count = 0
		  AND last_error IS NULL`, 1, childAddress)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_forward_blocks
		WHERE chain_id = 1 AND block_hash = $1 AND source_stage = 'trace'
		  AND status = 'succeeded'`, 2, forwardBlock.Block.Hash().Bytes())
	execFixture(t, ctx, db, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 2, 1, 4, $2, 'trace', 3, 'complete', '{"frames":1}')
		ON CONFLICT DO NOTHING`, traceJobID, forwardBlock.Block.Hash().Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_forward_blocks
		WHERE source_job_id = $1 AND source_generation = 2`, 1, traceJobID)
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("settle replayed trace generation: processed=%t error=%v", processed, err)
	}
	childArtifact, found, err := repository.VerifiedContract(
		ctx, 1, "0x"+hex.EncodeToString(childAddress),
	)
	if err != nil || !found || childArtifact.VerificationOrigin != verify.VerificationOriginFactoryDerived ||
		childArtifact.DerivedFrom == nil ||
		childArtifact.DerivedFrom.CreatorAddress != "0x"+hex.EncodeToString(factoryAddress) ||
		childArtifact.DerivedFrom.ParentContractName != "A" ||
		len(childArtifact.DerivedChildren) != 1 ||
		childArtifact.DerivedChildren[0].Address != "0x"+hex.EncodeToString(grandchildAddress) {
		t.Fatalf("child provenance: found=%t artifact=%+v error=%v", found, childArtifact, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE chain_id = 1 AND created_address = $1`, 0, wrongEpochChildAddress)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE chain_id = 1 AND address = $1`, 0, wrongEpochChildAddress)

	var compilationID string
	if err := db.QueryRowContext(ctx, `
		SELECT id::text FROM verification_compilation_units
		WHERE source_job_id = (
			SELECT verification_job_id FROM verified_contracts
			WHERE chain_id = 1 AND address = $1 AND code_hash = $2
		)`, factoryAddress, factoryCodeHash).Scan(&compilationID); err != nil {
		t.Fatal(err)
	}

	// Trace publication may precede the proxy generation that persists the
	// created address's exact runtime. The later proxy generation must wake the
	// pending attempt without an operator backfill.
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (
			1, 4, $1, $2, 0, 'pending', '0', 2, 'CREATE', $3, $4,
			0, 50000, 21000, $5, $6, NULL, FALSE, TRUE
		)`, forwardBlock.Block.Hash().Bytes(), forwardBlock.Block.Transactions()[0].Hash().Bytes(),
		childAddress, pendingGrandchildAddress, grandchildCreation, grandchildRuntime)
	execFixture(t, ctx, db, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 3, 1, 4, $2, 'trace', 3, 'complete', '{"frames":2}')`,
		traceJobID, forwardBlock.Block.Hash().Bytes())
	processed, err = forwardWorker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("dispatch pending-runtime trace generation: processed=%t error=%v", processed, err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("record pending-runtime attempt: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE compilation_id = $1::uuid AND created_address = $2
		  AND status = 'pending_runtime'`, 1, compilationID, pendingGrandchildAddress)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 4, $2, $3, $4, TRUE)`,
		pendingGrandchildAddress, forwardBlock.Block.Hash().Bytes(), grandchildCodeHash,
		grandchildRuntime)
	var proxyJobID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO durable_jobs (
			chain_id, kind, stage, stage_version, idempotency_key, payload
		) VALUES (1, 'enrichment', 'proxy', 2, 'derived-proxy-fixture',
			jsonb_build_object('block_number', '4', 'block_hash', $1::text))
		RETURNING id`, forwardBlock.Block.Hash().Hex()).Scan(&proxyJobID); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO durable_stage_publications (
			job_id, job_generation, chain_id, block_number, block_hash,
			stage, stage_version, state, details
		) VALUES ($1, 1, 1, 4, $2, 'proxy', 2, 'complete', '{}')`,
		proxyJobID, forwardBlock.Block.Hash().Bytes())
	processed, err = forwardWorker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("dispatch pending-runtime proxy generation: processed=%t error=%v", processed, err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("publish proxy-woken derived match: processed=%t error=%v", processed, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE compilation_id = $1::uuid AND created_address = $2
		  AND status = 'matched' AND verification_job_id IS NOT NULL`, 1,
		compilationID, pendingGrandchildAddress)
	identity := verify.DerivedTraceIdentity{
		CompilationID: compilationID, BlockNumber: 1,
		BlockHash:   factoryBlock.Block.Hash().Bytes(),
		Transaction: factoryBlock.Block.Transactions()[0].Hash().Bytes(), TracePath: "1",
	}
	preparedCandidates := make([]verify.CandidateArtifact, len(unit.Candidates))
	for index, candidate := range unit.Candidates {
		preparedCandidates[index], err = verify.RestoreCandidateArtifact(candidate)
		if err != nil {
			t.Fatalf("restore prepared candidate %d: %v", index, err)
		}
	}
	preparedChild, preparedStatus, err := verify.PrepareDerivedMatch(
		preparedCandidates, unit.StandardJSON,
		verify.MatchInput{
			Creation: "0x" + hex.EncodeToString(childCreation),
			Runtime:  "0x" + hex.EncodeToString(childRuntime),
		},
	)
	if err != nil || preparedStatus != "matched" {
		t.Fatalf("prepare repeated derived match: status=%s error=%v", preparedStatus, err)
	}
	firstJobID, err := repository.CompleteDerived(ctx, identity, preparedChild)
	if err != nil {
		t.Fatalf("repeat derived publication: %v", err)
	}
	secondJobID, err := repository.CompleteDerived(ctx, identity, preparedChild)
	if err != nil || secondJobID != firstJobID {
		t.Fatalf("idempotent derived publication: first=%s second=%s error=%v", firstJobID, secondJobID, err)
	}
	execFixture(t, ctx, db, `
		UPDATE normalized_traces SET input = decode('6099', 'hex')
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2
		  AND trace_path = '1'`,
		factoryBlock.Block.Hash().Bytes(), factoryBlock.Block.Transactions()[0].Hash().Bytes())
	if _, err := repository.CompleteDerived(ctx, identity, preparedChild); !errors.Is(err, verify.ErrDerivedEvidenceStale) {
		t.Fatalf("changed evidence publication error = %v", err)
	}
	execFixture(t, ctx, db, `
		UPDATE normalized_traces SET input = $3
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2
		  AND trace_path = '1'`,
		factoryBlock.Block.Hash().Bytes(), factoryBlock.Block.Transactions()[0].Hash().Bytes(), childCreation)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs
		WHERE kind = 'derived' AND address = $1`, 1, childAddress)

	resolver, err := contractartifact.NewResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	resolved, found, err := resolver.ResolveAtBlock(
		ctx, "1", factoryAddress, 2, childBlock.Block.Hash().Bytes(),
	)
	if err != nil || !found || resolved.Resolution != contractartifact.ResolutionExactAddress ||
		!json.Valid(resolved.Source.Sources) {
		t.Fatalf("resolve historical factory: found=%t result=%+v error=%v", found, resolved, err)
	}
	_, found, err = resolver.ResolveAtBlock(
		ctx, "1", factoryAddress, 3, replacementBlock.Block.Hash().Bytes(),
	)
	if err != nil || found {
		t.Fatalf("replacement factory reused old artifact: found=%t error=%v", found, err)
	}

	execFixture(t, ctx, db, `
		UPDATE normalized_traces SET canonical = FALSE
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2 AND trace_path = '1'`,
		factoryBlock.Block.Hash().Bytes(), factoryBlock.Block.Transactions()[0].Hash().Bytes(),
	)
	execFixture(t, ctx, db, `
		UPDATE contract_code_observations SET canonical = FALSE
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
		childAddress, factoryBlock.Block.Hash().Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE created_address = $1 AND status = 'stale'
		  AND stale_from_status = 'matched'`, 1, childAddress)
	if _, found, err := repository.VerifiedContract(ctx, 1, "0x"+hex.EncodeToString(childAddress)); err != nil || found {
		t.Fatalf("orphan derived publication remained current: found=%t error=%v", found, err)
	}
	execFixture(t, ctx, db, `
		UPDATE normalized_traces SET canonical = TRUE
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2 AND trace_path = '1'`,
		factoryBlock.Block.Hash().Bytes(), factoryBlock.Block.Transactions()[0].Hash().Bytes(),
	)
	execFixture(t, ctx, db, `
		UPDATE contract_code_observations SET canonical = TRUE
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
		childAddress, factoryBlock.Block.Hash().Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE created_address = $1 AND status = 'matched'
		  AND stale_from_status IS NULL`, 1, childAddress)
	if _, found, err := repository.VerifiedContract(ctx, 1, "0x"+hex.EncodeToString(childAddress)); err != nil || !found {
		t.Fatalf("reattached derived publication did not recover: found=%t error=%v", found, err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO derived_verification_scans (
			compilation_id, chain_id, creator_address, creator_code_hash,
			valid_from_block, cursor_block_number, status
		) VALUES ($1::uuid, 1, $2, $3, 2, 2, 'succeeded')`,
		compilationID, factoryAddress, factoryCodeHash,
	)
	var requestID int64
	var scanCount int
	var requestedAt time.Time
	if err := db.QueryRowContext(ctx, dbgen.DerivedVerifyRequestBackfill,
		"1", factoryAddress, "reviewed integration backfill",
	).Scan(&requestID, &scanCount, &requestedAt); err != nil {
		t.Fatalf("request derived backfill: %v", err)
	}
	if requestID <= 0 || scanCount != 1 || requestedAt.IsZero() {
		t.Fatalf("derived backfill request id=%d scans=%d at=%s", requestID, scanCount, requestedAt)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_backfill_requests
		WHERE id = $1 AND chain_id = 1 AND creator_address = $2
		  AND reason = 'reviewed integration backfill' AND scan_count = 1`, 1,
		requestID, factoryAddress,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE compilation_id = $1::uuid AND creator_address = $2
		  AND valid_from_block = 1 AND cursor_block_number = 1
		  AND status = 'queued' AND last_error IS NULL`, 1,
		compilationID, factoryAddress,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE compilation_id = $1::uuid AND creator_address = $2
		  AND valid_from_block = 2 AND status = 'failed'
		  AND last_error = 'superseded_epoch_start'`, 1,
		compilationID, factoryAddress,
	)

	// A successful page is progress, not a failed attempt. Five exact pages
	// plus the terminal empty page must complete instead of exhausting the
	// scan's default five-attempt failure budget.
	execFixture(t, ctx, db, `
		UPDATE derived_verification_scans
		SET status = 'succeeded', attempt_count = 0, last_error = NULL
		WHERE creator_address <> $1 AND status = 'queued'`, childAddress)
	execFixture(t, ctx, db, `
		UPDATE derived_verification_scans
		SET status = 'queued', cursor_block_number = 1,
			cursor_transaction_hash = decode(repeat('00', 32), 'hex'),
			cursor_trace_path = '', attempt_count = 0, last_error = NULL
		WHERE compilation_id = $1::uuid AND creator_address = $2`,
		compilationID, childAddress,
	)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		)
		SELECT 1, 4, $1, $2, 0,
		       'page.' || lpad(series::text, 4, '0'), '0', 2, 'CREATE',
		       $3, $4, 0, 50000, 21000, decode('6099', 'hex'), $5,
		       NULL, FALSE, TRUE
		FROM generate_series(1, 500) AS series`,
		forwardBlock.Block.Hash().Bytes(), forwardBlock.Block.Transactions()[0].Hash().Bytes(),
		childAddress, grandchildAddress, grandchildRuntime,
	)
	pagedObserver := &derivedObservationRecorder{}
	pagedWorker, err := derivedverify.NewWorker(db, repository, derivedverify.Options{
		WorkerID: "derived-pagination", LeaseDuration: 300 * time.Millisecond,
		PollInterval: time.Millisecond, MaxTraces: 100, PublishMatches: true,
		Observer: pagedObserver,
	})
	if err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 6; page++ {
		processed, processErr := pagedWorker.ProcessOne(ctx)
		if processErr != nil || !processed {
			t.Fatalf("process derived page %d: processed=%t error=%v", page, processed, processErr)
		}
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE compilation_id = $1::uuid AND creator_address = $2
		  AND status = 'no_match'`, 500, compilationID, childAddress)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_scans
		WHERE compilation_id = $1::uuid AND creator_address = $2
		  AND status = 'succeeded' AND attempt_count = 0`, 1,
		compilationID, childAddress,
	)
	if pagedObserver.count("lease", "renewed") == 0 {
		t.Fatal("short-lease pagination completed without exercising the heartbeat")
	}

	// A detach racing the non-match write cannot leave a live attempt after the
	// canonicality trigger has already run.
	execFixture(t, ctx, db, `
		UPDATE normalized_traces SET canonical = FALSE
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2
		  AND trace_path = '0'`,
		replacementBlock.Block.Hash().Bytes(), replacementBlock.Block.Transactions()[0].Hash().Bytes())
	var racedStatus string
	if err := db.QueryRowContext(ctx, dbgen.DerivedVerifyRecordAttempt,
		"00000000-0000-0000-0000-000000000099", "1", "3",
		replacementBlock.Block.Hash().Bytes(), replacementBlock.Block.Transactions()[0].Hash().Bytes(),
		"0", factoryAddress, wrongEpochChildAddress, "CREATE", compilationID, "no_match",
	).Scan(&racedStatus); err != nil || racedStatus != "stale" {
		t.Fatalf("record detached attempt status=%q error=%v", racedStatus, err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE id = '00000000-0000-0000-0000-000000000099'::uuid
		  AND status = 'stale' AND stale_from_status = 'no_match'`, 1)
}

type derivedObservationRecorder struct {
	mu           sync.Mutex
	observations []derivedverify.Observation
}

func (recorder *derivedObservationRecorder) RecordDerivedVerification(observation derivedverify.Observation) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.observations = append(recorder.observations, observation)
}

func (recorder *derivedObservationRecorder) count(kind, result string) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	count := 0
	for _, observation := range recorder.observations {
		if observation.Kind == kind && observation.Result == result {
			count++
		}
	}
	return count
}

func derivedCandidate(name, creation, runtime string) verify.CandidateArtifact {
	return verify.CandidateArtifact{
		FileName: "A.sol", ContractName: name,
		Language: verify.LanguageSolidity, CompilerVersion: "0.8.30+commit.73712a01",
		CreationBytecode: creation, RuntimeBytecode: runtime,
		ABI: json.RawMessage(`[]`), CompilationArtifacts: json.RawMessage(`{}`),
		CreationCodeArtifacts: json.RawMessage(`{}`), RuntimeCodeArtifacts: json.RawMessage(`{}`),
	}
}
