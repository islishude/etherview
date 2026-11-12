package facilitator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/x402settlement"
	exact "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultAuthorizationFieldsAndHostileTypes(t *testing.T) {
	cfg := testConfig().Chains[0]
	service := &Service{chains: map[string]*chainRuntime{cfg.Network: {adapter: &chainClient{config: cfg}}}}
	for _, test := range []struct {
		name  string
		extra map[string]any
		valid bool
	}{
		{"omitted", map[string]any{"name": "USD", "version": "1"}, true},
		{"method omitted", map[string]any{"paymentFlow": "authorization"}, true},
		{"flow omitted", map[string]any{"assetTransferMethod": "eip3009"}, true},
		{"null", map[string]any{"paymentFlow": nil}, false},
		{"number", map[string]any{"assetTransferMethod": 1}, false},
		{"empty", map[string]any{"assetTransferMethod": ""}, false},
		{"unknown", map[string]any{"paymentFlow": "receipt"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := testRequest()
			in.PaymentRequirements.Extra = test.extra
			in.PaymentPayload.Accepted = in.PaymentRequirements
			body, _ := json.Marshal(in)
			_, _, _, _, err := service.decode(httptest.NewRequest("POST", "/verify", bytes.NewReader(body)))
			if (err == nil) != test.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestVerificationErrorClassificationAndLiveness(t *testing.T) {
	for _, code := range []string{exact.ErrFailedToVerifySignature, exact.ErrEip3009SimulationFailed, exact.ErrPermit2SimulationFailed, "private https://secret.invalid"} {
		if x402settlement.BusinessReason(code) != "" {
			t.Fatal("infrastructure classified as rejection", code)
		}
	}
	if x402settlement.BusinessReason(exact.ErrPermit2AllowanceRequired) != exact.ErrPermit2AllowanceRequired {
		t.Fatal("lost approval signal")
	}
	observation := &verificationObservation{}
	recordRPCFailure(context.WithValue(t.Context(), verificationContext{}, observation), errors.New("RPC connection lost"))
	if !observation.unavailable.Load() {
		t.Fatal("RPC failure not retained independently of SDK error")
	}
	s := &Service{fatal: make(chan struct{})}
	s.active.Store(true)
	for _, status := range []int{http.StatusNoContent, http.StatusServiceUnavailable} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		if w.Code != status {
			t.Fatal(w.Code, status)
		}
		s.loseLock()
	}
}

func TestSDKRPCFaultRemainsRetryable(t *testing.T) {
	for _, method := range []string{"eip3009", "permit2"} {
		for _, consumed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/consumed=%t", method, consumed), func(t *testing.T) {
				in := proofRequest(t, method)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						ID     json.RawMessage   `json:"id"`
						Method string            `json:"method"`
						Params []json.RawMessage `json:"params"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid RPC")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if consumed && request.Method == "eth_getBlockByNumber" {
						_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(0), GasLimit: 30000000}})
						return
					}
					// Only a hash-pinned nonce read works; SDK verification probes
					// still fail, as they do with unsupported optional token methods.
					if consumed && request.Method == "eth_call" && len(request.Params) == 2 && len(request.Params[1]) > 0 && request.Params[1][0] == '{' {
						proof, e := proofFor(in)
						if e != nil {
							t.Error(e)
							return
						}
						word := big.NewInt(1)
						if proof.method == "permit2" {
							word.Lsh(word, uint(new(big.Int).And(proof.nonce, big.NewInt(255)).Uint64()))
						}
						data := make([]byte, 32)
						word.FillBytes(data)
						_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": hexutil.Bytes(data)})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32000, "message": "private upstream timeout"}})
				}))
				defer server.Close()
				client, err := dialRPC(t.Context(), server.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				key, err := crypto.GenerateKey()
				if err != nil {
					t.Fatal(err)
				}
				adapter := &chainClient{client: client, signer: &localSigner{key: key}, config: testConfig().Chains[0]}
				ch := &chainRuntime{adapter: adapter, scheme: exact.NewExactEvmScheme(adapter, nil)}
				response, err := verifyPayment(t.Context(), ch, in)
				if consumed {
					if err != nil || response == nil || response.InvalidReason != "authorization_reconciliation_required" {
						t.Fatal(response, err)
					}
					return
				}
				var operation *operationError
				if response != nil || !errors.As(err, &operation) || operation.code != "chain_unavailable" {
					t.Fatal(response, err)
				}
			})
		}
	}
}
