// Package dbaccess bridges the repository's database/sql pgx pool to the
// generated sqlc query package. Queries execute only while the underlying
// pgx stdlib connection is pinned by database/sql.
package dbaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/stdlib"
)

// WithQueries pins one pgx stdlib connection for the callback and exposes the
// generated sqlc Queries bound to that exact connection.
func WithQueries(ctx context.Context, database *sql.DB, callback func(*dbgen.Queries) error) error {
	if database == nil {
		return errors.New("sqlc query database is nil")
	}
	if callback == nil {
		return errors.New("sqlc query callback is nil")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlc connection: %w", err)
	}
	defer func() { _ = connection.Close() }()
	return connection.Raw(func(driverConnection any) error {
		pgxConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("sqlc requires pgx stdlib, got %T", driverConnection)
		}
		if err := callback(dbgen.New(pgxConnection.Conn())); err != nil {
			return fmt.Errorf("execute sqlc query: %w", err)
		}
		return nil
	})
}

// WithTransaction pins one pgx stdlib connection and commits generated sqlc
// calls as one transaction. The callback must not retain the Queries value.
func WithTransaction(ctx context.Context, database *sql.DB, callback func(*dbgen.Queries) error) error {
	if database == nil {
		return errors.New("sqlc transaction database is nil")
	}
	if callback == nil {
		return errors.New("sqlc transaction callback is nil")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlc transaction connection: %w", err)
	}
	defer func() { _ = connection.Close() }()
	return connection.Raw(func(driverConnection any) error {
		pgxConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("sqlc transaction requires pgx stdlib, got %T", driverConnection)
		}
		transaction, err := pgxConnection.Conn().Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin sqlc transaction: %w", err)
		}
		defer func() { _ = transaction.Rollback(ctx) }()
		if err := callback(dbgen.New(transaction)); err != nil {
			return fmt.Errorf("execute sqlc transaction: %w", err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("commit sqlc transaction: %w", err)
		}
		return nil
	})
}
