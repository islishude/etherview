// Package testpgx provides native PostgreSQL test fixtures.
package testpgx

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool opens a lazy pool with the test's exact session configuration. Callers
// own closing it so isolated schemas are dropped only after clients release.
func Pool(t testing.TB, config *pgx.ConnConfig, maximum int32) *pgxpool.Pool {
	t.Helper()
	options, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	options.ConnConfig = config.Copy()
	options.MaxConns = maximum
	options.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
