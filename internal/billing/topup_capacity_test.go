package billing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func TestTopupCapacityRefusalFinishesWithoutUnknownSettlement(t *testing.T) {
	for _, reason := range []string{"chain_busy", "chain_pending"} {
		t.Run(reason, func(t *testing.T) {
			dispatcher, _, accounts, _, codec, intent := testTopupDispatcher(t)
			facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/verify":
					_ = json.NewEncoder(w).Encode(x402.VerifyResponse{IsValid: true, Payer: intent.Payer.Hex()})
				case "/settle":
					_ = json.NewEncoder(w).Encode(x402.SettleResponse{Success: false, ErrorReason: reason, Network: x402.Network(intent.Network), Payer: intent.Payer.Hex(), Amount: intent.AmountAtomic})
				default:
					t.Errorf("unexpected facilitator call %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer facilitator.Close()
			client, err := x402wire.NewClient(x402wire.ClientOptions{BaseURL: facilitator.URL, UnsafeAllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}, Timeout: time.Second, MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			dispatcher.facilitator = client
			challenge := httptest.NewRecorder()
			dispatcher.Serve(challenge, httptest.NewRequest(http.MethodPost, "http://localhost:8080/pay", nil), intent, func(http.ResponseWriter, Account, TopupIntent, Payment) { t.Fatal("missing payment succeeded") }, nil)
			header := make(http.Header)
			header.Set(x402wire.PaymentRequiredHeader, challenge.Header().Get(x402wire.PaymentRequiredHeader))
			requirements, err := codec.DecodePaymentRequiredAll(header)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, requirements[0].Resource().URL, nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, topupPaymentHeader(t, requirements[0], intent.Payer))
			response := httptest.NewRecorder()
			dispatcher.Serve(response, request, intent, func(http.ResponseWriter, Account, TopupIntent, Payment) { t.Fatal("refused payment credited") }, nil)
			if response.Code != http.StatusPaymentRequired || accounts.beginCalls != 1 || accounts.failCalls != 1 || accounts.unknownCalls != 0 || accounts.pendingCalls != 0 || accounts.creditCalls != 0 {
				t.Fatalf("refusal stranded intent: status=%d accounts=%+v body=%s", response.Code, accounts, response.Body)
			}
		})
	}
}
