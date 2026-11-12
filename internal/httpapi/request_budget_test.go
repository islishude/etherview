package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
)

func TestRequestBudgetCancelsWorkAndReturnsTypedTimeout(t *testing.T) {
	for _, path := range []string{"/api/v1/blocks", "/v2/api"} {
		t.Run(path, func(t *testing.T) {
			handler := RequestBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				if r.Context().Err() != context.DeadlineExceeded {
					t.Error("missing shared deadline")
				}
				w.Header().Set("Content-Length", "999")
				w.Header().Set("PAYMENT-RESPONSE", "stale")
				writeError(w, r, 500, "storage_failure", "should be replaced", nil)
			}), time.Millisecond)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			if response.Code != 504 || !strings.Contains(response.Body.String(), "request_timeout") || strings.Contains(response.Body.String(), "storage_failure") || response.Header().Get("Content-Length") != "" || response.Header().Get("PAYMENT-RESPONSE") != "" {
				t.Fatalf("timeout response: %d %s", response.Code, response.Body)
			}
		})
	}
}
func TestRequestBudgetDoesNotAppendErrorAfterCommit(t *testing.T) {
	response := httptest.NewRecorder()
	RequestBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "prefix")
		<-r.Context().Done()
		if _, err := io.WriteString(w, "suffix"); err != context.DeadlineExceeded {
			t.Errorf("write error = %v", err)
		}
	}), time.Millisecond).ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/blocks", nil))
	if response.Code != 200 || response.Body.String() != "prefix" {
		t.Fatalf("mixed response: %d %s", response.Code, response.Body)
	}
}
func TestRequestBudgetOnlyExemptsExactGETStreams(t *testing.T) {
	for _, test := range []struct {
		method, path string
		exempt       bool
	}{
		{"GET", "/api/v1/events", true}, {"GET", "/api/v1/home/stream", true},
		{"POST", "/api/v1/events", false}, {"GET", "/api/v1/events/extra", false},
	} {
		RequestBudget(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, deadline := r.Context().Deadline()
			if deadline == test.exempt {
				t.Errorf("unexpected deadline for %s %s", test.method, test.path)
			}
		}), time.Second).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(test.method, test.path, nil))
	}
}

func TestRequestBudgetSelectsPaymentBeforeAuthentication(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		payment      bool
	}{
		{"POST", "/api/v1/billing/topup-intents/id/pay", true},
		{"POST", "/api/v1/billing/topup-intents/id/pay?view=1", true},
		{"GET", "/api/v1/billing/topup-intents/id/pay", false},
		{"POST", "/api/v1/billing/topup-intents/id/pay/extra", false},
		{"POST", "/api/v1/billing/topup-intents/nested/id/pay", false},
		{"POST", "/api/v1/billing/topup-intents//pay", false},
		{"POST", "/api/v1/billing/topup-intents/../pay", false},
		{"POST", "/api/v1/billing/topup-intents", false},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			handler := RequestBudget(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				// This is the authentication position; no inner route has run yet.
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				expected := 20 * time.Second
				if tt.payment {
					expected = 75 * time.Second
				}
				if !ok || remaining > expected || remaining < expected-time.Second {
					t.Fatalf("selected budget %s, expected %s", remaining, expected)
				}
			}), 20*time.Second, 75*time.Second)
			// Include the real service boundary: it must not install another
			// ordinary deadline outside the payment budget.
			service := NewService(config.Default(), handler)
			service.server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))
		})
	}
}
