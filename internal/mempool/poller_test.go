package mempool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func TestBuildSnapshotRequiresFullUnminedTransactionsAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	observed := time.Unix(100, 0).UTC()
	block := pendingTestBlock(t, 1)
	block.Transactions[0].Transaction.Extra = map[string]json.RawMessage{"futureField": json.RawMessage(`{"enabled":true}`)}
	snapshot, err := buildSnapshot(block, "mempool-primary", PollerOptions{
		ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20,
	}, observed)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Transactions) != 1 || snapshot.Transactions[0].Hash == "" ||
		!strings.Contains(string(snapshot.Transactions[0].Raw), `"futureField"`) {
		t.Fatalf("snapshot did not retain full transaction: %+v", snapshot)
	}
	if !snapshot.ExpiresAt.Equal(observed.Add(time.Minute)) || snapshot.Transactions[0].From != "0x0000000000000000000000000000000000000001" {
		t.Fatalf("unexpected snapshot metadata: %+v", snapshot)
	}

	height := ethrpc.QuantityFromUint64(1)
	blockHash := pendingTestHash(8)
	index := ethrpc.QuantityFromUint64(0)
	for name, mutate := range map[string]func(*ethrpc.Transaction){
		"block hash":   func(transaction *ethrpc.Transaction) { transaction.BlockHash = &blockHash },
		"block number": func(transaction *ethrpc.Transaction) { transaction.BlockNumber = &height },
		"index":        func(transaction *ethrpc.Transaction) { transaction.TransactionIndex = &index },
	} {
		mined := pendingTestBlock(t, 1)
		mutate(mined.Transactions[0].Transaction)
		if _, err := buildSnapshot(mined, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, observed); err == nil {
			t.Fatalf("accepted pending transaction with a mined %s", name)
		}
	}
	identifiedBlock := pendingTestBlock(t, 1)
	identifiedBlock.Hash = &blockHash
	if _, err := buildSnapshot(identifiedBlock, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, observed); err == nil {
		t.Fatal("accepted a pending block with a mined identity")
	}
	hashOnly := pendingTestBlock(t, 1)
	hashOnly.Transactions[0].Transaction = nil
	if _, err := buildSnapshot(hashOnly, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, observed); err == nil {
		t.Fatal("accepted hash-only pending transaction")
	}
}

func TestBuildSnapshotRejectsWrongChainAndDuplicateHashes(t *testing.T) {
	t.Parallel()
	options := PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}
	wrongChain := pendingTestBlock(t, 2)
	if _, err := buildSnapshot(wrongChain, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "chain ID") {
		t.Fatalf("wrong-chain error = %v", err)
	}
	duplicate := pendingTestBlock(t, 1)
	duplicate.Transactions = append(duplicate.Transactions, duplicate.Transactions[0])
	if _, err := buildSnapshot(duplicate, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestBuildSnapshotEnforcesTransactionAndResponseLimits(t *testing.T) {
	t.Parallel()
	block := pendingTestBlock(t, 1)
	options := PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 0, MaxResponseBytes: 1 << 20}
	if _, err := buildSnapshot(block, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "transactions") {
		t.Fatalf("transaction limit error = %v", err)
	}
	options.MaxTransactions = 10
	options.MaxResponseBytes = 10
	if _, err := buildSnapshot(block, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("response limit error = %v", err)
	}
}

func TestPollerPersistsExplicitFailureWithoutReturningStaleSuccess(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	poller, err := NewPoller(errorSource{err: SourceError{State: StateUnavailable, Code: "method_not_supported"}}, store, PollerOptions{
		ChainID: 1, PollInterval: time.Second, Retention: time.Minute,
		MaxTransactions: 10, MaxResponseBytes: 1 << 20, Now: func() time.Time { return time.Unix(7, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Cycle(context.Background()); err == nil {
		t.Fatal("expected source error")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.snapshots) != 0 || len(store.failures) != 1 || store.failures[0].State != StateUnavailable || store.failures[0].Code != "method_not_supported" {
		t.Fatalf("recorded store state = snapshots %d failures %+v", len(store.snapshots), store.failures)
	}
}

func TestPoolSourceUsesPendingFullTransactionRPC(t *testing.T) {
	t.Parallel()
	caller := &recordingCaller{block: pendingTestBlock(t, 1)}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "pending", Client: caller, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeMempool: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	block, endpoint, err := (PoolSource{Pool: pool}).PendingBlock(context.Background())
	if err != nil || block == nil || endpoint != "pending" {
		t.Fatalf("block=%v endpoint=%q error=%v", block, endpoint, err)
	}
	if caller.method != "eth_getBlockByNumber" || len(caller.params) != 2 || caller.params[0] != "pending" || caller.params[1] != true {
		t.Fatalf("RPC call = %s %#v", caller.method, caller.params)
	}
}

func TestPendingCursorIsOpaqueVersionedAndStrict(t *testing.T) {
	t.Parallel()
	cursor := pendingCursor{
		Version: cursorVersion, ChainID: "1", SnapshotID: 4,
		BeforeFirstSeen: time.Unix(5, 0).UTC(), BeforeHash: pendingTestHash(9).String(),
	}
	encoded, err := encodePendingCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePendingCursor(encoded)
	if err != nil || decoded.SnapshotID != cursor.SnapshotID || decoded.BeforeHash != cursor.BeforeHash {
		t.Fatalf("decoded=%+v error=%v", decoded, err)
	}
	unknown := `{"v":1,"chain_id":"1","snapshot_id":4,"before_first_seen":"1970-01-01T00:00:05Z","before_hash":"` + cursor.BeforeHash + `","extra":true}`
	if _, err := decodePendingCursor(base64Raw(unknown)); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("unknown field error=%v", err)
	}
}

type errorSource struct{ err error }

func (source errorSource) PendingBlock(context.Context) (*ethrpc.Block, string, error) {
	return nil, "pending", source.err
}

type recordingStore struct {
	mu        sync.Mutex
	snapshots []Snapshot
	failures  []Failure
}

func (store *recordingStore) StoreSnapshot(_ context.Context, snapshot Snapshot) (SnapshotInfo, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshots = append(store.snapshots, snapshot)
	return SnapshotInfo{ID: int64(len(store.snapshots))}, nil
}

func (store *recordingStore) StoreFailure(_ context.Context, failure Failure) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.failures = append(store.failures, failure)
	return nil
}

type recordingCaller struct {
	method string
	params []any
	block  *ethrpc.Block
}

func (caller *recordingCaller) Call(_ context.Context, method string, params []any, result any) error {
	caller.method, caller.params = method, params
	destination, ok := result.(**ethrpc.Block)
	if !ok {
		return fmt.Errorf("unexpected result type %T", result)
	}
	*destination = caller.block
	return nil
}

func pendingTestBlock(t *testing.T, chainID uint64) *ethrpc.Block {
	t.Helper()
	zero := pendingTestHash(0)
	from := pendingTestAddress(1)
	to := pendingTestAddress(2)
	txHash := pendingTestHash(3)
	txType := ethrpc.QuantityFromUint64(2)
	chain := ethrpc.QuantityFromUint64(chainID)
	transaction := &ethrpc.Transaction{
		Hash: txHash, Type: &txType, From: from, To: &to,
		Nonce: ethrpc.QuantityFromUint64(7), Gas: ethrpc.QuantityFromUint64(21_000),
		Value: ethrpc.QuantityFromUint64(9), Input: ethrpc.Data("0x"), ChainID: &chain,
	}
	return &ethrpc.Block{
		ParentHash: zero, Sha3Uncles: zero, TransactionsRoot: zero, StateRoot: zero, ReceiptsRoot: zero,
		ExtraData: ethrpc.Data("0x"), GasLimit: ethrpc.QuantityFromUint64(30_000_000),
		GasUsed: ethrpc.QuantityFromUint64(0), Timestamp: ethrpc.QuantityFromUint64(1),
		Transactions: []ethrpc.TransactionRef{{Hash: txHash, Transaction: transaction}},
		Uncles:       []ethrpc.Hash{},
	}
}

func pendingTestHash(value uint64) ethrpc.Hash {
	hash, err := ethrpc.ParseHash(fmt.Sprintf("0x%064x", value))
	if err != nil {
		panic(err)
	}
	return hash
}

func pendingTestAddress(value uint64) ethrpc.Address {
	address, err := ethrpc.ParseAddress(fmt.Sprintf("0x%040x", value))
	if err != nil {
		panic(err)
	}
	return address
}

func base64Raw(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
