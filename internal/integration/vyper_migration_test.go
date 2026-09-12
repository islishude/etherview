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
	if _, err := db.ExecContext(ctx, `CREATE TABLE etherview_schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
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
		_, err := db.ExecContext(ctx, `INSERT INTO verification_jobs (
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
			if _, err := db.ExecContext(ctx, `UPDATE verification_jobs SET status='cancelled', leased_by=NULL, lease_token=NULL, lease_expires_at=NULL WHERE id=$1::uuid`, id); err != nil {
				t.Fatal(err)
			}
		}
		var snapshot string
		if err := db.QueryRowContext(ctx, `SELECT row_to_json(job)::text FROM verification_jobs job WHERE id=$1::uuid`, id).Scan(&snapshot); err != nil {
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
		if err := db.QueryRowContext(ctx, `SELECT row_to_json(job)::text FROM verification_jobs job WHERE id=$1::uuid`, id).Scan(&after); err != nil {
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
