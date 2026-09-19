// Package dbaccess owns native pgx query and transaction execution.
package dbaccess

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Database is the native query and transaction contract shared by repositories.
// pgxpool.Pool implements it directly; no driver bridge or alternate pool exists.
type Database interface {
	dbgen.DBTX
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

const cleanupTimeout = 5 * time.Second

// Rollback releases a transaction even after its request context is cancelled.
// pgx closes the underlying connection when rollback cannot be completed.
func Rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// Discard removes an outcome-uncertain session from its pool before closing it.
func Discard(ctx context.Context, conn *pgxpool.Conn) {
	physical := conn.Hijack()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = physical.Close(cleanup)
}

func WithQueries(ctx context.Context, database Database, callback func(*dbgen.Queries) error) error {
	if database == nil || callback == nil {
		return errors.New("sqlc database and callback are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return callback(dbgen.New(database))
}

func WithTransaction(ctx context.Context, database Database, callback func(*dbgen.Queries) error) error {
	return WithTransactionOptions(ctx, database, pgx.TxOptions{}, callback)
}

func WithTransactionOptions(ctx context.Context, database Database, options pgx.TxOptions, callback func(*dbgen.Queries) error) error {
	if database == nil || callback == nil {
		return errors.New("sqlc transaction database and callback are required")
	}
	tx, err := database.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin sqlc transaction: %w", err)
	}
	defer Rollback(ctx, tx)
	if err := callback(dbgen.New(database).WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit sqlc transaction: %w", err)
	}
	return nil
}
