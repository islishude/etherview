//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVyperMigrationPreservesCancelledJobs(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, `CREATE TABLE etherview_schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, ctx, db, "0065_vyper_verification")
	payload := []byte(`{"kind":"vyper_standard_json","language":"vyper","compiler_version":"0.4.3","target_file":"A.vy","standard_json":{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{}}}`)
	digest := sha256.Sum256(payload)
	executorDigest := sha256.Sum256([]byte("cancelled-vyper-runtime"))
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compilerDigest, err := hex.DecodeString(verify.VyperCompilerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := map[string]string{}
	for index, bound := range []bool{false, true} {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 67000+index)
		status := "cancelled"
		if bound {
			status = "queued"
		}
		_, err := db.Exec(ctx, `INSERT INTO verification_jobs (
   id, kind, language, compiler_version, request, request_payload, request_digest, status
  ) VALUES ($1::uuid, 'vyper_standard_json', 'vyper', '0.4.3', $2::jsonb, $3, $4, $5)`, id, string(payload), payload, digest[:], status)
		if err != nil {
			t.Fatalf("insert legacy job (bound=%t): %v", bound, err)
		}
		if bound {
			lease, found, err := repository.Claim(ctx, "legacy-worker", time.Minute)
			if err != nil || !found || lease.Job.ID != id {
				t.Fatalf("claim legacy job: found=%t error=%v", found, err)
			}
			provenance := verify.CompilerProvenance{Kind: verify.CompilerVyper, Platform: verify.CompilerPlatformPythonWheel, ExecutorKind: verify.VyperExecutorKind, ExecutionPolicy: verify.TrustedSubprocessPolicy, ExecutorDigest: executorDigest}
			copy(provenance.Digest[:], compilerDigest)
			if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE verification_jobs SET status='cancelled', leased_by=NULL, lease_token=NULL, lease_expires_at=NULL WHERE id=$1::uuid`, id); err != nil {
				t.Fatal(err)
			}
		}
		var snapshot string
		if err := db.QueryRow(ctx, `SELECT row_to_json(job)::text FROM verification_jobs job WHERE id=$1::uuid`, id).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		snapshots[id] = snapshot
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	for id, before := range snapshots {
		var after string
		if err := db.QueryRow(ctx, `SELECT row_to_json(job)::text FROM verification_jobs job WHERE id=$1::uuid`, id).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("cancelled job %s changed during migration", id)
		}
	}
	if _, found, err := repository.Claim(ctx, "migration-regression", time.Minute); err != nil || found {
		t.Fatalf("cancelled jobs became runnable: found=%t error=%v", found, err)
	}
}

func TestVyperWASMMigrationRequiresDrainedQueue(t *testing.T) {
	for _, status := range []string{"queued", "running"} {
		t.Run(status, func(t *testing.T) {
			db := newIsolatedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if _, err := db.Exec(ctx, `CREATE TABLE etherview_schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
				t.Fatal(err)
			}
			applyMigrationsThrough(t, ctx, db, "0071_native_transfers")
			payload := []byte(`{"kind":"vyper_standard_json","language":"vyper","compiler_version":"0.4.3","target_file":"A.vy","standard_json":{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{}}}`)
			digest := sha256.Sum256(payload)
			_, err := db.Exec(ctx, `INSERT INTO verification_jobs
(id, kind, language, compiler_version, catalog_language, request, request_payload, request_digest, status)
VALUES ('00000000-0000-4000-8000-000000072000', 'vyper_standard_json', 'vyper', '0.4.3', 'vyper', $1::jsonb, $2, $3, 'queued')`, string(payload), payload, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			if status == "running" {
				repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if _, found, err := repository.Claim(ctx, "drain-regression", time.Minute); err != nil || !found {
					t.Fatalf("claim: found=%t error=%v", found, err)
				}
			}
			if err := store.RunMigrations(ctx, db); err == nil {
				t.Fatal("migration accepted an undrained Vyper queue")
			}
			if _, err := db.Exec(ctx, `UPDATE verification_jobs SET status='cancelled', leased_by=NULL, lease_token=NULL, lease_expires_at=NULL`); err != nil {
				t.Fatal(err)
			}
			if err := store.RunMigrations(ctx, db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVyperWASMMigrationPreservesNativeCatalogProvenance(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx := t.Context()
	if _, err := db.Exec(ctx, `CREATE TABLE etherview_schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, ctx, db, "0071_native_transfers")
	generation := seedVyperRuntime(t, db)
	executor := sha256.Sum256([]byte("fixture-vyper-runtime"))
	runtimes := fmt.Sprintf(`[{"protocol":"etherview-vyper-runtime-v3","manifest_sha256":"%x"}]`, executor)
	if _, err := db.Exec(ctx, `UPDATE compiler_catalog_entries SET vyper_runtimes=$1 WHERE generation_id=$2`, runtimes, generation); err != nil {
		t.Fatal(err)
	}
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256:\n    return 42\n"}},"settings":{}}`)
	job, _, err := repository.SubmitV2(ctx, verify.SubmissionV2{Kind: verify.JobVyperStandardJSON, Language: verify.LanguageVyper, CompilerVersion: "0.4.3", TargetFile: "A.vy", StandardJSON: input, Bytecodes: []verify.BytecodePair{{Runtime: "0x6000"}}})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(ctx, "native-catalog", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim: %t %v", found, err)
	}
	compiler := &vyperFixtureCompiler{generation: generation}
	provenance, err := compiler.Provenance(verify.LanguageVyper, "0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	provenance.ExecutorKind = "etherview_vyper_v3"
	if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE verification_jobs SET status='cancelled',leased_by=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=$1::uuid`, job.ID); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRow(ctx, `SELECT row_to_json(j)::text FROM verification_jobs j WHERE id=$1::uuid`, job.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT row_to_json(j)::text FROM verification_jobs j WHERE id=$1::uuid`, job.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("native catalog provenance changed during migration")
	}
	if _, err := db.Exec(ctx, `UPDATE verification_jobs SET status='queued' WHERE id=$1::uuid`, job.ID); err == nil {
		t.Fatal("legacy bound job became runnable after migration")
	}
}
