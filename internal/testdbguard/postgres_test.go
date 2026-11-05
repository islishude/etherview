//go:build integration

package testdbguard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deliberate deadlocks live in their own Compose database, never in the managed
// integration database whose zero-deadlock gate surrounds this test process.
func TestDatabaseDeadlockGuardPostgres(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(root, testcompose.UniqueProjectName("etherview-deadlock-selftest"), "e2e/integration/compose.yaml")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := project.Down(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := project.Up(t.Context(), "postgres"); err != nil {
		t.Fatal(err)
	}
	binding, err := project.Port(t.Context(), "postgres", 5432)
	if err != nil {
		t.Fatal(err)
	}
	url := "postgres://etherview:etherview-integration@" + binding + "/etherview?sslmode=disable"
	t.Run("borrowed pool connection", func(t *testing.T) {
		pool, err := pgxpool.New(t.Context(), url)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			pool.Close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		err = ClosePool(ctx, pool)
		conn.Release()
		if err == nil {
			t.Fatal("leaked borrower escaped shutdown bound")
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cleanupCancel()
		if err := ClosePool(cleanupCtx, pool); err != nil {
			t.Fatal(err)
		}
	})

	for _, mode := range []string{"deadlock", "wait", "injected", "reset"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			guard, err := Start(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			finished := false
			t.Cleanup(func() {
				if !finished {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 25*time.Second)
					defer cleanupCancel()
					if _, cleanupErr := guard.Finish(cleanupCtx); cleanupErr != nil {
						t.Log(cleanupErr)
					}
				}
			})
			a, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			closeFixtureConnection(t, a)
			b, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			closeFixtureConnection(t, b)
			if _, err := a.Exec(ctx, "CREATE TABLE IF NOT EXISTS deadlock_fixture (id integer PRIMARY KEY, value integer); TRUNCATE deadlock_fixture; INSERT INTO deadlock_fixture VALUES (1,0),(2,0)"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "injected":
				_, err = a.Exec(ctx, `DO $$ BEGIN RAISE EXCEPTION 'injected transaction abort' USING ERRCODE='40P01'; END $$`)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "40P01" {
					t.Fatalf("injection: %v", err)
				}
			case "reset":
				if _, err = a.Exec(ctx, "SELECT pg_stat_reset()"); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err = a.Exec(ctx, "BEGIN; UPDATE deadlock_fixture SET value=value+1 WHERE id=1"); err != nil {
					t.Fatal(err)
				}
				if _, err = b.Exec(ctx, "BEGIN; UPDATE deadlock_fixture SET value=value+1 WHERE id=2"); err != nil {
					t.Fatal(err)
				}
				waiting := make(chan error, 1)
				go func() { _, err := a.Exec(ctx, "UPDATE deadlock_fixture SET value=value+1 WHERE id=2"); waiting <- err }()
				probe, err := pgx.Connect(ctx, url)
				if err != nil {
					t.Fatal(err)
				}
				closeFixtureConnection(t, probe)
				for {
					var blocked bool
					if err := probe.QueryRow(ctx, "SELECT $1::integer = ANY(pg_blocking_pids($2))", int32(b.PgConn().PID()), int32(a.PgConn().PID())).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(10 * time.Millisecond):
					}
				}
				if err := probe.Close(ctx); err != nil {
					t.Fatal(err)
				}
				if mode == "deadlock" {
					_, _ = b.Exec(ctx, "UPDATE deadlock_fixture SET value=value+1 WHERE id=1")
				}
				// Release whichever transaction survived; do not assume victim selection.
				_, _ = b.Exec(ctx, "ROLLBACK")
				if waitErr := <-waiting; mode == "wait" && waitErr != nil {
					t.Fatal(waitErr)
				}
				_, _ = a.Exec(ctx, "ROLLBACK")
			}
			if err := a.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err := b.Close(ctx); err != nil {
				t.Fatal(err)
			}
			finished = true
			report, err := guard.Finish(ctx)
			wantFailure := mode == "deadlock" || mode == "reset"
			if (err != nil) != wantFailure {
				t.Fatalf("mode %s: report=%+v error=%v", mode, report, err)
			}
			if mode == "deadlock" && report.Final.Deadlocks <= report.Baseline.Deadlocks {
				t.Fatal("missed swallowed deadlock")
			}
		})
	}
}

func closeFixtureConnection(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
}
