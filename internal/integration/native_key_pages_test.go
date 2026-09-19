//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/auth"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/jackc/pgx/v5"
)

type observedReadDatabase struct {
	dbaccess.Database
	beforeSecondPage func()
	options          pgx.TxOptions
	pages            int
}

func (database *observedReadDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	database.options = options
	tx, err := database.Database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &observedReadTransaction{Tx: tx, database: database}, nil
}

type observedReadTransaction struct {
	pgx.Tx
	database *observedReadDatabase
}

func (tx *observedReadTransaction) Query(ctx context.Context, statement string, args ...any) (pgx.Rows, error) {
	if strings.HasPrefix(statement, "-- name: AuthLegacyListAPIKeys") {
		tx.database.pages++
		if tx.database.pages == 2 {
			tx.database.beforeSecondPage()
		}
	}
	return tx.Tx.Query(ctx, statement, args...)
}

func TestNativeAPIKeyPagesKeepOneSnapshotAndCompleteResults(t *testing.T) {
	database := newMigratedPostgres(t)
	_, err := database.Exec(t.Context(), `INSERT INTO api_keys(prefix,digest,name,rate_per_second,burst,created_at,scopes)
 SELECT lpad(number::text,10,'0'),decode(repeat('ab',32),'hex'),'page fixture',1,1,'2026-01-01'::timestamptz,ARRAY['api:read']::text[]
 FROM generate_series(1,513) AS number`)
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedReadDatabase{Database: database}
	observed.beforeSecondPage = func() {
		if _, err := database.Exec(t.Context(), `DELETE FROM api_keys WHERE prefix='0000000513'`); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := auth.NewPostgresRepository(observed)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := repository.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if observed.pages != 2 || observed.options.IsoLevel != pgx.RepeatableRead || observed.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("pagination lost snapshot: pages=%d options=%+v", observed.pages, observed.options)
	}
	if len(keys) != 513 {
		t.Fatalf("pagination truncated snapshot: got %d keys", len(keys))
	}
	for index, key := range keys {
		if key.Prefix != fmt.Sprintf("%010d", index+1) {
			t.Fatalf("key %d missing or duplicated: %s", index, key.Prefix)
		}
	}
	next, err := repository.List(t.Context())
	if err != nil || len(next) != 512 {
		t.Fatalf("next snapshot did not observe committed deletion: count=%d err=%v", len(next), err)
	}
}
