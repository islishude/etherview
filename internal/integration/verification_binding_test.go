//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVerificationPublicationRequiresCanonicalCodeObservation(t *testing.T) {
	tests := []struct {
		name                string
		observationAddress  uint64
		observationBlock    uint64
		observationHash     uint64
		observationCodeHash []byte
		canonical           bool
		wantPublished       bool
	}{
		{name: "exact canonical identity", observationAddress: 720, observationBlock: 1, observationHash: 7_201, canonical: true, wantPublished: true},
		{name: "stale block observation", observationAddress: 720, observationBlock: 0, observationHash: 7_200, canonical: true},
		{name: "noncanonical observation flag", observationAddress: 720, observationBlock: 1, observationHash: 7_201, canonical: false},
		{name: "mismatched code hash", observationAddress: 720, observationBlock: 1, observationHash: 7_201, observationCodeHash: make([]byte, 32), canonical: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			coreRepository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatalf("create core repository: %v", err)
			}
			genesis := testBundle(0, testHash(7_200), testHash(0), testHash(8_200), "verification-binding-genesis")
			block := testBundle(1, testHash(7_201), testHash(7_200), testHash(8_201), "verification-binding-block")
			commitCanonical(t, ctx, coreRepository, genesis)
			commitCanonical(t, ctx, coreRepository, block)

			runtimeBytecode := []byte{0x60, 0x01}
			codeHash := keccak256(runtimeBytecode)
			observationCodeHash := test.observationCodeHash
			if observationCodeHash == nil {
				observationCodeHash = codeHash
			}
			execFixture(t, ctx, db, `
				INSERT INTO contract_code_observations (
					chain_id, address, block_number, block_hash, code_hash, code, canonical
				) VALUES (1, $1, $2, $3, $4, $5, $6)`,
				mustBytes(t, testAddress(test.observationAddress)), test.observationBlock,
				mustBytes(t, testHash(test.observationHash)), observationCodeHash, runtimeBytecode, test.canonical,
			)

			repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
			if err != nil {
				t.Fatalf("create verification repository: %v", err)
			}
			request := verify.Request{
				ChainID: 1, Address: testAddress(720).String(),
				CodeHash: "0x" + hex.EncodeToString(codeHash), AtBlockHash: testHash(7_201).String(),
				Language: verify.LanguageSolidity, CompilerVersion: "v0.8.30+commit.73712a01",
				ContractIdentifier: "A.sol:A",
				StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
				CreationBytecode:   "0x6001", RuntimeBytecode: "0x6001",
			}
			if _, _, err := repository.Submit(ctx, request); err != nil {
				t.Fatalf("submit verification job: %v", err)
			}
			lease, found, err := repository.Claim(ctx, "integration-worker", time.Minute)
			if err != nil || !found {
				t.Fatalf("claim verification job: found=%t error=%v", found, err)
			}
			var compilerDigest [32]byte
			compilerDigest[0] = 1
			if err := repository.BindCompiler(ctx, lease, verify.CompilerProvenance{
				Kind: verify.CompilerContainer, Digest: compilerDigest, HardIsolated: true,
			}); err != nil {
				t.Fatalf("bind verification compiler: %v", err)
			}
			completion := verify.Completion{
				Kind: verify.MatchExact, Match: verify.MatchResult{Creation: verify.MatchExact, Runtime: verify.MatchExact},
				Artifact: verify.Artifact{ABI: json.RawMessage(`[]`)},
				Sources:  json.RawMessage(`{"A.sol":{"content":"contract A {}"}}`),
				Settings: json.RawMessage(`{}`),
			}
			err = repository.Complete(ctx, lease, completion)
			if test.wantPublished {
				if err != nil {
					t.Fatalf("publish exact canonical verification: %v", err)
				}
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 1)
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 1)
				var resultJobID, compilerKind string
				var requestDigest, compilerDigest []byte
				if err := db.QueryRowContext(ctx, `
					SELECT job_id::text, request_digest, compiler_kind, compiler_digest
					FROM verification_results`).Scan(
					&resultJobID, &requestDigest, &compilerKind, &compilerDigest,
				); err != nil {
					t.Fatalf("query immutable verification result: %v", err)
				}
				if resultJobID != lease.Job.ID || len(requestDigest) != 32 ||
					compilerKind != "container" || len(compilerDigest) != 32 {
					t.Fatalf("result provenance job=%s request=%x compiler=%s/%x", resultJobID, requestDigest, compilerKind, compilerDigest)
				}
				if _, err := db.ExecContext(ctx, `UPDATE verification_results SET result_kind = 'metadata_only'`); err == nil {
					t.Fatal("immutable verification result accepted an update")
				}
				secondRequest := request
				secondRequest.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A { function y() external {} }"}},"settings":{}}`)
				if _, created, err := repository.Submit(ctx, secondRequest); err != nil || !created {
					t.Fatalf("submit second exact result created=%t error=%v", created, err)
				}
				secondLease, found, err := repository.Claim(ctx, "integration-worker-2", time.Minute)
				if err != nil || !found {
					t.Fatalf("claim second exact result found=%t error=%v", found, err)
				}
				secondCompilerDigest := compilerDigest
				secondCompilerDigest[0] = 2
				var secondDigest [32]byte
				copy(secondDigest[:], secondCompilerDigest)
				if err := repository.BindCompiler(ctx, secondLease, verify.CompilerProvenance{
					Kind: verify.CompilerContainer, Digest: secondDigest, HardIsolated: true,
				}); err != nil {
					t.Fatalf("bind second compiler: %v", err)
				}
				secondCompletion := completion
				secondCompletion.Sources = json.RawMessage(`{"A.sol":{"content":"contract A { function y() external {} }"}}`)
				if err := repository.Complete(ctx, secondLease, secondCompletion); err != nil {
					t.Fatalf("complete second exact result: %v", err)
				}
				var preferredResult, projectedResult string
				if err := db.QueryRowContext(ctx, `
					SELECT job_id::text FROM verification_results
					ORDER BY request_digest ASC LIMIT 1`).Scan(&preferredResult); err != nil {
					t.Fatalf("select deterministic preferred result: %v", err)
				}
				if err := db.QueryRowContext(ctx, `
					SELECT verification_job_id::text FROM verified_contracts`).Scan(&projectedResult); err != nil {
					t.Fatalf("select projected verification result: %v", err)
				}
				if projectedResult != preferredResult {
					t.Fatalf("projected result=%s, want deterministic result=%s", projectedResult, preferredResult)
				}
				assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 2)
				if _, err := db.ExecContext(ctx, `
					UPDATE verification_jobs
					SET status = 'failed', result_kind = NULL, result = NULL,
					    error_code = 'compile_failed'
					WHERE id = $1::uuid`, resultJobID); err == nil {
					t.Fatal("terminal job accepted a state conflicting with its immutable result")
				}
				if _, err := db.ExecContext(ctx, `
					UPDATE verified_contracts SET abi = '[{"type":"error"}]'::jsonb`); err == nil {
					t.Fatal("verified contract projection accepted material conflicting with its source result")
				}
				return
			}
			if !errors.Is(err, verify.ErrTargetNotCanonical) {
				t.Fatalf("unbound publication error=%v", err)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
			assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verification_jobs
				WHERE status = 'failed' AND error_code = 'target_not_canonical'`, 1)
		})
	}
}

func TestVerificationSubmissionDigestAndFailedResubmission(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	seedVerificationChain(t, ctx, db, 7_300)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	request := integrationVerificationRequest(730, 7_300)
	public := verify.SubmissionOptions{RequiresHardIsolation: true}
	first, created, err := repository.Submit(ctx, request, public)
	if err != nil || !created {
		t.Fatalf("first submit created=%t error=%v", created, err)
	}
	duplicate, created, err := repository.Submit(ctx, request, public)
	if err != nil || created || duplicate.ID != first.ID {
		t.Fatalf("duplicate submit job=%s created=%t error=%v", duplicate.ID, created, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET max_attempts = max_attempts + 1
		WHERE id = $1::uuid`, first.ID); err == nil {
		t.Fatal("verification job accepted an identity/budget mutation")
	}

	differentInput := request
	differentInput.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A { function x() external {} }"}},"settings":{}}`)
	different, created, err := repository.Submit(ctx, differentInput, public)
	if err != nil || !created || different.ID == first.ID || different.RequestDigest == first.RequestDigest {
		t.Fatalf("different input submit job=%s created=%t error=%v", different.ID, created, err)
	}
	private, created, err := repository.Submit(ctx, request)
	if err != nil || !created || private.ID == first.ID || private.RequestDigest == first.RequestDigest {
		t.Fatalf("different policy submit job=%s created=%t error=%v", private.ID, created, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(DISTINCT request_digest) FROM verification_jobs`, 3)

	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'failed', error_code = 'compile_failed', updated_at = clock_timestamp()
		WHERE id = $1::uuid`, first.ID); err != nil {
		t.Fatalf("terminalize first submission: %v", err)
	}
	retry, created, err := repository.Submit(ctx, request, public)
	if err != nil || !created || retry.ID == first.ID || retry.RequestDigest != first.RequestDigest {
		t.Fatalf("failed resubmission job=%s created=%t error=%v", retry.ID, created, err)
	}
}

func TestVerificationDurableIsolationProvenanceAndAttemptBudget(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	seedVerificationChain(t, ctx, db, 7_400)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	request := integrationVerificationRequest(740, 7_400)
	if _, _, err := repository.Submit(ctx, request, verify.SubmissionOptions{RequiresHardIsolation: true}); err != nil {
		t.Fatalf("submit public verification: %v", err)
	}
	lease, found, err := repository.Claim(ctx, "public-worker", time.Minute)
	if err != nil || !found || lease.Job.AttemptCount != 1 || lease.Job.MaxAttempts != 1 || !lease.Job.RequiresHardIsolation {
		t.Fatalf("public claim=%+v found=%t error=%v", lease.Job, found, err)
	}
	var processDigest [32]byte
	processDigest[0] = 1
	if err := repository.BindCompiler(ctx, lease, verify.CompilerProvenance{
		Kind: verify.CompilerProcess, Digest: processDigest,
	}); !errors.Is(err, verify.ErrSandboxRequired) {
		t.Fatalf("process compiler binding error=%v", err)
	}
	var containerDigest [32]byte
	containerDigest[0] = 2
	if err := repository.BindCompiler(ctx, lease, verify.CompilerProvenance{
		Kind: verify.CompilerContainer, Digest: containerDigest, HardIsolated: true,
	}); err != nil {
		t.Fatalf("bind hard-isolated compiler: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET compiler_digest = $2
		WHERE id = $1::uuid`, lease.Job.ID, processDigest[:]); err == nil {
		t.Fatal("verification job accepted a compiler provenance mutation")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET attempt_count = attempt_count - 1
		WHERE id = $1::uuid`, lease.Job.ID); err == nil {
		t.Fatal("verification job accepted an attempt-count rollback")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1::uuid`, lease.Job.ID); err != nil {
		t.Fatalf("expire verification lease: %v", err)
	}
	if exhausted, found, err := repository.Claim(ctx, "next-worker", time.Minute); err != nil || found {
		t.Fatalf("exhausted claim=%+v found=%t error=%v", exhausted, found, err)
	}
	job, found, err := repository.Job(ctx, lease.Job.ID)
	if err != nil || !found || job.Status != verify.JobFailed || job.ErrorCode != verify.ErrorAttemptsExhausted {
		t.Fatalf("exhausted job=%+v found=%t error=%v", job, found, err)
	}

	reclaimRepository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20, MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("create reclaim repository: %v", err)
	}
	reclaimRequest := request
	reclaimRequest.ContractIdentifier = "A.sol:Reclaim"
	if _, _, err := reclaimRepository.Submit(ctx, reclaimRequest); err != nil {
		t.Fatalf("submit reclaim verification: %v", err)
	}
	firstLease, found, err := reclaimRepository.Claim(ctx, "worker-a", time.Minute)
	if err != nil || !found {
		t.Fatalf("first reclaim claim found=%t error=%v", found, err)
	}
	if err := reclaimRepository.BindCompiler(ctx, firstLease, verify.CompilerProvenance{
		Kind: verify.CompilerProcess, Digest: processDigest,
	}); err != nil {
		t.Fatalf("bind first compiler: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1::uuid`, firstLease.Job.ID); err != nil {
		t.Fatalf("expire first reclaim lease: %v", err)
	}
	secondLease, found, err := reclaimRepository.Claim(ctx, "worker-b", time.Minute)
	if err != nil || !found || secondLease.Job.AttemptCount != 2 {
		t.Fatalf("second reclaim claim=%+v found=%t error=%v", secondLease.Job, found, err)
	}
	otherDigest := processDigest
	otherDigest[0] = 3
	if err := reclaimRepository.BindCompiler(ctx, secondLease, verify.CompilerProvenance{
		Kind: verify.CompilerProcess, Digest: otherDigest,
	}); !errors.Is(err, verify.ErrCompilerProvenanceConflict) {
		t.Fatalf("changed compiler binding error=%v", err)
	}
	if err := reclaimRepository.Fail(ctx, secondLease, verify.ErrorCompilerProvenanceMismatch); err != nil {
		t.Fatalf("terminalize provenance mismatch: %v", err)
	}
}

func seedVerificationChain(t *testing.T, ctx context.Context, db *sql.DB, hash uint64) {
	t.Helper()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(hash), testHash(0), testHash(hash+1), "verification-seed"))
}

func integrationVerificationRequest(address, blockHash uint64) verify.Request {
	runtimeBytecode := []byte{0x60, 0x01}
	return verify.Request{
		ChainID: 1, Address: testAddress(address).String(),
		CodeHash:    "0x" + hex.EncodeToString(keccak256(runtimeBytecode)),
		AtBlockHash: testHash(blockHash).String(), Language: verify.LanguageSolidity,
		CompilerVersion: "v0.8.30+commit.73712a01", ContractIdentifier: "A.sol:A",
		StandardJSON:     json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		CreationBytecode: "0x6001", RuntimeBytecode: "0x6001",
	}
}
