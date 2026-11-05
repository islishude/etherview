//go:build runtimee2e

package runtimee2e

import (
	"context"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/testdbguard"
)

// Hold a real job lock until the exact replay transaction is waiting, then
// produce the next block while the deployed workers and replay are active.
// The fixed source key makes both layouts publish the same generation.
func (h *harness) mineWithConcurrentReplay(ctx context.Context, timestamp uint64) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var id int64
	if err := h.db.QueryRow(ctx, `SELECT id FROM durable_jobs WHERE chain_id=1 AND stage='stats' AND payload->>'block_hash'=$1`, h.fixture.blockOneHash).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	holder, err := h.db.Begin(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, holder)
	if err := dbgen.New(holder).EnrichLegacyLockPublicationJob(ctx, id); err != nil {
		h.t.Fatal(err)
	}
	replay, err := h.db.Begin(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, replay)
	queue, err := enrich.NewPostgresJobQueue(h.db)
	if err != nil {
		h.t.Fatal(err)
	}
	hash, err := enrich.ParseWord(h.fixture.blockOneHash)
	if err != nil {
		h.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := queue.EnqueueTx(ctx, replay, enrich.EnqueueRequest{Stage: enrich.StatsStage, ChainID: "1", BlockHash: hash, BlockNumber: 1, Replay: enrich.ReplaySource{Kind: "runtime-concurrency", Key: "block-one"}})
		if err == nil {
			err = replay.Commit(ctx)
		}
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			if pendingErr := <-done; pendingErr != nil {
				h.t.Logf("replay cleanup: %v", pendingErr)
			}
		}
	}()
	if err := testdbguard.WaitBlocked(ctx, h.db, int32(replay.Conn().PgConn().PID()), int32(holder.Conn().PgConn().PID())); err != nil {
		h.t.Fatal(err)
	}
	h.mine(ctx, timestamp)
	if err := holder.Commit(ctx); err != nil {
		h.t.Fatal(err)
	}
	err = <-done
	joined = true
	if err != nil {
		h.t.Fatal(err)
	}
	h.writeJSONArtifact("concurrent-replay.json", map[string]any{"job_id": id, "block_hash": h.fixture.blockOneHash, "blocking_edge_verified": true})
}
