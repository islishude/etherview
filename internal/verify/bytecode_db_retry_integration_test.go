//go:build integration

package verify

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func TestBytecodeDBCompilerFailureRecovery(t *testing.T) {
	for _, failure := range []error{errors.New("compiler unavailable"), CompilerDownloadError{Code: ErrorCompilerDownloadUnavailable}, CompilerDownloadError{Code: ErrorCompilerChecksum}} {
		t.Run(failure.Error(), func(t *testing.T) {
			db := bytecodeDBPostgres(t)
			w, compiler := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(response).Encode(map[string]any{"sources": []BytecodeDBSource{bytecodeDBSourceFixture()}})
			})
			address := "0x" + strings.Repeat("11", 20)
			bytecodeDBSeedGenesis(t, db, address)
			tick := func() {
				t.Helper()
				bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
				if err := w.tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			tick()
			compiler.compileError = failure
			local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local", LeaseDuration: time.Minute, PollInterval: time.Second, MaxOutputBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if found, err := local.ProcessOne(t.Context()); err != nil || !found {
				t.Fatalf("found=%t err=%v", found, err)
			}
			var jobID string
			if err = db.QueryRow(t.Context(), `SELECT id::text FROM verification_jobs WHERE status='failed'`).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			tick()
			if found, err := local.ProcessOne(t.Context()); err != nil || found {
				t.Fatalf("retried inside cooldown: found=%t err=%v", found, err)
			}
			bytecodeDBExec(t, db, `UPDATE verification_jobs SET updated_at=now()-interval '25 hours'`)
			// Independent restarted workers compete over the same persistent target.
			other := *w
			bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
			var group sync.WaitGroup
			for _, worker := range []*BytecodeDBWorker{w, &other} {
				group.Go(func() {
					if err := worker.tick(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			group.Wait()
			compiler.compileError = nil
			found, err := local.ProcessOne(t.Context())
			if failure.Error() == string(ErrorCompilerChecksum) {
				if err != nil || found {
					t.Fatalf("deterministic download failure retried: %t %v", found, err)
				}
				return
			}
			if err != nil || !found {
				t.Fatalf("recovery found=%t err=%v", found, err)
			}
			artifact, found, err := w.repository.VerifiedContract(t.Context(), 1, address)
			if err != nil || !found || artifact.VerificationOrigin != VerificationOriginBytecodeDB {
				t.Fatalf("publication=%t origin=%s err=%v", found, artifact.VerificationOrigin, err)
			}
			var count int
			if err = db.QueryRow(t.Context(), `SELECT count(*) FROM verification_jobs WHERE id=$1::uuid AND status='succeeded' AND attempt_count=1 AND compiler_digest IS NOT NULL`, jobID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("lost bound retry identity: %d %v", count, err)
			}
			if err = db.QueryRow(t.Context(), `SELECT count(*) FROM verification_jobs`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate job: %d %v", count, err)
			}
			tick()
			if found, err := local.ProcessOne(t.Context()); err != nil || found {
				t.Fatalf("published job retried: %t %v", found, err)
			}
		})
	}
}

func TestBytecodeDBRetryRequiresLeaseAndCanonicalTarget(t *testing.T) {
	db := bytecodeDBPostgres(t)
	w, compiler := bytecodeDBIntegrationWorker(t, db, func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{"sources": []BytecodeDBSource{bytecodeDBSourceFixture()}})
	})
	bytecodeDBSeedGenesis(t, db, "0x"+strings.Repeat("11", 20))
	for range 2 {
		bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
		if err := w.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	compiler.compileError = errors.New("compiler unavailable")
	local, err := NewWorker(w.repository, compiler, WorkerOptions{WorkerID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = local.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	bytecodeDBExec(t, db, `UPDATE verification_jobs SET updated_at=now()-interval '25 hours'`)
	var attempt dbgen.BytecodeDBAttemptParams
	var id string
	if err = db.QueryRow(t.Context(), `SELECT service_id,target_digest,candidate_digest,support_digest,job_id::text FROM bytecode_db_attempts`).Scan(&attempt.ServiceID, &attempt.TargetDigest, &attempt.CandidateDigest, &attempt.SupportDigest, &id); err != nil {
		t.Fatal(err)
	}
	job, _, err := w.repository.Job(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(db)
	claim := func() dbgen.BytecodeDbTarget {
		t.Helper()
		bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET next_attempt_at=now()`)
		task, err := q.BytecodeDBClaimTarget(t.Context(), new("retry-test"), w.service, w.chain)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	task := claim()
	bytecodeDBExec(t, db, `UPDATE bytecode_db_targets SET lease_expires_at=now()-interval '1 second'`)
	if _, err = w.retryUnavailable(t.Context(), task, *job.RequestV2.Target, attempt); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease: %v", err)
	}
	task = claim()
	bytecodeDBExec(t, db, `DELETE FROM canonical_blocks WHERE chain_id=1`)
	if state, err := w.retryUnavailable(t.Context(), task, *job.RequestV2.Target, attempt); err != nil || state != "waiting_facts" {
		t.Fatalf("reorg: %s %v", state, err)
	}
	var status string
	if err = db.QueryRow(t.Context(), `SELECT status FROM verification_jobs WHERE id=$1::uuid`, id).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("job changed after fence loss: %s %v", status, err)
	}
}
