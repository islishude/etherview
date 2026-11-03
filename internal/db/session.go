package dbaccess

import (
	"context"
	"errors"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithSessionLock reserves one physical session from claim through publication.
// The callback must use the supplied database for all writes and finish all
// database work before returning. It may call external services between short
// transactions, but must not retain a snapshot across those calls. Losing the
// session makes subsequent writes fail; there is no pool fallback or reconnect.
// Contenders skip the cycle and try again on their next poll.
func WithSessionLock(ctx context.Context, database Database, key string, work func(Database) error) (bool, error) {
	pool, ok := database.(interface {
		Acquire(context.Context) (*pgxpool.Conn, error)
	})
	if !ok || work == nil || key == "" {
		return false, errors.New("runtime session lock requires a pool, key and callback")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	locked, err := dbgen.New(conn).TryRuntimeSessionLock(ctx, key)
	if err != nil {
		Discard(ctx, conn)
		return false, err
	}
	if !locked {
		conn.Release()
		return false, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		unlocked, err := dbgen.New(conn).ReleaseRuntimeSessionLock(cleanup, key)
		if err != nil || !unlocked {
			Discard(cleanup, conn)
		} else {
			conn.Release()
		}
	}()
	return true, work(conn)
}
