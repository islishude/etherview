//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/islishude/etherview/internal/maintenance"
)

func TestSearchCatalogFunctionsRemainBoundToTheirMigrationSchema(t *testing.T) {
	first, second := newMigratedPostgres(t), newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	firstSchema, secondSchema := currentTestSchema(t, ctx, first), currentTestSchema(t, ctx, second)
	for _, db := range []*pgxpool.Pool{first, second} {
		execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	}
	execFixture(t, ctx, second, `INSERT INTO operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'second schema')`, testAddress(940).String())
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation before cross-schema write=%d", got)
	}
	if _, err := second.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'first schema')`, quoteIdentifier(firstSchema)), testAddress(941).String()); err != nil {
		t.Fatal(err)
	}
	if got := catalogGeneration(t, ctx, second, firstSchema); got != 1 {
		t.Fatalf("first generation after explicit trigger=%d", got)
	}
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation changed after first-schema trigger=%d", got)
	}
	var minimum int64
	if err := second.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s.prune_search_catalog(1, 1000)`, quoteIdentifier(firstSchema),
	)).Scan(&minimum); err != nil {
		t.Fatal(err)
	}
	if got := catalogGeneration(t, ctx, second, firstSchema); got != 2 {
		t.Fatalf("first generation after explicit prune=%d minimum=%d", got, minimum)
	}
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation changed after first-schema prune=%d", got)
	}
}

func TestPostgresCatalogMaintenanceUsesTryLockAndBoundedAdapterBatch(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1), (2)`)
	now := time.Now().UTC().Truncate(time.Second)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	)
	SELECT 1, 'price', 'expired-' || value::text, 'unavailable', 'temporary',
		$1::timestamptz - interval '2 hours', $1::timestamptz - interval '1 hour'
	FROM generate_series(1, 4) AS value`, now)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	) VALUES (1, 'price', 'fresh', 'unavailable', 'temporary',
		$1::timestamptz - interval '1 minute', $1::timestamptz + interval '1 hour')`, now)
	execFixture(t, ctx, db, `INSERT INTO external_adapter_observations (
		chain_id, capability, observation_key, state, code, observed_at, expires_at
	) VALUES (2, 'price', 'other-chain-expired', 'unavailable', 'temporary',
		$1::timestamptz - interval '4 hours', $1::timestamptz - interval '3 hours')`, now)
	cleaner, err := maintenance.NewPostgresCatalogCleaner(db)
	if err != nil {
		t.Fatal(err)
	}

	lock, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background()) //nolint:errcheck
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_xact_lock(
		hashtext('etherview:search-catalog-maintenance'), hashtext('1'))`); err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || result.Ran || result.Deleted != 0 {
		t.Fatalf("locked sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 4, 1)
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	result, err = cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || !result.Ran || result.MinGeneration != 0 || result.Deleted != 2 {
		t.Fatalf("first sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 2, 1)
	result, err = cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || !result.Ran || result.Deleted != 2 {
		t.Fatalf("second sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 0, 1)
	assertAdapterObservationCounts(t, ctx, db, 2, now, 1, 0)
}

func currentTestSchema(t *testing.T, ctx context.Context, db *pgxpool.Pool) string {
	t.Helper()
	var schema string
	if err := db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func catalogGeneration(t *testing.T, ctx context.Context, db *pgxpool.Pool, schema string) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRow(ctx, fmt.Sprintf(
		`SELECT COALESCE((SELECT generation FROM %s.search_catalog_generations WHERE chain_id = 1), 0)`,
		quoteIdentifier(schema),
	)).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func assertAdapterObservationCounts(
	t *testing.T,
	ctx context.Context,
	db *pgxpool.Pool,
	chainID uint64,
	now time.Time,
	expired, fresh int,
) {
	t.Helper()
	var gotExpired, gotFresh int
	if err := db.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE expires_at <= $1),
		count(*) FILTER (WHERE expires_at > $1)
		FROM external_adapter_observations
		WHERE chain_id = $2`, now, chainID).Scan(&gotExpired, &gotFresh); err != nil {
		t.Fatal(err)
	}
	if gotExpired != expired || gotFresh != fresh {
		t.Fatalf("adapter observations expired=%d fresh=%d want=%d/%d", gotExpired, gotFresh, expired, fresh)
	}
}
