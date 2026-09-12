package enrich

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const queueTransactionAttempts = 5

// Retry only errors that prove PostgreSQL aborted the transaction. The caller
// must roll back before returning and start a fresh transaction on every call.
// Connection errors and ambiguous commits must never repeat a transition.
func retryAbortedQueueTransaction(ctx context.Context, transition func() error) error {
	for attempt := range queueTransactionAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := transition()
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) ||
			(postgresError.Code != "40P01" && postgresError.Code != "40001") ||
			attempt == queueTransactionAttempts-1 {
			return err
		}
		// Jitter keeps concurrent replicas from retrying the same lock cycle in
		// step. Neither a transaction nor a heartbeat guard survives this wait.
		delay := (25 * time.Millisecond) << attempt
		timer := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable queue transaction retry")
}
