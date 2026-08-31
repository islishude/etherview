package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
)

func TestPaymentAuthorizationIsAcceptedOnlyByAccountTopupPayment(t *testing.T) {
	t.Parallel()
	compatibility := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Etherscan: compatibility,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "native", method: http.MethodGet, path: "/api/v1/status"},
		{name: "preflight", method: http.MethodOptions, path: "/api/v1/status"},
		{name: "compatibility", method: http.MethodGet, path: "/v2/api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set(x402wire.PaymentSignatureHeader, "opaque")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), "payment authorization") {
				t.Fatalf("response status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/billing/topup-intents/00000000-0000-4000-8000-000000000001/pay",
		nil,
	)
	request.Header.Set(x402wire.PaymentSignatureHeader, "opaque")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable ||
		strings.Contains(response.Body.String(), "unexpected_payment_header") {
		t.Fatalf("top-up response status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeReadsRemainFreeWithoutRequestPaymentDispatcher(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope gen.StatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
}
