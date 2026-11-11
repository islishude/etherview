//go:build integration

package billing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

// Exercise the real facilitator HTTP client, response codec and PostgreSQL
// ledgers together. The facilitator fixture controls only its receipt outcome;
// the facilitator package separately verifies journal-before-broadcast recovery.
func TestTopupPendingHTTPResponseAndDurableReconciliation(t *testing.T) {
	db := newBillingPostgres(t)
	userID := uuid.NewString()
	payer := topupTestAddress(0x33)
	insertBillingUser(t, db, userID, payer)
	prepaid := newTestPrepaidLedger(t, db)
	payments, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	option := testPaymentOption("eip155:11155111", topupTestAddress(0x11), topupTestAddress(0x22))
	intent, err := prepaid.CreateTopupIntent(t.Context(), CreateTopupIntentInput{
		UserID: userID, Payer: payer, AmountAtomic: "100", ObservedAt: now, PaymentOption: option,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
	var settleCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/verify":
			_ = json.NewEncoder(w).Encode(x402.VerifyResponse{IsValid: true, Payer: payer.Hex()})
		case "/settle":
			settleCalls.Add(1)
			_ = json.NewEncoder(w).Encode(x402.SettleResponse{
				Success: false, ErrorReason: "settlement_pending", Transaction: hash.Hex(),
				Payer: payer.Hex(), Network: x402.Network(intent.Network), Amount: intent.AmountAtomic,
			})
		case "/progress":
			_ = json.NewEncoder(w).Encode(x402wire.SettlementProgress{Stage: "confirming", Transaction: hash.Hex()})
		default:
			t.Errorf("unexpected facilitator request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	client, err := x402wire.NewClient(x402wire.ClientOptions{
		BaseURL: upstream.URL, UnsafeAllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"},
		Timeout: time.Second, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, _, _, _, _, _ := testTopupDispatcher(t)
	dispatcher.payments, dispatcher.accounts, dispatcher.facilitator = payments, prepaid, client
	dispatcher.now = func() time.Time { return now }
	requirements, err := dispatcher.requirements(intent)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, requirements[0].Resource().URL, nil)
	request.Header.Set(x402wire.PaymentSignatureHeader, topupPaymentHeader(t, requirements[0], payer))
	response := httptest.NewRecorder()
	unexpectedSuccess := func(http.ResponseWriter, Account, TopupIntent, Payment) { t.Fatal("pending payment credited") }
	dispatcher.Serve(response, request, intent, unexpectedSuccess, nil)
	var settled x402.SettleResponse
	raw, decodeErr := base64.StdEncoding.DecodeString(response.Header().Get(x402wire.PaymentResponseHeader))
	if response.Code != http.StatusPaymentRequired || decodeErr != nil || json.Unmarshal(raw, &settled) != nil ||
		settled.Success || settled.ErrorReason != "settlement_pending" || settled.Transaction != hash.Hex() {
		t.Fatalf("pending response: status=%d settlement=%+v error=%v body=%s", response.Code, settled, decodeErr, response.Body)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "0", "0", "0")

	// Reconstruct both ledgers and the dispatcher, recovering only durable state.
	prepaid = newTestPrepaidLedger(t, db)
	payments, err = NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := prepaid.TopupIntent(t.Context(), userID, intent.ID)
	if err != nil || pending.State != TopupIntentSettling || pending.TransactionHash == nil ||
		*pending.TransactionHash != hash || pending.ActivePaymentID == nil ||
		pending.FailureCode == nil || *pending.FailureCode != "settlement_pending" {
		t.Fatalf("pending intent lost identity: %+v error=%v", pending, err)
	}
	paymentID := *pending.ActivePaymentID
	payment, err := payments.Get(t.Context(), paymentID)
	if err != nil || payment.State != StateSettling || payment.TransactionHash == nil || *payment.TransactionHash != hash {
		t.Fatalf("pending payment lost identity: %+v error=%v", payment, err)
	}
	assertBillingEntryCount(t, db, "topup", paymentID, 0)
	dispatcher, _, _, _, _, _ = testTopupDispatcher(t)
	dispatcher.payments, dispatcher.accounts, dispatcher.facilitator = payments, prepaid, client
	dispatcher.now = func() time.Time { return now }
	replay := httptest.NewRecorder()
	dispatcher.Serve(replay, request, pending, unexpectedSuccess, nil)
	if replay.Code != http.StatusConflict || settleCalls.Load() != 1 {
		t.Fatalf("pending replay resubmitted: status=%d settle_calls=%d", replay.Code, settleCalls.Load())
	}
	if _, err = prepaid.ReconcileTopupSettled(t.Context(), paymentID, common.Hash{31: 1}, now.Add(time.Second)); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("reconciliation replaced original hash: %v", err)
	}
	if _, err = prepaid.ReconcileTopupSettled(t.Context(), paymentID, hash, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = prepaid.ReconcileTopupSettled(t.Context(), paymentID, hash, now.Add(2*time.Second)); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("duplicate reconciliation credited twice: %v", err)
	}
	credited, err := prepaid.TopupIntent(t.Context(), userID, intent.ID)
	if err != nil || credited.State != TopupIntentCredited || credited.TransactionHash == nil || *credited.TransactionHash != hash {
		t.Fatalf("credited intent: %+v error=%v", credited, err)
	}
	replay = httptest.NewRecorder()
	dispatcher.Serve(replay, request, credited, unexpectedSuccess, nil)
	if replay.Code != http.StatusConflict || settleCalls.Load() != 1 {
		t.Fatalf("credited replay resubmitted: status=%d settle_calls=%d", replay.Code, settleCalls.Load())
	}
	assertBillingAccountAmounts(t, prepaid, userID, "100", "0", "100")
	assertBillingEntryCount(t, db, "topup", paymentID, 1)
}
