//go:build integration

package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabasePoolsApplyWriterAndReaderSessionBoundaries(t *testing.T) {
	t.Parallel()
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	cfg := config.DatabaseConfig{
		URL: databaseURL, MaxConnections: 2, MinConnections: 1,
		ConnectTimeout: 5 * time.Second, StatementTimeout: 5 * time.Second,
	}
	writer, err := openDatabase(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	reader, err := openReadDatabase(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })

	assertDatabaseSessionSetting(t, writer, "application_name", "etherview-writer")
	assertDatabaseSessionSetting(t, writer, "default_transaction_read_only", "off")
	assertDatabaseSessionSetting(t, reader, "application_name", "etherview-reader")
	assertDatabaseSessionSetting(t, reader, "default_transaction_read_only", "on")

	_, err = reader.Exec(
		context.Background(),
		`UPDATE etherview_schema_migrations SET version = version WHERE false`,
	)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "25006" {
		t.Fatalf("reader write error code=%q, want read_only_sql_transaction", postgresErrorCode(postgresError))
	}
}

func TestReadDatabaseStartupValidation(t *testing.T) {
	t.Parallel()
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	cfg := config.DatabaseConfig{
		URL: databaseURL, MaxConnections: 2, MinConnections: 1,
		ConnectTimeout: 5 * time.Second, StatementTimeout: 5 * time.Second,
	}
	writer, err := openDatabase(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	reader, err := openReadDatabase(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })

	if err := checkReadDatabaseSchema(context.Background(), reader); err != nil {
		t.Fatalf("matching reader schema: %v", err)
	}
	chainID := "18446744073709551614"
	genesis := mustIntegrationHash(t, "0x00000000000000000000000000000000000000000000000000000000000000a1")
	if err := store.BindChainIdentity(context.Background(), writer, chainID, genesis); err != nil {
		t.Fatalf("bind writer identity: %v", err)
	}
	writerIdentity := store.ChainIdentity{ChainID: chainID, GenesisHash: genesis}
	if err := validateReadDatabaseIdentity(context.Background(), reader, writerIdentity); err != nil {
		t.Fatalf("matching reader identity: %v", err)
	}
	writerIdentity.GenesisHash = mustIntegrationHash(
		t,
		"0x00000000000000000000000000000000000000000000000000000000000000a2",
	)
	if err := validateReadDatabaseIdentity(context.Background(), reader, writerIdentity); err == nil {
		t.Fatal("mismatched reader genesis was accepted")
	}

	emptySchemaConfig := cfg
	emptySchemaConfig.URL = databaseURLWithSearchPath(t, databaseURL, "pg_catalog")
	incompatibleReader, err := openReadDatabase(context.Background(), emptySchemaConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { incompatibleReader.Close() })
	if err := checkReadDatabaseSchema(context.Background(), incompatibleReader); !errors.Is(err, store.ErrSchemaIncompatible) {
		t.Fatalf("incompatible reader schema error=%v, want ErrSchemaIncompatible", err)
	}
}

func assertDatabaseSessionSetting(t *testing.T, database *pgxpool.Pool, name, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(
		context.Background(),
		`SELECT current_setting($1)`,
		name,
	).Scan(&got); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("%s=%q want %q", name, got, want)
	}
}

func postgresErrorCode(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func mustIntegrationHash(t *testing.T, value string) common.Hash {
	t.Helper()
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func databaseURLWithSearchPath(t *testing.T, rawURL, searchPath string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", searchPath)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func TestNativeDatabaseCloseDoesNotExtendExpiredShutdownBudget(t *testing.T) {
	t.Parallel()
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	database, err := openDatabase(t.Context(), config.DatabaseConfig{URL: databaseURL, MaxConnections: 1, ConnectTimeout: time.Second, StatementTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	borrowed, err := database.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer borrowed.Release()
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = closeDatabasePools(deadline, database)
	if !errors.Is(err, errDatabaseCloseTimeout) || time.Since(started) > time.Second {
		t.Fatalf("native pool extended shutdown: error=%v elapsed=%s", err, time.Since(started))
	}
	probe, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if conn, err := database.Acquire(probe); err == nil {
		conn.Release()
		t.Fatal("closing pool accepted a new borrower")
	}
}
