package billing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing/x402wire"
)

type recordedX402Outcome struct {
	operation string
	result    string
}

type recordingX402Observer struct {
	outcomes []recordedX402Outcome
}

func (observer *recordingX402Observer) ObserveX402Request(
	operation, result string,
) {
	observer.outcomes = append(
		observer.outcomes,
		recordedX402Outcome{operation: operation, result: result},
	)
}

func TestHTTPDispatcherObservesOneClosedTerminalOutcome(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*memoryPaymentLedger, *fakePaymentFacilitator)
		handler     http.Handler
		wantOutcome string
	}{
		{
			name:        "settled",
			configure:   func(*memoryPaymentLedger, *fakePaymentFacilitator) {},
			handler:     http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }),
			wantOutcome: "settled",
		},
		{
			name: "ledger unavailable",
			configure: func(ledger *memoryPaymentLedger, _ *fakePaymentFacilitator) {
				ledger.reserveErr = errors.New("writer unavailable")
			},
			handler:     http.NotFoundHandler(),
			wantOutcome: "ledger_unavailable",
		},
		{
			name: "verify unavailable",
			configure: func(_ *memoryPaymentLedger, facilitator *fakePaymentFacilitator) {
				facilitator.verifyErr = &x402wire.BoundaryError{
					Phase: x402wire.PhaseVerify,
					Class: x402wire.FailureUnavailable,
					Code:  x402wire.CodeFacilitatorUnavailable,
				}
			},
			handler:     http.NotFoundHandler(),
			wantOutcome: "verify_unavailable",
		},
		{
			name: "handler non success",
			configure: func(*memoryPaymentLedger, *fakePaymentFacilitator) {
			},
			handler:     http.NotFoundHandler(),
			wantOutcome: "handler_non_success",
		},
		{
			name: "settlement unknown",
			configure: func(_ *memoryPaymentLedger, facilitator *fakePaymentFacilitator) {
				facilitator.settleErr = errors.New("remote timeout")
			},
			handler: http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				_, _ = writer.Write([]byte(`{"captured":true}`))
			}),
			wantOutcome: "settlement_unknown",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testBillingConfig("listBlocks", "x402", "1")
			ledger := &memoryPaymentLedger{}
			facilitator := &fakePaymentFacilitator{}
			dispatcher := newTestDispatcher(
				t,
				cfg,
				ledger,
				facilitator,
				nil,
			)
			spec, _ := apiops.Lookup("listBlocks")
			header := requestPaymentHeader(
				t,
				dispatcher,
				spec,
				"/api/v1/blocks",
				[]string{"41", "42", "43", "44", "45"}[index],
			)
			test.configure(ledger, facilitator)
			observer := &recordingX402Observer{}
			dispatcher.observer = observer
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/blocks",
				nil,
			)
			request.Header.Set(x402wire.PaymentSignatureHeader, header)
			dispatcher.ServePaid(
				httptest.NewRecorder(),
				request,
				spec,
				APIKeyIdentity{},
				test.handler,
			)
			if len(observer.outcomes) != 1 ||
				observer.outcomes[0] != (recordedX402Outcome{
					operation: "listBlocks",
					result:    test.wantOutcome,
				}) {
				t.Fatalf("outcomes=%#v", observer.outcomes)
			}
		})
	}
}

func TestHTTPDispatcherObservesPaymentRequirement(t *testing.T) {
	cfg := testBillingConfig("listBlocks", "x402", "1")
	dispatcher := newTestDispatcher(
		t,
		cfg,
		&memoryPaymentLedger{},
		&fakePaymentFacilitator{},
		nil,
	)
	observer := &recordingX402Observer{}
	dispatcher.observer = observer
	spec, _ := apiops.Lookup("listBlocks")
	dispatcher.ServePaid(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil),
		spec,
		APIKeyIdentity{},
		http.NotFoundHandler(),
	)
	if len(observer.outcomes) != 1 ||
		observer.outcomes[0].result != "required" {
		t.Fatalf("outcomes=%#v", observer.outcomes)
	}
}
