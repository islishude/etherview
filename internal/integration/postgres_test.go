//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseEnvironment = "ETHERVIEW_TEST_DATABASE_URL"

func TestEmbeddedMigrationsAreIdempotentAndReportCompatibleState(t *testing.T) {
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	expected, err := store.Migrations()
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	before, err := store.ReadSchemaStatus(ctx, db)
	if err != nil {
		t.Fatalf("read empty schema status: %v", err)
	}
	if len(before.Applied) != 0 || len(before.Pending) != len(expected) {
		t.Fatalf("empty schema status = %+v, want %d pending migrations", before, len(expected))
	}
	if err := store.CheckSchema(ctx, db); !errors.Is(err, store.ErrSchemaIncompatible) {
		t.Fatalf("CheckSchema before migration error = %v, want ErrSchemaIncompatible", err)
	}

	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("first migration run: %v", err)
	}
	firstApplied := migrationLedger(t, ctx, db)
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	secondApplied := migrationLedger(t, ctx, db)
	if len(firstApplied) != len(expected) || len(secondApplied) != len(expected) {
		t.Fatalf("migration ledger lengths = %d then %d, want %d", len(firstApplied), len(secondApplied), len(expected))
	}
	for _, migration := range expected {
		first, ok := firstApplied[migration.Version]
		if !ok || first.Checksum != migration.Checksum {
			t.Fatalf("first ledger entry %q = %+v, present=%t", migration.Version, first, ok)
		}
		second := secondApplied[migration.Version]
		if second.Checksum != first.Checksum || !second.AppliedAt.Equal(first.AppliedAt) {
			t.Fatalf("migration %q changed on idempotent run: first=%+v second=%+v", migration.Version, first, second)
		}
	}

	after, err := store.ReadSchemaStatus(ctx, db)
	if err != nil {
		t.Fatalf("read migrated schema status: %v", err)
	}
	if len(after.Applied) != len(expected) || len(after.Pending) != 0 {
		t.Fatalf("migrated schema status = %+v, want all applied", after)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatalf("CheckSchema after migration: %v", err)
	}

	var currentSchema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		t.Fatalf("read current schema: %v", err)
	}
	if !strings.HasPrefix(currentSchema, "etherview_it_") {
		t.Fatalf("current schema = %q, want isolated integration schema", currentSchema)
	}
}

func TestEmbeddedMigrationsKeepNamedConstraintsSchemaLocal(t *testing.T) {
	first, second := newIsolatedPostgres(t), newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for index, db := range []*sql.DB{first, second} {
		if err := store.RunMigrations(ctx, db); err != nil {
			t.Fatalf("migrate isolated schema %d: %v", index+1, err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*)
			FROM pg_constraint
			WHERE conrelid = 'block_statistics'::regclass
			  AND conname IN (
			      'block_statistics_v2_nonnegative_check',
			      'block_statistics_v2_interval_check',
			      'block_statistics_v2_tps_check',
			      'block_statistics_v2_blob_check'
			  )
			  AND convalidated`).Scan(&count); err != nil || count != 4 {
			t.Fatalf("schema %d validated constraints=%d error=%v", index+1, count, err)
		}
		var schema string
		if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(ctx, `
				SELECT procedure.proname, setting
				FROM pg_proc AS procedure
				JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
				CROSS JOIN LATERAL unnest(procedure.proconfig) AS setting
				WHERE namespace.nspname = current_schema()
				  AND procedure.proname IN (
				      'reject_durable_stage_publication_mutation',
				      'require_enrichment_publication_protocol',
				      'enforce_enrichment_terminal_publication'
				  )`)
		if err != nil {
			t.Fatal(err)
		}
		pinned := make(map[string]string)
		for rows.Next() {
			var name, setting string
			if err := rows.Scan(&name, &setting); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			pinned[name] = setting
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		wantSearchPath := "search_path=" + schema + ", pg_catalog"
		if len(pinned) != 3 {
			t.Fatalf("schema %d pinned publication functions=%v", index+1, pinned)
		}
		for name, setting := range pinned {
			if setting != wantSearchPath {
				t.Errorf("schema %d function %s setting=%q want=%q", index+1, name, setting, wantSearchPath)
			}
		}
	}
}

func TestSQLCRuntimeBoundaryReadsChainIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	genesis := testHash(42)
	if err := store.BindChainIdentity(ctx, db, "1", genesis); err != nil {
		t.Fatalf("bind chain identity: %v", err)
	}
	identity, err := store.ReadChainIdentity(ctx, db, "1")
	if err != nil {
		t.Fatalf("read chain identity through sqlc: %v", err)
	}
	if identity.ChainID != "1" || !identity.GenesisHash.Equal(genesis) {
		t.Fatalf("chain identity = %+v, want chain 1 genesis %s", identity, genesis)
	}
}

func TestConcurrentRoleStartupBindsOneChainIdentityWithoutRetry(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	genesis := testHash(43)
	start := make(chan struct{})
	errorsByRole := make(chan error, 12)
	var roles sync.WaitGroup
	for range 12 {
		roles.Add(1)
		go func() {
			defer roles.Done()
			<-start
			errorsByRole <- store.BindChainIdentity(ctx, db, "1", genesis)
		}()
	}
	close(start)
	roles.Wait()
	close(errorsByRole)
	for err := range errorsByRole {
		if err != nil {
			t.Errorf("concurrent chain identity bind: %v", err)
		}
	}
	identity, err := store.ReadChainIdentity(ctx, db, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !identity.GenesisHash.Equal(genesis) {
		t.Fatalf("chain identity = %+v, want genesis %s", identity, genesis)
	}
	var beforeXID, afterXID string
	if err := db.QueryRowContext(ctx, `SELECT xmin::text FROM chains WHERE chain_id = 1`).Scan(&beforeXID); err != nil {
		t.Fatal(err)
	}
	if err := store.BindChainIdentity(ctx, db, "1", genesis); err != nil {
		t.Fatalf("repeat identical chain identity bind: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT xmin::text FROM chains WHERE chain_id = 1`).Scan(&afterXID); err != nil {
		t.Fatal(err)
	}
	if afterXID != beforeXID {
		t.Fatalf("identical chain identity bind rewrote row: xmin %s -> %s", beforeXID, afterXID)
	}
	if err := store.BindChainIdentity(ctx, db, "1", testHash(44)); err == nil ||
		!strings.Contains(err.Error(), "chain identity mismatch") {
		t.Fatalf("conflicting concurrent identity boundary error = %v", err)
	}
}

func TestConcurrentSyncStartupConfiguresOneCoreCoverageBoundary(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := store.BindChainIdentity(ctx, db, "1", testHash(45)); err != nil {
		t.Fatal(err)
	}
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsByReplica := make(chan error, 12)
	var replicas sync.WaitGroup
	for range 12 {
		replicas.Add(1)
		go func() {
			defer replicas.Done()
			<-start
			errorsByReplica <- repository.ConfigureIndex(ctx, "1", 0)
		}()
	}
	close(start)
	replicas.Wait()
	close(errorsByReplica)
	for err := range errorsByReplica {
		if err != nil {
			t.Errorf("concurrent core index configuration: %v", err)
		}
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || coverage.ConfiguredStart != 0 || len(coverage.Ranges) != 0 {
		t.Fatalf("concurrent core coverage = %+v exists=%t", coverage, exists)
	}
}

type ledgerEntry struct {
	Checksum  string
	AppliedAt time.Time
}

func migrationLedger(t *testing.T, ctx context.Context, db *sql.DB) map[string]ledgerEntry {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT version, checksum, applied_at
		FROM etherview_schema_migrations
		ORDER BY version`)
	if err != nil {
		t.Fatalf("query migration ledger: %v", err)
	}
	defer rows.Close()

	entries := make(map[string]ledgerEntry)
	for rows.Next() {
		var version string
		var entry ledgerEntry
		if err := rows.Scan(&version, &entry.Checksum, &entry.AppliedAt); err != nil {
			t.Fatalf("scan migration ledger: %v", err)
		}
		entries[version] = entry
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration ledger: %v", err)
	}
	return entries
}

func newMigratedPostgres(t *testing.T) *sql.DB {
	t.Helper()
	db := newIsolatedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run embedded migrations: %v", err)
	}
	if err := store.CheckSchema(ctx, db); err != nil {
		t.Fatalf("check migrated schema: %v", err)
	}
	return db
}

func newIsolatedPostgres(t *testing.T) *sql.DB {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(testDatabaseEnvironment))
	if rawURL == "" {
		t.Skipf("%s is not set", testDatabaseEnvironment)
	}

	adminConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnvironment, err)
	}
	adminConfig.RuntimeParams = cloneRuntimeParams(adminConfig.RuntimeParams)
	adminConfig.RuntimeParams["application_name"] = "etherview-integration-admin"
	adminDB := stdlib.OpenDB(*adminConfig)
	adminDB.SetMaxOpenConns(2)
	adminDB.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("connect to %s: %v", testDatabaseEnvironment, err)
	}
	schema := integrationSchemaName(t)
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteIdentifier(schema)); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create isolated schema %q: %v", schema, err)
	}

	testConfig := adminConfig.Copy()
	testConfig.RuntimeParams = cloneRuntimeParams(testConfig.RuntimeParams)
	testConfig.RuntimeParams["application_name"] = "etherview-integration-test"
	testConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(6)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteIdentifier(schema)+` CASCADE`)
		_ = adminDB.Close()
		t.Fatalf("connect with isolated search_path %q: %v", schema, err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close isolated PostgreSQL pool: %v", err)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(cleanupCtx, `DROP SCHEMA `+quoteIdentifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema %q: %v", schema, err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close PostgreSQL admin pool: %v", err)
		}
	})
	return db
}

func integrationSchemaName(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate integration schema suffix: %v", err)
	}
	return "etherview_it_" + hex.EncodeToString(random)
}

func cloneRuntimeParams(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func testHash(value uint64) ethrpc.Hash {
	hash, err := ethrpc.ParseHash(fmt.Sprintf("0x%064x", value))
	if err != nil {
		panic(err)
	}
	return hash
}

func testAddress(value uint64) ethrpc.Address {
	address, err := ethrpc.ParseAddress(fmt.Sprintf("0x%040x", value))
	if err != nil {
		panic(err)
	}
	return address
}

func testBundle(number uint64, blockHash, parentHash, transactionHash ethrpc.Hash, variant string) ethrpc.Bundle {
	blockNumber := ethrpc.QuantityFromUint64(number)
	transactionIndex := ethrpc.QuantityFromUint64(0)
	transactionType := ethrpc.QuantityFromUint64(2)
	status := ethrpc.QuantityFromUint64(1)
	gasUsed := ethrpc.QuantityFromUint64(21_000)
	zeroHash := testHash(0)
	from := testAddress(1)
	to := testAddress(2)
	logAddress := testAddress(3)
	logIndex := ethrpc.QuantityFromUint64(0)
	withdrawalAddress := testAddress(4)
	block := ethrpc.Block{
		Number:           &blockNumber,
		Hash:             &blockHash,
		ParentHash:       parentHash,
		Sha3Uncles:       zeroHash,
		TransactionsRoot: zeroHash,
		StateRoot:        zeroHash,
		ReceiptsRoot:     zeroHash,
		ExtraData:        ethrpc.DataFromBytes([]byte(variant)),
		GasLimit:         ethrpc.QuantityFromUint64(30_000_000),
		GasUsed:          gasUsed,
		Timestamp:        ethrpc.QuantityFromUint64(1_700_000_000 + number),
		Uncles:           []ethrpc.Hash{},
		Extra: map[string]json.RawMessage{
			"integrationVariant": json.RawMessage(fmt.Sprintf("%q", variant)),
		},
		Withdrawals: []ethrpc.Withdrawal{{
			Index:          ethrpc.QuantityFromUint64(number),
			ValidatorIndex: ethrpc.QuantityFromUint64(number + 100),
			Address:        withdrawalAddress,
			Amount:         ethrpc.QuantityFromUint64(32_000_000_000),
		}},
	}
	transaction := &ethrpc.Transaction{
		Hash:             transactionHash,
		Type:             &transactionType,
		BlockHash:        &blockHash,
		BlockNumber:      &blockNumber,
		TransactionIndex: &transactionIndex,
		From:             from,
		To:               &to,
		Nonce:            ethrpc.QuantityFromUint64(number),
		Gas:              ethrpc.QuantityFromUint64(21_000),
		Value:            ethrpc.QuantityFromUint64(1),
		Input:            ethrpc.DataFromBytes([]byte(variant)),
	}
	block.Transactions = []ethrpc.TransactionRef{{Hash: transactionHash, Transaction: transaction}}
	log := ethrpc.Log{
		LogIndex:         &logIndex,
		TransactionIndex: &transactionIndex,
		TransactionHash:  &transactionHash,
		BlockHash:        &blockHash,
		BlockNumber:      &blockNumber,
		Address:          logAddress,
		Data:             ethrpc.DataFromBytes([]byte(variant)),
		Topics:           []ethrpc.Hash{testHash(9_000)},
	}
	receipt := ethrpc.Receipt{
		TransactionHash:   transactionHash,
		TransactionIndex:  transactionIndex,
		BlockHash:         blockHash,
		BlockNumber:       blockNumber,
		CumulativeGasUsed: gasUsed,
		GasUsed:           &gasUsed,
		Logs:              []ethrpc.Log{log},
		LogsBloom:         ethrpc.Data("0x"),
		Status:            &status,
	}
	return ethrpc.Bundle{Block: block, Receipts: []ethrpc.Receipt{receipt}}
}

func mustBlockRef(t *testing.T, bundle ethrpc.Bundle) store.BlockRef {
	t.Helper()
	reference, err := store.RefFromBundle(bundle)
	if err != nil {
		t.Fatalf("derive block reference: %v", err)
	}
	return reference
}

func mustBytes(t *testing.T, value interface{ Bytes() ([]byte, error) }) []byte {
	t.Helper()
	encoded, err := value.Bytes()
	if err != nil {
		t.Fatalf("decode hex value: %v", err)
	}
	return encoded
}
