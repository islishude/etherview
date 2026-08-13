//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
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

func TestMempoolReplacementObservationsAreDirectAndEvidenceBound(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := store.BindChainIdentity(ctx, db, "1", testHash(2)); err != nil {
		t.Fatalf("bind chain identity: %v", err)
	}

	base := time.Unix(1_810_000_000, 0).UTC()
	readNow := base.Add(30 * time.Second)
	repository, err := mempool.NewPostgres(db, mempool.PostgresOptions{
		ChainID: 1, Enabled: true, Now: func() time.Time { return readNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	storeSnapshot := func(endpoint string, offset time.Duration, transactions ...mempool.Transaction) {
		t.Helper()
		observed := base.Add(offset)
		if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
			Endpoint: endpoint, ObservedAt: observed, ExpiresAt: observed.Add(10 * time.Minute),
			Transactions: transactions,
		}); err != nil {
			t.Fatalf("store snapshot %s at %s: %v", endpoint, offset, err)
		}
	}

	keyA, keyB := byte(1), byte(2)
	a := mempoolReplacementTransaction(t, keyA, 7, 2, base, base.Add(10*time.Minute))
	b := mempoolReplacementTransaction(t, keyA, 7, 3, base.Add(time.Second), base.Add(10*time.Minute+time.Second))
	c := mempoolReplacementTransaction(t, keyA, 7, 4, base.Add(2*time.Second), base.Add(10*time.Minute+2*time.Second))
	storeSnapshot("rpc-a", 0, a)
	storeSnapshot("rpc-a", time.Second, b)
	storeSnapshot("rpc-a", 2*time.Second, c)

	aDetail, err := repository.Lookup(ctx, a.Hash)
	if err != nil || aDetail.Kind != mempool.DetailReplaced || aDetail.ReplacementHash != b.Hash || aDetail.Transaction.ReplacesHash != nil {
		t.Fatalf("A detail = %+v, error=%v", aDetail, err)
	}
	bDetail, err := repository.Lookup(ctx, b.Hash)
	if err != nil || bDetail.Kind != mempool.DetailReplaced || bDetail.ReplacementHash != c.Hash ||
		bDetail.Transaction.ReplacesHash == nil || *bDetail.Transaction.ReplacesHash != a.Hash {
		t.Fatalf("B detail = %+v, error=%v", bDetail, err)
	}
	cDetail, err := repository.Lookup(ctx, c.Hash)
	if err != nil || cDetail.Kind != mempool.DetailPending || cDetail.Transaction.ReplacesHash == nil || *cDetail.Transaction.ReplacesHash != b.Hash {
		t.Fatalf("C detail = %+v, error=%v", cDetail, err)
	}
	var directRelations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mempool_transaction_replacements WHERE chain_id = 1`).Scan(&directRelations); err != nil || directRelations != 2 {
		t.Fatalf("direct replacement relation count = %d, error=%v", directRelations, err)
	}

	// Re-observing the same hash does not create a replacement.
	storeSnapshot("rpc-a", 3*time.Second, c)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mempool_transaction_replacements WHERE chain_id = 1`).Scan(&directRelations); err != nil || directRelations != 2 {
		t.Fatalf("same-hash relation count = %d, error=%v", directRelations, err)
	}

	// Endpoint changes, failed observations, and an intervening empty snapshot
	// each break the evidence chain.
	d := mempoolReplacementTransaction(t, keyA, 7, 5, base.Add(4*time.Second), base.Add(10*time.Minute+4*time.Second))
	storeSnapshot("rpc-b", 4*time.Second, d)
	if _, err := repository.Lookup(ctx, c.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("endpoint switch marked C replaced: %v", err)
	}
	if err := repository.StoreFailure(ctx, mempool.Failure{
		State: mempool.StateFailed, Endpoint: "rpc-b", Code: "rpc_request_failed",
		Message: "failed", ObservedAt: base.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	e := mempoolReplacementTransaction(t, keyA, 7, 6, base.Add(6*time.Second), base.Add(10*time.Minute+6*time.Second))
	storeSnapshot("rpc-b", 6*time.Second, e)
	if _, err := repository.Lookup(ctx, d.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("failure gap marked D replaced: %v", err)
	}
	storeSnapshot("rpc-b", 7*time.Second)
	f := mempoolReplacementTransaction(t, keyA, 7, 7, base.Add(8*time.Second), base.Add(9*time.Second))
	if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "rpc-b", ObservedAt: base.Add(8 * time.Second), ExpiresAt: base.Add(9 * time.Second),
		Transactions: []mempool.Transaction{f},
	}); err != nil {
		t.Fatal(err)
	}
	g := mempoolReplacementTransaction(t, keyA, 7, 8, base.Add(10*time.Second), base.Add(10*time.Minute+10*time.Second))
	storeSnapshot("rpc-b", 10*time.Second, g)
	if _, err := repository.Lookup(ctx, f.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("expired predecessor marked F replaced: %v", err)
	}

	// A stale write is retained only as a non-current observation and cannot
	// replace the current hash.
	h := mempoolReplacementTransaction(t, keyA, 7, 9, base.Add(12*time.Second), base.Add(10*time.Minute+12*time.Second))
	storeSnapshot("rpc-b", 12*time.Second, h)
	i := mempoolReplacementTransaction(t, keyA, 7, 10, base.Add(11*time.Second), base.Add(10*time.Minute+11*time.Second))
	storeSnapshot("rpc-b", 11*time.Second, i)
	current, err := repository.Lookup(ctx, h.Hash)
	if err != nil || current.Kind != mempool.DetailPending {
		t.Fatalf("stale write changed current transaction: %+v error=%v", current, err)
	}
	if _, err := repository.Lookup(ctx, i.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("stale hash became visible or replacement evidence: %v", err)
	}
	afterStale := mempoolReplacementTransaction(t, keyA, 7, 11, base.Add(13*time.Second), base.Add(10*time.Minute+13*time.Second))
	storeSnapshot("rpc-b", 13*time.Second, afterStale)
	if _, err := repository.Lookup(ctx, h.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("stale write failed to break H replacement continuity: %v", err)
	}
	current, err = repository.Lookup(ctx, afterStale.Hash)
	if err != nil || current.Kind != mempool.DetailPending {
		t.Fatalf("post-stale transaction = %+v error=%v", current, err)
	}

	// A different nonce or sender cannot replace the prior slot.
	j := mempoolReplacementTransaction(t, keyA, 8, 12, base.Add(14*time.Second), base.Add(10*time.Minute+14*time.Second))
	storeSnapshot("rpc-b", 14*time.Second, j)
	if _, err := repository.Lookup(ctx, afterStale.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("different nonce marked post-stale transaction replaced: %v", err)
	}
	k := mempoolReplacementTransaction(t, keyB, 8, 13, base.Add(15*time.Second), base.Add(10*time.Minute+15*time.Second))
	storeSnapshot("rpc-b", 15*time.Second, k)
	if _, err := repository.Lookup(ctx, j.Hash); !errors.Is(err, mempool.ErrNotFound) {
		t.Fatalf("different sender marked J replaced: %v", err)
	}

	conflicting := mempoolReplacementTransaction(t, keyA, 9, 14, base.Add(16*time.Second), base.Add(10*time.Minute+16*time.Second))
	conflictingReplacement := mempoolReplacementTransaction(t, keyA, 9, 15, base.Add(16*time.Second), base.Add(10*time.Minute+16*time.Second))
	if _, err := repository.StoreSnapshot(ctx, mempool.Snapshot{
		Endpoint: "rpc-b", ObservedAt: base.Add(16 * time.Second), ExpiresAt: base.Add(10*time.Minute + 16*time.Second),
		Transactions: []mempool.Transaction{conflicting, conflictingReplacement},
	}); err == nil || !strings.Contains(err.Error(), "sender and nonce slot") {
		t.Fatalf("conflicting slot error = %v", err)
	}

	readNow = base.Add(20 * time.Minute)
	storeSnapshot("rpc-b", 20*time.Minute)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mempool_transaction_replacements WHERE chain_id = 1`).Scan(&directRelations); err != nil || directRelations != 0 {
		t.Fatalf("expired replacement relation count = %d, error=%v", directRelations, err)
	}
}

func mustHashBytes(t *testing.T, value string) []byte {
	t.Helper()
	if !common.IsHexHash(value) {
		t.Fatalf("invalid hash %q", value)
	}
	return common.HexToHash(value).Bytes()
}

func mempoolIntegrationTransaction(t *testing.T, value uint64, firstSeen, expires time.Time) mempool.Transaction {
	t.Helper()
	to := testAddress(value + 100)
	chainID := big.NewInt(1)
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     value,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(2),
		Gas:       21_000,
		To:        &to,
		Value:     new(big.Int).SetUint64(value * 10),
		Data:      []byte{},
	})
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	fields, err := marshalIntegrationFields(wire)
	if err != nil {
		t.Fatal(err)
	}
	setIntegrationJSON(fields, "from", from)
	setIntegrationJSON(fields, "futurePendingField", value)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	toString := to.String()
	typeString := "2"
	maxFee, priorityFee := "2", "1"
	return mempool.Transaction{
		Hash: wire.Hash().String(), From: from.String(), To: &toString,
		Nonce: fmt.Sprint(value), Value: fmt.Sprint(value * 10), Gas: "21000", Type: &typeString,
		MaxFeePerGas: &maxFee, MaxPriorityFeePerGas: &priorityFee,
		Input: "0x", Raw: raw, FirstSeenAt: firstSeen, LastSeenAt: firstSeen,
		ExpiresAt: expires, Endpoint: "pending-a",
	}
}

func mempoolReplacementTransaction(
	t *testing.T,
	keySeed byte,
	nonce uint64,
	feeCap uint64,
	firstSeen time.Time,
	expires time.Time,
) mempool.Transaction {
	t.Helper()
	to := testAddress(200)
	chainID := big.NewInt(1)
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: new(big.Int).SetUint64(feeCap - 1),
		GasFeeCap: new(big.Int).SetUint64(feeCap), Gas: 21_000, To: &to,
		Value: big.NewInt(1), Data: []byte{},
	})
	keyBytes := make([]byte, 32)
	keyBytes[len(keyBytes)-1] = keySeed
	key, err := crypto.ToECDSA(keyBytes)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	fields, err := marshalIntegrationFields(wire)
	if err != nil {
		t.Fatal(err)
	}
	setIntegrationJSON(fields, "from", from)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	toString := to.String()
	typeString := "2"
	maxFee := fmt.Sprint(feeCap)
	priorityFee := fmt.Sprint(feeCap - 1)
	return mempool.Transaction{
		Hash: wire.Hash().String(), From: from.String(), To: &toString,
		Nonce: fmt.Sprint(nonce), Value: "1", Gas: "21000", Type: &typeString,
		MaxFeePerGas: &maxFee, MaxPriorityFeePerGas: &priorityFee,
		Input: "0x", Raw: raw, FirstSeenAt: firstSeen, LastSeenAt: firstSeen,
		ExpiresAt: expires, Endpoint: "rpc",
	}
}
