package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/islishude/etherview/internal/components"
)

var errDatabaseCloseTimeout = errors.New("PostgreSQL pool shutdown exceeded the remaining shutdown budget")

type databaseCloser interface{ Close() }

// Native pools wait for borrowed connections. Start every close together and
// bound that wait by the existing service shutdown deadline. A timed-out close
// continues rejecting acquisitions while the caller exits the failed process.
func closeDatabasePools(ctx context.Context, pools ...databaseCloser) error {
	if len(pools) == 0 {
		return nil
	}
	var workers sync.WaitGroup
	for _, pool := range pools {
		workers.Go(pool.Close)
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(errDatabaseCloseTimeout, ctx.Err())
	}
}

func closeRuntimeDatabases(ctx context.Context, lifecycle *components.Lifecycle, timeout time.Duration, pools []databaseCloser) error {
	deadline, ok := lifecycle.ShutdownDeadline()
	if !ok {
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		deadline = time.Now().Add(timeout)
	}
	cleanup, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	return closeDatabasePools(cleanup, pools...)
}
