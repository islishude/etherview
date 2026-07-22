//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/store"
)

func TestMempoolSnapshotsRemainCursorStableAndExposeFailures(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := store.BindChainIdentity(ctx, db, "1", testHash(1)); err != nil {
		t.Fatalf("bind chain identity: %v", err)
	}

	base := time.Unix(1_800_000_000, 0).UTC()
	readNow := base.Add(time.Minute)
	repository, err := mempool.NewPostgres(db, mempool.PostgresOptions{
		ChainID: 1, Enabled: true, Now: func() time.Time { return readNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	// A separately constructed reader models an API-only process. It observes
	// exactly the snapshots written by the sync-side repository through the
	// shared PostgreSQL contract, with no in-process cache or notification.
	apiRepository, err := mempool.NewPostgres(db, mempool.PostgresOptions{
		ChainID: 1, Enabled: true, Now: func() time.Time { return readNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := mempoolIntegrationTransaction(t, 1, base, base.Add(10*time.Minute))
	second := mempoolIntegrationTransaction(t, 2, base, base.Add(10*time.Minute))
	firstSnapshot, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "pending-a", ObservedAt: base, ExpiresAt: base.Add(10 * time.Minute),
		Transactions: []mempool.Transaction{first, second},
	})
	if err != nil {
		t.Fatalf("store first snapshot: %v", err)
	}
	var futureField string
	if err := db.QueryRowContext(ctx, `
		SELECT raw ->> 'futurePendingField'
		FROM mempool_transactions
		WHERE chain_id = 1 AND tx_hash = $1`, mustHashBytes(t, first.Hash)).Scan(&futureField); err != nil || futureField != "1" {
		t.Fatalf("persisted future transaction field = %q, error=%v", futureField, err)
	}
	page, err := apiRepository.Pending(ctx, "", 1)
	if err != nil {
		t.Fatalf("read first page: %v", err)
	}
	if page.Snapshot.ID != firstSnapshot.ID || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].Hash != second.Hash {
		t.Fatalf("first page = %+v", page)
	}
	firstCursor := page.NextCursor

	third := mempoolIntegrationTransaction(t, 3, base.Add(time.Second), base.Add(10*time.Minute+time.Second))
	second.LastSeenAt = base.Add(time.Second)
	second.ExpiresAt = base.Add(10*time.Minute + time.Second)
	if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "pending-b", ObservedAt: base.Add(time.Second), ExpiresAt: base.Add(10*time.Minute + time.Second),
		Transactions: []mempool.Transaction{second, third},
	}); err != nil {
		t.Fatalf("store second snapshot: %v", err)
	}
	oldPage, err := apiRepository.Pending(ctx, firstCursor, 10)
	if err != nil {
		t.Fatalf("continue first snapshot cursor: %v", err)
	}
	if oldPage.Snapshot.ID != firstSnapshot.ID || len(oldPage.Items) != 1 || oldPage.Items[0].Hash != first.Hash {
		t.Fatalf("old snapshot page changed after a later poll: %+v", oldPage)
	}

	if err := repository.StoreFailure(ctx, mempool.Failure{
		State: mempool.StateFailed, Endpoint: "pending-b", Code: "rpc_request_failed",
		Message: "pending RPC request failed", ObservedAt: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("store failure status: %v", err)
	}
	var state, endpoint, errorCode, errorMessage string
	var latestSnapshotID int64
	var lastSuccess time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT state, endpoint_name, error_code, error_message, latest_snapshot_id, last_success_at
		FROM mempool_status WHERE chain_id = 1`,
	).Scan(&state, &endpoint, &errorCode, &errorMessage, &latestSnapshotID, &lastSuccess); err != nil {
		t.Fatalf("read auditable failure status: %v", err)
	}
	if state != "failed" || endpoint != "pending-b" || errorCode != "rpc_request_failed" || errorMessage == "" || latestSnapshotID <= 0 || lastSuccess.IsZero() {
		t.Fatalf("auditable failure status = %q %q %q %q %d %v", state, endpoint, errorCode, errorMessage, latestSnapshotID, lastSuccess)
	}
	if _, err := apiRepository.Pending(ctx, "", 10); err == nil {
		t.Fatal("failed capability returned a misleading successful page")
	} else {
		var capability mempool.CapabilityError
		if !errors.As(err, &capability) || capability.State != mempool.StateFailed || capability.Code != "rpc_request_failed" {
			t.Fatalf("failure error = %T %v", err, err)
		}
	}

	if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "pending-b", ObservedAt: base.Add(3 * time.Second), ExpiresAt: base.Add(10*time.Minute + 3*time.Second),
		Transactions: []mempool.Transaction{second},
	}); err != nil {
		t.Fatalf("restore successful status: %v", err)
	}
	readNow = base.Add(11 * time.Minute)
	if _, err := apiRepository.Pending(ctx, "", 10); err == nil {
		t.Fatal("expired latest snapshot returned a misleading successful page")
	} else {
		var capability mempool.CapabilityError
		if !errors.As(err, &capability) || capability.State != mempool.StateUnavailable || capability.Code != "snapshot_expired" {
			t.Fatalf("expired latest snapshot error = %T %v", err, err)
		}
	}
	if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "pending-b", ObservedAt: readNow, ExpiresAt: readNow.Add(10 * time.Minute),
		Transactions: []mempool.Transaction{},
	}); err != nil {
		t.Fatalf("store expiry snapshot: %v", err)
	}
	if _, err := apiRepository.Pending(ctx, firstCursor, 10); !errors.Is(err, mempool.ErrInvalidCursor) {
		t.Fatalf("expired cursor error = %v, want ErrInvalidCursor", err)
	}
	empty, err := apiRepository.Pending(ctx, "", 10)
	if err != nil || len(empty.Items) != 0 || empty.Snapshot.TransactionCount != 0 {
		t.Fatalf("confirmed empty snapshot = %+v, error=%v", empty, err)
	}
}

func mustHashBytes(t *testing.T, value string) []byte {
	t.Helper()
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hash.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mempoolIntegrationTransaction(t *testing.T, value uint64, firstSeen, expires time.Time) mempool.Transaction {
	t.Helper()
	hash := testHash(1_000 + value)
	from := testAddress(value)
	to := testAddress(value + 100)
	txType := ethrpc.QuantityFromUint64(2)
	chainID := ethrpc.QuantityFromUint64(1)
	wire := ethrpc.Transaction{
		Hash: hash, Type: &txType, From: from, To: &to,
		Nonce: ethrpc.QuantityFromUint64(value), Gas: ethrpc.QuantityFromUint64(21_000),
		Value: ethrpc.QuantityFromUint64(value * 10), Input: ethrpc.Data("0x"), ChainID: &chainID,
		Extra: map[string]json.RawMessage{"futurePendingField": json.RawMessage(fmt.Sprintf("%d", value))},
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	toString := to.String()
	typeString := "2"
	return mempool.Transaction{
		Hash: hash.String(), From: from.String(), To: &toString,
		Nonce: fmt.Sprint(value), Value: fmt.Sprint(value * 10), Gas: "21000", Type: &typeString,
		Input: "0x", Raw: raw, FirstSeenAt: firstSeen, LastSeenAt: firstSeen,
		ExpiresAt: expires, Endpoint: "pending-a",
	}
}
