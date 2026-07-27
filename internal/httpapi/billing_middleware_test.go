package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type httpBillingLedger struct {
	reserveCalls atomic.Int32
	settleCalls  atomic.Int32
	state        billing.State
}

func (ledger *httpBillingLedger) Reserve(
	_ context.Context,
	input billing.ReserveInput,
) (billing.Reservation, error) {
	ledger.reserveCalls.Add(1)
	ledger.state = billing.StateReserved
	return billing.Reservation{
		Owned: true, Owner: "00000000-0000-4000-8000-000000000002",
		Payment: billing.Payment{
			ID:      "00000000-0000-4000-8000-000000000001",
			ChainID: 84532, Operation: input.Operation, Method: http.MethodGet,
			Network: input.Network, Asset: input.Asset,
			AmountAtomic: input.AmountAtomic, Recipient: input.Recipient,
			State: billing.StateReserved, CreatedAt: input.ObservedAt,
			UpdatedAt:            input.ObservedAt,
			ReservationExpiresAt: input.ObservedAt.Add(2 * time.Minute),
		},
	}, nil
}

func (ledger *httpBillingLedger) MarkVerified(
	_ context.Context,
	input billing.VerifiedInput,
) (billing.Payment, error) {
	ledger.state = billing.StateVerified
	return billing.Payment{ID: input.PaymentID, State: billing.StateVerified}, nil
}

func (ledger *httpBillingLedger) StartHandler(
	_ context.Context,
	id, _ string,
	at time.Time,
) (billing.Payment, error) {
	ledger.state = billing.StateVerified
	return billing.Payment{ID: id, State: billing.StateVerified, HandlerStartedAt: &at}, nil
}

func (ledger *httpBillingLedger) BeginSettlement(
	_ context.Context,
	id, _ string,
	at time.Time,
) (billing.Payment, error) {
	ledger.state = billing.StateSettling
	return billing.Payment{ID: id, State: billing.StateSettling, SettlingAt: &at}, nil
}

func (ledger *httpBillingLedger) MarkSettlementUnknown(
	_ context.Context,
	id, _ string,
	_ time.Time,
) (billing.Payment, error) {
	ledger.state = billing.StateSettling
	return billing.Payment{ID: id, State: billing.StateSettling}, nil
}

func (ledger *httpBillingLedger) MarkSettled(
	_ context.Context,
	id, _ string,
	_ common.Hash,
	at time.Time,
) (billing.Payment, error) {
	ledger.settleCalls.Add(1)
	ledger.state = billing.StateSettled
	return billing.Payment{ID: id, State: billing.StateSettled, SettledAt: &at}, nil
}

func (ledger *httpBillingLedger) MarkFailed(
	_ context.Context,
	id, _ string,
	_ string,
	at time.Time,
) (billing.Payment, error) {
	ledger.state = billing.StateFailed
	return billing.Payment{ID: id, State: billing.StateFailed, FailedAt: &at}, nil
}

type httpBillingFacilitator struct {
	verifyCalls atomic.Int32
	settleCalls atomic.Int32
	verifyErr   error
	settleErr   error
}

func (facilitator *httpBillingFacilitator) VerifyPayment(
	context.Context,
	x402wire.Payment,
	x402wire.Requirement,
) (*x402.VerifyResponse, error) {
	facilitator.verifyCalls.Add(1)
	if facilitator.verifyErr != nil {
		return nil, facilitator.verifyErr
	}
	return &x402.VerifyResponse{
		IsValid: true, Payer: "0x3333333333333333333333333333333333333333",
	}, nil
}

func (facilitator *httpBillingFacilitator) SettlePayment(
	context.Context,
	x402wire.Payment,
	x402wire.Requirement,
) (*x402.SettleResponse, error) {
	facilitator.settleCalls.Add(1)
	if facilitator.settleErr != nil {
		return nil, facilitator.settleErr
	}
	return &x402.SettleResponse{
		Success:     true,
		Payer:       "0x3333333333333333333333333333333333333333",
		Transaction: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Network:     "eip155:84532", Amount: "1000",
	}, nil
}

func (*httpBillingFacilitator) OriginDigest() [32]byte { return [32]byte{0x42} }

func httpBillingConfig(access string) config.Config {
	cfg := config.Default()
	cfg.Features.X402Billing = true
	cfg.Server.PublicURL = "http://localhost:8080"
	cfg.Chain.ID = 84532
	cfg.Billing.Network = "eip155:84532"
	cfg.Billing.Asset = "0x1111111111111111111111111111111111111111"
	cfg.Billing.Recipient = "0x2222222222222222222222222222222222222222"
	cfg.Billing.AssetEIP712Name = "USDC"
	cfg.Billing.AssetEIP712Version = "2"
	cfg.Billing.FingerprintPepper = strings.Repeat("f", 32)
	cfg.Billing.Routes = map[string]config.BillingRouteConfig{
		"listBlocks": {Access: access, AmountAtomic: "1000"},
	}
	return cfg
}

func httpBillingHandler(
	t *testing.T,
	cfg config.Config,
	quotaCalls *atomic.Int32,
	quotaStatus *atomic.Int32,
) (*Handler, *httpBillingLedger, *httpBillingFacilitator) {
	t.Helper()
	ledger := &httpBillingLedger{}
	facilitator := &httpBillingFacilitator{}
	dispatcher, err := billing.NewHTTPDispatcher(billing.DispatcherOptions{
		Config: cfg, Ledger: ledger, Facilitator: facilitator,
		Now: func() time.Time {
			return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	quota := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			quotaCalls.Add(1)
			if quotaStatus != nil && quotaStatus.Load() != 0 {
				w.WriteHeader(int(quotaStatus.Load()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	handler, err := New(Options{
		Config: cfg,
		Reader: fakeReader{status: StatusSnapshot{
			CoreReady: true, Completeness: gen.Completeness{},
		}},
		Billing: dispatcher, Quota: quota,
		RequestID: func() string { return "request-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, ledger, facilitator
}

func TestEnabledBillingRequiresWriterBackedDispatcher(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.X402Billing = true
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}})
	if err == nil || handler != nil {
		t.Fatalf("handler=%v error=%v", handler, err)
	}
}

func paymentHeaderFromChallenge(t *testing.T, encoded string) string {
	t.Helper()
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var required x402.PaymentRequired
	if err := json.Unmarshal(decoded, &required); err != nil {
		t.Fatal(err)
	}
	payment := x402.PaymentPayload{
		X402Version: 2,
		Payload: map[string]interface{}{
			"signature": "0x" + strings.Repeat("11", 65),
			"authorization": map[string]interface{}{
				"from": "0x3333333333333333333333333333333333333333",
				"to":   required.Accepts[0].PayTo, "value": required.Accepts[0].Amount,
				"validAfter": "0", "validBefore": "9999999999",
				"nonce": "0x" + strings.Repeat("01", 32),
			},
		},
		Accepted: required.Accepts[0], Resource: required.Resource,
	}
	body, err := json.Marshal(payment)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(body)
}

func TestBillingDispatchBypassesQuotaOnlyForPurePaidPath(t *testing.T) {
	t.Parallel()
	var quotaCalls atomic.Int32
	cfg := httpBillingConfig("x402")
	handler, ledger, facilitator := httpBillingHandler(t, cfg, &quotaCalls, nil)
	repository := auth.NewMemoryRepository()
	manager := auth.Manager{
		Repository: repository, Pepper: []byte(strings.Repeat("p", 32)),
	}
	issued, err := manager.Create(context.Background(), "test", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)

	challenge := httptest.NewRecorder()
	challengeRequest := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	challengeRequest.Header.Set("X-API-Key", issued.Token)
	protected.ServeHTTP(challenge, challengeRequest)
	if challenge.Code != http.StatusPaymentRequired || quotaCalls.Load() != 0 {
		t.Fatalf("challenge=%d quota=%d", challenge.Code, quotaCalls.Load())
	}
	paymentHeader := paymentHeaderFromChallenge(
		t, challenge.Header().Get(x402wire.PaymentRequiredHeader),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set("X-API-Key", issued.Token)
	request.Header.Set(x402wire.PaymentSignatureHeader, paymentHeader)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || quotaCalls.Load() != 0 ||
		ledger.state != billing.StateSettled ||
		facilitator.verifyCalls.Load() != 1 || facilitator.settleCalls.Load() != 1 {
		t.Fatalf(
			"status=%d quota=%d state=%s verify=%d settle=%d body=%s",
			response.Code, quotaCalls.Load(), ledger.state,
			facilitator.verifyCalls.Load(), facilitator.settleCalls.Load(),
			response.Body.String(),
		)
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	invalid.Header.Set("X-API-Key", "invalid")
	invalidResponse := httptest.NewRecorder()
	protected.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestBillingAPIKeyBypassKeepsQuotaAndRejectsPaymentHeader(t *testing.T) {
	t.Parallel()
	var quotaCalls atomic.Int32
	var quotaStatus atomic.Int32
	cfg := httpBillingConfig("api_key_or_x402")
	handler, _, facilitator := httpBillingHandler(t, cfg, &quotaCalls, &quotaStatus)
	repository := auth.NewMemoryRepository()
	manager := auth.Manager{
		Repository: repository, Pepper: []byte(strings.Repeat("p", 32)),
	}
	issued, err := manager.Create(context.Background(), "test", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	protected := manager.Middleware(false, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set("X-API-Key", issued.Token)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || quotaCalls.Load() != 1 ||
		facilitator.verifyCalls.Load() != 0 {
		t.Fatalf("status=%d quota=%d verify=%d", response.Code, quotaCalls.Load(), facilitator.verifyCalls.Load())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set("X-API-Key", issued.Token)
	request.Header.Set(x402wire.PaymentSignatureHeader, "unexpected")
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || quotaCalls.Load() != 1 {
		t.Fatalf("payment bypass status=%d quota=%d body=%s", response.Code, quotaCalls.Load(), response.Body.String())
	}

	quotaStatus.Store(http.StatusTooManyRequests)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set("X-API-Key", issued.Token)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || quotaCalls.Load() != 2 ||
		facilitator.verifyCalls.Load() != 0 {
		t.Fatalf("quota status=%d calls=%d verify=%d", response.Code, quotaCalls.Load(), facilitator.verifyCalls.Load())
	}
}

func TestBillingAPIKeyOrX402WithoutKeyUsesPayment(t *testing.T) {
	t.Parallel()
	var quotaCalls atomic.Int32
	cfg := httpBillingConfig("api_key_or_x402")
	handler, ledger, facilitator := httpBillingHandler(t, cfg, &quotaCalls, nil)

	challenge := httptest.NewRecorder()
	handler.ServeHTTP(
		challenge,
		httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil),
	)
	if challenge.Code != http.StatusPaymentRequired || quotaCalls.Load() != 0 {
		t.Fatalf("challenge=%d quota=%d", challenge.Code, quotaCalls.Load())
	}
	paymentHeader := paymentHeaderFromChallenge(
		t, challenge.Header().Get(x402wire.PaymentRequiredHeader),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set(x402wire.PaymentSignatureHeader, paymentHeader)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || quotaCalls.Load() != 0 ||
		ledger.state != billing.StateSettled ||
		facilitator.verifyCalls.Load() != 1 ||
		facilitator.settleCalls.Load() != 1 {
		t.Fatalf(
			"status=%d quota=%d state=%s verify=%d settle=%d body=%s",
			response.Code, quotaCalls.Load(), ledger.state,
			facilitator.verifyCalls.Load(), facilitator.settleCalls.Load(),
			response.Body.String(),
		)
	}
}

func TestFacilitatorOutageAffectsOnlyPricedProductionMuxRoute(t *testing.T) {
	t.Parallel()
	var quotaCalls atomic.Int32
	handler, ledger, facilitator := httpBillingHandler(
		t,
		httpBillingConfig("x402"),
		&quotaCalls,
		nil,
	)

	challenge := httptest.NewRecorder()
	handler.ServeHTTP(
		challenge,
		httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil),
	)
	if challenge.Code != http.StatusPaymentRequired {
		t.Fatalf(
			"challenge status=%d body=%s",
			challenge.Code,
			challenge.Body.String(),
		)
	}
	paymentHeader := paymentHeaderFromChallenge(
		t,
		challenge.Header().Get(x402wire.PaymentRequiredHeader),
	)
	facilitator.verifyErr = &x402wire.BoundaryError{
		Phase: x402wire.PhaseVerify,
		Class: x402wire.FailureUnavailable,
		Code:  x402wire.CodeFacilitatorUnavailable,
	}
	paidRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/blocks",
		nil,
	)
	paidRequest.Header.Set(x402wire.PaymentSignatureHeader, paymentHeader)
	paidResponse := httptest.NewRecorder()
	handler.ServeHTTP(paidResponse, paidRequest)
	if paidResponse.Code != http.StatusServiceUnavailable ||
		ledger.state != billing.StateFailed ||
		facilitator.verifyCalls.Load() != 1 ||
		facilitator.settleCalls.Load() != 0 {
		t.Fatalf(
			"paid status=%d state=%s verify=%d settle=%d body=%s",
			paidResponse.Code,
			ledger.state,
			facilitator.verifyCalls.Load(),
			facilitator.settleCalls.Load(),
			paidResponse.Body.String(),
		)
	}

	for _, target := range []string{
		"/api/v1/status",
		"/api/v1/billing/config",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, target, nil),
		)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"free %s status=%d body=%s",
				target,
				response.Code,
				response.Body.String(),
			)
		}
	}
	if facilitator.verifyCalls.Load() != 1 ||
		facilitator.settleCalls.Load() != 0 {
		t.Fatalf(
			"free routes reached facilitator: verify=%d settle=%d",
			facilitator.verifyCalls.Load(),
			facilitator.settleCalls.Load(),
		)
	}
}

func TestBillingFreeUnknownAndCompatibilityPathsRejectPaymentHeader(t *testing.T) {
	t.Parallel()
	var quotaCalls atomic.Int32
	handler, _, _ := httpBillingHandler(t, httpBillingConfig("x402"), &quotaCalls, nil)
	for _, target := range []string{
		"/api/v1/status", "/api/v1/genesis/accounts",
		"/api/v1/unknown", "/v2/api", "/",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set(x402wire.PaymentSignatureHeader, "unexpected")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	head := httptest.NewRequest(http.MethodHead, "/api/v1/blocks", nil)
	head.Header.Set(x402wire.PaymentSignatureHeader, "unexpected")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusBadRequest {
		t.Fatalf("HEAD status=%d body=%s", headResponse.Code, headResponse.Body.String())
	}
	options := httptest.NewRequest(http.MethodOptions, "/api/v1/blocks", nil)
	options.Header.Set(x402wire.PaymentSignatureHeader, "unexpected")
	optionsResponse := httptest.NewRecorder()
	handler.ServeHTTP(optionsResponse, options)
	if optionsResponse.Code != http.StatusBadRequest {
		t.Fatalf("OPTIONS status=%d body=%s", optionsResponse.Code, optionsResponse.Body.String())
	}
	if quotaCalls.Load() != 0 {
		t.Fatalf("unexpected payment headers reached quota %d times", quotaCalls.Load())
	}
}

func TestOperationCatalogPatternsAreRegisteredByTheProductionMux(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Catalog: &fakeCatalog{},
		Events: events.NewBroker(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range apiops.All() {
		target := catalogRequestTarget(t, spec)
		request := httptest.NewRequest(spec.Method, target, nil)
		_, pattern := handler.mux.Handler(request)
		if pattern != spec.MuxPattern {
			t.Errorf(
				"%s target=%s pattern=%q, want %q",
				spec.ID, target, pattern, spec.MuxPattern,
			)
		}
		matched, ok := handler.matchedOperation(request)
		if !ok || matched.ID != spec.ID {
			t.Errorf(
				"%s target=%s matched=%q ok=%t",
				spec.ID, target, matched.ID, ok,
			)
		}
	}
}

func TestBillableCatalogPatternsAreRegisteredByTheProductionMux(t *testing.T) {
	t.Parallel()
	cfg := httpBillingConfig("x402")
	cfg.Billing.Routes = make(map[string]config.BillingRouteConfig)
	for _, operation := range apiops.EligibleIDs() {
		cfg.Billing.Routes[operation] = config.BillingRouteConfig{
			Access: "x402", AmountAtomic: "1000",
		}
	}
	ledger := &httpBillingLedger{}
	facilitator := &httpBillingFacilitator{}
	dispatcher, err := billing.NewHTTPDispatcher(billing.DispatcherOptions{
		Config: cfg, Ledger: ledger, Facilitator: facilitator,
	})
	if err != nil {
		t.Fatal(err)
	}
	var quotaCalls atomic.Int32
	quota := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			quotaCalls.Add(1)
			next.ServeHTTP(writer, request)
		})
	}
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Catalog: &fakeCatalog{},
		Billing: dispatcher, Quota: quota,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range apiops.All() {
		if !spec.BillingEligible {
			continue
		}
		target := catalogRequestTarget(t, spec)
		request := httptest.NewRequest(spec.Method, target, nil)
		matched, ok := handler.matchedOperation(request)
		if !ok || matched.ID != spec.ID ||
			matched.MuxPattern != spec.MuxPattern {
			t.Errorf(
				"%s matched=%q pattern=%q ok=%t",
				spec.ID, matched.ID, matched.MuxPattern, ok,
			)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusPaymentRequired ||
			response.Header().Get(x402wire.PaymentRequiredHeader) == "" {
			t.Errorf(
				"%s target=%s status=%d body=%s",
				spec.ID, target, response.Code, response.Body.String(),
			)
		}
	}
	if quotaCalls.Load() != 0 || ledger.reserveCalls.Load() != 0 ||
		facilitator.verifyCalls.Load() != 0 ||
		facilitator.settleCalls.Load() != 0 {
		t.Fatalf(
			"quota=%d reserve=%d verify=%d settle=%d",
			quotaCalls.Load(), ledger.reserveCalls.Load(),
			facilitator.verifyCalls.Load(), facilitator.settleCalls.Load(),
		)
	}
}

func catalogRequestTarget(t *testing.T, spec apiops.Spec) string {
	t.Helper()
	path := "/api/v1" + spec.OpenAPIPath
	query := make(url.Values)
	for _, parameter := range spec.Parameters {
		switch parameter.In {
		case apiops.ParameterPath:
			path = strings.ReplaceAll(
				path, "{"+parameter.Name+"}",
				catalogParameterExample(t, parameter.Type),
			)
		case apiops.ParameterQuery:
			if parameter.Required {
				query.Set(
					parameter.Name,
					catalogParameterExample(t, parameter.Type),
				)
			}
		default:
			t.Fatalf("%s has invalid parameter location %q", spec.ID, parameter.In)
		}
	}
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return target
}

func catalogParameterExample(t *testing.T, parameterType apiops.ParameterType) string {
	t.Helper()
	switch parameterType {
	case apiops.ParameterAddress:
		return "0x" + strings.Repeat("11", 20)
	case apiops.ParameterBlockIdentifier, apiops.ParameterInteger,
		apiops.ParameterUint256:
		return "1"
	case apiops.ParameterHash:
		return "0x" + strings.Repeat("22", 32)
	case apiops.ParameterOpaqueCursor:
		return "opaque"
	case apiops.ParameterText:
		return "query"
	case apiops.ParameterUUID:
		return "550e8400-e29b-41d4-a716-446655440000"
	default:
		t.Fatalf("no request example for parameter type %q", parameterType)
		return ""
	}
}
