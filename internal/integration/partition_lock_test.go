//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPostgresPartitionExistingRangeDoesNotBlockABIReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	insertDefaultPartitionFixtures(t, ctx, db)
	if err := store.EnsureBlockPartitions(ctx, db, testPartitionLower, testPartitionUpper, 0); err != nil {
		t.Fatal(err)
	}
	replay, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, replay)
	clearPartitionABIReplay(t, ctx, replay)

	// A fresh repository has no local partition cache, as on a second sync
	// replica. It must finish while the replay transaction still owns its locks.
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ensureCtx, ensureCancel := context.WithTimeout(ctx, 2*time.Second)
	defer ensureCancel()
	if err := repository.EnsureBlockPartitions(ensureCtx, testPartitionLower, testPartitionUpper); err != nil {
		t.Fatalf("existing range blocked by ABI replay: %v", err)
	}
	if err := replay.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertPartitionRangeCount(t, ctx, db, "transaction_effective_execution_identities", 0)
	assertPartitionRangeCount(t, ctx, db, "transaction_inclusions", 1)
}

// Pause after the first conflicting lock attempt has rolled back, with a real
// PostgreSQL transaction still open. This lets the test inspect the released
// lock set and finish the reverse-order writer before provisioning retries.
type observedPartitionDatabase struct {
	dbaccess.Database
	released chan uint32
	resume   chan struct{}
	once     sync.Once
}

func (db *observedPartitionDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := db.Database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &observedPartitionTx{Tx: tx, observer: db}, nil
}

type observedPartitionTx struct {
	pgx.Tx
	observer *observedPartitionDatabase
}

func (tx *observedPartitionTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := tx.Tx.Exec(ctx, sql, args...)
	if err == nil && sql == "ROLLBACK TO SAVEPOINT partition_table_locks" {
		tx.observer.once.Do(func() {
			tx.observer.released <- tx.Conn().PgConn().PID()
			select {
			case <-tx.observer.resume:
			case <-ctx.Done():
			}
		})
	}
	return tag, err
}

func TestPostgresPartitionProvisioningReleasesPartialLocksForABIReplay(t *testing.T) {
	for _, cancelProvisioning := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelProvisioning), func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			insertDefaultPartitionFixtures(t, ctx, db)
			replay, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer dbaccess.Rollback(ctx, replay)
			// The ABI cleanup can acquire its main DELETE target before the
			// CTE's abi_decodings target, opposite to the partition DDL order.
			if _, err := replay.Exec(ctx, "LOCK TABLE transaction_effective_execution_identities IN ROW EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			observed := &observedPartitionDatabase{
				Database: db, released: make(chan uint32, 1), resume: make(chan struct{}),
			}
			var resumeOnce sync.Once
			resume := func() { resumeOnce.Do(func() { close(observed.resume) }) }
			defer resume()
			ensureCtx, ensureCancel := context.WithCancel(ctx)
			defer ensureCancel()
			result := make(chan error, 1)
			go func() {
				result <- store.EnsureBlockPartitions(ensureCtx, observed, testPartitionLower, testPartitionUpper, 0)
			}()
			var pid uint32
			select {
			case pid = <-observed.released:
			case err := <-result:
				t.Fatalf("provisioning ended before lock contention: %v", err)
			case <-ctx.Done():
				t.Fatal("provisioning did not release its partial lock set")
			}
			var locks int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_locks
				WHERE pid = $1 AND locktype = 'relation' AND mode = 'AccessExclusiveLock'`, pid).Scan(&locks); err != nil {
				t.Fatal(err)
			}
			if locks != 0 {
				t.Fatalf("provisioning retained %d partial table locks", locks)
			}
			// A short timeout prevents a broken implementation from creating a
			// deliberate database deadlock in the shared zero-deadlock suite.
			if _, err := replay.Exec(ctx, "SET LOCAL lock_timeout = '500ms'"); err != nil {
				t.Fatal(err)
			}
			clearPartitionABIReplay(t, ctx, replay)
			if cancelProvisioning {
				ensureCancel()
			}
			if err := replay.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			resume()
			select {
			case err := <-result:
				if cancelProvisioning {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled provisioning = %v", err)
					}
				} else if err != nil {
					t.Fatalf("provisioning after ABI replay: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("provisioning did not finish")
			}
			if cancelProvisioning {
				assertPartitionRangeCount(t, ctx, db, "transaction_inclusions_default", 1)
				// Cancellation must release lifecycle locks and leave no partial
				// DDL or cache entry that could prevent a later successful call.
				if err := store.EnsureBlockPartitions(ctx, db, testPartitionLower, testPartitionUpper, 0); err != nil {
					t.Fatal(err)
				}
			}
			for parent, child := range partitionTableNames {
				assertAttachedPartition(t, ctx, db, parent, child)
				assertPartitionRangeCount(t, ctx, db, parent+"_default", 0)
				want := 1
				if parent == "transaction_effective_execution_identities" {
					want = 0
				}
				assertPartitionRangeCount(t, ctx, db, child, want)
			}
		})
	}
}

func clearPartitionABIReplay(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	var chainID, number pgtype.Numeric
	if err := chainID.Scan("1"); err != nil {
		t.Fatal(err)
	}
	if err := number.Scan(fmt.Sprint(testPartitionLower)); err != nil {
		t.Fatal(err)
	}
	if err := dbgen.New(tx).EnrichClearABIReplayOutputs(ctx, chainID, number, mustBytes(t, testHash(60_000))); err != nil {
		t.Fatalf("clear ABI replay outputs: %v", err)
	}
}
