//go:build integration

package facilitator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math/big"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func journalDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "facilitator_" + strings.ReplaceAll(uuid.New().String(), "-", "_")
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = store.RunMigrations(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return pool, parsed.String()
}
func TestFacilitatorJournalCrashBoundaries(t *testing.T) {
	pool, _ := journalDB(t)
	q := dbgen.New(pool)
	digest := sha256.Sum256([]byte("request"))
	in := dbgen.FacilitatorReserveParams{AuthorizationID: "network:authorization", RequestDigest: digest[:], RequestBody: []byte(`{}`), Network: "eip155:31337", SignerAddress: bytes.Repeat([]byte{1}, 20)}
	n, err := q.FacilitatorReserve(t.Context(), in)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	n, err = q.FacilitatorReserve(t.Context(), in)
	if err != nil || n != 0 {
		t.Fatal("duplicate reservation", n, err)
	}
	row, err := q.FacilitatorGet(t.Context(), in.AuthorizationID)
	if err != nil || row.State != "reserved" || len(row.TransactionHash) != 0 {
		t.Fatal("pre-sign state", row, err)
	}
	signed := dbgen.FacilitatorSaveSignedParams{AuthorizationID: in.AuthorizationID, Nonce: pgtype.Numeric{Int: big.NewInt(7), Valid: true}, RawTransaction: []byte{1, 2, 3}, TransactionHash: bytes.Repeat([]byte{2}, 32)}
	n, err = q.FacilitatorSaveSigned(t.Context(), signed)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	signed.RawTransaction = []byte{4, 5}
	n, err = q.FacilitatorSaveSigned(t.Context(), signed)
	if err != nil || n != 0 {
		t.Fatal("replaced signed bytes", n, err)
	}
	// A fresh query instance sees exactly the same transaction after either a pre-broadcast
	// crash or an ambiguous broadcast response. No in-memory pending store is required.
	row, err = dbgen.New(pool).FacilitatorGet(t.Context(), in.AuthorizationID)
	if err != nil || !bytes.Equal(row.RawTransaction, []byte{1, 2, 3}) {
		t.Fatal("lost durable signed bytes", err)
	}
	second := in
	second.AuthorizationID = "second"
	if _, err = q.FacilitatorReserve(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	signed.AuthorizationID = second.AuthorizationID
	if _, err = q.FacilitatorSaveSigned(t.Context(), signed); err == nil {
		t.Fatal("nonce reused")
	}
	second.Network = "eip155:48815"
	second.AuthorizationID = "other-chain"
	if _, err = q.FacilitatorReserve(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	signed.AuthorizationID = second.AuthorizationID
	if _, err = q.FacilitatorSaveSigned(t.Context(), signed); err != nil {
		t.Fatal("cross-chain nonce was blocked", err)
	}
	response := []byte(`{"success":true,"network":"eip155:31337"}`)
	if n, err = q.FacilitatorFinish(t.Context(), in.AuthorizationID, "succeeded", response); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err = q.FacilitatorFinish(t.Context(), in.AuthorizationID, "failed", []byte(`{}`)); err != nil || n != 0 {
		t.Fatal("terminal outcome changed", n, err)
	}
}
func TestFacilitatorSingletonConnectionLoss(t *testing.T) {
	_, url := journalDB(t)
	a, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(context.Background()) }()
	b, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close(context.Background()) }()
	ok, err := dbgen.New(a).FacilitatorAcquireLock(t.Context())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = dbgen.New(b).FacilitatorAcquireLock(t.Context())
	if err != nil || ok {
		t.Fatal("second active instance", ok, err)
	}
	s := &Service{lock: a}
	s.active.Store(true)
	if err = a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.guard(t.Context()) == nil || s.active.Load() {
		t.Fatal("lost lock still active")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ok, err = dbgen.New(b).FacilitatorAcquireLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
