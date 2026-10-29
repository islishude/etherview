//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pgtype "github.com/jackc/pgx/v5/pgtype"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

func TestPostgresDurableJobLifecycleAndTerminalOnlyRequeue(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle(0, testHash(700), testHash(0), testHash(701), "queue-lifecycle")
	commitCanonical(t, ctx, repository, bundle)
	reference := mustBlockRef(t, bundle)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatalf("create PostgreSQL enrichment queue: %v", err)
	}
	word, err := enrich.ParseWord(testHash(700).String())
	if err != nil {
		t.Fatalf("parse job block hash: %v", err)
	}
	stage := enrich.StageID{Name: "queue-lifecycle", Version: 1}
	request := enrich.EnqueueRequest{
		Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Payload: []byte(`{"source":"integration"}`), Priority: 7, MaxAttempts: 3,
	}
	first, err := queue.Enqueue(ctx, request)
	if err != nil || !first.Created {
		t.Fatalf("first enqueue = %+v, err=%v", first, err)
	}
	duplicate, err := queue.Enqueue(ctx, request)
	if err != nil || duplicate.Created || duplicate.Job.ID != first.Job.ID {
		t.Fatalf("duplicate enqueue = %+v, err=%v, first=%+v", duplicate, err, first)
	}
	if err := queue.Requeue(ctx, first.Job); !errors.Is(err, enrich.ErrJobBusy) {
		t.Fatalf("queued-job requeue error = %v, want ErrJobBusy", err)
	}
	assertJobState(t, ctx, db, first.Job.ID, jobState{Status: "queued", Attempts: 0})

	lease, found, err := queue.Claim(ctx, "integration-worker", []enrich.StageID{stage}, time.Minute)
	if err != nil || !found || lease.Job.ID != first.Job.ID || lease.Job.Attempt != 1 || lease.Token == "" {
		t.Fatalf("first claim = %+v, found=%t, err=%v", lease, found, err)
	}
	if err := queue.Requeue(ctx, lease.Job); !errors.Is(err, enrich.ErrJobBusy) {
		t.Fatalf("leased-job requeue error = %v, want ErrJobBusy", err)
	}
	leased := readJobState(t, ctx, db, first.Job.ID)
	if leased.Status != "leased" || leased.Attempts != 1 || !leased.LeasedBy.Valid || leased.LeasedBy.String != "integration-worker" || !leased.LeaseToken.Valid || leased.LeaseToken.String != lease.Token {
		t.Fatalf("leased job state = %+v", leased)
	}
	if err := queue.Renew(ctx, lease, time.Minute); err != nil {
		t.Fatalf("renew owned lease: %v", err)
	}
	if err := queue.Finish(ctx, lease, enrich.StageResult{
		State: enrich.ResultComplete, Details: map[string]string{"events": "2"},
	}); err != nil {
		t.Fatalf("finish owned lease: %v", err)
	}
	succeeded := readJobState(t, ctx, db, first.Job.ID)
	if succeeded.Status != "succeeded" || succeeded.Attempts != 1 || succeeded.LeasedBy.Valid || succeeded.LeaseToken.Valid || !succeeded.Result.Valid || succeeded.Result.String == "" {
		t.Fatalf("succeeded job state = %+v", succeeded)
	}
	if err := queue.Requeue(ctx, lease.Job); err != nil {
		t.Fatalf("requeue succeeded job: %v", err)
	}
	assertJobState(t, ctx, db, first.Job.ID, jobState{Status: "queued", Attempts: 0})

	secondLease, found, err := queue.Claim(ctx, "integration-worker-2", []enrich.StageID{stage}, time.Minute)
	if err != nil || !found || secondLease.Job.ID != first.Job.ID || secondLease.Job.Attempt != 1 || secondLease.Token == lease.Token {
		t.Fatalf("second claim = %+v, found=%t, err=%v", secondLease, found, err)
	}
	if err := queue.Finish(ctx, secondLease, enrich.StageResult{
		State: enrich.ResultFailed, Error: "fixture failure",
	}); err != nil {
		t.Fatalf("fail second lease: %v", err)
	}
	failed := readJobState(t, ctx, db, first.Job.ID)
	if failed.Status != "failed" || failed.Attempts != 1 || !failed.LastError.Valid || failed.LastError.String != "fixture failure" {
		t.Fatalf("failed job state = %+v", failed)
	}
	if err := queue.Requeue(ctx, secondLease.Job); err != nil {
		t.Fatalf("requeue failed job: %v", err)
	}
	assertJobState(t, ctx, db, first.Job.ID, jobState{Status: "queued", Attempts: 0})
}

// Raising an abort SQLSTATE after the marker write deterministically exercises
// the terminal publication rollback boundary, without relying on victim
// selection or timing. The sequence survives rollback; marker/job writes do not.
func TestEnrichmentTerminalTransactionRetriesPostgresAbort(t *testing.T) {
	for _, code := range []string{"40P01", "40001"} {
		for _, operation := range []string{"retry", "finish"} {
			t.Run(code+"/"+operation, func(t *testing.T) {
				db := newMigratedPostgres(t)
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				repository, _ := store.NewPostgresRepository(db)
				block := testBundle(0, testHash(119_000), testHash(0), testHash(119_001), "queue-transaction-abort")
				commitCanonical(t, ctx, repository, block)
				execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = now()`)
				reference := mustBlockRef(t, block)
				word, _ := enrich.ParseWord(reference.Hash.String())
				queue, _ := enrich.NewPostgresJobQueue(db)
				job, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
					Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number, MaxAttempts: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				lease, found, err := queue.Claim(ctx, "abort-worker", []enrich.StageID{enrich.TraceStage}, time.Minute)
				if err != nil || !found {
					t.Fatalf("claim found=%t err=%v", found, err)
				}
				execFixture(t, ctx, db, `CREATE SEQUENCE terminal_abort_attempt`)
				execFixture(t, ctx, db, `CREATE FUNCTION abort_first_terminal_marker() RETURNS trigger LANGUAGE plpgsql AS $$
     BEGIN
      IF nextval('terminal_abort_attempt') = 1 THEN
       RAISE EXCEPTION 'injected transaction abort' USING ERRCODE = '`+code+`';
      END IF;
      RETURN NEW;
     END $$`)
				execFixture(t, ctx, db, `CREATE TRIGGER terminal_abort AFTER INSERT OR UPDATE ON block_stage_results
     FOR EACH ROW EXECUTE FUNCTION abort_first_terminal_marker()`)
				const reason = "fixture terminal outcome"
				if operation == "retry" {
					err = queue.Retry(ctx, lease, enrich.Retry{Reason: reason})
				} else {
					err = queue.Finish(ctx, lease, enrich.StageResult{State: enrich.ResultFailed, Error: reason})
				}
				if err != nil {
					t.Fatal(err)
				}
				assertPublishedTerminalNoJournal(t, ctx, db, job.Job.ID, 1, enrich.ResultFailed, reason)
				assertEnrichmentJobTerminal(t, ctx, db, job.Job.ID, "failed", 1)
				var attempts int
				if err := db.QueryRow(ctx, `SELECT last_value FROM terminal_abort_attempt`).Scan(&attempts); err != nil || attempts != 2 {
					t.Fatalf("transaction attempts=%d err=%v", attempts, err)
				}
			})
		}
	}
}

type jobState struct {
	Status     string
	Attempts   int
	LeasedBy   pgtype.Text
	LeaseToken pgtype.Text
	Result     pgtype.Text
	LastError  pgtype.Text
}

func readJobState(t *testing.T, ctx context.Context, db *pgxpool.Pool, id string) jobState {
	t.Helper()
	var state jobState
	if err := db.QueryRow(ctx, `
		SELECT status, attempts, leased_by, lease_token, result::text, last_error
		FROM durable_jobs
		WHERE id = $1`, id).Scan(
		&state.Status, &state.Attempts, &state.LeasedBy, &state.LeaseToken, &state.Result, &state.LastError,
	); err != nil {
		t.Fatalf("read durable job %s: %v", id, err)
	}
	return state
}

func assertJobState(t *testing.T, ctx context.Context, db *pgxpool.Pool, id string, want jobState) {
	t.Helper()
	got := readJobState(t, ctx, db, id)
	if got.Status != want.Status || got.Attempts != want.Attempts || got.LeasedBy.Valid || got.LeaseToken.Valid || got.Result.Valid || got.LastError.Valid {
		t.Fatalf("durable job %s state = %+v, want status=%q attempts=%d with cleared lease/result/error", id, got, want.Status, want.Attempts)
	}
}
