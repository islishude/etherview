//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestNativeTransferHeartbeatDuringPublication(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block, err := newIntegrationBundle(integrationBundleOptions{Number: 0, Amsterdam: true, ExtraData: []byte("native-heartbeat")})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Block.Hash().Hex())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.NativeTransferStage, ChainID: "1", BlockHash: word}); err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "native-heartbeat", []enrich.StageID{enrich.NativeTransferStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim found=%t err=%v", found, err)
	}

	renewed := false
	publicationDB := &nativePublicationHookDB{Database: db, afterResult: func() error {
		// The real result INSERT has completed and holds the coverage row lock.
		// Renewal uses another connection and must finish before publication can
		// reach its final guarded CAS, just as when the heartbeat owns the guard.
		renewCtx, renewCancel := context.WithTimeout(ctx, 3*time.Second)
		defer renewCancel()
		if err := queue.Renew(renewCtx, lease, 2*time.Minute); err != nil {
			return err
		}
		renewed = true
		return nil
	}}
	publicationQueue, err := enrich.NewPostgresJobQueue(publicationDB)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresNativeTransferProcessor(publicationDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessLease(ctx, lease, publicationQueue); err != nil {
		t.Fatalf("publish with concurrent renewal: %v", err)
	}
	if !renewed {
		t.Fatal("publication did not exercise concurrent renewal")
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM published_block_stage_results WHERE durable_job_id=$1`, 1, lease.Job.ID)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_transfer_coverage WHERE chain_id=1 AND covered @> 0::numeric`, 1)
}

// These wrappers only schedule renewal after a real PostgreSQL result write;
// all SQL, transactions, locks and publication transitions remain production code.
type nativePublicationHookDB struct {
	dbaccess.Database
	afterResult func() error
}

func (db *nativePublicationHookDB) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := db.Database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &nativePublicationHookTx{Tx: tx, afterResult: db.afterResult}, nil
}

type nativePublicationHookTx struct {
	pgx.Tx
	afterResult func() error
}

func (tx *nativePublicationHookTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := tx.Tx.QueryRow(ctx, sql, args...)
	if strings.HasPrefix(sql, "-- name: EnrichLegacyInsertPublishedStageResult ") {
		return nativePublicationHookRow{Row: row, afterResult: tx.afterResult}
	}
	return row
}

type nativePublicationHookRow struct {
	pgx.Row
	afterResult func() error
}

func (row nativePublicationHookRow) Scan(dest ...any) error {
	if err := row.Row.Scan(dest...); err != nil {
		return err
	}
	return row.afterResult()
}
