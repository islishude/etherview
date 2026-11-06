package testdbguard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Concurrent releases all named operations through one start barrier and joins
// them before returning. Callers classify expected domain conflicts inside the
// operation. Each operation must honor ctx; the shared deadline is 30 seconds.
func Concurrent(ctx context.Context, operations map[string]func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ready := sync.WaitGroup{}
	done := sync.WaitGroup{}
	start := make(chan struct{})
	results := make(chan error, len(operations))
	for name, operation := range operations {
		ready.Add(1)
		done.Go(func() {
			ready.Done()
			select {
			case <-ctx.Done():
				results <- ctx.Err()
				return
			case <-start:
			}
			if err := operation(ctx); err != nil {
				results <- fmt.Errorf("%s: %w", name, err)
				cancel()
			}
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	var result error
	for err := range results {
		result = errors.Join(result, err)
	}
	return result
}

// WaitBlocked proves an exact wait edge, rather than counting unrelated locks.
func WaitBlocked(ctx context.Context, db dbgen.DBTX, waiter, blocker int32) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := dbgen.New(db).TestDeadlockSessions(ctx)
		if err != nil {
			return errors.New("read blocking relationship")
		}
		for _, row := range rows {
			if row.Pid == waiter && slices.Contains(row.Blockers, blocker) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for backend %d blocked by %d: %w", waiter, blocker, ctx.Err())
		case <-ticker.C:
		}
	}
}

// ClosePool bounds waiting for a leaked borrower. The caller must stop work
// first and retain ownership of database teardown if this returns an error.
func ClosePool(ctx context.Context, pool *pgxpool.Pool) error {
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("database pool still has borrowed connections")
	}
}
