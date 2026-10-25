//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	pgtype "github.com/jackc/pgx/v5/pgtype"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

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
	compilation := verify.AuthenticatedCompilation{
		StandardJSON: submission.StandardJSONVariants[0],
		Candidates: []verify.CandidateArtifact{{
			FileName: "A.sol", ContractName: "A",
			Language:         verify.LanguageSolidity,
			CompilerVersion:  "0.8.30+commit.73712a01",
			CreationBytecode: "0x6000",
			RuntimeBytecode:  "0x" + hex.EncodeToString(runtime),
			ABI:              json.RawMessage(`[]`), CompilationArtifacts: json.RawMessage(`{}`),
			CreationCodeArtifacts: json.RawMessage(`{}`), RuntimeCodeArtifacts: json.RawMessage(`{}`),
		}},
	}
	if err := repository.CompleteV2(
		ctx, lease, "verification_success", outcome, compilation,
	); err != nil {
		t.Fatalf("complete verifier-v2 job: %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 1)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM verification_compilation_units AS unit
		JOIN verification_compilation_contracts AS candidate
		  ON candidate.compilation_id = unit.id
		WHERE unit.source_job_id = $1::uuid
		  AND unit.request_digest = $2
		  AND unit.standard_json_payload = $3
		  AND unit.compiler_sha256 = $4
		  AND unit.executor_sha256 = $5
		  AND candidate.file_name = 'A.sol'
		  AND candidate.contract_name = 'A'
		  AND candidate.creation_bytecode = decode('6000', 'hex')
		  AND candidate.runtime_bytecode = $6`, 1,
		job.ID, job.RequestDigest[:], []byte(submission.StandardJSONVariants[0]),
		compilerDigest[:], executorDigest[:], runtime,
	)
	if _, err := db.Exec(ctx, `
		UPDATE verification_compilation_contracts SET contract_name = 'Changed'
		WHERE compilation_id = (
			SELECT id FROM verification_compilation_units WHERE source_job_id = $1::uuid
		)`, job.ID); err == nil {
		t.Fatal("immutable authenticated compilation candidate accepted an update")
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_function_selector_sets
		WHERE verification_job_id = $1::uuid AND status = 'complete'
		  AND function_count = 0`, 1, job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_function_selectors
		WHERE verification_job_id = $1::uuid`, 0, job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE match_type = 'full' AND code_hash = $1
		  AND verification_job_id = $2::uuid`, 1, codeHash, job.ID)
	if _, err := db.Exec(ctx, `
		UPDATE verification_results SET outcome_kind = 'verification_failure'
		WHERE job_id = $1::uuid`, job.ID); err == nil {
		t.Fatal("immutable verifier-v2 result accepted an update")
	}
	if _, err := db.Exec(ctx, `
		DELETE FROM verification_results WHERE job_id = $1::uuid`, job.ID); err == nil {
		t.Fatal("immutable verifier-v2 result accepted a delete")
	}
}

func TestVerifiedSelectorIndexMarksMalformedVerifiedABIUnavailable(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	address := mustBytes(t, testAddress(729))
	codeHash := mustBytes(t, testHash(7_299))
	insertVerifiedContractFixture(
		t, ctx, db, address, codeHash, 1, nil, "0.8.30", "Malformed",
		`[{"type":"function","name":"broken","inputs":[{"type":"tuple"}]}]`, `{}`, `{}`,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_function_selector_sets
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2
		  AND status = 'invalid' AND function_count = 0
		  AND warning = 'verified_abi_invalid'`, 1, address, codeHash)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_function_selectors
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2`, 0, address, codeHash)
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
	if _, err := db.Exec(ctx, `
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
	if _, err := db.Exec(ctx, `
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
	for _, representation := range []string{"omitted", "null"} {
		t.Run(representation, func(t *testing.T) {
			testGeasRuntimePublication(t, representation)
		})
	}
}

func testGeasRuntimePublication(t *testing.T, representation string) {
	t.Helper()
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
	var catalogGeneration pgtype.Int8
	var platform, executorKind string
	if err := db.QueryRow(ctx, `
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
	// The worker omits creation_match when no creation entrypoint was supplied.
	// An explicit JSON null also represents no creation-match evidence.
	if representation == "omitted" {
		outcome = json.RawMessage(strings.Replace(string(outcome), `"creation_match":null,`, "", 1))
	}
	for _, test := range []struct {
		name string
		edit func(map[string]json.RawMessage)
	}{
		{"invented creation match", func(fields map[string]json.RawMessage) {
			fields["creation_match"] = json.RawMessage(`{"match_type":"full","transformations":[],"values":{}}`)
		}},
		{"creation entrypoint without match", func(fields map[string]json.RawMessage) {
			fields["settings"] = json.RawMessage(`{"runtime_entrypoint":"system/main.eas","creation_entrypoint":"system/main.eas","stack_check":true}`)
			delete(fields, "creation_match")
		}},
		{"partial runtime", func(fields map[string]json.RawMessage) {
			fields["runtime_match"] = json.RawMessage(`{"match_type":"partial","transformations":[],"values":{}}`)
		}},
	} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(outcome, &fields); err != nil {
			t.Fatal(err)
		}
		test.edit(fields)
		invalid, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		err = repository.CompleteV2(ctx, lease, "verification_success", invalid)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "23514" || databaseError.ConstraintName != "verification_results_success_shape_check" {
			t.Fatalf("%s: expected result shape rejection, got %v", test.name, err)
		}
	}
	if err := repository.CompleteV2(ctx, lease, "verification_success", outcome); err != nil {
		t.Fatalf("complete Geas verification: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE verification_job_id = $1::uuid AND language = 'geas'
		  AND compiler_version = '0.3.3' AND match_type = 'full'
		  AND abi = '[]'::jsonb AND libraries = '{}'::jsonb`, 1, job.ID)
	stored, found, err := repository.Job(ctx, job.ID)
	if err != nil || !found || stored.Status != verify.JobSucceeded {
		t.Fatalf("stored runtime-only job: found=%t status=%s error=%v", found, stored.Status, err)
	}
	verified, found, err := repository.VerifiedContract(ctx, 1, "0x"+hex.EncodeToString(address))
	if err != nil || !found || verified.CreationMatch != nil || verified.RuntimeMatch == nil || verified.RuntimeMatch.MatchType != verify.VerificationMatchFull {
		t.Fatalf("runtime-only artifact: found=%t artifact=%+v error=%v", found, verified, err)
	}
	queued, found, err := repository.Job(ctx, solc.ID)
	if err != nil || !found || queued.Status != verify.JobQueued || queued.AttemptCount != 0 {
		t.Fatalf("solc job during Geas-only availability: job=%+v found=%t error=%v", queued, found, err)
	}
	if _, err := db.Exec(ctx, `
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
	if err := db.QueryRow(ctx, `
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

func TestSolcJSBaselineBindsCompilerOnce(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
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
		t.Fatalf("submit baseline job: created=%t error=%v", created, err)
	}
	lease, found, err := repository.Claim(ctx, "solcjs-baseline-worker", time.Minute)
	if err != nil || !found || lease.Job.ID != job.ID {
		t.Fatalf("claim baseline job: found=%t lease=%+v error=%v", found, lease, err)
	}
	provenance := solcJSProvenance(generation, compilerDigest, executorDigest)
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatalf("bind baseline executor: %v", err)
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

func insertVerifierV2Compiler(
	t *testing.T,
	ctx context.Context,
	db *pgxpool.Pool,
) (int64, [sha256.Size]byte, [sha256.Size]byte) {
	t.Helper()
	catalogDigest := sha256.Sum256([]byte("verifier-v2-integration-catalog"))
	compilerDigest := sha256.Sum256([]byte("verifier-v2-integration-compiler"))
	executorDigest := sha256.Sum256([]byte("verifier-v2-integration-solcjs-executor"))
	var generation int64
	if err := db.QueryRow(ctx, `
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
