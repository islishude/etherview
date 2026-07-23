package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/web"
)

type fakeReader struct {
	status StatusSnapshot
	err    error
}

type panicReader struct{ fakeReader }

func (panicReader) Status(context.Context) (StatusSnapshot, error) {
	panic("rpc https://user:secret@example.invalid and api-key-secret")
}

type panicValueReader struct {
	fakeReader
	value any
}

type compatibilityBackendStub struct{}

func (compatibilityBackendStub) Execute(context.Context, etherscan.Request) (any, error) {
	return "unused", nil
}

func (reader panicValueReader) Status(context.Context) (StatusSnapshot, error) {
	panic(reader.value)
}

func (f fakeReader) Status(context.Context) (StatusSnapshot, error) { return f.status, f.err }
func (f fakeReader) Blocks(context.Context, string, int) ([]gen.Block, string, error) {
	return []gen.Block{}, "next", f.err
}
func (f fakeReader) Block(context.Context, string) (gen.Block, error) {
	return gen.Block{}, f.err
}
func (f fakeReader) Transactions(context.Context, string, int) ([]gen.Transaction, string, error) {
	return []gen.Transaction{}, "next", f.err
}
func (f fakeReader) Transaction(context.Context, string) (gen.Transaction, error) {
	return gen.Transaction{}, f.err
}
func (f fakeReader) Address(context.Context, string) (gen.AddressSummary, error) {
	return gen.AddressSummary{}, f.err
}
func (f fakeReader) Search(context.Context, string, string, int) ([]gen.SearchResult, string, error) {
	return []gen.SearchResult{}, "", f.err
}

func testHandler(t *testing.T, reader Reader) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	cfg.Security.AllowedOrigins = []string{"https://explorer.example"}
	handler, err := New(Options{Config: cfg, Reader: reader, RequestID: func() string { return "request-1" }, Now: func() time.Time { return time.Unix(1, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestStatusUsesStringQuantitiesAndCompleteness(t *testing.T) {
	t.Parallel()
	complete := gen.Completeness{Core: gen.StageStateComplete, Trace: gen.StageStateUnavailable, Metadata: gen.StageStatePending, State: gen.StageStateComplete}
	handler := testHandler(t, fakeReader{status: StatusSnapshot{LatestBlock: 100, IndexedBlock: 98, CoverageStart: 0, CoverageEnd: 98, CoreReady: true, Completeness: complete}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("native API cache policy = %q", recorder.Header().Get("Cache-Control"))
	}
	var response gen.StatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ChainId != "11155111" || response.Data.Lag != "2" || response.Meta.RequestId != "request-1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestReadyRequiresRuntimeLifecycleAndDurableCoreReadiness(t *testing.T) {
	t.Parallel()
	runtimeReady := false
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{status: StatusSnapshot{CoreReady: true}},
		RuntimeReady: func() bool { return runtimeReady },
		RequestID:    func() string { return "ready-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		return recorder
	}
	if recorder := request(); recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "not_ready") {
		t.Fatalf("starting status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	runtimeReady = true
	if recorder := request(); recorder.Code != http.StatusOK {
		t.Fatalf("ready status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInputValidationStopsReader(t *testing.T) {
	t.Parallel()
	handler := testHandler(t, fakeReader{err: errors.New("reader should not be called")})
	for _, path := range []string{
		"/api/v1/blocks/not-a-block",
		"/api/v1/transactions/0x12",
		"/api/v1/addresses/0x12",
		"/api/v1/search?q=",
		"/api/v1/blocks?limit=101",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestReaderErrorsMapToMachineStates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		code int
		body string
	}{
		{ErrNotFound, 404, "not_found"},
		{ErrUnavailable, 503, "capability_unavailable"},
		{ErrNotReady, 503, "not_ready"},
		{ErrInvalidCursor, 400, "invalid_cursor"},
		{errors.New("db"), 500, "query_failed"},
	} {
		handler := testHandler(t, fakeReader{err: test.err})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/blocks/1", nil))
		if recorder.Code != test.code || !strings.Contains(recorder.Body.String(), test.body) {
			t.Fatalf("err=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}

func TestReaderCapabilityErrorIncludesOnlyStableDetails(t *testing.T) {
	t.Parallel()
	err := NewCapabilityUnavailableError("name", "failed", "resolver_failure")
	handler := testHandler(t, fakeReader{err: err})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=alice.eth", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "capability_unavailable" ||
		response.Error.Details["capability"] != "name" ||
		response.Error.Details["state"] != "failed" ||
		response.Error.Details["code"] != "resolver_failure" {
		t.Fatalf("response=%+v", response)
	}
	secret := "https://operator:secret@example.invalid/private"
	if invalid := NewCapabilityUnavailableError("name", "failed", secret); invalid != ErrUnavailable {
		t.Fatalf("unsafe capability detail was retained: %#v", invalid)
	}
}

func TestCORSIsExactAllowlistAndRequestIDIsBounded(t *testing.T) {
	t.Parallel()
	handler := testHandler(t, fakeReader{status: StatusSnapshot{CoreReady: true}})
	for _, origin := range []string{"https://explorer.example", "https://evil.example"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("X-Request-ID", strings.Repeat("x", 129))
		handler.ServeHTTP(recorder, request)
		if origin == "https://explorer.example" && recorder.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("allowed origin missing")
		}
		if origin != "https://explorer.example" && recorder.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unallowed origin reflected")
		}
		if recorder.Header().Get("X-Request-ID") != "request-1" {
			t.Fatal("oversized request ID was trusted")
		}
	}
}

func TestCORSPreflightUsesExactAllowlist(t *testing.T) {
	t.Parallel()
	handler := testHandler(t, fakeReader{})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/blocks", nil)
	request.Header.Set("Origin", "https://explorer.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Origin") != "https://explorer.example" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodOptions, "/api/v1/blocks", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRecoveredPanicDoesNotLogSensitiveValue(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	handler, err := New(Options{
		Config: config.Default(), Reader: panicReader{},
		Logger:    slog.New(slog.NewTextHandler(&logs, nil)),
		RequestID: func() string { return "panic-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	const expected = "{\"error\":{\"code\":\"internal_error\",\"message\":\"internal server error\",\"request_id\":\"panic-request\"}}\n"
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != expected {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Request-ID") != "panic-request" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("panic response headers=%v", recorder.Header())
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "example.invalid") {
		t.Fatalf("panic value leaked into logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "error_type=string") {
		t.Fatalf("sanitized panic type missing: %s", logs.String())
	}
}

func TestRecoveredPanicPreservesEtherscanEnvelope(t *testing.T) {
	var logs bytes.Buffer
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		Etherscan: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("etherscan-panic-secret")
		}),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)), RequestID: func() string { return "etherscan-panic-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v2/api", nil))
	const expected = "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"query failed\"}\n"
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != expected {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(logs.String(), "etherscan-panic-secret") {
		t.Fatalf("compatibility panic leaked: %s", logs.String())
	}
}

func TestCompatibilityRouteCoexistsWithOuterPreflightAndOwnsUnsupportedMethods(t *testing.T) {
	cfg := config.Default()
	cfg.Chain.ID = 1
	cfg.Security.AllowedOrigins = []string{"https://explorer.example"}
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		Etherscan: etherscan.Handler{ChainID: 1, Backend: compatibilityBackendStub{}},
		RequestID: func() string { return "compatibility-route-request" },
	})
	if err != nil {
		t.Fatalf("construct handler with compatibility route: %v", err)
	}
	for _, method := range []string{http.MethodPut, http.MethodHead} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/v2/api", nil))
		const expected = "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"unsupported HTTP method\"}\n"
		if response.Code != http.StatusMethodNotAllowed || response.Body.String() != expected {
			t.Errorf("%s status=%d body=%q", method, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodOptions, "/v2/api", nil)
	request.Header.Set("Origin", "https://explorer.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://explorer.example" {
		t.Fatalf("preflight status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestHandlerRecoveryHandlesUncomparableValuesAndPassesAbortSentinel(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "slice", value: []byte("native-slice-panic-secret")},
		{name: "map", value: map[string]string{"secret": "native-map-panic-secret"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler, err := New(Options{
				Config: config.Default(), Reader: panicValueReader{value: test.value},
				Logger: slog.New(slog.NewTextHandler(&logs, nil)), RequestID: func() string { return "uncomparable-request" },
			})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"request_id":"uncomparable-request"`) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if strings.Contains(logs.String(), "panic-secret") {
				t.Fatalf("uncomparable panic leaked: %s", logs.String())
			}
		})
	}

	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		Etherscan: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	var recovered any
	response := httptest.NewRecorder()
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/api", nil))
	}()
	if recovered != http.ErrAbortHandler || response.Body.Len() != 0 {
		t.Fatalf("abort sentinel=%#v status=%d body=%q", recovered, response.Code, response.Body.String())
	}
}

func TestCommittedInnerPanicKeepsWireResponseAndCountsOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := observability.NewLogger(observability.LoggerOptions{Writer: &logs})
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Logger: logger,
		Etherscan: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
			_, _ = response.Write([]byte("compatibility-partial"))
			response.(http.Flusher).Flush()
			panic("inner-committed-panic-secret")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := observability.NewRegistry("test", "api")
	wrapped := observability.HTTPMiddleware(handler, observability.HTTPOptions{
		Registry: registry, Logger: logger, Route: handler.RoutePattern,
		PanicResponse: WriteRecoveredPanicResponse,
	})
	response := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		wrapped.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/api", nil))
	}()
	if recovered != http.ErrAbortHandler || response.Code != http.StatusAccepted || response.Body.String() != "compatibility-partial" {
		t.Fatalf("recovered=%#v status=%d body=%q", recovered, response.Code, response.Body.String())
	}
	metric := `etherview_http_panics_total{method="GET",route="/v2/api"} 1`
	if !strings.Contains(registry.Gather(), metric) || strings.Contains(logs.String(), "inner-committed-panic-secret") {
		t.Fatalf("panic signal/log mismatch:\n%s\n%s", registry.Gather(), logs.String())
	}
}

func TestPublicHTTPServerUsesStableErrorLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := observability.NewLogger(observability.LoggerOptions{Writer: &logs})
	service := NewService(config.Default(), http.NotFoundHandler(), logger)
	if service.server.ErrorLog == nil {
		t.Fatal("public server has no internal error logger")
	}
	service.server.ErrorLog.Print("public panic secret and stack text")
	if strings.Contains(logs.String(), "public panic secret") || !strings.Contains(logs.String(), `"error_code":"http_server_error"`) {
		t.Fatalf("public server logger was not stable: %s", logs.String())
	}
}

func TestRoutePatternUsesRegisteredMuxPatternsAndBoundsCatchAll(t *testing.T) {
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Catalog: &fakeCatalog{},
		Web: webui.NewHandler(), Etherscan: http.NotFoundHandler(), Metrics: http.NotFoundHandler(),
		Events: events.NewBroker(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		path   string
		accept string
		want   string
	}{
		{http.MethodOptions, "/api/v1/blocks", "", "/{path...}"},
		{http.MethodGet, "/health/live", "", "/health/live"},
		{http.MethodGet, "/health/ready", "", "/health/ready"},
		{http.MethodGet, "/metrics", "", "/metrics"},
		{http.MethodGet, "/api/v1/status", "", "/api/v1/status"},
		{http.MethodGet, "/api/v1/config", "", "/api/v1/config"},
		{http.MethodGet, "/api/v1/blocks", "", "/api/v1/blocks"},
		{http.MethodGet, "/api/v1/blocks/123456", "", "/api/v1/blocks/{id}"},
		{http.MethodGet, "/api/v1/transactions", "", "/api/v1/transactions"},
		{http.MethodGet, "/api/v1/transactions/0xsecret", "", "/api/v1/transactions/{hash}"},
		{http.MethodGet, "/api/v1/pending", "", "/api/v1/pending"},
		{http.MethodGet, "/api/v1/transactions/0xsecret/trace", "", "/api/v1/transactions/{hash}/trace"},
		{http.MethodGet, "/api/v1/addresses/0xsecret", "", "/api/v1/addresses/{address}"},
		{http.MethodGet, "/api/v1/addresses/0xsecret/nfts", "", "/api/v1/addresses/{address}/nfts"},
		{http.MethodGet, "/api/v1/tokens", "", "/api/v1/tokens"},
		{http.MethodGet, "/api/v1/tokens/0xsecret", "", "/api/v1/tokens/{address}"},
		{http.MethodGet, "/api/v1/tokens/0xsecret/transfers", "", "/api/v1/tokens/{address}/transfers"},
		{http.MethodGet, "/api/v1/nfts/0xsecret/42", "", "/api/v1/nfts/{address}/{token_id}"},
		{http.MethodGet, "/api/v1/nfts/0xsecret/42/media", "", "/api/v1/nfts/{address}/{token_id}/media"},
		{http.MethodGet, "/api/v1/stats/blocks", "", "/api/v1/stats/blocks"},
		{http.MethodGet, "/api/v1/stats/summary", "", "/api/v1/stats/summary"},
		{http.MethodGet, "/api/v1/search", "", "/api/v1/search"},
		{http.MethodPost, "/api/v1/verification/jobs", "", "/api/v1/verification/jobs"},
		{http.MethodGet, "/api/v1/verification/jobs/job-secret", "", "/api/v1/verification/jobs/{id}"},
		{http.MethodGet, "/api/v1/contracts/0xsecret/verification", "", "/api/v1/contracts/{address}/verification"},
		{http.MethodGet, "/api/v1/sourcify/contracts/0xsecret", "", "/api/v1/sourcify/contracts/{address}"},
		{http.MethodPost, "/api/v1/sourcify/imports", "", "/api/v1/sourcify/imports"},
		{http.MethodPost, "/api/v1/verification/jobs/job-secret/sourcify", "", "/api/v1/verification/jobs/{id}/sourcify"},
		{http.MethodGet, "/api/v1/sourcify/jobs/job-secret", "", "/api/v1/sourcify/jobs/{verification_id}"},
		{http.MethodGet, "/api/v1/events", "", "/api/v1/events"},
		{http.MethodGet, "/v2/api", "", "/v2/api"},
		{http.MethodGet, "/", "text/html", "/"},
		{http.MethodGet, "/blocks/123456", "text/html", "/{spa...}"},
		{http.MethodGet, "/assets/user-secret.js", "*/*", "/assets/*"},
		{http.MethodGet, "/api/v1/not-registered/user-secret", "text/html", "unmatched"},
		{http.MethodGet, "/blocks/123456", "application/json", "unmatched"},
		{http.MethodDelete, "/api/v1/blocks/123456", "", "method_not_allowed"},
		{http.MethodGet, "/api/v1/verification/jobs", "", "method_not_allowed"},
		{http.MethodPost, "/blocks/123456", "text/html", "method_not_allowed"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		if test.accept != "" {
			request.Header.Set("Accept", test.accept)
		}
		if got := handler.RoutePattern(request); got != test.want {
			t.Errorf("RoutePattern(%s %s) = %q, want %q", test.method, test.path, got, test.want)
		} else if strings.Contains(got, "123456") || strings.Contains(got, "secret") {
			t.Errorf("RoutePattern(%s %s) leaked an identifier: %q", test.method, test.path, got)
		}
	}
}

func TestCursorRoundTripAndRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	type cursor struct {
		Number uint64 `json:"number"`
		Hash   string `json:"hash"`
	}
	encoded, err := EncodeCursor(cursor{Number: 7, Hash: "0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded cursor
	if err := DecodeCursor(encoded, &decoded); err != nil || decoded.Number != 7 {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if err := DecodeCursor("%%%", &decoded); err == nil {
		t.Fatal("expected malformed cursor rejection")
	}
	unknownValue, err := EncodeCursor(struct {
		Number uint64 `json:"number"`
		Extra  bool   `json:"extra"`
	}{Number: 7, Extra: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeCursor(unknownValue, &decoded); err == nil {
		t.Fatal("expected unknown cursor value field rejection")
	}
	unknownEnvelope := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"value":{"number":7,"hash":"0xabc"},"extra":true}`))
	if err := DecodeCursor(unknownEnvelope, &decoded); err == nil {
		t.Fatal("expected unknown cursor envelope field rejection")
	}
	trailingEnvelope := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"value":{"number":7,"hash":"0xabc"}} true`))
	if err := DecodeCursor(trailingEnvelope, &decoded); err == nil {
		t.Fatal("expected trailing cursor payload rejection")
	}
	nullValue := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"value":null}`))
	if err := DecodeCursor(nullValue, &decoded); err == nil {
		t.Fatal("expected null cursor value rejection")
	}
	if _, err := EncodeCursor(struct {
		Value string `json:"value"`
	}{Value: strings.Repeat("x", maximumOpaqueCursorLength)}); err == nil {
		t.Fatal("expected oversized encoded cursor rejection")
	}
	if err := DecodeCursor(strings.Repeat("x", maximumOpaqueCursorLength+1), &decoded); err == nil {
		t.Fatal("expected oversized cursor input rejection")
	}
}
