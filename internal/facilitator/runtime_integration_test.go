//go:build integration

package facilitator

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/islishude/etherview/internal/billing/x402wire"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	x402 "github.com/x402-foundation/x402/go/v2"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const fixturePayerKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

type journalRPC struct {
	t             *testing.T
	chain         int64
	config        ChainConfig
	queries       *dbgen.Queries
	mu            sync.Mutex
	tx            *types.Transaction
	sends         atomic.Int64
	blocked       atomic.Bool
	broadcast     chan struct{}
	pauseVerify   atomic.Bool
	verifyEntered chan struct{}
	verifyRelease chan struct{}
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
		if m.pauseVerify.CompareAndSwap(true, false) {
			close(m.verifyEntered)
			select {
			case <-m.verifyRelease:
			case <-r.Context().Done():
				return
			}
		}
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
	payload.Resource = &x402.ResourceInfo{URL: "http://localhost/pay", MimeType: "application/json"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatal(err)
	}
	header := make(http.Header)
	header.Set(x402wire.PaymentSignatureHeader, base64.StdEncoding.EncodeToString(raw))
	normalized, err := codec.DecodePaymentSignature(header)
	if err != nil {
		t.Fatal(err)
	}
	payload = normalized.Payload()
	return paymentRequest{X402Version: 2, PaymentRequirements: payload.Accepted, PaymentPayload: payload}
}
func TestFacilitatorConcurrentChainsAndDurableReplay(t *testing.T) {
	clearCredentialEnvironment(t)
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
	cfg.Database.URLFile = write("database", []byte(url))
	cfg.Auth.BearerTokenFile = write("token", []byte(strings.Repeat("t", 32)))
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
		m := &journalRPC{t: t, chain: id, config: cfg.Chains[i], queries: dbgen.New(pool), broadcast: make(chan struct{}), verifyEntered: make(chan struct{}), verifyRelease: make(chan struct{})}
		server := httptest.NewServer(http.HandlerFunc(m.handler))
		defer server.Close()
		cfg.Chains[i].RPC.URLFile = write(fmt.Sprint("rpc", i), []byte(server.URL))
		mocks = append(mocks, m)
	}

	for _, source := range []string{"direct", "file"} {
		t.Run("environment_"+source, func(t *testing.T) {
			injected := cfg
			injected.Database.URLFile = "missing-yaml-db"
			injected.Auth.BearerTokenFile = "missing-yaml-token"
			if source == "direct" {
				t.Setenv("X402_DATABASE_URL", url)
				t.Setenv("X402_BEARER_TOKEN", strings.Repeat("e", 32))
			} else {
				t.Setenv("X402_DATABASE_URL_FILE", cfg.Database.URLFile)
				t.Setenv("X402_BEARER_TOKEN_FILE", write("env-token", []byte(strings.Repeat("e", 32))))
			}
			service, e := New(t.Context(), injected)
			if e != nil {
				t.Fatal(e)
			}
			defer service.Close()
			for _, token := range []string{strings.Repeat("e", 32), strings.Repeat("t", 32)} {
				request := httptest.NewRequest(http.MethodGet, "/supported", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				result := httptest.NewRecorder()
				service.Handler().ServeHTTP(result, request)
				expected := http.StatusOK
				if token == strings.Repeat("t", 32) {
					expected = http.StatusUnauthorized
				}
				if result.Code != expected {
					t.Fatalf("authentication status %d, want %d", result.Code, expected)
				}
			}
		})
	}
	// Every startup failure after acquiring resources must release the singleton
	// lock so a corrected configuration can immediately start.
	for _, stage := range []string{"signer", "chain_identity", "rpc", "cancelled"} {
		t.Run("startup_cleanup_"+stage, func(t *testing.T) {
			broken := cfg
			broken.Chains = append([]ChainConfig(nil), cfg.Chains...)
			broken.Signers = map[string]SignerConfig{"key": cfg.Signers["key"]}
			ctx := t.Context()
			switch stage {
			case "signer":
				sc := broken.Signers["key"]
				sc.PasswordFile = write("wrong-password", []byte("wrong"))
				broken.Signers["key"] = sc
			case "chain_identity":
				broken.Chains[1].Network = "eip155:999"
			case "rpc":
				broken.Chains[1].RPC = RPCConfig{URL: "http://127.0.0.1:1", AllowInsecureHTTP: true}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			failed, e := New(ctx, broken)
			if e == nil {
				failed.Close()
				t.Fatal("startup should fail")
			}
			recovered, e := New(t.Context(), cfg)
			if e != nil {
				t.Fatal("startup leaked resources", e)
			}
			recovered.Close()
			recovered.Close()
		})
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

	// A duplicate can refuse admission while its first copy is verifying but
	// has not reserved yet. Both must replay the durable refusal, without signing.
	racing := signedTestRequest(t, cfg.Chains[0].Network)
	mocks[0].pauseVerify.Store(true)
	releaseVerify := sync.OnceFunc(func() { close(mocks[0].verifyRelease) })
	defer releaseVerify()
	raceDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { raceDone <- post(racing) }()
	select {
	case <-mocks[0].verifyEntered:
	case w := <-raceDone:
		t.Fatalf("verification did not pause: %s", w.Body)
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not start")
	}
	if w := post(racing); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"chain_busy"`)) {
		t.Fatalf("concurrent refusal failed: %d %s", w.Code, w.Body)
	}
	releaseVerify()
	select {
	case w := <-raceDone:
		if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"chain_busy"`)) || mocks[0].sends.Load() != 0 {
			t.Fatalf("reservation loser signed or lost refusal: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reservation loser did not finish")
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

	wireServer := httptest.NewServer(s.Handler())
	defer wireServer.Close()
	wireClient, e := x402wire.NewClient(x402wire.ClientOptions{BaseURL: wireServer.URL, UnsafeAllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}, Timeout: 2 * time.Second, MaxResponseBytes: 1 << 20, Headers: map[string]string{"Authorization": "Bearer " + strings.Repeat("t", 32)}})
	if e != nil {
		t.Fatal(e)
	}
	settleWire := func(in paymentRequest) (*x402.SettleResponse, error) {
		p, _ := json.Marshal(in.PaymentPayload)
		r, _ := json.Marshal(in.PaymentRequirements)
		return wireClient.Settle(t.Context(), p, r)
	}
	refused := signedTestRequest(t, cfg.Chains[0].Network)
	refusal, e := settleWire(refused)
	if !x402wire.IsFailure(e, x402wire.FailureRejected) || refusal == nil || refusal.Transaction != "" {
		t.Fatalf("busy request not definitively rejected: %+v %v", refusal, e)
	}
	pending, e := settleWire(first)
	if !x402wire.IsFailure(e, x402wire.FailureSettlementPending) || pending == nil || pending.Transaction == "" {
		t.Fatalf("in-flight duplicate was rejected: %+v %v", pending, e)
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
	refusal, e = settleWire(refused)
	if !x402wire.IsFailure(e, x402wire.FailureRejected) || refusal == nil || mocks[0].sends.Load() != 1 {
		t.Fatal("refused authorization was later submitted", e)
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
	if w := post(refused); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"chain_busy"`)) || mocks[0].sends.Load() != 1 {
		t.Fatal("restart forgot durable refusal", w.Body)
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
