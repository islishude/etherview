//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIncrementalAnalyticsIdlePlansAndPublicationCost(t *testing.T) {
	for _, size := range []int{16, 128} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx := t.Context()
			repository, _ := store.NewPostgresRepository(db)
			if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
				t.Fatal(err)
			}
			base := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
			blocks := make([]chainbundle.Bundle, size)
			parent := common.Hash{}
			for index := range blocks {
				blocks[index] = analyticsBundle(t, uint64(index), parent, uint64(base.Unix())+uint64(index)*12, 1, int64(index+1), fmt.Sprint("analytics-scale-", index))
				parent = blocks[index].Block.Hash()
			}
			started := time.Now()
			if _, err := repository.CommitCanonicalSegment(ctx, "1", blocks); err != nil {
				t.Fatal(err)
			}
			t.Logf("canonical batch blocks=%d commit=%s", size, time.Since(started))
			published := newDerivedPublicationHarness(t, db, newDerivedProcessors(t, db))
			durations := make([]time.Duration, 0, size)
			for _, block := range blocks {
				started = time.Now()
				published.process(t, ctx, block)
				durations = append(durations, time.Since(started))
			}
			slices.Sort(durations)
			t.Logf("publication samples=%d p95=%s", size, durations[(size*95+99)/100-1])
			now := analyticsRollupTestTime(t, ctx, db, base.Add(24*time.Hour))
			worker, _ := analytics.NewRollupWorker(db, analytics.RollupWorkerOptions{ChainID: 1, Now: func() time.Time { return now }})
			if result, err := worker.RunOnce(ctx); err != nil || !result.Published {
				t.Fatalf("rollup=%+v err=%v", result, err)
			}
			var total, complete, jobs, results, journals int
			if err := db.QueryRow(ctx, `SELECT total_blocks,completed_blocks,(SELECT count(*) FROM durable_jobs),(SELECT count(*) FROM block_stage_results),(SELECT count(*) FROM block_journals) FROM chart_source_totals WHERE chain_id=1`).Scan(&total, &complete, &jobs, &results, &journals); err != nil {
				t.Fatal(err)
			}
			if total != size || complete != size || jobs < size || results < size || journals < size {
				t.Fatalf("dataset=%d/%d jobs=%d results=%d journals=%d", total, complete, jobs, results, journals)
			}
			t.Logf("dataset canonical=%d jobs=%d results=%d journals=%d", total, jobs, results, journals)
			source, err := os.ReadFile("../db/queries/analytics_write.sql")
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"AnalyticsWriteRefreshBackfill", "AnalyticsWriteRollupMetrics"} {
				_, statement, found := strings.Cut(string(source), "-- name: "+name+" :")
				if !found {
					t.Fatal(name)
				}
				_, statement, _ = strings.Cut(statement, "\n")
				statement, _, _ = strings.Cut(statement, "-- name:")
				statement = strings.ReplaceAll(statement, "sqlc.arg('chain_id')", "1")
				statement = strings.ReplaceAll(statement, "sqlc.arg('now')", "clock_timestamp()")
				var plan json.RawMessage
				if err = db.QueryRow(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+statement).Scan(&plan); err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range []string{"canonical_blocks", "durable_jobs", "block_journals", "block_stage_results"} {
					if strings.Contains(string(plan), `"Relation Name": "`+forbidden+`"`) {
						t.Fatalf("idle query scanned %s: %s", forbidden, plan)
					}
				}
				t.Logf("%s EXPLAIN=%s", name, plan)
			}
			started = time.Now()
			idle, err := worker.RunOnce(ctx)
			if err != nil || idle.Published {
				t.Fatalf("idle=%+v err=%v", idle, err)
			}
			t.Logf("idle worker=%s", time.Since(started))
		})
	}
}
