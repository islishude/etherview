package mempool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

func TestBuildSnapshotRequiresFullUnminedTransactionsAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	observed := time.Unix(100, 0).UTC()
	block := pendingTestBlock(t, 1)
	block = mutatePendingTransaction(t, block, func(transaction map[string]any) {
		transaction["futureField"] = map[string]any{"enabled": true}
	})
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
	if !snapshot.ExpiresAt.Equal(observed.Add(time.Minute)) || snapshot.Transactions[0].From != pendingTestSender().Hex() {
		t.Fatalf("unexpected snapshot metadata: %+v", snapshot)
	}

	blockHash := pendingTestHash(8)
	for name, mutate := range map[string]func(map[string]any){
		"block hash":   func(transaction map[string]any) { transaction["blockHash"] = blockHash.Hex() },
		"block number": func(transaction map[string]any) { transaction["blockNumber"] = "0x1" },
		"index":        func(transaction map[string]any) { transaction["transactionIndex"] = "0x0" },
	} {
		mined := mutatePendingTransaction(t, pendingTestBlock(t, 1), mutate)
		if _, err := buildSnapshot(mined, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, observed); err == nil {
			t.Fatalf("accepted pending transaction with a mined %s", name)
		}
	}
	identifiedBlock := mutatePendingBlock(t, pendingTestBlock(t, 1), func(block map[string]any) {
		block["hash"] = blockHash.Hex()
	})
	if _, err := buildSnapshot(identifiedBlock, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, observed); err == nil {
		t.Fatal("accepted a pending block with a mined identity")
	}
	hashOnly := mutatePendingBlock(t, pendingTestBlock(t, 1), func(block map[string]any) {
		block["transactions"] = []any{pendingTestHash(3).Hex()}
	})
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
	duplicate := mutatePendingBlock(t, pendingTestBlock(t, 1), func(block map[string]any) {
		transactions := block["transactions"].([]any)
		block["transactions"] = append(transactions, transactions[0])
	})
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
	service := &pendingRPCService{block: pendingTestBlock(t, 1)}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "pending", Client: newPendingRPCClient(t, service), Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeMempool: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	block, endpoint, err := (PoolSource{Pool: pool}).PendingBlock(context.Background())
	if err != nil || block == nil || endpoint != "pending" {
		t.Fatalf("block=%v endpoint=%q error=%v", block, endpoint, err)
	}
	if service.tag != "pending" || !service.full {
		t.Fatalf("RPC call = %q full=%t", service.tag, service.full)
	}
}

func TestPendingCursorIsOpaqueVersionedAndStrict(t *testing.T) {
	t.Parallel()
	cursor := pendingCursor{
		Version: cursorVersion, ChainID: "1", SnapshotID: 4,
		BeforeFirstSeen: time.Unix(5, 0).UTC(), BeforeHash: pendingTestHash(9).Hex(),
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

func (source errorSource) PendingBlock(context.Context) (json.RawMessage, string, error) {
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

type pendingRPCService struct {
	tag   string
	full  bool
	block json.RawMessage
}

func (service *pendingRPCService) GetBlockByNumber(_ context.Context, tag string, full bool) (json.RawMessage, error) {
	service.tag, service.full = tag, full
	return service.block, nil
}

func newPendingRPCClient(t *testing.T, service *pendingRPCService) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func pendingTestBlock(t *testing.T, chainID uint64) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x2")
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(chainID), Nonce: 7, Gas: 21_000, To: &to,
		Value: big.NewInt(9), GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1),
	})
	transaction, err = types.SignTx(transaction, types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := transaction.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var transactionObject map[string]any
	if err := json.Unmarshal(encoded, &transactionObject); err != nil {
		t.Fatal(err)
	}
	transactionObject["from"] = pendingTestSender().Hex()
	transactionObject["blockHash"] = nil
	transactionObject["blockNumber"] = nil
	transactionObject["transactionIndex"] = nil
	block, err := json.Marshal(map[string]any{
		"hash": nil, "number": nil, "transactions": []any{transactionObject},
	})
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func mutatePendingBlock(t *testing.T, raw json.RawMessage, mutate func(map[string]any)) json.RawMessage {
	t.Helper()
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	mutate(block)
	encoded, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mutatePendingTransaction(t *testing.T, raw json.RawMessage, mutate func(map[string]any)) json.RawMessage {
	t.Helper()
	return mutatePendingBlock(t, raw, func(block map[string]any) {
		transactions := block["transactions"].([]any)
		transaction := transactions[0].(map[string]any)
		mutate(transaction)
	})
}

func pendingTestHash(value uint64) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(value))
}

func pendingTestSender() common.Address {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return crypto.PubkeyToAddress(key.PublicKey)
}

func base64Raw(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
