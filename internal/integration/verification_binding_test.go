//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVerifierV2PublishesOnlyCanonicalRuntimeAndKeepsResultImmutable(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	genesis := testBundle(0, testHash(7_200), testHash(0), testHash(8_200), "verifier-v2-genesis")
	block := testBundle(1, testHash(7_201), testHash(7_200), testHash(8_201), "verifier-v2-block")
	commitCanonical(t, ctx, coreRepository, genesis)
	commitCanonical(t, ctx, coreRepository, block)

	runtime := []byte{0x60, 0x01}
	codeHash := keccak256(runtime)
	address := mustBytes(t, testAddress(720))
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 1, $2, $3, $4, TRUE)`,
		address, mustBytes(t, testHash(7_201)), codeHash, runtime,
	)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	submission := verifierV2AddressSubmission(
		address, codeHash, mustBytes(t, testHash(7_201)), runtime,
	)
	job, created, err := repository.SubmitV2(ctx, submission)
	if err != nil || !created {
		t.Fatalf("submit verifier-v2 job: created=%t error=%v", created, err)
	}
	duplicate, created, err := repository.SubmitV2(ctx, submission)
	if err != nil || created || duplicate.ID != job.ID {
		t.Fatalf("deduplicate verifier-v2 job: created=%t id=%s error=%v", created, duplicate.ID, err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim verifier-v2 job: found=%t error=%v", found, err)
	}
	provenance := solcJSProvenance(generation, compilerDigest, executorDigest)
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatalf("bind verifier-v2 compiler: %v", err)
	}
	outcome := verifierV2SuccessOutcome(t, "full")
	if err := repository.CompleteV2(ctx, lease, "verification_success", outcome); err != nil {
		t.Fatalf("complete verifier-v2 job: %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 1)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE match_type = 'full' AND code_hash = $1
		  AND verification_job_id = $2::uuid`, 1, codeHash, job.ID)
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_results SET outcome_kind = 'verification_failure'
		WHERE job_id = $1::uuid`, job.ID); err == nil {
		t.Fatal("immutable verifier-v2 result accepted an update")
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM verification_results WHERE job_id = $1::uuid`, job.ID); err == nil {
		t.Fatal("immutable verifier-v2 result accepted a delete")
	}
}

func TestVerifierV2RejectsAddressPublicationAfterCanonicalIdentityChanges(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	commitCanonical(t, ctx, coreRepository, testBundle(
		0, testHash(7_300), testHash(0), testHash(8_300), "verifier-v2-stale-genesis",
	))
	commitCanonical(t, ctx, coreRepository, testBundle(
		1, testHash(7_301), testHash(7_300), testHash(8_301), "verifier-v2-stale-block",
	))
	runtime := []byte{0x60, 0x02}
	codeHash := keccak256(runtime)
	address := mustBytes(t, testAddress(721))
	// The observation is retained but explicitly noncanonical, modeling the
	// identity fence seen after a reorg.
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 1, $2, $3, $4, FALSE)`,
		address, mustBytes(t, testHash(7_301)), codeHash, runtime,
	)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	submission := verifierV2AddressSubmission(
		address, codeHash, mustBytes(t, testHash(7_301)), runtime,
	)
	if _, _, err := repository.SubmitV2(ctx, submission); err != nil {
		t.Fatalf("submit stale verifier-v2 job: %v", err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2-stale", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim stale verifier-v2 job: found=%t error=%v", found, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatalf("bind stale verifier-v2 compiler: %v", err)
	}
	err = repository.CompleteV2(ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "partial"))
	if !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("stale publication error = %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
}

func TestVerifierV2PublishesAuthenticatedGenesisRuntimeOnlyTarget(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(7_320), testHash(0), testHash(8_320), "verifier-v2-predeploy-genesis",
	)
	commitCanonical(t, ctx, coreRepository, genesis)
	address := testAddress(732)
	runtime := []byte{0x60, 0x03}
	codeHash := keccak256(runtime)
	insertAuthenticatedGenesisPredeploy(
		t, ctx, db, genesis.Block.Hash(), genesis.Block.Root(), address.Bytes(), runtime,
	)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	submission := verifierV2AddressSubmission(
		address.Bytes(), codeHash, genesis.Block.Hash().Bytes(), runtime,
	)
	submission.Bytecodes[0].Creation = ""
	submission.Target.CreationBytecode = ""
	submission.Target.GenesisPredeploy = true
	job, created, err := service.SubmitV2(ctx, submission)
	if err != nil || !created {
		t.Fatalf("submit Genesis verifier-v2 job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2-genesis", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim Genesis verifier-v2 job: found=%t error=%v", found, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "full"),
	); err == nil {
		t.Fatal("Genesis publication accepted a creation match")
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2GenesisSuccessOutcome(t, "full"),
	); err != nil {
		t.Fatalf("complete Genesis verifier-v2 job: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_results
		WHERE job_id = $1::uuid AND constructor_arguments IS NULL
		  AND outcome->'creation_match' = 'null'::jsonb
		  AND outcome->'runtime_match' IS NOT NULL`, 1, job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE verification_job_id = $1::uuid AND valid_from_block = 0
		  AND constructor_arguments IS NULL`, 1, job.ID)
	verified, found, err := repository.VerifiedContract(ctx, 1, address.Hex())
	if err != nil || !found || verified.CreationMatch != nil || verified.RuntimeMatch == nil ||
		verified.ConstructorArguments != "" {
		t.Fatalf("published Genesis artifact: found=%t artifact=%+v error=%v", found, verified, err)
	}
}

func TestVerifierV2RejectsGenesisPublicationAfterProofDisappears(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(7_330), testHash(0), testHash(8_330), "verifier-v2-predeploy-stale",
	)
	commitCanonical(t, ctx, coreRepository, genesis)
	address := testAddress(733)
	runtime := []byte{0x60, 0x04}
	codeHash := keccak256(runtime)
	insertAuthenticatedGenesisPredeploy(
		t, ctx, db, genesis.Block.Hash(), genesis.Block.Root(), address.Bytes(), runtime,
	)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	submission := verifierV2AddressSubmission(
		address.Bytes(), codeHash, genesis.Block.Hash().Bytes(), runtime,
	)
	submission.Bytecodes[0].Creation = ""
	submission.Target.CreationBytecode = ""
	submission.Target.GenesisPredeploy = true
	job, created, err := service.SubmitV2(ctx, submission)
	if err != nil || !created {
		t.Fatalf("submit stale Genesis job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2-genesis-stale", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim stale Genesis job: found=%t error=%v", found, err)
	}
	if err := repository.BindCompiler(
		ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest),
	); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `
		DELETE FROM genesis_account_observations
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2`,
		genesis.Block.Hash().Bytes(), address.Bytes(),
	)
	err = repository.CompleteV2(
		ctx, lease, "verification_success", verifierV2GenesisSuccessOutcome(t, "full"),
	)
	if !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("stale Genesis publication error = %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_results WHERE job_id = $1::uuid`, 0, job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts WHERE verification_job_id = $1::uuid`, 0, job.ID)
}

func TestVerifierV2LeaseReclaimKeepsPinnedCompilerIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	submission := verify.SubmissionV2{
		Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01",
		StandardJSON:    json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		StandardJSONVariants: []json.RawMessage{
			json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}},
	}
	job, _, err := repository.SubmitV2(ctx, submission)
	if err != nil {
		t.Fatalf("submit reclaim job: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET compiler_platform = $2, catalog_generation_id = $3,
		    compiler_digest = $4
		WHERE id = $1::uuid`,
		job.ID, verify.CompilerPlatformEmscriptenWASM32, generation, compilerDigest[:],
	); err == nil {
		t.Fatal("migration trigger accepted provenance binding without an active lease")
	}
	first, found, err := repository.Claim(ctx, "worker-a", 10*time.Millisecond)
	if err != nil || !found {
		t.Fatalf("first claim: found=%t error=%v", found, err)
	}
	provenance := solcJSProvenance(generation, compilerDigest, executorDigest)
	if err := repository.BindCompiler(ctx, first, provenance); err != nil {
		t.Fatalf("bind compiler before lease expiry: %v", err)
	}
	execFixture(t, ctx, db, `
		UPDATE verification_jobs
		SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1::uuid`, first.Job.ID)
	second, found, err := repository.Claim(ctx, "worker-b", time.Minute)
	if err != nil || !found || second.Job.ID != first.Job.ID ||
		second.Job.AttemptCount != first.Job.AttemptCount+1 {
		t.Fatalf("reclaim: found=%t job=%s attempts=%d error=%v",
			found, second.Job.ID, second.Job.AttemptCount, err)
	}
	if second.Job.RequestV2.CatalogGenerationID != generation ||
		second.Job.RequestV2.CompilerPlatform != verify.CompilerPlatformEmscriptenWASM32 ||
		second.Job.RequestV2.CompilerDigest != hex.EncodeToString(compilerDigest[:]) ||
		second.Job.RequestV2.ExecutorKind != verify.SolcJSExecutorKind ||
		second.Job.RequestV2.ExecutionPolicy != verify.TrustedSubprocessPolicy ||
		second.Job.RequestV2.ExecutorDigest != hex.EncodeToString(executorDigest[:]) {
		t.Fatalf("reclaim changed pinned provenance: %#v", second.Job.RequestV2)
	}
	if second.Job.Compiler == nil ||
		second.Job.Compiler.CatalogGeneration != generation ||
		second.Job.Compiler.Platform != verify.CompilerPlatformEmscriptenWASM32 ||
		second.Job.Compiler.Digest != compilerDigest ||
		second.Job.Compiler.ExecutorDigest != executorDigest ||
		second.Job.Compiler.ExecutorKind != verify.SolcJSExecutorKind ||
		second.Job.Compiler.ExecutionPolicy != verify.TrustedSubprocessPolicy {
		t.Fatalf("reclaim lost bound compiler: %#v", second.Job.Compiler)
	}
	conflicting := provenance
	conflicting.Digest = sha256.Sum256([]byte("different compiler"))
	if err := repository.BindCompiler(ctx, second, conflicting); !errors.Is(err, verify.ErrCompilerProvenanceConflict) {
		t.Fatalf("rebind compiler error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET compiler_digest = decode(repeat('42', 32), 'hex')
		WHERE id = $1::uuid`, second.Job.ID); err == nil {
		t.Fatal("migration trigger accepted a compiler provenance rewrite")
	}
}

func TestVerifierV2CatalogOutageLeavesCompilerQueuedAndClaimsProxy(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindChainIdentity(ctx, db, "1", testHash(7_401)); err != nil {
		t.Fatalf("bind selective-claim chain: %v", err)
	}
	compile, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01",
		StandardJSON:    json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		StandardJSONVariants: []json.RawMessage{
			json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}},
	})
	if err != nil || !created {
		t.Fatalf("submit compiler job: created=%t error=%v", created, err)
	}
	proxy, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobProxy,
		Target: &verify.VerificationTarget{
			ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
			CodeHash:    "0x" + strings.Repeat("22", 32),
			AtBlockHash: "0x" + strings.Repeat("33", 32),
		},
		ProxyTarget: &verify.ProxyVerificationTarget{
			Kind: "eip1967", ImplementationAddress: "0x" + strings.Repeat("44", 20),
			ImplementationCodeHash: "0x" + strings.Repeat("55", 32),
			ExpectedImplementation: "0x" + strings.Repeat("44", 20),
		},
	})
	if err != nil || !created {
		t.Fatalf("submit proxy job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.ClaimRunnable(ctx, "api-catalog-offline", time.Minute, verify.CompilerAvailability{})
	if err != nil || !found || lease.Job.ID != proxy.ID || lease.Job.Kind != verify.JobProxy {
		t.Fatalf("catalog-offline claim: lease=%+v found=%t error=%v", lease, found, err)
	}
	queued, found, err := repository.Job(ctx, compile.ID)
	if err != nil || !found || queued.Status != verify.JobQueued || queued.AttemptCount != 0 {
		t.Fatalf("compiler job while catalog unavailable: job=%+v found=%t error=%v", queued, found, err)
	}
	compileLease, found, err := repository.ClaimRunnable(ctx, "api-catalog-recovered", time.Minute, verify.CompilerAvailability{SolcJS: true, Geas: true})
	if err != nil || !found || compileLease.Job.ID != compile.ID {
		t.Fatalf("catalog-recovered claim: lease=%+v found=%t error=%v", compileLease, found, err)
	}
}

func TestGeasVerificationBindsWithoutCatalogAndPublishesExactRuntime(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coreRepository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(
		0, testHash(7_450), testHash(0), testHash(8_450), "geas-genesis",
	)
	block := testBundle(
		1, testHash(7_451), genesis.Block.Hash(), testHash(8_451), "geas-block",
	)
	commitCanonical(t, ctx, coreRepository, genesis)
	commitCanonical(t, ctx, coreRepository, block)
	blockHash := block.Block.Hash()
	runtime := []byte{0x60, 0x01}
	codeHash := keccak256(runtime)
	address := mustBytes(t, testAddress(745))
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, 1, $2, $3, $4, TRUE)`,
		address, mustBytes(t, blockHash), codeHash, runtime,
	)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	// This older solc job proves family availability is applied before FIFO
	// ordering, rather than allowing a catalog outage to starve Geas.
	solc, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01",
		StandardJSON:    json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		StandardJSONVariants: []json.RawMessage{
			json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}},
	})
	if err != nil || !created {
		t.Fatalf("submit solc control job: created=%t error=%v", created, err)
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := service.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobAddress, Language: verify.LanguageGeas, CompilerVersion: "0.3.3",
		Geas: &verify.GeasRequest{
			Sources:           map[string]string{"system/main.eas": "push 1\n", "unused.eas": "push 2\n"},
			RuntimeEntrypoint: "system/main.eas",
		},
		Target: &verify.VerificationTarget{
			ChainID: 1, Address: "0x" + hex.EncodeToString(address),
			CodeHash: "0x" + hex.EncodeToString(codeHash), AtBlockHash: blockHash.Hex(),
			CreationBytecode: "0x6000", RuntimeBytecode: "0x6001",
		},
		Bytecodes: []verify.BytecodePair{{Creation: "0x6000", Runtime: "0x6001"}},
	})
	if err != nil || !created {
		t.Fatalf("submit Geas job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.ClaimRunnable(
		ctx, "geas-only", time.Minute, verify.CompilerAvailability{Geas: true},
	)
	if err != nil || !found || lease.Job.ID != job.ID {
		t.Fatalf("Geas-only claim: lease=%+v found=%t error=%v", lease, found, err)
	}
	compilerDigest := sha256.Sum256([]byte("github.com/fjl/geas@v0.3.3"))
	executorDigest := sha256.Sum256([]byte("etherview-geas-compiler"))
	wrongFamily := verify.CompilerProvenance{
		Kind: verify.CompilerSolcJS, Digest: compilerDigest, ExecutorDigest: executorDigest,
		ExecutorKind: verify.SolcJSExecutorKind, ExecutionPolicy: verify.TrustedSubprocessPolicy,
		Platform: verify.CompilerPlatformEmscriptenWASM32, CatalogGeneration: 1,
	}
	if err := repository.BindCompiler(ctx, lease, wrongFamily); !errors.Is(err, verify.ErrCompilerProvenanceConflict) {
		t.Fatalf("cross-family Geas binding error = %v", err)
	}
	provenance := verify.CompilerProvenance{
		Kind: verify.CompilerGeas, Digest: compilerDigest, ExecutorDigest: executorDigest,
		ExecutorKind: verify.GeasExecutorKind, ExecutionPolicy: verify.TrustedSubprocessPolicy,
		Platform: verify.CompilerPlatformGoModule,
	}
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatalf("bind Geas compiler: %v", err)
	}
	var catalogGeneration sql.NullInt64
	var platform, executorKind string
	if err := db.QueryRowContext(ctx, `
		SELECT catalog_generation_id, compiler_platform, executor_kind
		FROM verification_jobs WHERE id = $1::uuid`, job.ID,
	).Scan(&catalogGeneration, &platform, &executorKind); err != nil {
		t.Fatal(err)
	}
	if catalogGeneration.Valid || platform != verify.CompilerPlatformGoModule || executorKind != verify.GeasExecutorKind {
		t.Fatalf("stored Geas provenance generation=%+v platform=%q executor=%q", catalogGeneration, platform, executorKind)
	}
	outcome := json.RawMessage(`{
		"kind":"verification_success",
		"file_name":"system/main.eas",
		"contract_name":"main",
		"language":"geas",
		"compiler_version":"0.3.3",
		"settings":{"runtime_entrypoint":"system/main.eas","stack_check":true},
		"sources":{"system/main.eas":{"content":"push 1\n"}},
		"abi":[],
		"compilation_artifacts":{},
		"creation_code_artifacts":{},
		"runtime_code_artifacts":{},
		"creation_match":null,
		"runtime_match":{"match_type":"full","transformations":[],"values":{}},
		"libraries":{},
		"is_blueprint":false
	}`)
	tamperedOutcome := json.RawMessage(strings.Replace(
		string(outcome), `"push 1\n"`, `"push 2\n"`, 1,
	))
	if err := repository.CompleteV2(ctx, lease, "verification_success", tamperedOutcome); err == nil {
		t.Fatal("Geas publication accepted source content that disagreed with its durable request")
	}
	missingCreationMatch := json.RawMessage(strings.Replace(
		string(outcome), `"creation_match":null,`, "", 1,
	))
	if err := repository.CompleteV2(ctx, lease, "verification_success", missingCreationMatch); err == nil {
		t.Fatal("Geas publication accepted a missing creation_match field")
	}
	if err := repository.CompleteV2(ctx, lease, "verification_success", outcome); err != nil {
		t.Fatalf("complete Geas verification: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE verification_job_id = $1::uuid AND language = 'geas'
		  AND compiler_version = '0.3.3' AND match_type = 'full'
		  AND abi = '[]'::jsonb AND libraries = '{}'::jsonb`, 1, job.ID)
	queued, found, err := repository.Job(ctx, solc.ID)
	if err != nil || !found || queued.Status != verify.JobQueued || queued.AttemptCount != 0 {
		t.Fatalf("solc job during Geas-only availability: job=%+v found=%t error=%v", queued, found, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET executor_digest = decode(repeat('42', 32), 'hex')
		WHERE id = $1::uuid`, job.ID); err == nil {
		t.Fatal("Geas executor provenance rewrite was accepted")
	}
}

func TestVerifierV2CatalogGenerationIsArchitectureNeutral(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	generation, compilerDigest, _ := insertVerifierV2Compiler(t, ctx, db)
	catalog, err := verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{
		Sources: map[verify.Language]string{
			verify.LanguageSolidity: "https://compiler.example/emscripten-wasm32/list.json",
		},
		Platform:       verify.CompilerPlatformEmscriptenWASM32,
		AllowedOrigins: []string{"https://compiler.example"},
		Freshness:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup(
		ctx, verify.LanguageSolidity, "0.8.30+commit.73712a01",
	)
	if err != nil {
		t.Fatal(err)
	}
	if entry.GenerationID != generation ||
		entry.Platform != verify.CompilerPlatformEmscriptenWASM32 ||
		entry.ArtifactSHA256 != compilerDigest {
		t.Fatalf("architecture-neutral catalog entry = %#v", entry)
	}
}

func TestVerifierV2CatalogVersionsUseSemanticOrder(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	catalogDigest := sha256.Sum256([]byte("semantic-version-order-catalog"))
	var generation int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO compiler_catalog_generations (
			language, source_url, catalog_digest, entry_count
		) VALUES ('solidity', 'https://compiler.example/list.json', $1, 4)
		RETURNING id`, catalogDigest[:]).Scan(&generation); err != nil {
		t.Fatalf("insert compiler generation: %v", err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO compiler_catalog_entries (
			generation_id, language, version, platform, artifact_url,
			artifact_sha256, max_bytes
		) VALUES
			($1, 'solidity', '0.8.20+commit.a1b79de6', 'emscripten-wasm32',
			 'https://compiler.example/soljson-0.8.20.js', decode(repeat('20', 32), 'hex'), 209715200),
			($1, 'solidity', '0.8.3+commit.8d00100c', 'emscripten-wasm32',
			 'https://compiler.example/soljson-0.8.3.js', decode(repeat('03', 32), 'hex'), 209715200),
			($1, 'solidity', '0.8.31-pre.1+commit.cccccccc', 'emscripten-wasm32',
			 'https://compiler.example/soljson-0.8.31-pre.1.js', decode(repeat('31', 32), 'hex'), 209715200),
			($1, 'solidity', '0.8.30+commit.73712a01', 'emscripten-wasm32',
			 'https://compiler.example/soljson-0.8.30.js', decode(repeat('30', 32), 'hex'), 209715200)`, generation)
	execFixture(t, ctx, db, `
		INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ('solidity', $1)`, generation)
	catalog, err := verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{
		Sources: map[verify.Language]string{
			verify.LanguageSolidity: "https://compiler.example/emscripten-wasm32/list.json",
		},
		Platform:       verify.CompilerPlatformEmscriptenWASM32,
		AllowedOrigins: []string{"https://compiler.example"},
		Freshness:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"0.8.3+commit.8d00100c",
		"0.8.20+commit.a1b79de6",
		"0.8.30+commit.73712a01",
		"0.8.31-pre.1+commit.cccccccc",
	}
	for _, language := range []verify.Language{verify.LanguageSolidity, verify.LanguageYul} {
		versions, err := catalog.Versions(ctx, language)
		if err != nil {
			t.Fatalf("list %s compiler versions: %v", language, err)
		}
		if !slices.Equal(versions, want) {
			t.Fatalf("%s compiler versions = %v, want %v", language, versions, want)
		}
	}
}

func TestVerifierV2ConcurrentAPIConsumersLeaseJobOnce(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01",
		StandardJSON:    json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		StandardJSONVariants: []json.RawMessage{
			json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}},
	})
	if err != nil || !created {
		t.Fatalf("submit concurrent claim job: created=%t error=%v", created, err)
	}
	type claimResult struct {
		lease verify.VerificationLease
		found bool
		err   error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var group sync.WaitGroup
	for _, worker := range []string{"api-a", "api-b"} {
		group.Add(1)
		go func(worker string) {
			defer group.Done()
			<-start
			lease, found, claimErr := repository.ClaimRunnable(ctx, worker, time.Minute, verify.CompilerAvailability{SolcJS: true, Geas: true})
			results <- claimResult{lease: lease, found: found, err: claimErr}
		}(worker)
	}
	close(start)
	group.Wait()
	close(results)
	var claims int
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent claim error: %v", result.err)
		}
		if result.found {
			claims++
			if result.lease.Job.ID != job.ID {
				t.Fatalf("claimed unexpected job %s", result.lease.Job.ID)
			}
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent API claims = %d, want 1", claims)
	}
}

func TestSolcJSExecutorMigrationDeletesVyperAndPreservesSolidity(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	applyMigrationsThrough(t, ctx, db, "0030_deferred_compiler_provenance")
	solidityGeneration, solidityCompiler, solidityRunner := insertLegacyCompilerCatalog(
		t, ctx, db, verify.LanguageSolidity, "0.8.30+commit.73712a01",
	)
	vyperGeneration, vyperCompiler, vyperRunner := insertLegacyCompilerCatalog(
		t, ctx, db, verify.Language("vyper"), "0.4.0",
	)
	blockHash := bytes.Repeat([]byte{0x51}, 32)
	solidityAddress, solidityCodeHash := bytes.Repeat([]byte{0x11}, 20), bytes.Repeat([]byte{0x41}, 32)
	vyperAddress, vyperCodeHash := bytes.Repeat([]byte{0x22}, 20), bytes.Repeat([]byte{0x42}, 32)
	proxyAddress, proxyCodeHash := bytes.Repeat([]byte{0x33}, 20), bytes.Repeat([]byte{0x43}, 32)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1)`); err != nil {
		t.Fatalf("insert migration chain fixture: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blocks (
			chain_id, number, hash, parent_hash, timestamp, raw
		) VALUES (1, 0, $1, $2, 0, '{}'::jsonb)`,
		blockHash, bytes.Repeat([]byte{0}, 32),
	); err != nil {
		t.Fatalf("insert migration block fixture: %v", err)
	}
	const solidityJobID = "00000000-0000-4000-8000-000000003101"
	const vyperJobID = "00000000-0000-4000-8000-000000003102"
	insertLegacyAddressPublication(
		t, ctx, tx, solidityJobID, verify.LanguageSolidity,
		"0.8.30+commit.73712a01", solidityGeneration, solidityCompiler,
		solidityRunner, solidityAddress, solidityCodeHash, blockHash,
	)
	insertLegacyAddressPublication(
		t, ctx, tx, vyperJobID, verify.Language("vyper"), "0.4.0",
		vyperGeneration, vyperCompiler, vyperRunner,
		vyperAddress, vyperCodeHash, blockHash,
	)
	const queuedJobID = "00000000-0000-4000-8000-000000003104"
	const runningJobID = "00000000-0000-4000-8000-000000003105"
	for _, fixture := range []struct {
		id     string
		status string
	}{
		{id: queuedJobID, status: "queued"},
		{id: runningJobID, status: "running"},
	} {
		requestDigest := sha256.Sum256([]byte("migration-active:" + fixture.id))
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO verification_jobs (
				id, kind, language, catalog_language, compiler_version,
				compiler_platform, catalog_generation_id, compiler_digest,
				runner_digest, request, request_payload, request_digest,
				requires_hard_isolation, status, leased_by, lease_token,
				lease_expires_at, attempt_count
			) VALUES (
				$1::uuid, 'solidity_standard_json', 'solidity', 'solidity',
				'0.8.30+commit.73712a01', 'linux-amd64', $2, $3, $4,
				'{}'::jsonb, convert_to('{}', 'UTF8'), $5, TRUE, $6,
				CASE WHEN $6 = 'running' THEN 'legacy-worker' END,
				CASE WHEN $6 = 'running' THEN 'legacy-lease' END,
				CASE WHEN $6 = 'running' THEN clock_timestamp() + interval '1 hour' END,
				CASE WHEN $6 = 'running' THEN 1 ELSE 0 END
			)`,
			fixture.id, solidityGeneration, solidityCompiler[:], solidityRunner[:],
			requestDigest[:], fixture.status,
		); err != nil {
			t.Fatalf("insert active legacy job %s: %v", fixture.status, err)
		}
	}
	const proxyJobID = "00000000-0000-4000-8000-000000003103"
	proxyRequest := map[string]any{
		"kind": "proxy",
		"target": map[string]any{
			"Address":     "0x" + hex.EncodeToString(proxyAddress),
			"CodeHash":    "0x" + hex.EncodeToString(proxyCodeHash),
			"AtBlockHash": "0x" + hex.EncodeToString(blockHash),
		},
		"proxy_target": map[string]any{
			"kind":                     "eip1967",
			"implementation_address":   "0x" + hex.EncodeToString(vyperAddress),
			"implementation_code_hash": "0x" + hex.EncodeToString(vyperCodeHash),
		},
	}
	proxyPayload, err := json.Marshal(proxyRequest)
	if err != nil {
		t.Fatal(err)
	}
	proxyRequestDigest := sha256.Sum256([]byte("migration-vyper-proxy"))
	proxyOutcome, err := json.Marshal(map[string]any{
		"kind":                     "proxy_verification_success",
		"proxy_address":            "0x" + hex.EncodeToString(proxyAddress),
		"proxy_code_hash":          "0x" + hex.EncodeToString(proxyCodeHash),
		"observation_block_hash":   "0x" + hex.EncodeToString(blockHash),
		"proxy_kind":               "eip1967",
		"implementation_address":   "0x" + hex.EncodeToString(vyperAddress),
		"implementation_code_hash": "0x" + hex.EncodeToString(vyperCodeHash),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, proxy_code_hash,
			proxy_kind, implementation_address, implementation_code_hash,
			confidence, canonical
		) VALUES (1, $1, 0, $2, $3, 'eip1967', $4, $5, 'verified', TRUE)`,
		proxyAddress, blockHash, proxyCodeHash, vyperAddress, vyperCodeHash,
	); err != nil {
		t.Fatalf("insert Vyper-dependent proxy observation: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, chain_id, address, code_hash, block_hash, request,
			request_payload, request_digest, status, attempt_count,
			outcome_kind, outcome
		) VALUES (
			$1::uuid, 'proxy', 1, $2, $3, $4, $5::jsonb, $6, $7,
			'succeeded', 1, 'proxy_verification_success', $8::jsonb
		)`,
		proxyJobID, proxyAddress, proxyCodeHash, blockHash,
		string(proxyPayload), proxyPayload, proxyRequestDigest[:], string(proxyOutcome),
	); err != nil {
		t.Fatalf("insert Vyper-dependent proxy job: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome
		) VALUES ($1::uuid, $2, 'proxy_verification_success', $3::jsonb)`,
		proxyJobID, proxyRequestDigest[:], string(proxyOutcome),
	); err != nil {
		t.Fatalf("insert Vyper-dependent proxy result: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_proxy_contracts (
			chain_id, proxy_address, proxy_code_hash, observation_block_number,
			observation_block_hash, proxy_kind, implementation_address,
			implementation_code_hash, verification_job_id, request_digest
		) VALUES (1, $1, $2, 0, $3, 'eip1967', $4, $5, $6::uuid, $7)`,
		proxyAddress, proxyCodeHash, blockHash, vyperAddress, vyperCodeHash,
		proxyJobID, proxyRequestDigest[:],
	); err != nil {
		t.Fatalf("insert Vyper-dependent proxy publication: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit pre-0031 fixtures: %v", err)
	}

	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("apply solc-js executor migration: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs
		WHERE id = $1::uuid AND status = 'succeeded'
		  AND compiler_platform = 'linux-amd64'
		  AND executor_kind = 'legacy_runner'
		  AND execution_policy = 'legacy_hard_isolation'
		  AND executor_digest = $2`, 1, solidityJobID, solidityRunner[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE verification_job_id = $1::uuid AND language = 'solidity'`,
		1, solidityJobID,
	)
	for _, jobID := range []string{vyperJobID, proxyJobID} {
		assertRowCount(t, ctx, db, `
			SELECT count(*) FROM verification_jobs WHERE id = $1::uuid`, 0, jobID)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts WHERE language = 'vyper'`, 0)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM pg_class
		WHERE oid = to_regclass(current_schema() || '.verified_proxy_contracts')`, 0)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM compiler_catalog_generations WHERE language = 'vyper'`, 0)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs
		WHERE id IN ($1::uuid, $2::uuid)
		  AND status = 'failed' AND error_code = 'executor_migrated'
		  AND leased_by IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL
		  AND executor_kind = 'legacy_runner'
		  AND execution_policy = 'legacy_hard_isolation'
		  AND executor_digest = $3`, 2, queuedJobID, runningJobID, solidityRunner[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'verification_jobs'
		  AND column_name IN ('runner_digest', 'requires_hard_isolation')`, 0)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO compiler_catalog_generations (
			language, source_url, catalog_digest, entry_count
		) VALUES ('vyper', 'https://compiler.example/vyper/list.json',
			decode(repeat('91', 32), 'hex'), 1)`); err == nil {
		t.Fatal("post-migration Vyper catalog write was accepted")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version,
			request, request_payload, request_digest
		) VALUES (
			'00000000-0000-4000-8000-000000003106',
			'vyper_standard_json', 'vyper', 'vyper', '0.4.0',
			'{}'::jsonb, convert_to('{}', 'UTF8'),
			decode(repeat('92', 32), 'hex')
		)`); err == nil {
		t.Fatal("post-migration Vyper job write was accepted")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verified_contracts SET language = 'vyper'
		WHERE verification_job_id = $1::uuid`, solidityJobID); err == nil {
		t.Fatal("post-migration Vyper publication write was accepted")
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM verification_results WHERE job_id = $1::uuid`,
		solidityJobID,
	); err == nil {
		t.Fatal("migration did not restore result immutability")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version,
			compiler_platform, catalog_generation_id, compiler_digest,
			executor_kind, execution_policy, executor_digest,
			request, request_payload, request_digest
		) VALUES (
			'00000000-0000-4000-8000-000000003107',
			'solidity_standard_json', 'solidity', 'solidity',
			'0.8.30+commit.73712a01', 'linux-amd64', $1, $2,
			'legacy_runner', 'legacy_hard_isolation', $3,
			'{}'::jsonb, convert_to('{}', 'UTF8'),
			decode(repeat('93', 32), 'hex')
		)`, solidityGeneration, solidityCompiler[:], solidityRunner[:]); err == nil {
		t.Fatal("new pre-bound legacy executor job was accepted")
	}

	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
		Kind: verify.JobSolidityStandardJSON, Language: verify.LanguageSolidity,
		CompilerVersion: "0.8.30+commit.73712a01",
		StandardJSON:    json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		StandardJSONVariants: []json.RawMessage{
			json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		},
		Bytecodes: []verify.BytecodePair{{Runtime: "0x6001"}},
	})
	if err != nil || !created {
		t.Fatalf("submit post-migration job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "solcjs-migration-worker", time.Minute)
	if err != nil || !found || lease.Job.ID != job.ID {
		t.Fatalf("claim post-migration job: found=%t lease=%+v error=%v", found, lease, err)
	}
	provenance := solcJSProvenance(generation, compilerDigest, executorDigest)
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatalf("bind post-migration executor: %v", err)
	}
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatalf("retry same executor digest: %v", err)
	}
	conflicting := provenance
	conflicting.ExecutorDigest = sha256.Sum256([]byte("different-executor"))
	if err := repository.BindCompiler(ctx, lease, conflicting); !errors.Is(
		err, verify.ErrCompilerProvenanceConflict,
	) {
		t.Fatalf("conflicting executor retry error = %v", err)
	}
}

func applyMigrationsThrough(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	lastVersion string,
) {
	t.Helper()
	migrations, err := store.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	found := false
	for _, migration := range migrations {
		if migration.Version > lastVersion {
			break
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %s: %v", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO etherview_schema_migrations (version, checksum)
			VALUES ($1, $2)`, migration.Version, migration.Checksum,
		); err != nil {
			t.Fatalf("record migration %s: %v", migration.Version, err)
		}
		found = migration.Version == lastVersion
	}
	if !found {
		t.Fatalf("migration %s not found", lastVersion)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migrations through %s: %v", lastVersion, err)
	}
}

func insertLegacyCompilerCatalog(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	language verify.Language,
	version string,
) (int64, [sha256.Size]byte, [sha256.Size]byte) {
	t.Helper()
	catalogDigest := sha256.Sum256([]byte("legacy-catalog:" + string(language)))
	compilerDigest := sha256.Sum256([]byte("legacy-compiler:" + string(language)))
	runnerDigest := sha256.Sum256([]byte("legacy-runner:" + string(language)))
	var generation int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO compiler_catalog_generations (
			language, source_url, catalog_digest, entry_count
		) VALUES ($1, $2, $3, 1) RETURNING id`,
		language, "https://compiler.example/"+string(language)+"/list.json",
		catalogDigest[:],
	).Scan(&generation); err != nil {
		t.Fatalf("insert legacy %s generation: %v", language, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO compiler_catalog_entries (
			generation_id, language, version, platform, artifact_url,
			artifact_sha256, max_bytes
		) VALUES ($1, $2, $3, 'linux-amd64', $4, $5, 209715200)`,
		generation, language, version,
		"https://compiler.example/"+string(language)+"/compiler",
		compilerDigest[:],
	); err != nil {
		t.Fatalf("insert legacy %s catalog entry: %v", language, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ($1, $2)`,
		language, generation,
	); err != nil {
		t.Fatalf("activate legacy %s catalog: %v", language, err)
	}
	return generation, compilerDigest, runnerDigest
}

func insertLegacyAddressPublication(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	language verify.Language,
	version string,
	generation int64,
	compilerDigest [sha256.Size]byte,
	runnerDigest [sha256.Size]byte,
	address []byte,
	codeHash []byte,
	blockHash []byte,
) {
	t.Helper()
	requestDigest := sha256.Sum256([]byte("legacy-publication:" + jobID))
	outcome := `{"kind":"verification_success","runtime_match":{"match_type":"full"}}`
	fileName := "Contract.sol"
	if language == verify.Language("vyper") {
		fileName = "Contract.vy"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version,
			compiler_platform, catalog_generation_id, compiler_digest,
			runner_digest, chain_id, address, code_hash, block_hash,
			request, request_payload, request_digest, requires_hard_isolation,
			status, attempt_count, outcome_kind, outcome
		) VALUES (
			$1::uuid, 'address', $2, $2, $3, 'linux-amd64', $4, $5, $6,
			1, $7, $8, $9, '{}'::jsonb, convert_to('{}', 'UTF8'), $10,
			TRUE, 'succeeded', 1, 'verification_success', $11::jsonb
		)`,
		jobID, language, version, generation, compilerDigest[:], runnerDigest[:],
		address, codeHash, blockHash, requestDigest[:], outcome,
	); err != nil {
		t.Fatalf("insert legacy %s job: %v", language, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name,
			contract_name, language, compiler_version, match_type, abi,
			sources, settings, compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			$1::uuid, $2, 'verification_success', $3::jsonb, $4,
			'Contract', $5, $6, 'full', '[]'::jsonb, '{}'::jsonb,
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
			'{}'::jsonb, FALSE
		)`,
		jobID, requestDigest[:], outcome, fileName, language, version,
	); err != nil {
		t.Fatalf("insert legacy %s result: %v", language, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block,
			verification_job_id, request_digest, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts,
			runtime_code_artifacts, libraries, is_blueprint
		) VALUES (
			1, $1, $2, 0, $3::uuid, $4, $5, 'Contract', $6, $7,
			'full', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
			'{}'::jsonb, '{}'::jsonb, '{}'::jsonb, FALSE
		)`,
		address, codeHash, jobID, requestDigest[:], fileName, language, version,
	); err != nil {
		t.Fatalf("insert legacy %s publication: %v", language, err)
	}
}

func insertVerifierV2Compiler(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) (int64, [sha256.Size]byte, [sha256.Size]byte) {
	t.Helper()
	catalogDigest := sha256.Sum256([]byte("verifier-v2-integration-catalog"))
	compilerDigest := sha256.Sum256([]byte("verifier-v2-integration-compiler"))
	executorDigest := sha256.Sum256([]byte("verifier-v2-integration-solcjs-executor"))
	var generation int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO compiler_catalog_generations (
			language, source_url, catalog_digest, entry_count
		) VALUES ('solidity', 'https://compiler.example/list.json', $1, 1)
		RETURNING id`, catalogDigest[:]).Scan(&generation); err != nil {
		t.Fatalf("insert compiler generation: %v", err)
	}
	execFixture(t, ctx, db, `
		INSERT INTO compiler_catalog_entries (
			generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes
		) VALUES ($1, 'solidity', '0.8.30+commit.73712a01', 'emscripten-wasm32',
			'https://compiler.example/soljson.js', $2, 209715200)`,
		generation, compilerDigest[:],
	)
	execFixture(t, ctx, db, `
		INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ('solidity', $1)
		ON CONFLICT (language) DO UPDATE
		SET generation_id = EXCLUDED.generation_id, updated_at = now()`, generation)
	return generation, compilerDigest, executorDigest
}

func verifierV2AddressSubmission(
	address, codeHash, blockHash, runtime []byte,
) verify.SubmissionV2 {
	standardJSON := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	return verify.SubmissionV2{
		Kind: verify.JobAddress, Language: verify.LanguageSolidity,
		CompilerVersion:      "0.8.30+commit.73712a01",
		StandardJSON:         standardJSON,
		StandardJSONVariants: []json.RawMessage{standardJSON},
		Bytecodes: []verify.BytecodePair{{
			Creation: "0x6000", Runtime: "0x" + hex.EncodeToString(runtime),
		}},
		Target: &verify.VerificationTarget{
			ChainID: 1, Address: "0x" + hex.EncodeToString(address),
			CodeHash:         "0x" + hex.EncodeToString(codeHash),
			AtBlockHash:      "0x" + hex.EncodeToString(blockHash),
			CreationBytecode: "0x6000",
			RuntimeBytecode:  "0x" + hex.EncodeToString(runtime),
		},
	}
}

func solcJSProvenance(
	generation int64,
	compilerDigest [sha256.Size]byte,
	executorDigest [sha256.Size]byte,
) verify.CompilerProvenance {
	return verify.CompilerProvenance{
		Kind:              verify.CompilerSolcJS,
		Digest:            compilerDigest,
		ExecutorDigest:    executorDigest,
		ExecutorKind:      verify.SolcJSExecutorKind,
		ExecutionPolicy:   verify.TrustedSubprocessPolicy,
		CatalogGeneration: generation,
		Platform:          verify.CompilerPlatformEmscriptenWASM32,
		ArtifactURL:       "https://compiler.example/soljson.js",
		ArtifactMaxBytes:  209715200,
	}
}

func verifierV2SuccessOutcome(t *testing.T, match string) json.RawMessage {
	return verifierV2SuccessOutcomeWithCreation(t, match, true)
}

func verifierV2GenesisSuccessOutcome(t *testing.T, match string) json.RawMessage {
	return verifierV2SuccessOutcomeWithCreation(t, match, false)
}

func verifierV2SuccessOutcomeWithCreation(
	t *testing.T,
	match string,
	includeCreation bool,
) json.RawMessage {
	t.Helper()
	var creationMatch any
	if includeCreation {
		creationMatch = map[string]any{
			"match_type": match, "transformations": []any{}, "values": map[string]any{},
		}
	}
	outcome, err := json.Marshal(map[string]any{
		"kind": "verification_success", "file_name": "A.sol",
		"contract_name": "A", "language": "solidity",
		"compiler_version": "0.8.30+commit.73712a01",
		"abi":              []any{}, "sources": map[string]any{"A.sol": map[string]any{"content": "contract A {}"}},
		"settings": map[string]any{}, "compilation_artifacts": map[string]any{},
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
		"creation_match":          creationMatch,
		"runtime_match": map[string]any{
			"match_type": match, "transformations": []any{}, "values": map[string]any{},
		},
		"libraries": map[string]any{}, "is_blueprint": false,
	})
	if err != nil {
		t.Fatalf("marshal verifier-v2 outcome: %v", err)
	}
	return outcome
}
