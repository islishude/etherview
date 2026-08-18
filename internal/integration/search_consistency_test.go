//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/chainbundle"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestSearchCatalogFunctionsRemainBoundToTheirMigrationSchema(t *testing.T) {
	first, second := newMigratedPostgres(t), newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	firstSchema, secondSchema := currentTestSchema(t, ctx, first), currentTestSchema(t, ctx, second)
	for _, db := range []*sql.DB{first, second} {
		execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	}
	execFixture(t, ctx, second, `INSERT INTO operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'second schema')`, testAddress(940).String())
	if got := catalogGeneration(t, ctx, second, secondSchema); got != 1 {
		t.Fatalf("second generation before cross-schema write=%d", got)
	}
	if _, err := second.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.operator_labels
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
	if err := second.QueryRowContext(ctx, fmt.Sprintf(
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

func TestENSObservationsAreImmutableAndDriveSearchCatalog(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, core, testBundle(0, testHash(99), testHash(0), testHash(999), "ens-search"))
	repository, err := ensresolver.NewRepository(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	first, err := repository.CreateGeneration(ctx, ensresolver.GenerationCandidate{
		PolicyKey: "sha256:" + fmt.Sprintf("%064x", 1), CoinType: big.NewInt(60),
		OfficialEndpoint: "mainnet", OfficialBlock: ensresolver.BlockRef{Number: 100, Hash: testHash(100)},
		CreatedAt: now, FreshUntil: now.Add(time.Minute), RetainUntil: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	address := testAddress(100)
	observation := ensresolver.Observation{
		GenerationID: first.ID, Source: ensresolver.SourceOfficial, Direction: "forward",
		LookupKey: "alice.eth", Outcome: ensresolver.OutcomeResolved, Name: "alice.eth",
		Address: address, Resolver: testAddress(101), ObservedAt: now,
	}
	stored, err := repository.RecordObservation(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	idempotent, err := repository.RecordObservation(ctx, observation)
	if err != nil || idempotent.ID != stored.ID {
		t.Fatalf("idempotent observation=%+v error=%v", idempotent, err)
	}
	conflict := observation
	conflict.Address = testAddress(102)
	if _, err := repository.RecordObservation(ctx, conflict); !errors.Is(err, ensresolver.ErrIdentityConflict) {
		t.Fatalf("conflicting observation error=%v", err)
	}
	var source string
	var observationID int64
	var open bool
	if err := db.QueryRowContext(ctx, `SELECT name_source, name_observation_id,
		valid_to_generation IS NULL FROM search_catalog_documents
		WHERE chain_id = 1 AND source_kind = 'name'`).Scan(&source, &observationID, &open); err != nil {
		t.Fatal(err)
	}
	if source != "ens" || observationID != stored.ID || !open {
		t.Fatalf("search document source=%q observation=%d open=%t", source, observationID, open)
	}
	execFixture(t, ctx, db, `INSERT INTO operator_labels
		(chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'alice.eth')`, testAddress(103).String())
	resolved := ensresolver.ForwardResolution{
		ObservationID: stored.ID, Outcome: ensresolver.OutcomeResolved,
		Name: "alice.eth", Address: address, Source: ensresolver.SourceOfficial,
	}
	reader, err := query.NewPostgresReader(db, query.Options{
		ChainID: 1, NameResolver: integrationNameResolver(func(context.Context, string) (ensresolver.ForwardResolution, error) {
			return resolved, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPage, cursor, err := reader.Search(ctx, "Alice.Eth", "", 1)
	if err != nil || len(firstPage) != 1 || cursor == "" {
		t.Fatalf("first ENS page=%+v cursor=%q error=%v", firstPage, cursor, err)
	}

	secondAt := now.Add(2 * time.Minute)
	second, err := repository.CreateGeneration(ctx, ensresolver.GenerationCandidate{
		PolicyKey: first.PolicyKey, CoinType: big.NewInt(60), OfficialEndpoint: "mainnet",
		OfficialBlock: ensresolver.BlockRef{Number: 101, Hash: testHash(101)},
		CreatedAt:     secondAt, FreshUntil: secondAt.Add(time.Minute), RetainUntil: secondAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	noRecord, err := repository.RecordObservation(ctx, ensresolver.Observation{
		GenerationID: second.ID, Source: ensresolver.SourceOfficial, Direction: "forward",
		LookupKey: "alice.eth", Outcome: ensresolver.OutcomeNoRecord, Name: "alice.eth", ObservedAt: secondAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	frozenPage, _, err := reader.Search(ctx, "Alice.Eth", cursor, 1)
	if err != nil || len(frozenPage) != 1 || frozenPage[0].NameSource == nil {
		t.Fatalf("frozen ENS page=%+v error=%v", frozenPage, err)
	}
	resolved = ensresolver.ForwardResolution{
		ObservationID: noRecord.ID, Outcome: ensresolver.OutcomeNoRecord,
		Name: "alice.eth", Source: ensresolver.SourceOfficial,
	}
	current, _, err := reader.Search(ctx, "Alice.Eth", "", 20)
	if err != nil || len(current) != 1 || current[0].NameSource != nil {
		t.Fatalf("current no-record search=%+v error=%v", current, err)
	}
	var openDocuments int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM search_catalog_documents
		WHERE chain_id = 1 AND source_kind = 'name' AND logical_identity = 'alice.eth'
		  AND valid_to_generation IS NULL`).Scan(&openDocuments); err != nil {
		t.Fatal(err)
	}
	if openDocuments != 0 {
		t.Fatalf("no-record left %d open search documents", openDocuments)
	}
	if generation := currentCatalogGeneration(t, ctx, db); generation != 3 {
		t.Fatalf("catalog generation=%d, want 3", generation)
	}
}

type integrationNameResolver func(context.Context, string) (ensresolver.ForwardResolution, error)

func (resolve integrationNameResolver) ResolveForward(
	ctx context.Context,
	name string,
) (ensresolver.ForwardResolution, error) {
	return resolve(ctx, name)
}

func TestCustomENSGenerationAndSearchInvalidateOnReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(200), testHash(0), testHash(2_000), "ens-genesis")
	oldOne := testBundle(1, testHash(201), testHash(200), testHash(2_001), "ens-old")
	newOne := testBundle(1, testHash(202), testHash(200), testHash(2_002), "ens-new")
	for _, bundle := range []chainbundle.Bundle{genesis, oldOne} {
		commitCanonical(t, ctx, core, bundle)
	}
	repository, err := ensresolver.NewRepository(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	oldRef := mustBlockRef(t, oldOne)
	generation, err := repository.CreateGeneration(ctx, ensresolver.GenerationCandidate{
		PolicyKey: "sha256:" + fmt.Sprintf("%064x", 2), CoinType: big.NewInt(60),
		OfficialEndpoint: "mainnet", OfficialBlock: ensresolver.BlockRef{Number: 100, Hash: testHash(300)},
		CustomEndpoint: "custom", CustomCoinType: big.NewInt(60),
		CustomBlock: &ensresolver.BlockRef{Number: oldRef.Number, Hash: oldRef.Hash},
		CreatedAt:   now, FreshUntil: now.Add(time.Minute), RetainUntil: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordObservation(ctx, ensresolver.Observation{
		GenerationID: generation.ID, Source: ensresolver.SourceCustom, Direction: "forward",
		LookupKey: "custom.eth", Outcome: ensresolver.OutcomeResolved, Name: "custom.eth",
		Address: testAddress(200), Resolver: testAddress(201), ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := reader.Search(ctx, "custom", "", 20)
	if err != nil || len(results) != 1 || results[0].NameSource == nil || string(*results[0].NameSource) != "custom_ens" {
		t.Fatalf("custom search before reorg=%+v error=%v", results, err)
	}
	ancestor, replacement := mustBlockRef(t, genesis), mustBlockRef(t, newOne)
	if err := core.ApplyReorg(ctx, "1", store.Reorg{
		Ancestor: ancestor, Detached: []store.BlockRef{oldRef}, Attached: []chainbundle.Bundle{newOne},
		Checkpoint: store.NewCoreCheckpoint(replacement), Reason: "ENS custom source reorg",
	}); err != nil {
		t.Fatal(err)
	}
	results, _, err = reader.Search(ctx, "custom", "", 20)
	if err != nil || len(results) != 0 {
		t.Fatalf("custom search after reorg=%+v error=%v", results, err)
	}
	if _, err := repository.Generation(ctx, generation.ID, generation.PolicyKey, now); err == nil {
		t.Fatal("detached custom ENS generation remained readable")
	}
}

func TestENSAddressNameSnapshotExpires(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	repository, err := ensresolver.NewRepository(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	generation, err := repository.CreateGeneration(ctx, ensresolver.GenerationCandidate{
		PolicyKey: "sha256:" + fmt.Sprintf("%064x", 3), CoinType: big.NewInt(60),
		OfficialEndpoint: "mainnet", OfficialBlock: ensresolver.BlockRef{Number: 100, Hash: testHash(400)},
		CreatedAt: now, FreshUntil: now.Add(time.Minute), RetainUntil: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.CreateSnapshot(ctx, generation.ID, now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repository.SnapshotGeneration(ctx, snapshot, generation.PolicyKey, now.Add(time.Minute)); err != nil || got.ID != generation.ID {
		t.Fatalf("snapshot generation=%+v error=%v", got, err)
	}
	if _, err := repository.SnapshotGeneration(ctx, snapshot, generation.PolicyKey, now.Add(3*time.Minute)); !errors.Is(err, ensresolver.ErrSnapshotInvalid) {
		t.Fatalf("expired snapshot error=%v", err)
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

	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback() //nolint:errcheck
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_xact_lock(
		hashtext('etherview:search-catalog-maintenance'), hashtext('1'))`); err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(ctx, 1, 1000, 2, now)
	if err != nil || result.Ran || result.Deleted != 0 {
		t.Fatalf("locked sweep result=%+v error=%v", result, err)
	}
	assertAdapterObservationCounts(t, ctx, db, 1, now, 4, 1)
	if err := lock.Commit(); err != nil {
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

func currentTestSchema(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func catalogGeneration(t *testing.T, ctx context.Context, db *sql.DB, schema string) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COALESCE((SELECT generation FROM %s.search_catalog_generations WHERE chain_id = 1), 0)`,
		quoteIdentifier(schema),
	)).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func currentCatalogGeneration(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT generation
		FROM search_catalog_generations WHERE chain_id = 1), 0)`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func assertAdapterObservationCounts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	chainID uint64,
	now time.Time,
	expired, fresh int,
) {
	t.Helper()
	var gotExpired, gotFresh int
	if err := db.QueryRowContext(ctx, `SELECT
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
