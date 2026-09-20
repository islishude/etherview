//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeTransactionCancellationReturnsSinglePoolConnection(t *testing.T) {
	database := newMigratedPostgres(t)
	config := database.Config()
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = dbaccess.WithTransaction(ctx, pool, func(*dbgen.Queries) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transaction: %v", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("cancelled transaction retained its pool connection")
	}
	probe, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := pool.Ping(probe); err != nil {
		t.Fatalf("single connection not reusable: %v", err)
	}
}

func TestNativeDiscardRemovesLockedPhysicalSessionAfterCancellation(t *testing.T) {
	database := newMigratedPostgres(t)
	config := database.Config()
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var originalPID int32
	if err := conn.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&originalPID); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	const key int64 = -9182026123
	if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	dbaccess.Discard(cancelled, conn)
	probe, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	next, err := pool.Acquire(probe)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	var nextPID int32
	var acquired bool
	if err := next.QueryRow(probe, `SELECT pg_backend_pid(), pg_try_advisory_lock($1)`, key).Scan(&nextPID, &acquired); err != nil {
		t.Fatal(err)
	}
	if originalPID == nextPID || !acquired {
		t.Fatalf("uncertain lock session was recycled: old=%d new=%d acquired=%t", originalPID, nextPID, acquired)
	}
	if _, err := next.Exec(probe, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
}
