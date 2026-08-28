package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type topupPaymentLedgerFake struct {
	reserveCalls int
	verifyCalls  int
	payment      Payment
}

func (fake *topupPaymentLedgerFake) Reserve(
	_ context.Context,
	input ReserveInput,
) (Reservation, error) {
	fake.reserveCalls++
	fake.payment = Payment{
		ID:        "74e5f6a2-30ef-4d7f-93b8-e430f1fdfac1",
		Operation: input.Operation, Method: input.Method, Purpose: input.Purpose,
		Network: input.Network, Asset: input.Asset, AmountAtomic: input.AmountAtomic,
		Recipient: input.Recipient, State: StateReserved,
	}
	return Reservation{
		Owned: true, Owner: "74e5f6a2-30ef-4d7f-93b8-e430f1fdfac2",
		Payment: fake.payment,
	}, nil
}

func (fake *topupPaymentLedgerFake) MarkVerified(
	_ context.Context,
	input VerifiedInput,
) (Payment, error) {
	fake.verifyCalls++
	fake.payment.State = StateVerified
	fake.payment.Payer = &input.Payer
	fake.payment.UserID = input.UserID
	return fake.payment, nil
}

func (fake *topupPaymentLedgerFake) Get(context.Context, string) (Payment, error) {
	return fake.payment, nil
}

type topupAccountLedgerFake struct {
	beginCalls   int
	failCalls    int
	pendingCalls int
	unknownCalls int
	creditCalls  int
	intent       TopupIntent
}

func (fake *topupAccountLedgerFake) BeginTopupSettlement(
	context.Context, string, string, string, time.Time,
) error {
	fake.beginCalls++
	return nil
}

func (fake *topupAccountLedgerFake) MarkTopupSettlementUnknown(
	context.Context, string, string, string, time.Time,
) error {
	fake.unknownCalls++
	return nil
}

func (fake *topupAccountLedgerFake) MarkTopupSettlementPending(
	context.Context, string, string, string, common.Hash, time.Time,
) error {
	fake.pendingCalls++
	return nil
}

func (fake *topupAccountLedgerFake) CreditTopup(
	context.Context, string, string, common.Hash, time.Time,
) (Account, error) {
	fake.creditCalls++
	return Account{UserID: fake.intent.UserID, AvailableAtomic: fake.intent.AmountAtomic}, nil
}

func (fake *topupAccountLedgerFake) FailTopupPayment(
	context.Context, string, string, string, string, time.Time,
) error {
	fake.failCalls++
	return nil
}

func (fake *topupAccountLedgerFake) FailTopupSettlement(
	context.Context, string, string, string, string, time.Time,
) error {
	fake.failCalls++
	return nil
}

func (fake *topupAccountLedgerFake) TopupIntent(
	context.Context, string, string,
) (TopupIntent, error) {
	return fake.intent, nil
}

type topupFacilitatorFake struct {
	verifyCalls int
	settleCalls int
	verify      *x402.VerifyResponse
	verifyErr   error
	settle      *x402.SettleResponse
	settleErr   error
}

func (fake *topupFacilitatorFake) VerifyPayment(
	context.Context, x402wire.Payment, x402wire.Requirement,
) (*x402.VerifyResponse, error) {
	fake.verifyCalls++
	return fake.verify, fake.verifyErr
}

func (fake *topupFacilitatorFake) SettlePayment(
	context.Context, x402wire.Payment, x402wire.Requirement,
) (*x402.SettleResponse, error) {
	fake.settleCalls++
	return fake.settle, fake.settleErr
}

func (*topupFacilitatorFake) OriginDigest() [32]byte { return [32]byte{0x44} }

func TestTopupDispatcherEmitsOrderedMethodsAndRejectsPayerMismatchBeforeLedger(t *testing.T) {
	t.Parallel()
	dispatcher, payments, accounts, facilitator, codec, intent := testTopupDispatcher(t)
	request := httptest.NewRequest(
		http.MethodPost,
		"http://localhost:8080/api/v1/billing/topup-intents/"+intent.ID+"/pay",
		nil,
	)
	recorder := httptest.NewRecorder()
	dispatcher.Serve(recorder, request, intent, func(http.ResponseWriter, Account, TopupIntent, Payment) {
		t.Fatal("missing payment succeeded")
	}, nil)
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("challenge status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	requiredHeader := make(http.Header)
	requiredHeader.Set(x402wire.PaymentRequiredHeader, recorder.Header().Get(x402wire.PaymentRequiredHeader))
	requirements, err := codec.DecodePaymentRequiredAll(requiredHeader)
	if err != nil || len(requirements) != 2 ||
		requirements[0].TransferMethod() != x402wire.TransferMethodEIP3009 ||
		requirements[1].TransferMethod() != x402wire.TransferMethodPermit2 {
		t.Fatalf("requirements=%+v error=%v", requirements, err)
	}

	mismatched := topupPaymentHeader(t, requirements[0], topupTestAddress(0x55))
	request = httptest.NewRequest(http.MethodPost, requirements[0].Resource().URL, nil)
	request.Header.Set(x402wire.PaymentSignatureHeader, mismatched)
	recorder = httptest.NewRecorder()
	dispatcher.Serve(recorder, request, intent, func(http.ResponseWriter, Account, TopupIntent, Payment) {
		t.Fatal("payer mismatch succeeded")
	}, nil)
	if recorder.Code != http.StatusBadRequest || payments.reserveCalls != 0 ||
		facilitator.verifyCalls != 0 || accounts.beginCalls != 0 {
		t.Fatalf("mismatch status=%d reserve=%d verify=%d begin=%d body=%s",
			recorder.Code, payments.reserveCalls, facilitator.verifyCalls,
			accounts.beginCalls, recorder.Body.String())
	}
}

func TestTopupDispatcherPersistsOnlyStrictSettlementPendingHash(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		transaction string
		wantPending int
		wantUnknown int
	}{
		{name: "valid pending", transaction: "0x" + strings.Repeat("aa", 32), wantPending: 1},
		{name: "missing hash", wantUnknown: 1},
		{name: "malformed hash", transaction: "0x12", wantUnknown: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dispatcher, payments, accounts, facilitator, codec, intent := testTopupDispatcher(t)
			challenge := httptest.NewRecorder()
			dispatcher.Serve(
				challenge,
				httptest.NewRequest(http.MethodPost, "http://localhost:8080/pay", nil),
				intent,
				func(http.ResponseWriter, Account, TopupIntent, Payment) {}, nil,
			)
			header := make(http.Header)
			header.Set(x402wire.PaymentRequiredHeader, challenge.Header().Get(x402wire.PaymentRequiredHeader))
			requirements, err := codec.DecodePaymentRequiredAll(header)
			if err != nil {
				t.Fatal(err)
			}
			facilitator.verify = &x402.VerifyResponse{IsValid: true, Payer: intent.Payer.Hex()}
			facilitator.settle = &x402.SettleResponse{
				Success: false, ErrorReason: "settlement_pending",
				Payer: intent.Payer.Hex(), Transaction: test.transaction,
				Network: x402.Network(intent.Network), Amount: intent.AmountAtomic,
			}
			facilitator.settleErr = &x402wire.BoundaryError{
				Phase: x402wire.PhaseSettle, Class: x402wire.FailureSettlementPending,
				Code: x402wire.CodeSettlementPending,
			}
			request := httptest.NewRequest(http.MethodPost, requirements[0].Resource().URL, nil)
			request.Header.Set(
				x402wire.PaymentSignatureHeader,
				topupPaymentHeader(t, requirements[0], intent.Payer),
			)
			recorder := httptest.NewRecorder()
			dispatcher.Serve(recorder, request, intent, func(http.ResponseWriter, Account, TopupIntent, Payment) {
				t.Fatal("pending settlement succeeded")
			}, nil)
			if recorder.Code != http.StatusPaymentRequired && recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if payments.reserveCalls != 1 || payments.verifyCalls != 1 ||
				accounts.beginCalls != 1 || accounts.pendingCalls != test.wantPending ||
				accounts.unknownCalls != test.wantUnknown || accounts.creditCalls != 0 {
				t.Fatalf("payments=%+v accounts=%+v", payments, accounts)
			}
			if test.wantPending == 1 && recorder.Header().Get(x402wire.PaymentResponseHeader) == "" {
				t.Fatal("valid pending response omitted PAYMENT-RESPONSE")
			}
			if test.wantPending == 0 && recorder.Header().Get(x402wire.PaymentResponseHeader) != "" {
				t.Fatal("invalid pending response exposed PAYMENT-RESPONSE")
			}
		})
	}
}

func testTopupDispatcher(
	t *testing.T,
) (*TopupDispatcher, *topupPaymentLedgerFake, *topupAccountLedgerFake, *topupFacilitatorFake, *x402wire.Codec, TopupIntent) {
	t.Helper()
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	intent := TopupIntent{
		ID:      "74e5f6a2-30ef-4d7f-93b8-e430f1fdfac4",
		UserID:  "74e5f6a2-30ef-4d7f-93b8-e430f1fdfac5",
		Network: "eip155:31337", Asset: topupTestAddress(0x11),
		AmountAtomic: "1000", Recipient: topupTestAddress(0x22),
		Payer: topupTestAddress(0x33), State: TopupIntentOpen,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	payments := &topupPaymentLedgerFake{}
	accounts := &topupAccountLedgerFake{intent: intent}
	facilitator := &topupFacilitatorFake{}
	dispatcher, err := NewTopupDispatcher(TopupDispatcherOptions{
		Payments: payments, Accounts: accounts, Facilitator: facilitator,
		Codec: codec, FingerprintPepper: []byte(strings.Repeat("f", 32)),
		PublicOrigin:      "http://localhost:8080",
		Methods:           []string{x402wire.TransferMethodEIP3009, x402wire.TransferMethodPermit2},
		MaxTimeoutSeconds: 60, AssetName: "Local USD", AssetVersion: "1",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher, payments, accounts, facilitator, codec, intent
}

func topupTestAddress(value byte) common.Address {
	var address common.Address
	for index := range address {
		address[index] = value
	}
	return address
}

func topupPaymentHeader(
	t *testing.T,
	requirement x402wire.Requirement,
	payer common.Address,
) string {
	t.Helper()
	resource := requirement.Resource()
	payment := x402.PaymentPayload{
		X402Version: 2,
		Payload: map[string]any{
			"signature": "0x" + strings.Repeat("11", 65),
			"authorization": map[string]any{
				"from": payer.Hex(), "to": requirement.SDK().PayTo,
				"value": requirement.SDK().Amount, "validAfter": "0",
				"validBefore": "9999999999", "nonce": "0x" + strings.Repeat("01", 32),
			},
		},
		Accepted: requirement.SDK(), Resource: &resource,
	}
	encoded, err := json.Marshal(payment)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
