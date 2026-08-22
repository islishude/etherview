//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/contractartifact"
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
			(1, $5, 2, $6, $7, $8, TRUE),
			(1, $9, 3, $10, $11, $12, TRUE),
			(1, $13, 3, $10, $7, $8, TRUE)`,
		factoryAddress, factoryBlock.Block.Hash().Bytes(), factoryCodeHash, factoryRuntime,
		childAddress, childBlock.Block.Hash().Bytes(), childCodeHash, childRuntime,
		factoryAddress, replacementBlock.Block.Hash().Bytes(), replacementFactoryCodeHash, replacementFactoryRuntime,
		wrongEpochChildAddress,
	)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, created_address, value, gas, gas_used, input, output,
			error, reverted, canonical
		) VALUES (
			1, 2, $1, $2, 0, '0', '', 1, 'CREATE2', $3, $4,
			0, 50000, 21000, $5, $6, NULL, FALSE, TRUE
		)`,
		childBlock.Block.Hash().Bytes(), childBlock.Block.Transactions()[0].Hash().Bytes(),
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
		factoryAddress, factoryCodeHash, factoryBlock.Block.Hash().Bytes(), factoryRuntime,
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
		WHERE chain_id = 1 AND creator_address = $1 AND status = 'queued'`, 1,
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
		  AND verified.code_hash = $2 AND verified.valid_from_block = 2
		  AND verified.file_name = 'A.sol' AND verified.contract_name = 'Child'
		  AND job.kind = 'derived' AND job.status = 'succeeded'`, 1,
		childAddress, childCodeHash,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE chain_id = 1 AND created_address = $1 AND call_type = 'CREATE2'
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
		) VALUES (1, $1, 4, $2, $3, $4, TRUE)`,
		grandchildAddress, forwardBlock.Block.Hash().Bytes(), grandchildCodeHash, grandchildRuntime,
	)
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
	execFixture(t, ctx, db, `
		INSERT INTO block_stage_results (
			chain_id, block_number, block_hash, stage, stage_version, state, details
		) VALUES (1, 4, $1, 'trace', 3, 'complete', '{"frames":1}')`,
		forwardBlock.Block.Hash().Bytes(),
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
	identity := verify.DerivedTraceIdentity{
		CompilationID: compilationID, BlockNumber: 2,
		BlockHash:   childBlock.Block.Hash().Bytes(),
		Transaction: childBlock.Block.Transactions()[0].Hash().Bytes(), TracePath: "0",
	}
	firstJobID, err := repository.CompleteDerived(ctx, identity)
	if err != nil {
		t.Fatalf("repeat derived publication: %v", err)
	}
	secondJobID, err := repository.CompleteDerived(ctx, identity)
	if err != nil || secondJobID != firstJobID {
		t.Fatalf("idempotent derived publication: first=%s second=%s error=%v", firstJobID, secondJobID, err)
	}
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
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2 AND trace_path = '0'`,
		childBlock.Block.Hash().Bytes(), childBlock.Block.Transactions()[0].Hash().Bytes(),
	)
	execFixture(t, ctx, db, `
		UPDATE contract_code_observations SET canonical = FALSE
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
		childAddress, childBlock.Block.Hash().Bytes(),
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
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2 AND trace_path = '0'`,
		childBlock.Block.Hash().Bytes(), childBlock.Block.Transactions()[0].Hash().Bytes(),
	)
	execFixture(t, ctx, db, `
		UPDATE contract_code_observations SET canonical = TRUE
		WHERE chain_id = 1 AND address = $1 AND block_hash = $2`,
		childAddress, childBlock.Block.Hash().Bytes(),
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM derived_verification_attempts
		WHERE created_address = $1 AND status = 'matched'
		  AND stale_from_status IS NULL`, 1, childAddress)
	if _, found, err := repository.VerifiedContract(ctx, 1, "0x"+hex.EncodeToString(childAddress)); err != nil || !found {
		t.Fatalf("reattached derived publication did not recover: found=%t error=%v", found, err)
	}
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
