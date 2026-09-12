//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

// Raising an abort SQLSTATE after the marker write deterministically exercises
// the same rollback boundary as the CI deadlock, without relying on victim
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
				if err := db.QueryRowContext(ctx, `SELECT last_value FROM terminal_abort_attempt`).Scan(&attempts); err != nil || attempts != 2 {
					t.Fatalf("transaction attempts=%d err=%v", attempts, err)
				}
			})
		}
	}
}
