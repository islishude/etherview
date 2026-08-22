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

type txpoolContent struct {
	Pending map[string]map[string]json.RawMessage `json:"pending"`
	Queued  map[string]map[string]json.RawMessage `json:"queued"`
}

func TestBuildSnapshotBuildsFromPendingAndQueuedTxpoolPools(t *testing.T) {
	t.Parallel()
	observed := time.Unix(100, 0).UTC()
	content := mutatePendingTxpoolTransaction(t, txpoolTestContent(t, 1), func(transaction map[string]any) {
		transaction["futureField"] = map[string]any{"enabled": true}
	})
	snapshot, err := buildSnapshot(content, "mempool-primary", PollerOptions{
		ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20,
	}, observed)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Transactions) != 2 {
		t.Fatalf("unexpected tx count: %d", len(snapshot.Transactions))
	}
	if !snapshot.ExpiresAt.Equal(observed.Add(time.Minute)) || snapshot.Transactions[0].From != pendingTestSender().Hex() {
		t.Fatalf("unexpected snapshot metadata: %+v", snapshot)
	}
	if !strings.Contains(string(snapshot.Transactions[0].Raw), `"futureField"`) &&
		!strings.Contains(string(snapshot.Transactions[1].Raw), `"futureField"`) {
		t.Fatalf("future field was not preserved: %+v", snapshot)
	}
}

func TestBuildSnapshotRejectsTxpoolEntryThatIsHashOnlyOrMined(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(map[string]any){
		"block hash":   func(transaction map[string]any) { transaction["blockHash"] = pendingTestHash(8).Hex() },
		"block number": func(transaction map[string]any) { transaction["blockNumber"] = "0x1" },
		"index":        func(transaction map[string]any) { transaction["transactionIndex"] = "0x0" },
	} {
		mutated := mutatePendingTxpoolTransaction(t, txpoolTestContent(t, 1), mutate)
		if _, err := buildSnapshot(mutated, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, time.Unix(1, 0)); err == nil {
			t.Fatalf("accepted txpool tx with mined %s", name)
		}
	}
	hashOnly := mutatePendingTxpoolTransactionToHashOnly(t, txpoolTestContent(t, 1))
	if _, err := buildSnapshot(hashOnly, "rpc", PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}, time.Unix(1, 0)); err == nil {
		t.Fatal("accepted hash-only txpool transaction")
	}
}

func TestBuildSnapshotRejectsWrongChainAndDuplicateHashes(t *testing.T) {
	t.Parallel()
	options := PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20}
	wrongChain := txpoolTestContent(t, 2)
	if _, err := buildSnapshot(wrongChain, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "chain ID") {
		t.Fatalf("wrong-chain error = %v", err)
	}
	duplicate := duplicateTxpoolTransaction(t, txpoolTestContent(t, 1))
	if _, err := buildSnapshot(duplicate, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate error = %v", err)
	}
	conflict := conflictingTxpoolSlot(t, txpoolTestContent(t, 1))
	if _, err := buildSnapshot(conflict, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "sender and nonce slot") {
		t.Fatalf("conflicting slot error = %v", err)
	}
}

func TestBuildSnapshotEnforcesTransactionAndResponseLimits(t *testing.T) {
	t.Parallel()
	content := txpoolTestContent(t, 1)
	options := PollerOptions{ChainID: 1, Retention: time.Minute, MaxTransactions: 0, MaxResponseBytes: 1 << 20}
	if _, err := buildSnapshot(content, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "transactions") {
		t.Fatalf("transaction limit error = %v", err)
	}
	options.MaxTransactions = 10
	options.MaxResponseBytes = 10
	if _, err := buildSnapshot(content, "rpc", options, time.Unix(1, 0)); err == nil || !strings.Contains(err.Error(), "bytes") {
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

func TestPollerUsesTypedFailureClassification(t *testing.T) {
	t.Parallel()
	options := PollerOptions{
		ChainID: 1, PollInterval: time.Second, Retention: time.Minute,
		MaxTransactions: 10, MaxResponseBytes: 1 << 20, Now: func() time.Time { return time.Unix(7, 0) },
	}
	t.Run("invalid snapshot", func(t *testing.T) {
		poller, err := NewPoller(contentSource{content: json.RawMessage(`{"pending":`)}, &recordingStore{}, options)
		if err != nil {
			t.Fatal(err)
		}
		cycleErr := poller.Cycle(context.Background())
		if pollErrorCode(cycleErr) != "invalid_snapshot" {
			t.Fatalf("invalid snapshot code=%q error=%v", pollErrorCode(cycleErr), cycleErr)
		}
	})
	t.Run("snapshot storage", func(t *testing.T) {
		store := &recordingStore{snapshotErr: errors.New("database snapshot write failed")}
		poller, err := NewPoller(contentSource{content: txpoolTestContent(t, 1)}, store, options)
		if err != nil {
			t.Fatal(err)
		}
		cycleErr := poller.Cycle(context.Background())
		if pollErrorCode(cycleErr) != "storage_or_internal_failure" {
			t.Fatalf("storage code=%q error=%v", pollErrorCode(cycleErr), cycleErr)
		}
	})
	t.Run("failure status storage", func(t *testing.T) {
		store := &recordingStore{failureErr: errors.New("database snapshot status failed")}
		poller, err := NewPoller(errorSource{err: SourceError{State: StateUnavailable, Code: "method_not_supported"}}, store, options)
		if err != nil {
			t.Fatal(err)
		}
		cycleErr := poller.Cycle(context.Background())
		if pollErrorCode(cycleErr) != "storage_or_internal_failure" {
			t.Fatalf("failure storage code=%q error=%v", pollErrorCode(cycleErr), cycleErr)
		}
	})
}

func TestPoolSourceUsesTxpoolContentRPC(t *testing.T) {
	t.Parallel()
	service := &pendingRPCService{content: txpoolTestContent(t, 1)}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "txpool", Client: newPendingRPCClient(t, service), Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeMempool: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	content, endpoint, err := (PoolSource{Pool: pool}).PendingTransactions(context.Background())
	if err != nil || content == nil || endpoint != "txpool" {
		t.Fatalf("content=%v endpoint=%q error=%v", content, endpoint, err)
	}
	if !service.called || service.method != "txpool_content" {
		t.Fatalf("RPC call = %#v", service)
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

func (source errorSource) PendingTransactions(context.Context) (json.RawMessage, string, error) {
	return nil, "rpc", source.err
}

type contentSource struct{ content json.RawMessage }

func (source contentSource) PendingTransactions(context.Context) (json.RawMessage, string, error) {
	return source.content, "rpc", nil
}

type recordingStore struct {
	mu          sync.Mutex
	snapshots   []Snapshot
	failures    []Failure
	snapshotErr error
	failureErr  error
}

func (store *recordingStore) StoreSnapshot(_ context.Context, snapshot Snapshot) (SnapshotInfo, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.snapshotErr != nil {
		return SnapshotInfo{}, store.snapshotErr
	}
	store.snapshots = append(store.snapshots, snapshot)
	return SnapshotInfo{ID: int64(len(store.snapshots))}, nil
}

func (store *recordingStore) StoreFailure(_ context.Context, failure Failure) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failureErr != nil {
		return store.failureErr
	}
	store.failures = append(store.failures, failure)
	return nil
}

type pendingRPCService struct {
	called  bool
	method  string
	content json.RawMessage
}

func (service *pendingRPCService) Content(_ context.Context) (json.RawMessage, error) {
	service.called = true
	service.method = "txpool_content"
	return service.content, nil
}

func newPendingRPCClient(t *testing.T, service *pendingRPCService) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("txpool", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func txpoolTestContent(t *testing.T, chainID uint64) json.RawMessage {
	t.Helper()
	pending := pendingTestTransaction(t, chainID, 7)
	queued := pendingTestTransaction(t, chainID, 8)
	sender := pendingTestSender().Hex()
	content := txpoolContent{
		Pending: map[string]map[string]json.RawMessage{
			sender: {"0x7": pending},
		},
		Queued: map[string]map[string]json.RawMessage{
			sender: {"0x8": queued},
		},
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mutatePendingTxpoolTransaction(t *testing.T, raw json.RawMessage, mutate func(map[string]any)) json.RawMessage {
	t.Helper()
	var parsed txpoolContent
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Pending) == 0 {
		t.Fatal("missing pending txpool transactions")
	}
	for addr, byNonce := range parsed.Pending {
		for nonce, transactionRaw := range byNonce {
			var transaction map[string]any
			if err := json.Unmarshal(transactionRaw, &transaction); err != nil {
				t.Fatal(err)
			}
			mutate(transaction)
			mutatedRaw, err := json.Marshal(transaction)
			if err != nil {
				t.Fatal(err)
			}
			parsed.Pending[addr][nonce] = mutatedRaw
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			return encoded
		}
	}
	t.Fatal("pending txpool tx not found")
	return nil
}

func mutatePendingTxpoolTransactionToHashOnly(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var parsed txpoolContent
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	for addr, byNonce := range parsed.Pending {
		for nonce := range byNonce {
			parsed.Pending[addr][nonce] = json.RawMessage(`"` + pendingTestHash(3).Hex() + `"`)
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			return encoded
		}
	}
	t.Fatal("pending txpool tx not found")
	return nil
}

func duplicateTxpoolTransaction(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var parsed txpoolContent
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, byNonce := range parsed.Pending {
		for _, transaction := range byNonce {
			for queuedAddr, queuedByNonce := range parsed.Queued {
				for queuedNonce := range queuedByNonce {
					parsed.Queued[queuedAddr][queuedNonce] = transaction
					encoded, err := json.Marshal(parsed)
					if err != nil {
						t.Fatal(err)
					}
					return encoded
				}
			}
		}
	}
	t.Fatal("txpool tx not found")
	return nil
}

func conflictingTxpoolSlot(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var parsed txpoolContent
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	sender := pendingTestSender().Hex()
	parsed.Queued[sender]["0x7"] = pendingTestTransactionWithFees(t, 1, 7, 4, 2)
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func pendingTestTransaction(t *testing.T, chainID uint64, nonce uint64) json.RawMessage {
	t.Helper()
	return pendingTestTransactionWithFees(t, chainID, nonce, 2, 1)
}

func pendingTestTransactionWithFees(t *testing.T, chainID uint64, nonce, feeCap, tipCap uint64) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x2")
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(chainID), Nonce: nonce, Gas: 21_000, To: &to,
		Value: big.NewInt(9), GasFeeCap: new(big.Int).SetUint64(feeCap), GasTipCap: new(big.Int).SetUint64(tipCap),
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
	encoded, err = json.Marshal(transactionObject)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
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
