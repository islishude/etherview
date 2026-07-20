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
)

type fakeReader struct {
	status StatusSnapshot
	err    error
}

type panicReader struct{ fakeReader }

func (panicReader) Status(context.Context) (StatusSnapshot, error) {
	panic("rpc https://user:secret@example.invalid and api-key-secret")
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
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "internal_error") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "example.invalid") {
		t.Fatalf("panic value leaked into logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "panic_type=string") {
		t.Fatalf("sanitized panic type missing: %s", logs.String())
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
