//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/billing/paymentoption"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/x402settlement"
	x402 "github.com/x402-foundation/x402/go/v2"
)

// Real budget middleware, HTTP facilitator transport and PostgreSQL ledgers.
// Only chain execution is controlled by the fixture. The two budgets are scaled
// down, preserving auth -> Verify -> Settle -> detached bounded ledger completion.
func TestPaymentRequestBudgetAndRecovery(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout=%t", timeout), func(t *testing.T) {
			db := newMigratedPostgres(t)
			if _, err := db.Exec(t.Context(), `INSERT INTO chains (chain_id) VALUES (11155111)`); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			user := uuid.NewString()
			payer := common.HexToAddress("0x" + strings.Repeat("33", 20))
			if _, err := db.Exec(t.Context(), `INSERT INTO users (id,chain_id,address,role,status,created_at,updated_at) VALUES ($1::uuid,11155111,$2,'user','active',$3,$3)`, user, payer.Bytes(), now); err != nil {
				t.Fatal(err)
			}
			accounts, err := billing.NewPrepaidLedger(db, billing.PrepaidOptions{ChainID: 11155111, TopupTTL: 10 * time.Minute, UsageTTL: 2 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			payments, err := billing.NewPostgresLedger(db, 11155111, 2*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			decimals := uint8(6)
			option := paymentoption.Option{ID: "default", Name: "Local USD", Network: "eip155:11155111", Asset: common.HexToAddress("0x11").Hex(), Recipient: common.HexToAddress("0x22").Hex(), AssetDecimals: &decimals, AssetEIP712Name: "Local USD", AssetEIP712Version: "1", AssetTransferMethods: []string{"eip3009"}, MinimumTopupAmountAtomic: "1", MaximumTopupAmountAtomic: "10000", CreditRateNumerator: "1", CreditRateDenominator: "1"}
			intent, err := accounts.CreateTopupIntent(t.Context(), billing.CreateTopupIntentInput{UserID: user, Payer: payer, AmountAtomic: "100", ObservedAt: now, PaymentOption: option})
			if err != nil {
				t.Fatal(err)
			}
			hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
			var verifyCalls, settleCalls, resultCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/result" {
					resultCalls.Add(1)
					var ref x402settlement.Reference
					if json.NewDecoder(r.Body).Decode(&ref) != nil {
						t.Error("invalid result reference")
						return
					}
					_ = json.NewEncoder(w).Encode(x402settlement.Result{Reference: ref, State: "succeeded", OriginalTransaction: hash.Hex(), TransactionConfirmed: true, Response: &x402.SettleResponse{Success: true, Transaction: hash.Hex(), Network: x402.Network(intent.Network), Payer: payer.Hex(), Amount: "100"}, Evidence: &x402settlement.Evidence{Transaction: hash.Hex(), BlockHash: common.HexToHash("0x1234").Hex(), BlockNumber: "1", LogIndex: "0", Source: "original", Network: intent.Network, Payer: payer.Hex(), Recipient: option.Recipient, Asset: option.Asset, Amount: "100"}})
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				switch r.URL.Path {
				case "/verify":
					verifyCalls.Add(1)
					select {
					case <-time.After(100 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
					_ = json.NewEncoder(w).Encode(x402.VerifyResponse{IsValid: true, Payer: payer.Hex()})
				case "/settle":
					settleCalls.Add(1)
					if timeout {
						<-r.Context().Done()
						return
					}
					select {
					case <-time.After(600 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
					_ = json.NewEncoder(w).Encode(x402.SettleResponse{Success: false, ErrorReason: "settlement_pending", Transaction: hash.Hex(), Network: x402.Network(intent.Network), Payer: payer.Hex(), Amount: "100"})
				case "/progress":
					_ = json.NewEncoder(w).Encode(x402wire.SettlementProgress{Stage: "confirming", Transaction: hash.Hex()})
				default:
					t.Errorf("unexpected upstream call %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			client, err := x402wire.NewClient(x402wire.ClientOptions{BaseURL: upstream.URL, UnsafeAllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}, Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher, err := billing.NewTopupDispatcher(billing.TopupDispatcherOptions{Network: intent.Network, Payments: payments, Accounts: accounts, Facilitator: client, Codec: codec, FingerprintPepper: []byte(strings.Repeat("f", 32)), PublicOrigin: "http://localhost:8080", Methods: []string{"eip3009"}, MaxTimeoutSeconds: 60, AssetName: "Local USD", AssetVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/billing/topup-intents/" + intent.ID + "/pay"
			success := func(http.ResponseWriter, billing.Account, billing.TopupIntent, billing.Payment) {
				t.Error("uncertain payment credited synchronously")
			}
			challenge := httptest.NewRecorder()
			dispatcher.Serve(challenge, httptest.NewRequest("POST", path, nil), intent, success, nil)
			requirement, err := codec.DecodePaymentRequired(challenge.Header())
			if err != nil {
				t.Fatal(err)
			}
			resource := requirement.Resource()
			payload := x402.PaymentPayload{X402Version: 2, Accepted: requirement.SDK(), Resource: &resource, Payload: map[string]any{"signature": "0x" + strings.Repeat("11", 65), "authorization": map[string]any{"from": payer.Hex(), "to": option.Recipient, "value": "100", "validAfter": "0", "validBefore": "9999999999", "nonce": "0x" + strings.Repeat("01", 32)}}}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			signed := base64.StdEncoding.EncodeToString(encoded)
			var logs bytes.Buffer
			registry := observability.NewRegistry("test", "api")
			handler := httpapi.RequestBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/blocks" {
					<-r.Context().Done()
					w.WriteHeader(503)
					return
				}
				// Authentication latency itself exceeds the ordinary request budget.
				select {
				case <-time.After(250 * time.Millisecond):
				case <-r.Context().Done():
					w.WriteHeader(503)
					return
				}
				current, readErr := accounts.TopupIntent(r.Context(), user, intent.ID)
				if readErr != nil {
					t.Error(readErr)
					w.WriteHeader(503)
					return
				}
				dispatcher.Serve(w, r, current, success, nil)
			}), 200*time.Millisecond, 3*time.Second)
			observed := observability.HTTPMiddleware(handler, observability.HTTPOptions{Registry: registry, Logger: observability.NewLogger(observability.LoggerOptions{Writer: &logs}), Route: func(r *http.Request) string {
				if r.Method == "POST" {
					return "/api/v1/billing/topup-intents/{id}/pay"
				}
				return "/api/v1/blocks"
			}})
			send := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", path, nil)
				r.Header.Set(x402wire.PaymentSignatureHeader, signed)
				w := httptest.NewRecorder()
				observed.ServeHTTP(w, r)
				return w
			}
			response := send()
			want := 402
			if timeout {
				want = 504
			}
			if response.Code != want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body)
			}
			if timeout {
				if !strings.Contains(response.Body.String(), "request_timeout") || response.Header().Get(x402wire.PaymentResponseHeader) != "" {
					t.Fatalf("invalid timeout response: %v %s", response.Header(), response.Body)
				}
			} else {
				var settled x402.SettleResponse
				raw, decodeErr := base64.StdEncoding.DecodeString(response.Header().Get(x402wire.PaymentResponseHeader))
				if decodeErr != nil || json.Unmarshal(raw, &settled) != nil || settled.Success || settled.ErrorReason != "settlement_pending" || settled.Transaction != hash.Hex() {
					t.Fatalf("lost pending response: %+v %v", settled, decodeErr)
				}
			}
			assertBudgetObservation(t, &logs, registry, "POST", "/api/v1/billing/topup-intents/{id}/pay", want)
			pending, err := accounts.TopupIntent(t.Context(), user, intent.ID)
			if err != nil || pending.State != billing.TopupIntentSettling || pending.ActivePaymentID == nil {
				t.Fatalf("lost pending intent: %+v %v", pending, err)
			}
			known := pending.TransactionHash
			if known == nil || *known != hash {
				t.Fatalf("lost original hash: %+v", pending)
			}
			account, err := accounts.Account(t.Context(), user)
			if err != nil || account.TotalCreditAtomic != "0" {
				t.Fatalf("premature credit: %+v %v", account, err)
			}
			if replay := send(); replay.Code != 409 || settleCalls.Load() != 1 || verifyCalls.Load() != 1 {
				t.Fatalf("retry resubmitted payment: %d verify=%d settle=%d", replay.Code, verifyCalls.Load(), settleCalls.Load())
			}

			// Browser has left. Only the API result worker may finish the ledger.
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- dispatcher.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			deadline := time.Now().Add(8 * time.Second)
			for {
				account, err = accounts.Account(t.Context(), user)
				if err != nil {
					t.Fatal(err)
				}
				if account.TotalCreditAtomic == "100" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("result worker did not credit: %+v", account)
				}
				time.Sleep(50 * time.Millisecond)
			}
			final, err := accounts.TopupIntent(t.Context(), user, intent.ID)
			if err != nil || final.TransactionHash == nil || *final.TransactionHash != hash || final.State != billing.TopupIntentCredited {
				t.Fatalf("recovery lost original hash: %+v %v", final, err)
			}
			if replay := send(); replay.Code != 409 || settleCalls.Load() != 1 || resultCalls.Load() != 1 {
				t.Fatalf("recovery replay: %d settle=%d result=%d", replay.Code, settleCalls.Load(), resultCalls.Load())
			}
			var entries int
			if err := db.QueryRow(t.Context(), `SELECT count(*) FROM billing_account_entries WHERE kind='topup' AND source_id=$1::uuid`, *pending.ActivePaymentID).Scan(&entries); err != nil || entries != 1 {
				t.Fatalf("credit entries=%d err=%v", entries, err)
			}
			logs.Reset()
			ordinary := httptest.NewRecorder()
			observed.ServeHTTP(ordinary, httptest.NewRequest("GET", "/api/v1/blocks", nil))
			if ordinary.Code != 504 || !strings.Contains(ordinary.Body.String(), "request_timeout") {
				t.Fatalf("ordinary timeout: %d %s", ordinary.Code, ordinary.Body)
			}
			assertBudgetObservation(t, &logs, registry, "GET", "/api/v1/blocks", 504)
		})
	}
}

func assertBudgetObservation(t *testing.T, logs *bytes.Buffer, registry *observability.Registry, method, route string, status int) {
	t.Helper()
	if !strings.Contains(logs.String(), fmt.Sprintf(`"status":%d`, status)) {
		t.Fatalf("wire status absent from log: %s", logs)
	}
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	expected := fmt.Sprintf(`etherview_http_requests_total{method=%q,route=%q,status=%q} 1`, method, route, fmt.Sprint(status))
	if !strings.Contains(response.Body.String(), expected) {
		t.Fatalf("missing final status metric %s", expected)
	}
}
