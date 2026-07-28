//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	generation, compilerDigest, runnerDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	submission := verifierV2AddressSubmission(
		generation, compilerDigest, runnerDigest, address, codeHash,
		mustBytes(t, testHash(7_201)), runtime,
	)
	job, created, err := repository.SubmitV2(ctx, submission, true)
	if err != nil || !created {
		t.Fatalf("submit verifier-v2 job: created=%t error=%v", created, err)
	}
	duplicate, created, err := repository.SubmitV2(ctx, submission, true)
	if err != nil || created || duplicate.ID != job.ID {
		t.Fatalf("deduplicate verifier-v2 job: created=%t id=%s error=%v", created, duplicate.ID, err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim verifier-v2 job: found=%t error=%v", found, err)
	}
	provenance := verify.CompilerProvenance{
		Kind: verify.CompilerRunner, Digest: compilerDigest, RunnerDigest: runnerDigest,
		CatalogGeneration: generation, Platform: verify.CompilerPlatformLinuxAMD64,
		ArtifactURL:      "https://compiler.example/solc",
		ArtifactMaxBytes: 209715200, HardIsolated: true,
	}
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
	generation, compilerDigest, runnerDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	submission := verifierV2AddressSubmission(
		generation, compilerDigest, runnerDigest, address, codeHash,
		mustBytes(t, testHash(7_301)), runtime,
	)
	if _, _, err := repository.SubmitV2(ctx, submission, true); err != nil {
		t.Fatalf("submit stale verifier-v2 job: %v", err)
	}
	lease, found, err := repository.Claim(ctx, "integration-verifier-v2-stale", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim stale verifier-v2 job: found=%t error=%v", found, err)
	}
	if err := repository.BindCompiler(ctx, lease, verify.CompilerProvenance{
		Kind: verify.CompilerRunner, Digest: compilerDigest, RunnerDigest: runnerDigest,
		CatalogGeneration: generation, Platform: verify.CompilerPlatformLinuxAMD64,
		ArtifactURL:      "https://compiler.example/solc",
		ArtifactMaxBytes: 209715200, HardIsolated: true,
	}); err != nil {
		t.Fatalf("bind stale verifier-v2 compiler: %v", err)
	}
	err = repository.CompleteV2(ctx, lease, "verification_success", verifierV2SuccessOutcome(t, "partial"))
	if !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("stale publication error = %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
}

func TestVerifierV2LeaseReclaimKeepsPinnedCompilerIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	generation, compilerDigest, runnerDigest := insertVerifierV2Compiler(t, ctx, db)
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
		Bytecodes:           []verify.BytecodePair{{Runtime: "0x6001"}},
		CatalogGenerationID: generation,
		CompilerPlatform:    verify.CompilerPlatformLinuxAMD64,
		CompilerDigest:      hex.EncodeToString(compilerDigest[:]),
		RunnerDigest:        hex.EncodeToString(runnerDigest[:]),
	}
	if _, _, err := repository.SubmitV2(ctx, submission, true); err != nil {
		t.Fatalf("submit reclaim job: %v", err)
	}
	wrongPlatform := submission
	wrongPlatform.CompilerPlatform = verify.CompilerPlatformWindowsAMD64
	if _, _, err := repository.SubmitV2(ctx, wrongPlatform, true); err == nil {
		t.Fatal("job accepted a compiler platform not bound to its catalog entry")
	}
	first, found, err := repository.Claim(ctx, "worker-a", 10*time.Millisecond)
	if err != nil || !found {
		t.Fatalf("first claim: found=%t error=%v", found, err)
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
		second.Job.RequestV2.CompilerPlatform != verify.CompilerPlatformLinuxAMD64 ||
		second.Job.RequestV2.CompilerDigest != hex.EncodeToString(compilerDigest[:]) ||
		second.Job.RequestV2.RunnerDigest != hex.EncodeToString(runnerDigest[:]) {
		t.Fatalf("reclaim changed pinned provenance: %#v", second.Job.RequestV2)
	}
}

func TestVerifierV2MigrationDeletesHistoricalVerificationData(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `DROP TABLE verified_contracts, verification_results, verification_jobs`)
	execFixture(t, ctx, db, `DROP TABLE compiler_catalog_heads, compiler_catalog_entries, compiler_catalog_generations`)
	execFixture(t, ctx, db, `
		CREATE TABLE verification_jobs (id UUID PRIMARY KEY, payload TEXT NOT NULL);
		CREATE TABLE verification_results (job_id UUID PRIMARY KEY, payload TEXT NOT NULL);
		CREATE TABLE verified_contracts (address BYTEA PRIMARY KEY, payload TEXT NOT NULL);
		INSERT INTO verification_jobs VALUES
			('00000000-0000-4000-8000-000000000027', 'historical-job');
		INSERT INTO verification_results VALUES
			('00000000-0000-4000-8000-000000000027', 'historical-result');
		INSERT INTO verified_contracts VALUES
			(decode(repeat('27', 20), 'hex'), 'historical-publication');
		DELETE FROM etherview_schema_migrations WHERE version = '0027_verifier_v2'`,
	)
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("reapply destructive verifier-v2 migration: %v", err)
	}
	for _, table := range []string{"verification_jobs", "verification_results", "verified_contracts"} {
		assertRowCount(t, ctx, db, `SELECT count(*) FROM `+table, 0)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'verification_jobs'
		  AND column_name IN ('kind', 'catalog_generation_id', 'request_digest', 'outcome_kind')`, 4)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name IN (
			'compiler_catalog_generations',
			'compiler_catalog_entries',
			'compiler_catalog_heads'
		  )`, 3)
}

func insertVerifierV2Compiler(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) (int64, [sha256.Size]byte, [sha256.Size]byte) {
	t.Helper()
	catalogDigest := sha256.Sum256([]byte("verifier-v2-integration-catalog"))
	compilerDigest := sha256.Sum256([]byte("verifier-v2-integration-compiler"))
	runnerDigest := sha256.Sum256([]byte("verifier-v2-integration-runner"))
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
		) VALUES ($1, 'solidity', '0.8.30+commit.73712a01', 'linux-amd64',
			'https://compiler.example/solc', $2, 209715200)`,
		generation, compilerDigest[:],
	)
	execFixture(t, ctx, db, `
		INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ('solidity', $1)`, generation)
	return generation, compilerDigest, runnerDigest
}

func verifierV2AddressSubmission(
	generation int64,
	compilerDigest, runnerDigest [sha256.Size]byte,
	address, codeHash, blockHash, runtime []byte,
) verify.SubmissionV2 {
	standardJSON := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	return verify.SubmissionV2{
		Kind: verify.JobAddress, Language: verify.LanguageSolidity,
		CompilerVersion:      "0.8.30+commit.73712a01",
		StandardJSON:         standardJSON,
		StandardJSONVariants: []json.RawMessage{standardJSON},
		Bytecodes: []verify.BytecodePair{{
			Runtime: "0x" + hex.EncodeToString(runtime),
		}},
		Target: &verify.VerificationTarget{
			ChainID: 1, Address: "0x" + hex.EncodeToString(address),
			CodeHash:        "0x" + hex.EncodeToString(codeHash),
			AtBlockHash:     "0x" + hex.EncodeToString(blockHash),
			RuntimeBytecode: "0x" + hex.EncodeToString(runtime),
		},
		CatalogGenerationID: generation,
		CompilerPlatform:    verify.CompilerPlatformLinuxAMD64,
		CompilerDigest:      hex.EncodeToString(compilerDigest[:]),
		RunnerDigest:        hex.EncodeToString(runnerDigest[:]),
	}
}

func verifierV2SuccessOutcome(t *testing.T, match string) json.RawMessage {
	t.Helper()
	outcome, err := json.Marshal(map[string]any{
		"kind": "verification_success", "file_name": "A.sol",
		"contract_name": "A", "language": "solidity",
		"compiler_version": "0.8.30+commit.73712a01",
		"abi":              []any{}, "sources": map[string]any{"A.sol": map[string]any{"content": "contract A {}"}},
		"settings": map[string]any{}, "compilation_artifacts": map[string]any{},
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
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
