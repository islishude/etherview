//go:build integration

package facilitator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	x402 "github.com/x402-foundation/x402/go/v2"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const fixturePayerKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

type journalRPC struct {
	t         *testing.T
	chain     int64
	config    ChainConfig
	queries   *dbgen.Queries
	mu        sync.Mutex
	tx        *types.Transaction
	sends     atomic.Int64
	blocked   atomic.Bool
	broadcast chan struct{}
}

func (m *journalRPC) handler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		m.t.Error("invalid rpc request")
		return
	}
	header := &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1), Time: uint64(time.Now().Unix())}
	// Fix timestamp so every independently fetched header has identical hash.
	header.Time = 1700000000
	var result any
	switch in.Method {
	case "eth_chainId":
		result = hexutil.EncodeBig(big.NewInt(m.chain))
	case "eth_getCode":
		var address string
		_ = json.Unmarshal(in.Params[0], &address)
		result = "0x01"
		if strings.EqualFold(address, "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266") {
			result = "0x"
		}
	case "eth_call":
		var call map[string]string
		_ = json.Unmarshal(in.Params[0], &call)
		data := call["input"]
		if data == "" {
			data = call["data"]
		}
		result = "0x" + strings.Repeat("0", 64)
		if strings.HasPrefix(data, "0x70a08231") {
			result = fmt.Sprintf("0x%064x", 1000000)
		}
		if strings.HasPrefix(data, "0xe3ee160e") {
			result = "0x"
		}
	case "eth_getTransactionCount":
		result = "0x0"
	case "eth_maxPriorityFeePerGas":
		result = "0x1"
	case "eth_getBlockByNumber":
		result = header
	case "eth_estimateGas":
		result = "0x186a0"
	case "eth_blockNumber":
		result = "0x1"
	case "eth_getBalance":
		result = "0x3635c9adc5dea00000"
	case "eth_sendRawTransaction":
		var raw string
		_ = json.Unmarshal(in.Params[0], &raw)
		encoded, err := hexutil.Decode(raw)
		if err != nil {
			m.t.Error(err)
			return
		}
		var tx types.Transaction
		if err = tx.UnmarshalBinary(encoded); err != nil {
			m.t.Error(err)
			return
		}
		rows, err := m.queries.FacilitatorPending(r.Context(), m.config.Network)
		if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].RawTransaction, encoded) || !bytes.Equal(rows[0].TransactionHash, tx.Hash().Bytes()) {
			m.t.Error("broadcast happened before durable signed transaction", err)
		}
		m.mu.Lock()
		m.tx = &tx
		m.mu.Unlock()
		if m.sends.Add(1) == 1 {
			close(m.broadcast)
		}
		result = tx.Hash()
	case "eth_getTransactionReceipt":
		m.mu.Lock()
		tx := m.tx
		m.mu.Unlock()
		if tx != nil && !m.blocked.Load() {
			amount := common.LeftPadBytes(big.NewInt(100).Bytes(), 32)
			result = &types.Receipt{Type: types.DynamicFeeTxType, TxHash: tx.Hash(), Status: 1, CumulativeGasUsed: 100000, GasUsed: 100000, BlockNumber: big.NewInt(1), BlockHash: header.Hash(), Logs: []*types.Log{{Address: common.HexToAddress(m.config.Tokens[0].Address), Topics: []common.Hash{crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")), common.HexToHash("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"), common.HexToHash(m.config.Recipients[0])}, Data: amount}}}
		}
	default:
		m.t.Errorf("unexpected RPC %s", in.Method)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in.ID, "result": result})
}
func signedTestRequest(t *testing.T, network string) paymentRequest {
	t.Helper()
	req := testRequest().PaymentRequirements
	req.Network = network
	req.Extra["name"] = "Local USD"
	req.Extra["version"] = "1"
	signer, err := evmsigners.NewClientSignerFromPrivateKey(fixturePayerKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := exactclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(t.Context(), req, x402.PaymentPayloadContext{})
	if err != nil {
		t.Fatal(err)
	}
	payload.Accepted = req
	return paymentRequest{X402Version: 2, PaymentRequirements: req, PaymentPayload: payload}
}
func TestFacilitatorConcurrentChainsAndDurableReplay(t *testing.T) {
	pool, url := journalDB(t)
	cfg := testConfig()
	dir := t.TempDir()
	write := func(name string, value []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, value, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg.DatabaseURLFile = write("database", []byte(url))
	cfg.BearerTokenFile = write("token", []byte(strings.Repeat("t", 32)))
	keyData, err := os.ReadFile("../../preview/facilitator/keystore.json")
	if err != nil {
		t.Fatal(err)
	}
	signer := cfg.Signers["key"]
	signer.KeystoreFile = write("key", keyData)
	signer.PasswordFile = write("password", []byte("preview-only-password"))
	cfg.Signers["key"] = signer
	cfg.Chains[0].Tokens[0].Methods = []string{"eip3009"}
	other := cfg.Chains[0]
	other.Network = "eip155:48815"
	cfg.Chains = append(cfg.Chains, other)
	mocks := []*journalRPC{}
	for i, id := range []int64{31337, 48815} {
		m := &journalRPC{t: t, chain: id, config: cfg.Chains[i], queries: dbgen.New(pool), broadcast: make(chan struct{})}
		server := httptest.NewServer(http.HandlerFunc(m.handler))
		defer server.Close()
		cfg.Chains[i].RPCURLFile = write(fmt.Sprint("rpc", i), []byte(server.URL))
		mocks = append(mocks, m)
	}
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	post := func(in paymentRequest) *httptest.ResponseRecorder {
		body, _ := json.Marshal(in)
		request := httptest.NewRequest(http.MethodPost, "/settle", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, request)
		return w
	}
	first := signedTestRequest(t, cfg.Chains[0].Network)
	mocks[0].blocked.Store(true)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post(first) }()
	select {
	case <-mocks[0].broadcast:
	case w := <-done:
		t.Fatalf("first settlement failed before broadcast: %d %s", w.Code, w.Body)
	case <-time.After(5 * time.Second):
		t.Fatal("no broadcast")
	}
	// Progress remains available while settlement owns the per-chain gate.
	progressRequest := func(in paymentRequest, authenticated bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(in)
		request := httptest.NewRequest(http.MethodPost, "/progress", bytes.NewReader(body))
		if authenticated {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		}
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, request)
		return result
	}
	if w := progressRequest(first, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated progress: %d", w.Code)
	}
	progressDeadline := time.Now().Add(3 * time.Second)
	for {
		w := progressRequest(first, true)
		var progress SettlementProgress
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &progress) != nil {
			t.Fatalf("progress failed: %d %s", w.Code, w.Body)
		}
		if progress.Stage == Confirming && progress.Transaction != nil {
			break
		}
		if time.Now().After(progressDeadline) {
			t.Fatalf("broadcast not observable: %+v", progress)
		}
		time.Sleep(10 * time.Millisecond)
	}
	changed := signedTestRequest(t, cfg.Chains[0].Network)
	changed.PaymentPayload = first.PaymentPayload
	changed.PaymentRequirements.Amount = "101"
	changed.PaymentPayload.Accepted = changed.PaymentRequirements
	if w := progressRequest(changed, true); w.Code != http.StatusConflict {
		t.Fatalf("progress digest mismatch: %d %s", w.Code, w.Body)
	}
	if mocks[0].sends.Load() != 1 {
		t.Fatal("progress broadcast another transaction")
	}
	busy := post(signedTestRequest(t, cfg.Chains[0].Network))
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("same-chain serialization failed: %d", busy.Code)
	}
	second := post(signedTestRequest(t, cfg.Chains[1].Network))
	var result x402.SettleResponse
	if err = json.Unmarshal(second.Body.Bytes(), &result); err != nil || !result.Success {
		t.Fatalf("independent chain blocked: %d %s %v", second.Code, second.Body, err)
	}
	mocks[0].blocked.Store(false)
	select {
	case w := <-done:
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Success {
			t.Fatalf("first payment failed: %s", w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receipt wait did not resume")
	}
	replay := post(first)
	if err = json.Unmarshal(replay.Body.Bytes(), &result); err != nil || !result.Success || mocks[0].sends.Load() != 1 {
		t.Fatal("durable replay rebroadcast", replay.Body)
	}
	s.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		reopened, openErr := New(t.Context(), cfg)
		if openErr == nil {
			s = reopened
			break
		}
		if openErr.Error() != "facilitator_already_active" || time.Now().After(deadline) {
			t.Fatal("restart failed", openErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	replay = post(first)
	if err = json.Unmarshal(replay.Body.Bytes(), &result); err != nil || !result.Success || mocks[0].sends.Load() != 1 {
		t.Fatal("restart rebroadcast a settled authorization", replay.Body)
	}
	conflict := first
	conflict.PaymentRequirements.Amount = "101"
	conflict.PaymentPayload.Accepted = conflict.PaymentRequirements
	if w := post(conflict); w.Code != http.StatusConflict {
		t.Fatalf("conflicting authorization was accepted: %d %s", w.Code, w.Body)
	}
	// The adapter lookup uses authorization identity, not the signature-key cache.
	raw, _ := json.Marshal(first)
	_, _, id, _, err := s.decode(httptest.NewRequest("POST", "/settle", bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), authorizationContext{}, id)
	hash, ok, err := (pendingJournal{queries: dbgen.New(pool)}).Get(ctx, "ignored-signature-key")
	if err != nil || !ok || hash != result.Transaction {
		t.Fatal("pending adapter did not use durable identity", err)
	}
}
