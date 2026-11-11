package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
				writeError(w, r, 500, "storage_failure", "should be replaced", nil)
			}), time.Millisecond)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			if response.Code != 504 || !strings.Contains(response.Body.String(), "request_timeout") || strings.Contains(response.Body.String(), "storage_failure") || response.Header().Get("Content-Length") != "" {
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
