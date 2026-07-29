package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
	x402 "github.com/x402-foundation/x402/go/v2"
)

const (
	testAsset     = "0x1111111111111111111111111111111111111111"
	testRecipient = "0x2222222222222222222222222222222222222222"
	testPayer     = "0x3333333333333333333333333333333333333333"
	testTxHash    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type memoryPaymentLedger struct {
	mu sync.Mutex

	reserved *ReserveInput
	payment  Payment
	owner    string

	reserveCalls    int
	verifyCalls     int
	handlerCalls    int
	settlingCalls   int
	settledCalls    int
	failedCalls     int
	unknownCalls    int
	lastFailureCode string

	reserveErr       error
	verifyErr        error
	handlerErr       error
	settlingErr      error
	settledErr       error
	failedErr        error
	unknownErr       error
	duplicatePayment *Payment
}

func (ledger *memoryPaymentLedger) Reserve(
	_ context.Context,
	input ReserveInput,
) (Reservation, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.reserveCalls++
	if err := validateReserveInput(input); err != nil {
		return Reservation{}, err
	}
	if ledger.reserveErr != nil {
		return Reservation{}, ledger.reserveErr
	}
	if ledger.duplicatePayment != nil {
		return Reservation{Payment: *ledger.duplicatePayment}, nil
	}
	if ledger.reserved != nil {
		if !sameReserveInput(*ledger.reserved, input) {
			return Reservation{}, ErrIntegrity
		}
		return Reservation{Payment: ledger.payment}, nil
	}
	copied := input
	ledger.reserved = &copied
	ledger.owner = "00000000-0000-4000-8000-000000000002"
	ledger.payment = Payment{
		ID:      "00000000-0000-4000-8000-000000000001",
		ChainID: 84532, Method: http.MethodGet, Operation: input.Operation,
		Network: input.Network, Asset: input.Asset, AmountAtomic: input.AmountAtomic,
		Recipient: input.Recipient, State: StateReserved,
		ReservationExpiresAt: input.ObservedAt.Add(2 * time.Minute),
		CreatedAt:            input.ObservedAt, UpdatedAt: input.ObservedAt,
		fingerprint: input.Fingerprint, resourceDigest: input.ResourceDigest,
		requirementDigest: input.RequirementDigest, facilitatorDigest: input.FacilitatorDigest,
	}
	return Reservation{Payment: ledger.payment, Owned: true, Owner: ledger.owner}, nil
}

func sameReserveInput(left, right ReserveInput) bool {
	return left.Fingerprint == right.Fingerprint &&
		left.Operation == right.Operation &&
		left.ResourceDigest == right.ResourceDigest &&
		left.RequirementDigest == right.RequirementDigest &&
		left.Network == right.Network && left.Asset == right.Asset &&
		left.AmountAtomic == right.AmountAtomic &&
		left.Recipient == right.Recipient &&
		left.FacilitatorDigest == right.FacilitatorDigest
}

func (ledger *memoryPaymentLedger) MarkVerified(
	_ context.Context,
	input VerifiedInput,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.verifyCalls++
	if ledger.verifyErr != nil {
		return Payment{}, ledger.verifyErr
	}
	if !ledger.validFence(input.PaymentID, input.Owner) || ledger.payment.State != StateReserved {
		return Payment{}, ErrStateConflict
	}
	ledger.payment.State = StateVerified
	ledger.payment.Payer = &input.Payer
	ledger.payment.UserID = input.UserID
	ledger.payment.APIKeyPrefix = input.APIKeyPrefix
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) StartHandler(
	_ context.Context,
	id, owner string,
	at time.Time,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.handlerCalls++
	if ledger.handlerErr != nil {
		return Payment{}, ledger.handlerErr
	}
	if !ledger.validFence(id, owner) || ledger.payment.State != StateVerified ||
		ledger.payment.HandlerStartedAt != nil {
		return Payment{}, ErrStateConflict
	}
	ledger.payment.HandlerStartedAt = &at
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) BeginSettlement(
	_ context.Context,
	id, owner string,
	at time.Time,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.settlingCalls++
	if ledger.settlingErr != nil {
		return Payment{}, ledger.settlingErr
	}
	if !ledger.validFence(id, owner) || ledger.payment.State != StateVerified ||
		ledger.payment.HandlerStartedAt == nil {
		return Payment{}, ErrStateConflict
	}
	ledger.payment.State = StateSettling
	ledger.payment.SettlingAt = &at
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) MarkSettlementUnknown(
	_ context.Context,
	id, owner string,
	at time.Time,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.unknownCalls++
	if ledger.unknownErr != nil {
		return Payment{}, ledger.unknownErr
	}
	if !ledger.validFence(id, owner) || ledger.payment.State != StateSettling ||
		ledger.payment.FailureCode != nil {
		return Payment{}, ErrStateConflict
	}
	code := "settlement_unknown"
	ledger.payment.FailureCode = &code
	ledger.payment.UpdatedAt = at
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) MarkSettled(
	_ context.Context,
	id, owner string,
	hash common.Hash,
	at time.Time,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.settledCalls++
	if ledger.settledErr != nil {
		return Payment{}, ledger.settledErr
	}
	if !ledger.validFence(id, owner) || ledger.payment.State != StateSettling ||
		ledger.payment.FailureCode != nil {
		return Payment{}, ErrStateConflict
	}
	ledger.payment.State = StateSettled
	ledger.payment.TransactionHash = &hash
	ledger.payment.SettledAt = &at
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) MarkFailed(
	_ context.Context,
	id, owner, code string,
	at time.Time,
) (Payment, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.failedCalls++
	ledger.lastFailureCode = code
	if ledger.failedErr != nil {
		return Payment{}, ledger.failedErr
	}
	if !ledger.validFence(id, owner) ||
		ledger.payment.State != StateReserved &&
			ledger.payment.State != StateVerified &&
			ledger.payment.State != StateSettling {
		return Payment{}, ErrStateConflict
	}
	ledger.payment.State = StateFailed
	ledger.payment.FailureCode = &code
	ledger.payment.FailedAt = &at
	return ledger.payment, nil
}

func (ledger *memoryPaymentLedger) validFence(id, owner string) bool {
	return ledger.payment.ID == id && ledger.owner == owner
}

type fakePaymentFacilitator struct {
	verifyCalls atomic.Int32
	settleCalls atomic.Int32

	verifyErr   error
	settleErr   error
	verify      *x402.VerifyResponse
	settle      *x402.SettleResponse
	verifyBlock <-chan struct{}
}

func (facilitator *fakePaymentFacilitator) VerifyPayment(
	ctx context.Context,
	_ x402wire.Payment,
	_ x402wire.Requirement,
) (*x402.VerifyResponse, error) {
	facilitator.verifyCalls.Add(1)
	if facilitator.verifyBlock != nil {
		select {
		case <-facilitator.verifyBlock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return facilitator.verify, facilitator.verifyErr
}

func (facilitator *fakePaymentFacilitator) SettlePayment(
	context.Context,
	x402wire.Payment,
	x402wire.Requirement,
) (*x402.SettleResponse, error) {
	facilitator.settleCalls.Add(1)
	return facilitator.settle, facilitator.settleErr
}

func (*fakePaymentFacilitator) OriginDigest() [32]byte {
	return [32]byte{0x42}
}

type fakePayerResolver struct {
	calls atomic.Int32
	id    string
	found bool
	err   error
	payer common.Address
}

func (resolver *fakePayerResolver) UserIDForPayer(
	_ context.Context,
	payer common.Address,
) (string, bool, error) {
	resolver.calls.Add(1)
	resolver.payer = payer
	return resolver.id, resolver.found, resolver.err
}

func testBillingConfig(operation, access, amount string) config.Config {
	cfg := config.Default()
	cfg.Features.X402Billing = true
	cfg.Server.PublicURL = "http://localhost:8080"
	cfg.Chain.ID = 84532
	cfg.Billing.Network = "eip155:84532"
	cfg.Billing.Asset = testAsset
	cfg.Billing.Recipient = testRecipient
	cfg.Billing.AssetEIP712Name = "USDC"
	cfg.Billing.AssetEIP712Version = "2"
	cfg.Billing.FingerprintPepper = strings.Repeat("f", 32)
	cfg.Billing.Routes = map[string]config.BillingRouteConfig{
		operation: {Access: access, AmountAtomic: amount},
	}
	return cfg
}

func newTestDispatcher(
	t *testing.T,
	cfg config.Config,
	ledger *memoryPaymentLedger,
	facilitator *fakePaymentFacilitator,
	resolver PayerUserResolver,
) *HTTPDispatcher {
	t.Helper()
	if facilitator.verify == nil && facilitator.verifyErr == nil {
		facilitator.verify = &x402.VerifyResponse{IsValid: true, Payer: testPayer}
	}
	if facilitator.settle == nil && facilitator.settleErr == nil {
		facilitator.settle = &x402.SettleResponse{
			Success: true, Payer: testPayer, Transaction: testTxHash,
			Network: x402.Network(cfg.Billing.Network),
			Amount:  cfg.Billing.Routes[firstRoute(cfg)].AmountAtomic,
		}
	}
	dispatcher, err := NewHTTPDispatcher(DispatcherOptions{
		Config: cfg, Ledger: ledger, Facilitator: facilitator,
		UserResolver: resolver,
		Now: func() time.Time {
			return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func firstRoute(cfg config.Config) string {
	for operation := range cfg.Billing.Routes {
		return operation
	}
	return ""
}

func requestPaymentHeader(
	t *testing.T,
	dispatcher *HTTPDispatcher,
	spec apiops.Spec,
	target string,
	nonceByte string,
) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-ID", "request-1")
	request := httptest.NewRequest(http.MethodGet, target, nil)
	dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, http.NotFoundHandler())
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("challenge status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	encoded := recorder.Header().Get(x402wire.PaymentRequiredHeader)
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var required x402.PaymentRequired
	if err := json.Unmarshal(decoded, &required); err != nil {
		t.Fatal(err)
	}
	if len(required.Accepts) != 1 || required.Resource == nil {
		t.Fatalf("invalid required header: %#v", required)
	}
	payment := x402.PaymentPayload{
		X402Version: x402wire.X402Version,
		Payload: map[string]any{
			"signature": "0x" + strings.Repeat("11", 65),
			"authorization": map[string]any{
				"from": testPayer, "to": required.Accepts[0].PayTo,
				"value":      required.Accepts[0].Amount,
				"validAfter": "0", "validBefore": "9999999999",
				"nonce": "0x" + strings.Repeat(nonceByte, 32),
			},
		},
		Accepted: required.Accepts[0],
		Resource: required.Resource,
	}
	body, err := json.Marshal(payment)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(body)
}

func TestHTTPDispatcherPolicyAndMissingPayment(t *testing.T) {
	t.Parallel()
	cfg := testBillingConfig("listBlocks", "x402", "1000")
	ledger := &memoryPaymentLedger{}
	facilitator := &fakePaymentFacilitator{}
	dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
	if dispatcher.Access("getStatus", APIKeyIdentity{}) != AccessFree ||
		dispatcher.Access("listBlocks", APIKeyIdentity{Authenticated: true, Prefix: "abcdefghij"}) != AccessPaid {
		t.Fatal("pure x402 policy did not stay paid")
	}

	cfg.Billing.Routes["listBlocks"] = config.BillingRouteConfig{
		Access: "api_key_or_x402", AmountAtomic: "1000",
	}
	dispatcher = newTestDispatcher(t, cfg, &memoryPaymentLedger{}, &fakePaymentFacilitator{}, nil)
	if dispatcher.Access("listBlocks", APIKeyIdentity{}) != AccessPaid ||
		dispatcher.Access("listBlocks", APIKeyIdentity{Authenticated: true}) != AccessAPIKey {
		t.Fatal("api_key_or_x402 policy mismatch")
	}

	spec, _ := apiops.Lookup("listBlocks")
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-ID", "request-1")
	dispatcher.ServePaid(
		recorder, httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil),
		spec, APIKeyIdentity{}, http.NotFoundHandler(),
	)
	if recorder.Code != http.StatusPaymentRequired ||
		recorder.Header().Get(x402wire.PaymentRequiredHeader) == "" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if ledger.reserveCalls != 0 || facilitator.verifyCalls.Load() != 0 {
		t.Fatal("missing payment reached the ledger or facilitator")
	}
}

func TestHTTPDispatcherRejectsInvalidHeadersBeforeReservation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header func(string) []string
		target string
	}{
		{
			name: "malformed",
			header: func(string) []string {
				return []string{"not-base64"}
			},
			target: "/api/v1/blocks",
		},
		{
			name: "multiple",
			header: func(valid string) []string {
				return []string{valid, valid}
			},
			target: "/api/v1/blocks",
		},
		{
			name: "oversized",
			header: func(string) []string {
				return []string{strings.Repeat("A", 16<<10+1)}
			},
			target: "/api/v1/blocks",
		},
		{
			name: "resource mismatch",
			header: func(valid string) []string {
				return []string{valid}
			},
			target: "/api/v1/blocks?limit=2",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			ledger := &memoryPaymentLedger{}
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			spec, _ := apiops.Lookup("listBlocks")
			nonce := []string{"10", "11", "12", "13"}[index]
			valid := requestPaymentHeader(
				t, dispatcher, spec, "/api/v1/blocks", nonce,
			)
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header[x402wire.PaymentSignatureHeader] = test.header(valid)
			handlerCalls := 0
			response := httptest.NewRecorder()
			dispatcher.ServePaid(
				response, request, spec, APIKeyIdentity{},
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					handlerCalls++
				}),
			)
			if response.Code != http.StatusBadRequest || ledger.reserveCalls != 0 ||
				facilitator.verifyCalls.Load() != 0 || handlerCalls != 0 {
				t.Fatalf(
					"status=%d reserve=%d verify=%d handler=%d body=%s",
					response.Code, ledger.reserveCalls,
					facilitator.verifyCalls.Load(), handlerCalls,
					response.Body.String(),
				)
			}
		})
	}
}

func TestHTTPDispatcherSettlesBeforeReleasingBoundedResponse(t *testing.T) {
	t.Parallel()
	cfg := testBillingConfig("listBlocks", "x402", "1000")
	ledger := &memoryPaymentLedger{}
	facilitator := &fakePaymentFacilitator{}
	resolver := &fakePayerResolver{
		id: "00000000-0000-4000-8000-000000000099", found: true,
	}
	dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, resolver)
	spec, _ := apiops.Lookup("listBlocks")
	header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", "01")

	handlerCalls := 0
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlerCalls++
		if operation, ok := PaidOperationFrom(request.Context()); !ok || operation != "listBlocks" {
			t.Fatal("paid context is missing or wrong")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Set-Cookie", "secret=value")
		writer.Header().Set("Connection", "X-Hop")
		writer.Header().Set("X-Hop", "secret")
		writer.Header().Set(x402wire.PaymentResponseHeader, "forged")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"data":"paid"}`))
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Host = "attacker.invalid"
	request.Header.Set(x402wire.PaymentSignatureHeader, header)
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-ID", "request-1")
	recorder.Header().Set("Cache-Control", "no-store")
	dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{
		Authenticated: true, Prefix: "abcdefghij",
	}, handler)

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"data":"paid"}` {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get(x402wire.PaymentResponseHeader) == "" ||
		recorder.Header().Get(x402wire.PaymentResponseHeader) == "forged" {
		t.Fatalf("payment response=%q", recorder.Header().Get(x402wire.PaymentResponseHeader))
	}
	for _, name := range []string{"Set-Cookie", "Connection", "X-Hop"} {
		if recorder.Header().Get(name) != "" {
			t.Fatalf("unsafe header %s leaked", name)
		}
	}
	if handlerCalls != 1 || facilitator.verifyCalls.Load() != 1 ||
		facilitator.settleCalls.Load() != 1 || ledger.settledCalls != 1 ||
		ledger.payment.State != StateSettled || resolver.calls.Load() != 1 {
		t.Fatalf(
			"calls handler=%d verify=%d settle=%d settled=%d state=%s resolver=%d",
			handlerCalls, facilitator.verifyCalls.Load(), facilitator.settleCalls.Load(),
			ledger.settledCalls, ledger.payment.State, resolver.calls.Load(),
		)
	}
	if ledger.payment.UserID == nil || *ledger.payment.UserID != resolver.id ||
		ledger.payment.APIKeyPrefix == nil || *ledger.payment.APIKeyPrefix != "abcdefghij" {
		t.Fatalf("attribution=%#v", ledger.payment)
	}
	expectedPayer, _ := addressFromHex(testPayer)
	if resolver.payer != expectedPayer {
		t.Fatalf("resolved payer=%x want=%x", resolver.payer, expectedPayer)
	}
}

func TestHTTPDispatcherPayerAssociationIsOptional(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		resolver      *fakePayerResolver
		wantResolvers int32
	}{
		{name: "user auth disabled"},
		{
			name: "payer has no existing user",
			resolver: &fakePayerResolver{
				found: false,
			},
			wantResolvers: 1,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			ledger := &memoryPaymentLedger{}
			facilitator := &fakePaymentFacilitator{}
			var resolver PayerUserResolver
			if test.resolver != nil {
				resolver = test.resolver
			}
			dispatcher := newTestDispatcher(
				t, cfg, ledger, facilitator, resolver,
			)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(
				t, dispatcher, spec, "/api/v1/blocks",
				[]string{"31", "32"}[index],
			)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			recorder := httptest.NewRecorder()
			dispatcher.ServePaid(
				recorder, request, spec, APIKeyIdentity{},
				http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					_, _ = writer.Write([]byte(`{"data":"accountless"}`))
				}),
			)
			if recorder.Code != http.StatusOK ||
				ledger.payment.State != StateSettled ||
				ledger.payment.UserID != nil ||
				facilitator.verifyCalls.Load() != 1 ||
				facilitator.settleCalls.Load() != 1 {
				t.Fatalf(
					"status=%d payment=%+v verify=%d settle=%d body=%s",
					recorder.Code, ledger.payment,
					facilitator.verifyCalls.Load(),
					facilitator.settleCalls.Load(), recorder.Body.String(),
				)
			}
			if test.resolver != nil &&
				test.resolver.calls.Load() != test.wantResolvers {
				t.Fatalf(
					"resolver calls=%d want=%d",
					test.resolver.calls.Load(), test.wantResolvers,
				)
			}
		})
	}
}

func TestHTTPDispatcherPreHandlerLedgerFailuresFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		configure     func(*memoryPaymentLedger) PayerUserResolver
		wantVerify    int32
		wantFailCalls int
		wantFailure   string
	}{
		{
			name: "reservation",
			configure: func(ledger *memoryPaymentLedger) PayerUserResolver {
				ledger.reserveErr = errors.New("reservation unavailable")
				return nil
			},
			wantVerify: 0, wantFailCalls: 0,
		},
		{
			name: "verified transition",
			configure: func(ledger *memoryPaymentLedger) PayerUserResolver {
				ledger.verifyErr = errors.New("verified transition unavailable")
				return nil
			},
			wantVerify: 1, wantFailCalls: 1, wantFailure: "ledger_verify_failed",
		},
		{
			name: "payer association",
			configure: func(*memoryPaymentLedger) PayerUserResolver {
				return &fakePayerResolver{
					err: errors.New("payer association unavailable"),
				}
			},
			wantVerify: 1, wantFailCalls: 1,
			wantFailure: "payer_attribution_unavailable",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			ledger := &memoryPaymentLedger{}
			resolver := test.configure(ledger)
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(
				t, cfg, ledger, facilitator, resolver,
			)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(
				t, dispatcher, spec, "/api/v1/blocks",
				[]string{"17", "18", "19"}[index],
			)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			handlerCalls := 0
			response := httptest.NewRecorder()
			dispatcher.ServePaid(
				response, request, spec, APIKeyIdentity{},
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					handlerCalls++
				}),
			)
			if response.Code != http.StatusServiceUnavailable ||
				handlerCalls != 0 ||
				facilitator.verifyCalls.Load() != test.wantVerify ||
				facilitator.settleCalls.Load() != 0 ||
				ledger.failedCalls != test.wantFailCalls {
				t.Fatalf(
					"status=%d handler=%d verify=%d settle=%d failed=%d body=%s",
					response.Code, handlerCalls,
					facilitator.verifyCalls.Load(),
					facilitator.settleCalls.Load(), ledger.failedCalls,
					response.Body.String(),
				)
			}
			if ledger.lastFailureCode != test.wantFailure {
				t.Fatalf(
					"failure code=%q want=%q",
					ledger.lastFailureCode, test.wantFailure,
				)
			}
		})
	}
}

func TestHTTPDispatcherFenceAndFailureCommitErrorsFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		configure     func(*memoryPaymentLedger)
		handler       http.Handler
		wantHandlers  int
		wantFailCalls int
	}{
		{
			name: "handler fence",
			configure: func(ledger *memoryPaymentLedger) {
				ledger.handlerErr = errors.New("handler fence unavailable")
			},
			handler:       http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			wantHandlers:  0,
			wantFailCalls: 1,
		},
		{
			name: "settlement fence",
			configure: func(ledger *memoryPaymentLedger) {
				ledger.settlingErr = errors.New("settlement fence unavailable")
			},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(`{"secret":"captured"}`))
			}),
			wantHandlers:  1,
			wantFailCalls: 1,
		},
		{
			name: "failure commit",
			configure: func(ledger *memoryPaymentLedger) {
				ledger.failedErr = errors.New("failure commit unavailable")
			},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(`{"secret":"captured"}`))
				writer.(http.Flusher).Flush()
			}),
			wantHandlers:  1,
			wantFailCalls: 1,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			ledger := &memoryPaymentLedger{}
			test.configure(ledger)
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(
				t, dispatcher, spec, "/api/v1/blocks",
				[]string{"14", "15", "16"}[index],
			)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			handlerCalls := 0
			response := httptest.NewRecorder()
			dispatcher.ServePaid(
				response, request, spec, APIKeyIdentity{},
				http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					handlerCalls++
					test.handler.ServeHTTP(writer, request)
				}),
			)
			if response.Code != http.StatusServiceUnavailable ||
				strings.Contains(response.Body.String(), "captured") ||
				handlerCalls != test.wantHandlers ||
				ledger.failedCalls != test.wantFailCalls ||
				facilitator.settleCalls.Load() != 0 {
				t.Fatalf(
					"status=%d handler=%d failed=%d settle=%d body=%s",
					response.Code, handlerCalls, ledger.failedCalls,
					facilitator.settleCalls.Load(), response.Body.String(),
				)
			}
		})
	}
}

func TestHTTPDispatcherHandlerFailuresNeverSettle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler http.Handler
		want    int
		code    string
		specMax int64
	}{
		{
			name: "non-2xx",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("missing"))
			}),
			want: http.StatusNotFound, code: "handler_non_success",
		},
		{
			name: "body overflow",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", 65)))
			}),
			want: http.StatusBadGateway, code: "handler_body_too_large",
		},
		{
			name: "catalog body overflow",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", 33)))
			}),
			want: http.StatusBadGateway, code: "handler_body_too_large",
			specMax: 32,
		},
		{
			name: "header overflow",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Large", strings.Repeat("x", 65))
				w.WriteHeader(http.StatusOK)
			}),
			want: http.StatusBadGateway, code: "handler_headers_too_large",
		},
		{
			name: "streaming",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.(http.Flusher).Flush()
			}),
			want: http.StatusBadGateway, code: "handler_streaming_unsupported",
		},
		{
			name: "panic",
			handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("hostile secret")
			}),
			want: http.StatusInternalServerError, code: "handler_panic",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			cfg.Billing.MaxBufferedResponseBytes = 64
			cfg.Billing.MaxCapturedHeaderBytes = 64
			ledger := &memoryPaymentLedger{}
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", "02")
			if test.specMax > 0 {
				spec.MaxResponseBytes = test.specMax
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			recorder := httptest.NewRecorder()
			dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, test.handler)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			if ledger.lastFailureCode != test.code || ledger.payment.State != StateFailed ||
				facilitator.settleCalls.Load() != 0 {
				t.Fatalf("failure=%q state=%s settle=%d", ledger.lastFailureCode, ledger.payment.State, facilitator.settleCalls.Load())
			}
		})
	}
}

func TestHTTPDispatcherVerificationAndSettlementFailuresAreFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		verifyErr    error
		settleErr    error
		settledErr   error
		wantStatus   int
		wantState    State
		wantUnknowns int
	}{
		{
			name: "verify unavailable",
			verifyErr: &x402wire.BoundaryError{
				Phase: x402wire.PhaseVerify, Class: x402wire.FailureUnavailable,
				Code: x402wire.CodeFacilitatorUnavailable,
			},
			wantStatus: http.StatusServiceUnavailable, wantState: StateFailed,
		},
		{
			name: "verify rejected",
			verifyErr: &x402wire.BoundaryError{
				Phase: x402wire.PhaseVerify, Class: x402wire.FailureRejected,
				Code: x402wire.CodeFacilitatorRejected,
			},
			wantStatus: http.StatusPaymentRequired, wantState: StateFailed,
		},
		{
			name: "settle rejected",
			settleErr: &x402wire.BoundaryError{
				Phase: x402wire.PhaseSettle, Class: x402wire.FailureRejected,
				Code: x402wire.CodeFacilitatorRejected,
			},
			wantStatus: http.StatusPaymentRequired, wantState: StateFailed,
		},
		{
			name: "settle unknown",
			settleErr: &x402wire.BoundaryError{
				Phase: x402wire.PhaseSettle, Class: x402wire.FailureSettlementUnknown,
				Code: x402wire.CodeSettlementUnknown,
			},
			wantStatus: http.StatusServiceUnavailable, wantState: StateSettling,
			wantUnknowns: 1,
		},
		{
			name:       "final ledger commit unknown",
			settledErr: errors.New("database result unknown"),
			wantStatus: http.StatusServiceUnavailable, wantState: StateSettling,
			wantUnknowns: 1,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			ledger := &memoryPaymentLedger{settledErr: test.settledErr}
			facilitator := &fakePaymentFacilitator{
				verifyErr: test.verifyErr, settleErr: test.settleErr,
			}
			if test.verifyErr != nil {
				facilitator.verify = nil
			}
			if test.settleErr != nil {
				facilitator.settle = nil
			}
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			spec, _ := apiops.Lookup("listBlocks")
			nonce := []string{"03", "04", "05", "06", "07"}[index]
			header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", nonce)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			recorder := httptest.NewRecorder()
			dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"ok":true}`))
				},
			))
			if recorder.Code != test.wantStatus || ledger.payment.State != test.wantState ||
				ledger.unknownCalls != test.wantUnknowns {
				t.Fatalf(
					"status=%d state=%s unknown=%d body=%s",
					recorder.Code, ledger.payment.State, ledger.unknownCalls, recorder.Body.String(),
				)
			}
			if test.wantState != StateSettled &&
				recorder.Header().Get(x402wire.PaymentResponseHeader) != "" {
				t.Fatal("payment response escaped before durable settlement")
			}
		})
	}
}

func TestHTTPDispatcherSettlingDuplicateNeverRunsWork(t *testing.T) {
	t.Parallel()
	for _, failureCode := range []*string{nil, new("settlement_unknown")} {
		name := "nil"
		if failureCode != nil {
			name = *failureCode
		}
		t.Run(name, func(t *testing.T) {
			duplicate := Payment{
				ID:    "00000000-0000-4000-8000-000000000001",
				State: StateSettling, FailureCode: failureCode,
			}
			ledger := &memoryPaymentLedger{duplicatePayment: &duplicate}
			facilitator := &fakePaymentFacilitator{}
			cfg := testBillingConfig("listBlocks", "x402", "1000")
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", "08")
			request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			handlerCalls := 0
			recorder := httptest.NewRecorder()
			dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) { handlerCalls++ },
			))
			if recorder.Code != http.StatusServiceUnavailable || handlerCalls != 0 ||
				facilitator.verifyCalls.Load() != 0 || facilitator.settleCalls.Load() != 0 {
				t.Fatalf(
					"status=%d handler=%d verify=%d settle=%d",
					recorder.Code, handlerCalls, facilitator.verifyCalls.Load(), facilitator.settleCalls.Load(),
				)
			}
		})
	}
}

func TestHTTPDispatcherCancellationPersistsFailureWithoutSettlement(t *testing.T) {
	t.Parallel()
	cfg := testBillingConfig("listBlocks", "x402", "1000")
	ledger := &memoryPaymentLedger{}
	facilitator := &fakePaymentFacilitator{}
	dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
	spec, _ := apiops.Lookup("listBlocks")
	header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", "0c")
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil).WithContext(ctx)
	request.Header.Set(x402wire.PaymentSignatureHeader, header)
	recorder := httptest.NewRecorder()
	dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
			cancel()
		},
	))
	if recorder.Code != http.StatusServiceUnavailable ||
		ledger.payment.State != StateFailed ||
		ledger.lastFailureCode != "request_canceled" ||
		facilitator.settleCalls.Load() != 0 {
		t.Fatalf(
			"status=%d state=%s code=%q settle=%d",
			recorder.Code, ledger.payment.State, ledger.lastFailureCode,
			facilitator.settleCalls.Load(),
		)
	}
}

func TestHTTPDispatcherDoesNotResolveUserBeforeVerifiedPayer(t *testing.T) {
	t.Parallel()
	cfg := testBillingConfig("listBlocks", "x402", "1000")
	ledger := &memoryPaymentLedger{}
	resolver := &fakePayerResolver{
		id: "00000000-0000-4000-8000-000000000099", found: true,
	}
	facilitator := &fakePaymentFacilitator{
		verifyErr: &x402wire.BoundaryError{
			Phase: x402wire.PhaseVerify, Class: x402wire.FailureRejected,
			Code: x402wire.CodeFacilitatorRejected,
		},
	}
	dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, resolver)
	spec, _ := apiops.Lookup("listBlocks")
	header := requestPaymentHeader(t, dispatcher, spec, "/api/v1/blocks", "0d")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set(x402wire.PaymentSignatureHeader, header)
	recorder := httptest.NewRecorder()
	dispatcher.ServePaid(recorder, request, spec, APIKeyIdentity{}, http.NotFoundHandler())
	if recorder.Code != http.StatusPaymentRequired || resolver.calls.Load() != 0 {
		t.Fatalf("status=%d resolver=%d", recorder.Code, resolver.calls.Load())
	}
}

func TestHTTPDispatcherOneAuthorizationOwnsOnlyOneOuterBinding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		operations []string
		firstPath  string
		secondPath string
	}{
		{
			name:       "operation",
			operations: []string{"listBlocks", "listTransactions"},
			firstPath:  "/api/v1/blocks", secondPath: "/api/v1/transactions",
		},
		{
			name:       "resource",
			operations: []string{"getBlock", "getBlock"},
			firstPath:  "/api/v1/blocks/1", secondPath: "/api/v1/blocks/2",
		},
		{
			name:       "query",
			operations: []string{"listBlocks", "listBlocks"},
			firstPath:  "/api/v1/blocks?cursor=first",
			secondPath: "/api/v1/blocks?cursor=second",
		},
	}
	for testIndex, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig(test.operations[0], "x402", "1000")
			cfg.Billing.Routes[test.operations[1]] = config.BillingRouteConfig{
				Access: "x402", AmountAtomic: "1000",
			}
			ledger := &memoryPaymentLedger{}
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(t, cfg, ledger, facilitator, nil)
			firstSpec, _ := apiops.Lookup(test.operations[0])
			secondSpec, _ := apiops.Lookup(test.operations[1])
			nonce := []string{"09", "0a", "0b"}[testIndex]
			firstHeader := requestPaymentHeader(t, dispatcher, firstSpec, test.firstPath, nonce)
			secondHeader := requestPaymentHeader(t, dispatcher, secondSpec, test.secondPath, nonce)
			firstFingerprint := fingerprintForHeader(t, dispatcher, firstHeader)
			secondFingerprint := fingerprintForHeader(t, dispatcher, secondHeader)
			if firstFingerprint != secondFingerprint {
				t.Fatal("the same authorization did not retain one global fingerprint")
			}

			handlerStarted := make(chan struct{})
			releaseHandler := make(chan struct{})
			var handlerCalls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if handlerCalls.Add(1) != 1 {
					t.Error("a duplicate outer binding reached the handler")
				}
				close(handlerStarted)
				<-releaseHandler
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			firstRequest := httptest.NewRequest(http.MethodGet, test.firstPath, nil)
			firstRequest.Header.Set(x402wire.PaymentSignatureHeader, firstHeader)
			firstResponse := httptest.NewRecorder()
			firstDone := make(chan struct{})
			go func() {
				defer close(firstDone)
				dispatcher.ServePaid(
					firstResponse, firstRequest, firstSpec, APIKeyIdentity{}, handler,
				)
			}()
			<-handlerStarted

			secondRequest := httptest.NewRequest(http.MethodGet, test.secondPath, nil)
			secondRequest.Header.Set(x402wire.PaymentSignatureHeader, secondHeader)
			secondResponse := httptest.NewRecorder()
			dispatcher.ServePaid(
				secondResponse, secondRequest, secondSpec, APIKeyIdentity{}, handler,
			)
			if secondResponse.Code != http.StatusConflict {
				t.Fatalf("second status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
			}

			// While the first handler still owns the fence, exercise the
			// persistence binding checks that cannot be represented by a
			// matching exact-EVM authorization (changed price/recipient), plus
			// explicit operation/resource/query digest changes.
			ledger.mu.Lock()
			base := *ledger.reserved
			ledger.mu.Unlock()
			variants := []ReserveInput{
				base, base, base, base, base,
			}
			variants[0].Operation = alternateEligibleOperation(base.Operation)
			variants[1].ResourceDigest[0] ^= 0xff
			variants[2].ResourceDigest[1] ^= 0xff // canonical query binding
			variants[3].AmountAtomic = "1001"
			variants[3].RequirementDigest[0] ^= 0xff
			variants[4].Recipient[0] ^= 0xff
			type reserveResult struct {
				index       int
				reservation Reservation
				err         error
			}
			results := make(chan reserveResult, len(variants))
			var reserveGroup sync.WaitGroup
			for index, variant := range variants {
				reserveGroup.Go(func() {
					reservation, err := ledger.Reserve(context.Background(), variant)
					results <- reserveResult{
						index: index, reservation: reservation, err: err,
					}
				})
			}
			reserveGroup.Wait()
			close(results)
			for result := range results {
				if !errors.Is(result.err, ErrIntegrity) || result.reservation.Owned {
					t.Fatalf(
						"variant %d reservation=%#v err=%v",
						result.index, result.reservation, result.err,
					)
				}
			}

			close(releaseHandler)
			<-firstDone
			if firstResponse.Code != http.StatusOK || handlerCalls.Load() != 1 ||
				facilitator.verifyCalls.Load() != 1 ||
				facilitator.settleCalls.Load() != 1 ||
				ledger.settledCalls != 1 {
				t.Fatalf(
					"first=%d handler=%d verify=%d settle=%d settled=%d",
					firstResponse.Code, handlerCalls.Load(), facilitator.verifyCalls.Load(),
					facilitator.settleCalls.Load(), ledger.settledCalls,
				)
			}
		})
	}
}

func fingerprintForHeader(
	t *testing.T,
	dispatcher *HTTPDispatcher,
	header string,
) [32]byte {
	t.Helper()
	payment, err := dispatcher.codec.DecodePaymentSignature(http.Header{
		x402wire.PaymentSignatureHeader: []string{header},
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := x402wire.Fingerprint(dispatcher.fingerprintPepper, payment)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func alternateEligibleOperation(operation string) string {
	if operation == "listBlocks" {
		return "listTransactions"
	}
	return "listBlocks"
}

//go:fix inline
func stringPointer(value string) *string { return new(value) }
