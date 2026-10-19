//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/islishude/etherview/internal/store"
)

func TestBaselineRejectsPreviousLedgerWithoutChangingIt(t *testing.T) {
	for _, baseline := range []struct {
		name   string
		ledger string
	}{
		{
			name: "development_chain",
			ledger: `INSERT INTO etherview_schema_migrations(version, checksum)
VALUES ('0001_core', repeat('a', 64)), ('0073_vyper_application_lock', repeat('b', 64))`,
		},
		{
			name: "single_file_baseline",
			// Exact 0001_core.sql checksum at e37769990e0fada0601b94ab747cf04636c9ef29,
			// before the domain split. Never rewrite an existing ledger to accept it.
			ledger: `INSERT INTO etherview_schema_migrations(version, checksum)
VALUES ('0001_core', '3cfc47dddac5acf53012a8c8ed43615ace5bb5d449f5194e3f7000cf94b4592a')`,
		},
	} {
		t.Run(baseline.name, func(t *testing.T) {
			assertBaselineRejectsPreviousLedger(t, baseline.ledger)
		})
	}
}

func assertBaselineRejectsPreviousLedger(t *testing.T, ledger string) {
	t.Helper()
	db := newIsolatedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `CREATE TABLE etherview_schema_migrations (
version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	// A previous development database has the same first version but different
	// bytes. It must fail before any baseline DDL or ledger replacement.
	execFixture(t, ctx, db, ledger)
	before := migrationLedger(t, ctx, db)
	if err := store.RunMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("old baseline migration error = %v", err)
	}
	after := migrationLedger(t, ctx, db)
	if len(after) != len(before) {
		t.Fatalf("ledger changed: before=%v after=%v", before, after)
	}
	for version, entry := range before {
		if after[version] != entry {
			t.Fatalf("old ledger entry changed: %s", version)
		}
	}
	if err := store.CheckSchema(ctx, db); !errors.Is(err, store.ErrSchemaIncompatible) {
		t.Fatalf("old schema compatibility error = %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_class
WHERE relnamespace = current_schema()::regnamespace AND relname = 'chains'`, 0)
}

func TestBaselineFailureRollsBackObjectsAndLedger(t *testing.T) {
	for _, existingLedger := range []bool{false, true} {
		name := "runner_created_ledger"
		if existingLedger {
			name = "existing_empty_ledger"
		}
		t.Run(name, func(t *testing.T) {
			assertBaselineFailureRollsBack(t, existingLedger)
		})
	}
}

func assertBaselineFailureRollsBack(t *testing.T, existingLedger bool) {
	t.Helper()
	db := newIsolatedPostgres(t)
	ctx := t.Context()
	if existingLedger {
		execFixture(t, ctx, db, `CREATE TABLE etherview_schema_migrations (
version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	}
	// A collision in 0016 must roll back objects and ledger entries from all
	// earlier files without touching the existing table or its row.
	execFixture(t, ctx, db, `CREATE TABLE watch_notifications (marker TEXT)`)
	execFixture(t, ctx, db, `INSERT INTO watch_notifications VALUES ('keep')`)
	if err := store.RunMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "apply migration 0016_watchlist") {
		t.Fatalf("expected late migration collision, got %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM watch_notifications WHERE marker = 'keep'`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_class
WHERE relnamespace = current_schema()::regnamespace
AND relname IN ('chains', 'durable_jobs_id_seq', 'verified_contracts', 'mempool_snapshots')`, 0)
	if existingLedger {
		assertRowCount(t, ctx, db, `SELECT count(*) FROM etherview_schema_migrations`, 0)
	} else {
		assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_class
WHERE relnamespace = current_schema()::regnamespace AND relname = 'etherview_schema_migrations'`, 0)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_proc
WHERE pronamespace = current_schema()::regnamespace`, 0)
	execFixture(t, ctx, db, `DROP TABLE watch_notifications`)
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineConcurrentRunnersApplyOnce(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx := t.Context()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- store.RunMigrations(ctx, db) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	expected, err := store.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM etherview_schema_migrations`, len(expected))
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineRejectsChangedLaterMigrationChecksum(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `UPDATE etherview_schema_migrations
SET checksum = repeat('a', 64) WHERE version = '0017_schema_bindings'`)
	before := migrationLedger(t, ctx, db)
	if err := store.RunMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "migration 0017_schema_bindings checksum changed") {
		t.Fatalf("changed later migration error = %v", err)
	}
	after := migrationLedger(t, ctx, db)
	if len(after) != len(before) {
		t.Fatalf("ledger changed: before=%v after=%v", before, after)
	}
	for version, entry := range before {
		if after[version] != entry {
			t.Fatalf("ledger entry changed: %s", version)
		}
	}
	if err := store.CheckSchema(ctx, db); !errors.Is(err, store.ErrSchemaIncompatible) {
		t.Fatalf("changed checksum compatibility error = %v", err)
	}
}

func TestBaselineRejectsUnknownLedgerVersion(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `INSERT INTO etherview_schema_migrations(version, checksum)
VALUES ('0073_vyper_application_lock', repeat('b', 64))`)
	if err := store.CheckSchema(ctx, db); !errors.Is(err, store.ErrSchemaIncompatible) {
		t.Fatalf("unknown migration compatibility error = %v", err)
	}
}

func TestBaselineReplayDefaultsAndGenerationGuards(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	var jobID string
	if err := db.QueryRow(ctx, `INSERT INTO durable_jobs
(chain_id, kind, stage, stage_version, idempotency_key, payload)
VALUES (1, 'enrichment', 'token', 2, 'baseline-default', '{}'::jsonb) RETURNING id::text`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	assertReplayGeneration(t, ctx, db, jobID, replayGenerationState{
		Status: "queued", Requested: 1, Claimed: 0, Completed: 0,
	})
	execFixture(t, ctx, db, `INSERT INTO transactional_outbox
(chain_id, topic, message_key, payload) VALUES (1, 'core.block.canonical', 'baseline-default', '{}'::jsonb)`)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transactional_outbox WHERE generation = 1`, 1)
	if _, err := db.Exec(ctx, `UPDATE durable_jobs SET requested_generation = 0 WHERE id = $1`, jobID); err == nil {
		t.Fatal("accepted a zero requested generation")
	}
	if _, err := db.Exec(ctx, `UPDATE durable_jobs SET status = 'leased',
leased_by = 'old-worker', lease_token = 'old-token', claimed_generation = 1, leased_generation = 1,
lease_expires_at = now() + interval '1 minute' WHERE id = $1`, jobID); err == nil || !strings.Contains(err.Error(), "requires publication protocol 2") {
		t.Fatalf("lease without publication protocol error = %v", err)
	}
}

func TestBaselinePreservesExceptionalConstraintCatalog(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	// CREATE TABLE silently validates inline NOT VALID checks and merges
	// a UNIQUE declaration equivalent to the PRIMARY KEY. Keep both objects.
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_constraint
WHERE conrelid = 'search_catalog_documents'::regclass
AND conname = 'search_catalog_documents_name_source_check'
AND contype = 'c' AND NOT convalidated`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_constraint original
JOIN pg_constraint duplicate ON duplicate.conrelid = original.conrelid
AND duplicate.conkey = original.conkey AND duplicate.oid <> original.oid
WHERE original.conrelid = 'verified_contracts'::regclass
AND original.conname = 'verified_contracts_selector_identity_unique'
AND original.contype = 'u' AND duplicate.contype = 'p'`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_constraint
WHERE conrelid = 'billing_topup_intents'::regclass
AND conname = 'billing_topup_intents_active_payment_fk'
AND confrelid = 'billing_payments'::regclass AND convalidated`, 1)
}

func TestBaselineSerialAndIdentityDefaults(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	for want := int64(1); want <= 2; want++ {
		var got int64
		if err := db.QueryRow(ctx, `INSERT INTO external_adapter_observations
(chain_id, capability, observation_key, state, code, observed_at, expires_at)
VALUES (1, 'price', 'baseline', 'unavailable', 'unavailable', now(), now() + interval '1 minute')
RETURNING id`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("sequence default returned %d, want %d", got, want)
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_class
WHERE oid = pg_get_serial_sequence('external_adapter_observations', 'id')::regclass
AND relname = 'external_adapter_observations_id_seq'`, 1)
	// ALWAYS identity must keep rejecting explicitly supplied IDs.
	_, err := db.Exec(ctx, `INSERT INTO durable_jobs
(id, chain_id, kind, stage, stage_version, idempotency_key, payload)
VALUES (123, 1, 'enrichment', 'token', 2, 'explicit-id', '{}'::jsonb)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "428C9" {
		t.Fatalf("explicit identity insert error = %v, want generated_always", err)
	}
}
