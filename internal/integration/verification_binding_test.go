//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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

func TestVerificationSucceededJobRequiresImmutableResult(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	seedVerificationChain(t, ctx, db, 7_250)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create verification repository: %v", err)
	}
	if _, _, err := repository.Submit(ctx, integrationVerificationRequest(725, 7_250)); err != nil {
		t.Fatalf("submit verification job: %v", err)
	}
	lease, found, err := repository.Claim(ctx, "integrity-worker", time.Minute)
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

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin bypass transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', result_kind = 'exact',
		    result = '{"match":{"creation":"exact","runtime":"exact"},"published":true}'::jsonb,
		    error_code = NULL, leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1::uuid`, lease.Job.ID); err != nil {
		t.Fatalf("stage resultless success: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("resultless successful verification job committed")
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running'`, 1, lease.Job.ID)
	if err := repository.Fail(ctx, lease, verify.ErrorCompileFailed); err != nil {
		t.Fatalf("terminalize update-regression fixture: %v", err)
	}

	insertTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin direct-insert bypass transaction: %v", err)
	}
	defer insertTx.Rollback()
	if _, err := insertTx.ExecContext(ctx, `
		INSERT INTO verification_jobs (
			id, chain_id, address, code_hash, block_hash, language,
			compiler_version, request, request_payload, request_digest,
			requires_hard_isolation, attempt_count, max_attempts,
			compiler_kind, compiler_digest, compiler_hard_isolated,
			status, result_kind, result
		)
		SELECT
			'00000000-0000-4000-8000-000000007252'::uuid,
			chain_id, address, code_hash, block_hash, language,
			compiler_version, request, request_payload, request_digest,
			requires_hard_isolation, attempt_count, max_attempts,
			compiler_kind, compiler_digest, compiler_hard_isolated,
			'succeeded', 'exact',
			'{"match":{"creation":"exact","runtime":"exact"},"published":true}'::jsonb
		FROM verification_jobs
		WHERE id = $1::uuid`, lease.Job.ID); err != nil {
		t.Fatalf("stage resultless successful insert: %v", err)
	}
	if err := insertTx.Commit(); err == nil {
		t.Fatal("direct resultless successful verification job insert committed")
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs WHERE status = 'succeeded'`, 0)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block, language,
			compiler_version, match_kind, contract_name, abi, sources, settings
		) VALUES (
			1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
			'Unsourced', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
		)`, mustBytes(t, testAddress(725)), mustBytes(t, testHash(7_251))); err == nil {
		t.Fatal("new unsourced verified-contract projection committed")
	}
}

func TestVerificationPublicationIntegrityMigrationPreservesOnlyExistingLegacyRows(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const (
		integrityVersion     = "0019_verification_publication_integrity"
		observabilityVersion = "0020_observability_active_repair_index"
	)
	migrations := migrateVerificationSchemaBefore(t, ctx, db, integrityVersion, func(migration store.Migration) {
		if migration.Version == "0015_search_catalog_consistency" {
			execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
			execFixture(t, ctx, db, `
				INSERT INTO verified_contracts (
					chain_id, address, code_hash, valid_from_block, language,
					compiler_version, match_kind, contract_name, abi, sources, settings
				) VALUES (
					1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
					'Legacy', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
				)`, mustBytes(t, testAddress(726)), mustBytes(t, testHash(7_260)))
		}
	})
	corruptRepository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{
		MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("create pre-integrity verification repository: %v", err)
	}
	if _, _, err := corruptRepository.Submit(ctx, integrationVerificationRequest(728, 7_280)); err != nil {
		t.Fatalf("submit pre-integrity verification job: %v", err)
	}
	corruptLease, found, err := corruptRepository.Claim(ctx, "pre-integrity-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim pre-integrity verification job: found=%t error=%v", found, err)
	}
	var corruptCompilerDigest [32]byte
	corruptCompilerDigest[0] = 1
	if err := corruptRepository.BindCompiler(ctx, corruptLease, verify.CompilerProvenance{
		Kind: verify.CompilerContainer, Digest: corruptCompilerDigest, HardIsolated: true,
	}); err != nil {
		t.Fatalf("bind pre-integrity verification compiler: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', result_kind = 'exact',
		    result = '{"match":{"creation":"exact","runtime":"exact"},"published":true}'::jsonb,
		    error_code = NULL, leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1::uuid`, corruptLease.Job.ID); err != nil {
		t.Fatalf("seed pre-0019 resultless success: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_jobs AS job
		WHERE job.id = $1::uuid AND job.status = 'succeeded'
		  AND job.compiler_kind = 'container'
		  AND NOT EXISTS (
		      SELECT 1 FROM verification_results AS immutable_result
		      WHERE immutable_result.job_id = job.id
		  )`, 1, corruptLease.Job.ID)

	before, err := store.ReadSchemaStatus(ctx, db)
	if err != nil {
		t.Fatalf("read pre-upgrade schema status: %v", err)
	}
	pendingIntegrity := false
	for _, version := range before.Pending {
		pendingIntegrity = pendingIntegrity || version == integrityVersion
	}
	if !pendingIntegrity {
		t.Fatalf("pre-upgrade pending migrations=%v", before.Pending)
	}
	if len(before.Pending) < 2 || before.Pending[0] != integrityVersion || before.Pending[1] != observabilityVersion {
		t.Fatalf("pre-upgrade migration order=%v, want %s then %s", before.Pending, integrityVersion, observabilityVersion)
	}
	beforeLedger := migrationLedger(t, ctx, db)
	beforeDDL := readVerificationPublicationDDLState(t, ctx, db)
	if err := store.RunMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "non-legacy successful verification job lacks its exact immutable result") {
		t.Fatalf("corrupt pre-upgrade migration error=%v", err)
	}
	failedLedger := migrationLedger(t, ctx, db)
	if len(failedLedger) != len(beforeLedger) {
		t.Fatalf("failed integrity migration changed ledger size: before=%d after=%d", len(beforeLedger), len(failedLedger))
	}
	for version, entry := range beforeLedger {
		if failed := failedLedger[version]; failed.Checksum != entry.Checksum || !failed.AppliedAt.Equal(entry.AppliedAt) {
			t.Fatalf("failed integrity migration changed %s: before=%+v after=%+v", version, entry, failed)
		}
	}
	failedStatus, err := store.ReadSchemaStatus(ctx, db)
	if err != nil {
		t.Fatalf("read failed-upgrade schema status: %v", err)
	}
	failedPendingIntegrity := false
	for _, version := range failedStatus.Pending {
		failedPendingIntegrity = failedPendingIntegrity || version == integrityVersion
	}
	if !failedPendingIntegrity {
		t.Fatalf("failed upgrade recorded integrity migration: pending=%v", failedStatus.Pending)
	}
	if failedDDL := readVerificationPublicationDDLState(t, ctx, db); failedDDL != beforeDDL {
		t.Fatalf("failed integrity migration changed publication DDL:\nbefore=%+v\nafter=%+v", beforeDDL, failedDDL)
	}
	// The failed migration released its relation locks and restored the 0016
	// source guard, under which a pre-0019 unsourced projection is still legal.
	execFixture(t, ctx, db, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block, language,
			compiler_version, match_kind, contract_name, abi, sources, settings
		) VALUES (
			1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
			'FailedMigrationLegacy', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
		)`, mustBytes(t, testAddress(729)), mustBytes(t, testHash(7_290)))

	if _, err := db.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, chain_id, address, code_hash, block_hash, block_number,
			request_digest, compiler_kind, compiler_digest, compiler_hard_isolated,
			result_kind, result, contract_name, abi, sources, settings
		)
		SELECT
			id, chain_id, address, code_hash, block_hash, 0,
			request_digest, compiler_kind, compiler_digest, compiler_hard_isolated,
			result_kind, result, 'A', '[]'::jsonb,
			'{"A.sol":{"content":"contract A {}"}}'::jsonb, '{}'::jsonb
		FROM verification_jobs
		WHERE id = $1::uuid`, corruptLease.Job.ID); err != nil {
		t.Fatalf("repair pre-0019 resultless success: %v", err)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run integrity upgrade: %v", err)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatalf("check upgraded schema: %v", err)
	}
	afterLedger := migrationLedger(t, ctx, db)
	for version, entry := range beforeLedger {
		if after := afterLedger[version]; after.Checksum != entry.Checksum || !after.AppliedAt.Equal(entry.AppliedAt) {
			t.Fatalf("migration %s changed across upgrade: before=%+v after=%+v", version, entry, after)
		}
	}
	for _, pendingVersion := range before.Pending {
		var expectedChecksum string
		for _, migration := range migrations {
			if migration.Version == pendingVersion {
				expectedChecksum = migration.Checksum
				break
			}
		}
		entry, ok := afterLedger[pendingVersion]
		if expectedChecksum == "" || !ok || entry.Checksum != expectedChecksum {
			t.Fatalf("new migration %s ledger entry=%+v present=%t expected checksum=%q", pendingVersion, entry, ok, expectedChecksum)
		}
	}

	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts
		WHERE contract_name IN ('Legacy', 'FailedMigrationLegacy')
		  AND verification_job_id IS NULL AND request_digest IS NULL`, 2)
	if _, err := db.ExecContext(ctx, `
		UPDATE verified_contracts SET contract_name = 'Changed'
		WHERE contract_name = 'Legacy'`); err == nil {
		t.Fatal("post-upgrade update of legacy unsourced projection committed")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO verified_contracts (
			chain_id, address, code_hash, valid_from_block, language,
			compiler_version, match_kind, contract_name, abi, sources, settings
		) VALUES (
			1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
			'NewUnsourced', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
		)`, mustBytes(t, testAddress(727)), mustBytes(t, testHash(7_270))); err == nil {
		t.Fatal("post-upgrade unsourced projection insert committed")
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verified_contracts WHERE contract_name = 'Legacy'`, 1)
}

func TestVerificationPublicationIntegrityMigrationSerializesUnsourcedWrites(t *testing.T) {
	for _, operation := range []string{"insert", "update"} {
		t.Run(operation, func(t *testing.T) {
			db := newIsolatedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()

			const integrityVersion = "0019_verification_publication_integrity"
			migrateVerificationSchemaBefore(t, ctx, db, integrityVersion, nil)
			execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
			execFixture(t, ctx, db, `
				INSERT INTO verified_contracts (
					chain_id, address, code_hash, valid_from_block, language,
					compiler_version, match_kind, contract_name, abi, sources, settings
				) VALUES (
					1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
					'OnlineLegacy', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
				)`, mustBytes(t, testAddress(7_291)), mustBytes(t, testHash(72_910)))

			var schema string
			if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
				t.Fatalf("read online-migration schema: %v", err)
			}
			migrationDB := openVerificationSchemaPool(t, ctx, schema, "etherview-verification-migration")
			writerDB := openVerificationSchemaPool(t, ctx, schema, "etherview-verification-writer")
			migrationPID := postgresBackendPID(t, ctx, migrationDB)
			writerPID := postgresBackendPID(t, ctx, writerDB)

			blocker, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin verification migration blocker: %v", err)
			}
			blockerHeld := true
			defer func() {
				if blockerHeld {
					_ = blocker.Rollback()
				}
			}()
			if _, err := blocker.ExecContext(ctx, `LOCK TABLE verification_jobs IN ROW EXCLUSIVE MODE`); err != nil {
				t.Fatalf("lock verification jobs ahead of migration: %v", err)
			}

			migrationDone := make(chan error, 1)
			go func() {
				migrationDone <- store.RunMigrations(ctx, migrationDB)
			}()
			waitForVerificationMigrationLockQueue(t, ctx, db, migrationPID)

			concurrentAddress := mustBytes(t, testAddress(7_292))
			concurrentCodeHash := mustBytes(t, testHash(72_920))
			writerDone := make(chan error, 1)
			go func() {
				var writeErr error
				switch operation {
				case "insert":
					_, writeErr = writerDB.ExecContext(ctx, `
						INSERT INTO verified_contracts (
							chain_id, address, code_hash, valid_from_block, language,
							compiler_version, match_kind, contract_name, abi, sources, settings
						) VALUES (
							1, $1, $2, 0, 'solidity', '0.8.30', 'exact',
							'ConcurrentUnsourced', '[]'::jsonb, '{}'::jsonb, '{}'::jsonb
						)`, concurrentAddress, concurrentCodeHash)
				case "update":
					_, writeErr = writerDB.ExecContext(ctx, `
						UPDATE verified_contracts
						SET contract_name = 'ConcurrentUnsourced'
						WHERE contract_name = 'OnlineLegacy'`)
				}
				writerDone <- writeErr
			}()
			waitForPostgresRelationLock(t, ctx, db, writerPID, "verified_contracts", "RowExclusiveLock", false)
			select {
			case err := <-writerDone:
				t.Fatalf("unsourced %s crossed the queued migration locks: %v", operation, err)
			default:
			}

			if err := blocker.Rollback(); err != nil {
				t.Fatalf("release verification migration blocker: %v", err)
			}
			blockerHeld = false
			select {
			case err := <-migrationDone:
				if err != nil {
					t.Fatalf("run online verification migration: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("online verification migration did not complete: %v", ctx.Err())
			}
			select {
			case err := <-writerDone:
				if err == nil || !strings.Contains(err.Error(), "verified contract projection requires an immutable result") {
					t.Fatalf("post-migration unsourced %s error=%v", operation, err)
				}
			case <-ctx.Done():
				t.Fatalf("blocked unsourced %s did not resume: %v", operation, ctx.Err())
			}

			if err := store.CheckSchema(ctx, db); err != nil {
				t.Fatalf("check online-migrated schema: %v", err)
			}
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verified_contracts
				WHERE contract_name = 'OnlineLegacy'
				  AND verification_job_id IS NULL AND request_digest IS NULL`, 1)
			assertRowCount(t, ctx, db, `
				SELECT count(*) FROM verified_contracts
				WHERE contract_name = 'ConcurrentUnsourced'`, 0)
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

func migrateVerificationSchemaBefore(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	stopVersion string,
	after func(store.Migration),
) []store.Migration {
	t.Helper()
	migrations, err := store.Migrations()
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE etherview_schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create pre-upgrade migration ledger: %v", err)
	}
	foundStop := false
	for _, migration := range migrations {
		if migration.Version == stopVersion {
			foundStop = true
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin pre-upgrade migration %s: %v", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply pre-upgrade migration %s: %v", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO etherview_schema_migrations (version, checksum)
			VALUES ($1, $2)`, migration.Version, migration.Checksum); err != nil {
			_ = tx.Rollback()
			t.Fatalf("record pre-upgrade migration %s: %v", migration.Version, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit pre-upgrade migration %s: %v", migration.Version, err)
		}
		if after != nil {
			after(migration)
		}
	}
	if !foundStop {
		t.Fatalf("missing migration %s", stopVersion)
	}
	return migrations
}

func openVerificationSchemaPool(t *testing.T, ctx context.Context, schema, applicationName string) *sql.DB {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(testDatabaseEnvironment))
	config, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnvironment, err)
	}
	config.RuntimeParams = cloneRuntimeParams(config.RuntimeParams)
	config.RuntimeParams["application_name"] = applicationName
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	// The tests record this pool's sole backend PID before starting the blocked
	// operation. Keeping exactly one live/idle connection makes that pg_locks
	// identity deterministic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("connect verification schema pool: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close verification schema pool: %v", err)
		}
	})
	return db
}

func postgresBackendPID(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var pid int
	if err := db.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read PostgreSQL backend PID: %v", err)
	}
	return pid
}

func waitForVerificationMigrationLockQueue(t *testing.T, ctx context.Context, db *sql.DB, pid int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var resultGranted, projectionGranted, jobWaiting bool
		err := db.QueryRowContext(ctx, `
			SELECT
				EXISTS (
					SELECT 1 FROM pg_locks
					WHERE pid = $1 AND locktype = 'relation'
					  AND relation = 'verification_results'::regclass
					  AND mode = 'ShareRowExclusiveLock' AND granted
				),
				EXISTS (
					SELECT 1 FROM pg_locks
					WHERE pid = $1 AND locktype = 'relation'
					  AND relation = 'verified_contracts'::regclass
					  AND mode = 'ShareRowExclusiveLock' AND granted
				),
				EXISTS (
					SELECT 1 FROM pg_locks
					WHERE pid = $1 AND locktype = 'relation'
					  AND relation = 'verification_jobs'::regclass
					  AND mode = 'ShareRowExclusiveLock' AND NOT granted
				)`, pid).Scan(&resultGranted, &projectionGranted, &jobWaiting)
		if err != nil {
			t.Fatalf("read verification migration locks: %v", err)
		}
		if resultGranted && projectionGranted && jobWaiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for verification migration lock queue: result_granted=%t projection_granted=%t job_waiting=%t: %v",
				resultGranted, projectionGranted, jobWaiting, ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func waitForPostgresRelationLock(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	pid int,
	relation, mode string,
	granted bool,
) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var found bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks
				WHERE pid = $1 AND locktype = 'relation'
				  AND relation = to_regclass($2)
				  AND mode = $3 AND granted = $4
			)`, pid, relation, mode, granted).Scan(&found); err != nil {
			t.Fatalf("read PostgreSQL relation lock: %v", err)
		}
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s %s granted=%t: %v", relation, mode, granted, ctx.Err())
		case <-ticker.C:
		}
	}
}

type verificationPublicationDDLState struct {
	resultFunction     string
	resultFunctionGUCs string
	sourceFunction     string
	sourceFunctionGUCs string
	triggers           string
}

func readVerificationPublicationDDLState(t *testing.T, ctx context.Context, db *sql.DB) verificationPublicationDDLState {
	t.Helper()
	var state verificationPublicationDDLState
	if err := db.QueryRowContext(ctx, `
		SELECT
			pg_get_functiondef('enforce_verification_result_job_state()'::regprocedure),
			COALESCE((
				SELECT array_to_string(proconfig, E'\n')
				FROM pg_proc
				WHERE oid = 'enforce_verification_result_job_state()'::regprocedure
			), ''),
			pg_get_functiondef('enforce_verified_contract_source()'::regprocedure),
			COALESCE((
				SELECT array_to_string(proconfig, E'\n')
				FROM pg_proc
				WHERE oid = 'enforce_verified_contract_source()'::regprocedure
			), ''),
			COALESCE((
				SELECT string_agg(
					trigger.tgrelid::regclass::text || ':' || trigger.tgname || ':' ||
					pg_get_triggerdef(trigger.oid), E'\n'
					ORDER BY trigger.tgrelid::regclass::text, trigger.tgname
				)
				FROM pg_trigger AS trigger
				WHERE NOT trigger.tgisinternal
				  AND trigger.tgrelid IN (
					  'verification_jobs'::regclass,
					  'verification_results'::regclass,
					  'verified_contracts'::regclass
				  )
			), '')`).Scan(
		&state.resultFunction,
		&state.resultFunctionGUCs,
		&state.sourceFunction,
		&state.sourceFunctionGUCs,
		&state.triggers,
	); err != nil {
		t.Fatalf("read verification publication DDL state: %v", err)
	}
	return state
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
