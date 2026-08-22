//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

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
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, factoryBlock)
	commitCanonical(t, ctx, core, childBlock)

	factoryAddress := mustBytes(t, testAddress(750))
	childAddress := mustBytes(t, testAddress(751))
	factoryRuntime := []byte{0x60, 0xaa}
	childCreation := []byte{0x60, 0x10}
	childRuntime := []byte{0x60, 0x11}
	factoryCodeHash := keccak256(factoryRuntime)
	childCodeHash := keccak256(childRuntime)
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES
			(1, $1, 1, $2, $3, $4, TRUE),
			(1, $5, 2, $6, $7, $8, TRUE)`,
		factoryAddress, factoryBlock.Block.Hash().Bytes(), factoryCodeHash, factoryRuntime,
		childAddress, childBlock.Block.Hash().Bytes(), childCodeHash, childRuntime,
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
