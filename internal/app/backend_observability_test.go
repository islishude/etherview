package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/adminstore"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/observability"
)

type panicLimiter struct{}

func (panicLimiter) Allow(context.Context, string, auth.Limit) (bool, time.Duration) {
	panic("outer-auth-rate-panic-secret")
}

func TestRepairStatusOutputFormatsAreClosedAndRedacted(t *testing.T) {
	requests := []adminstore.RepairRequest{{
		ID: 7, Operation: "reindex", Stage: "trace", FromBlock: 10, ToBlock: 20,
		Status: "failed", FailurePresent: true, RequestedAt: time.Unix(100, 0),
	}}
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeRepairRequests(&output, format, requests); err != nil {
				t.Fatal(err)
			}
			got := output.String()
			if !strings.Contains(strings.ToLower(got), "failure_present") || !strings.Contains(got, "true") {
				t.Fatalf("format %s omitted the bounded failure signal: %q", format, got)
			}
			if strings.Contains(strings.ToLower(got), "last_error") {
				t.Fatalf("format %s exposed the nested-error field: %q", format, got)
			}
		})
	}
	var output bytes.Buffer
	if err := writeRepairRequests(&output, "yaml", requests); err == nil || !strings.Contains(err.Error(), "json or table") {
		t.Fatalf("invalid output format error = %v", err)
	}
}

func TestPublicOuterAuthPanicPreservesNativeAndCompatibilityBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "native", path: "/api/v1/status"},
		{name: "etherscan", path: "/v2/api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := observability.NewRegistry("test", "api")
			var logs bytes.Buffer
			logger := observability.NewLogger(observability.LoggerOptions{Writer: &logs})
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("handler ran after rate limiter panic")
			})
			protected, err := (&Backend{}).protectPublicAPI(nil, config.Default(), registry, panicLimiter{}, next)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := observability.HTTPMiddleware(protected, observability.HTTPOptions{
				Registry: registry, Logger: logger,
				Route:         func(*http.Request) string { return test.path },
				PanicResponse: httpapi.WriteRecoveredPanicResponse,
			})
			response := httptest.NewRecorder()
			wrapped.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if test.path == "/v2/api" {
				const expected = "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"query failed\"}\n"
				if response.Body.String() != expected {
					t.Fatalf("compatibility body=%q", response.Body.String())
				}
			} else {
				requestID := response.Header().Get("X-Request-ID")
				expected := fmt.Sprintf("{\"error\":{\"code\":\"internal_error\",\"message\":\"internal server error\",\"request_id\":%q}}\n", requestID)
				if requestID == "" || len(requestID) > 128 || response.Body.String() != expected {
					t.Fatalf("request_id=%q body=%q", requestID, response.Body.String())
				}
			}
			if strings.Contains(logs.String(), "outer-auth-rate-panic-secret") ||
				!strings.Contains(registry.Gather(), `etherview_http_panics_total{method="GET",route="`+test.path+`"} 1`) {
				t.Fatalf("panic leaked or metric missing:\n%s\n%s", logs.String(), registry.Gather())
			}
		})
	}
}

func TestOperationalHTTPServerUsesStableErrorLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := observability.NewLogger(observability.LoggerOptions{Writer: &logs})
	server := (&operationalService{logger: logger}).httpServer()
	if server.ErrorLog == nil {
		t.Fatal("operational server has no internal error logger")
	}
	server.ErrorLog.Print("panic secret and stack text")
	if strings.Contains(logs.String(), "panic secret") || !strings.Contains(logs.String(), `"error_code":"http_server_error"`) {
		t.Fatalf("operational server logger was not stable: %s", logs.String())
	}
}
