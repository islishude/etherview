//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVyperBaselinePreservesCancelledProvenance(t *testing.T) {
	for _, bound := range []bool{false, true} {
		name := "unbound"
		if bound {
			name = "bound"
		}
		t.Run(name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx := t.Context()
			repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			job, created, err := repository.SubmitV2(ctx, verify.SubmissionV2{
				Kind: verify.JobVyperStandardJSON, Language: verify.LanguageVyper,
				CompilerVersion: "0.4.3", TargetFile: "A.vy",
				StandardJSON: []byte(`{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256:\n    return 42\n"}},"settings":{}}`),
				Bytecodes:    []verify.BytecodePair{{Runtime: "0x6000"}},
			})
			if err != nil || !created {
				t.Fatalf("submit: created=%t error=%v", created, err)
			}
			if bound {
				compiler := &vyperFixtureCompiler{generation: seedVyperRuntime(t, db)}
				provenance, err := compiler.Provenance(verify.LanguageVyper, "0.4.3")
				if err != nil {
					t.Fatal(err)
				}
				lease, found, err := repository.Claim(ctx, "baseline-worker", time.Minute)
				if err != nil || !found {
					t.Fatalf("claim: found=%t error=%v", found, err)
				}
				if err := repository.BindCompiler(ctx, lease, provenance); err != nil {
					t.Fatal(err)
				}
			}
			execFixture(t, ctx, db, `UPDATE verification_jobs SET status='cancelled',
leased_by=NULL, lease_token=NULL, lease_expires_at=NULL WHERE id=$1::uuid`, job.ID)
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
				t.Fatal("idempotent migration changed cancelled provenance")
			}
			if _, found, err := repository.Claim(ctx, "baseline-retry", time.Minute); err != nil || found {
				t.Fatalf("cancelled job became runnable: found=%t error=%v", found, err)
			}
		})
	}
}
