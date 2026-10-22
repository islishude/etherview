//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

func TestStatsQueueWaitsForSparseCanonicalParent(t *testing.T) {
	for _, start := range []uint64{0, 7} {
		t.Run(fmt.Sprint(start), func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			repository, _ := store.NewPostgresRepository(db)
			if err := repository.ConfigureIndex(ctx, "1", start); err != nil {
				t.Fatal(err)
			}
			parent, err := testfixture.New(testfixture.Options{Number: start, Timestamp: 100})
			if err != nil {
				t.Fatal(err)
			}
			child, err := testfixture.New(testfixture.Options{Number: start + 1, ParentHash: parent.Block.Hash(), Timestamp: 112})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{child}); err != nil {
				t.Fatal(err)
			}
			queue, _ := enrich.NewPostgresJobQueue(db)
			word, _ := enrich.ParseWord(child.Block.Hash().Hex())
			job, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
				Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: start + 1, MaxAttempts: 1, Priority: 100,
			})
			if err != nil {
				t.Fatal(err)
			}
			processor, _ := enrich.NewPostgresStatsProcessor(db)
			worker, _ := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ID: "sparse-stats", LeaseDuration: time.Minute})
			for range 4 {
				if found, err := worker.ProcessOne(ctx); err != nil || found {
					t.Fatalf("missing parent must wait: found=%t err=%v", found, err)
				}
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1 AND status = 'queued' AND attempts = 0`, 1, job.Job.ID)
			// A higher-priority blocked stats job must not starve independent work.
			if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: start + 1}); err != nil {
				t.Fatal(err)
			}
			lease, found, err := queue.Claim(ctx, "independent", []enrich.StageID{enrich.StatsStage, enrich.TokenStage}, time.Minute)
			if err != nil || !found || lease.Job.Stage != enrich.TokenStage {
				t.Fatalf("independent claim=%+v found=%t err=%v", lease, found, err)
			}
			if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{parent}); err != nil {
				t.Fatal(err)
			}
			// Retaining the exact parent fact alone is insufficient: it must be canonical.
			execFixture(t, ctx, db, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = $1`, start)
			if found, err := worker.ProcessOne(ctx); err != nil || found {
				t.Fatalf("orphan parent must wait: found=%t err=%v", found, err)
			}
			execFixture(t, ctx, db, `INSERT INTO canonical_blocks (chain_id, number, block_hash) VALUES (1, $1, $2)`, start, parent.Block.Hash().Bytes())
			if found, err := worker.ProcessOne(ctx); err != nil || !found {
				t.Fatalf("backfilled parent must release stats: found=%t err=%v", found, err)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1 AND status = 'succeeded' AND attempts = 1`, 1, job.Job.ID)
			assertRowCount(t, ctx, db, `SELECT count(*) FROM block_statistics WHERE chain_id = 1 AND block_hash = $1 AND block_interval_seconds = 12`, 1, child.Block.Hash().Bytes())
			// The configured start requires no indexed predecessor, including nonzero starts.
			word, _ = enrich.ParseWord(parent.Block.Hash().Hex())
			if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: start}); err != nil {
				t.Fatal(err)
			}
			if found, err := worker.ProcessOne(ctx); err != nil || !found {
				t.Fatalf("configured start must run: found=%t err=%v", found, err)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM block_statistics WHERE chain_id = 1 AND block_hash = $1 AND block_interval_seconds IS NULL`, 1, parent.Block.Hash().Bytes())
		})
	}
}

func TestStatsQueueMissingParentPreservesExpiredLeaseAndOrphanCleanup(t *testing.T) {
	for _, maxAttempts := range []uint32{1, 2} {
		t.Run(fmt.Sprint(maxAttempts), func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			repository, _ := store.NewPostgresRepository(db)
			parent := testBundle(0, testHash(125000), testHash(0), testHash(125001), "stats-parent")
			child := testBundle(1, testHash(125002), parent.Block.Hash(), testHash(125003), "stats-child")
			commitCanonical(t, ctx, repository, parent)
			commitCanonical(t, ctx, repository, child)
			configureAtomicStatsStart(t, ctx, db)
			queue, _ := enrich.NewPostgresJobQueue(db)
			word, _ := enrich.ParseWord(child.Block.Hash().Hex())
			job, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.StatsStage, ChainID: "1", BlockHash: word, BlockNumber: 1, MaxAttempts: maxAttempts})
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := queue.Claim(ctx, "expired", []enrich.StageID{enrich.StatsStage}, time.Minute); err != nil || !found {
				t.Fatalf("initial claim found=%t err=%v", found, err)
			}
			execFixture(t, ctx, db, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`)
			execFixture(t, ctx, db, `UPDATE durable_jobs SET lease_expires_at = clock_timestamp() - INTERVAL '1 second' WHERE id = $1`, job.Job.ID)
			if _, found, err := queue.Claim(ctx, "waiting", []enrich.StageID{enrich.StatsStage}, time.Minute); err != nil || found {
				t.Fatalf("expired lease must wait for dependency: found=%t err=%v", found, err)
			}
			assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1 AND status = 'leased' AND attempts = 1`, 1, job.Job.ID)
			// Once the child is also orphaned, it no longer waits for the parent.
			execFixture(t, ctx, db, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 1`)
			lease, found, err := queue.Claim(ctx, "orphan", []enrich.StageID{enrich.StatsStage}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if maxAttempts == 1 {
				if found {
					t.Fatal("exhausted orphan must terminate without a new lease")
				}
				assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1 AND status = 'failed'`, 1, job.Job.ID)
			} else {
				if !found || lease.Job.Attempt != 2 {
					t.Fatalf("orphan claim=%+v found=%t", lease, found)
				}
				processor, _ := enrich.NewPostgresStatsProcessor(db)
				if result, err := processor.ProcessLease(ctx, lease, queue); err != nil || result.State != enrich.ResultComplete {
					t.Fatalf("orphan cleanup=%+v err=%v", result, err)
				}
			}
		})
	}
}
