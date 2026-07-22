package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoggerRedactsSecretsAndURLPaths(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerOptions{Writer: &output, Service: "api", Version: "test"})
	logger.Info("configured",
		"database_url", "postgres://user:pass@db/etherview",
		"upstream_endpoint", "https://rpc.example/provider-secret?key=secret",
		slog.Group("request", "authorization", "Bearer value", "safe", "visible"),
		"failure", errors.New("nested database secret"),
		"panic_value", "panic input secret",
	)
	line := output.String()
	for _, forbidden := range []string{"user", "pass", "provider-secret", "Bearer value", "nested database secret", "panic input secret"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, line)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	if decoded["service"] != "api" || decoded["upstream_endpoint"] != "https://rpc.example" {
		t.Fatalf("unexpected structured log: %#v", decoded)
	}
}

func TestLoggerAddsTraceIdentityFromContext(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerOptions{Writer: &output})
	ctx, trace, _ := StartSpan(context.Background(), "operation", "", nil, time.Now)
	logger.InfoContext(ctx, "linked")
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["trace_id"] != trace.TraceID || decoded["span_id"] != trace.SpanID {
		t.Fatalf("trace identity missing from log: %#v", decoded)
	}
}

func TestTraceparentValidationAndSpanHook(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	parsed, err := ParseTraceparent(parent)
	if err != nil || !parsed.Sampled || parsed.TraceID == "" {
		t.Fatalf("ParseTraceparent() = %#v, %v", parsed, err)
	}
	for _, invalid := range []string{
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	} {
		if _, err := ParseTraceparent(invalid); err == nil {
			t.Fatalf("ParseTraceparent(%q) unexpectedly succeeded", invalid)
		}
	}

	sink := &recordingSink{}
	nowValues := []time.Time{time.Unix(100, 0), time.Unix(102, 0)}
	now := func() time.Time {
		value := nowValues[0]
		nowValues = nowValues[1:]
		return value
	}
	ctx, trace, finish := StartSpan(context.Background(), "GET /health/live", parent, sink, now)
	if trace.TraceID != parsed.TraceID || trace.ParentSpanID != parsed.ParentSpanID {
		t.Fatalf("span did not retain remote parent: %#v", trace)
	}
	if active, ok := TraceFromContext(ctx); !ok || active.SpanID != trace.SpanID {
		t.Fatal("active trace missing from context")
	}
	finish("ok", map[string]string{"http.route": "/health/live"})
	if len(sink.spans) != 1 || sink.spans[0].Duration != 2*time.Second {
		t.Fatalf("unexpected exported spans: %#v", sink.spans)
	}

	_, generated, _ := StartSpan(context.Background(), "invalid-parent", "not-a-traceparent", nil, time.Now)
	if !generated.Sampled || generated.TraceID == "" || generated.ParentSpanID != "" {
		t.Fatalf("invalid remote parent was not replaced safely: %#v", generated)
	}
}

type snapshotSource struct {
	snapshot durableSnapshot
	err      error
}

func (source *snapshotSource) Snapshot(context.Context) (durableSnapshot, error) {
	return source.snapshot, source.err
}

func TestDurableMetricRefreshRetainsLastPostgresSnapshotOnFailure(t *testing.T) {
	registry := NewRegistry("test", "maintenance")
	source := &snapshotSource{snapshot: durableSnapshot{
		jobs: map[pair]uint64{
			{First: "trace", Second: "queued"}:            7,
			{First: "unexpected-stage", Second: "leased"}: 2,
			{First: "trace", Second: "succeeded"}:         99,
		},
		verification: map[string]uint64{"running": 3, "failed": 99},
		repairs: map[pair]uint64{
			{First: "repair", Second: "queued"}:   1,
			{First: "reindex", Second: "running"}: 4,
			{First: "reindex", Second: "done"}:    99,
		},
		repairOldestSeconds: 91,
	}}
	collector, err := NewDurableCollector(source, registry, DurableCollectorOptions{
		Interval: time.Second, Timeout: time.Second, Now: func() time.Time { return time.Unix(123, 0) },
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	collector.refresh(t.Context())
	first := registry.Gather()
	for _, expected := range []string{
		`etherview_jobs_pending{queue="trace"} 7`,
		`etherview_durable_jobs{stage="other",status="leased"} 2`,
		`etherview_verification_jobs{status="running"} 3`,
		`etherview_repair_requests{operation="reindex",status="running"} 4`,
		`etherview_repair_oldest_queued_seconds 91`,
		`etherview_observability_last_refresh_timestamp_seconds 123`,
	} {
		if !strings.Contains(first, expected) {
			t.Fatalf("metric snapshot missing %q:\n%s", expected, first)
		}
	}
	for _, terminal := range []string{`status="succeeded"`, `status="failed"`, `status="done"`} {
		if strings.Contains(first, terminal) {
			t.Fatalf("terminal history leaked into current gauges through %q:\n%s", terminal, first)
		}
	}
	source.err = errors.New("postgres://operator:secret@database")
	collector.refresh(t.Context())
	second := registry.Gather()
	if !strings.Contains(second, `etherview_jobs_pending{queue="trace"} 7`) ||
		!strings.Contains(second, `etherview_observability_refresh_failures_total 1`) ||
		!strings.Contains(second, `etherview_observability_last_refresh_timestamp_seconds 123`) {
		t.Fatalf("failed refresh did not retain and mark prior snapshot:\n%s", second)
	}
}

func TestMetricsAndHTTPMiddleware(t *testing.T) {
	registry := NewRegistry("v1.2.3", "api")
	registry.SetSyncLag(7)
	registry.ObserveReorg(2)
	registry.RecordSyncHalt("finalized_reorg")
	registry.RecordRPC("head", "error")
	registry.SetJobsPending("trace", 9)
	registry.RecordMetadataFetch("ssrf_rejected")
	registry.RecordRateLimit("rejected")

	clock := []time.Time{time.Unix(1, 0), time.Unix(1, int64(20*time.Millisecond)), time.Unix(1, int64(20*time.Millisecond))}
	now := func() time.Time {
		value := clock[0]
		clock = clock[1:]
		return value
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/blocks/{id}", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusCreated)
	})
	handler := HTTPMiddleware(mux, HTTPOptions{
		Registry: registry, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Now: now,
		Route: func(request *http.Request) string { return MuxRoutePattern(mux, request) },
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks/123456", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("traceparent") == "" {
		t.Fatal("traceparent response header is missing")
	}

	exposition := registry.Gather()
	for _, expected := range []string{
		`etherview_build_info{role="api",version="v1.2.3"} 1`,
		`etherview_sync_lag_blocks 7`,
		`etherview_sync_halted{reason="finalized_reorg"} 1`,
		`etherview_http_requests_total{method="GET",route="/api/v1/blocks/{id}",status="201"} 1`,
		`etherview_rpc_requests_total{purpose="head",result="error"} 1`,
		`etherview_jobs_pending{queue="trace"} 9`,
		`etherview_metadata_fetches_total{result="ssrf_rejected"} 1`,
	} {
		if !strings.Contains(exposition, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, exposition)
		}
	}
	if strings.Contains(exposition, "123456") {
		t.Fatalf("metrics leaked high-cardinality path: %s", exposition)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResponse := httptest.NewRecorder()
	registry.Handler().ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK || !strings.Contains(metricsResponse.Header().Get("Content-Type"), "version=0.0.4") {
		t.Fatalf("unexpected metrics response: %d %q", metricsResponse.Code, metricsResponse.Header().Get("Content-Type"))
	}
}

func TestHTTPMiddlewarePanicBoundariesPreserveWireStatusAndEndSpanOnce(t *testing.T) {
	t.Run("operational response", func(t *testing.T) {
		registry := NewRegistry("test", "api")
		sink := &recordingSink{}
		var logs bytes.Buffer
		handler := HTTPMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("operational-panic-secret")
		}), HTTPOptions{
			Registry: registry, Logger: NewLogger(LoggerOptions{Writer: &logs}), SpanSink: sink,
			Route: func(*http.Request) string { return "/health/live" },
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
		if response.Code != http.StatusInternalServerError || response.Body.String() != "{\"status\":\"error\"}\n" {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
		if len(sink.spans) != 1 || sink.spans[0].StatusCode != "error" || sink.spans[0].Attributes["error.type"] != "http_handler_panic" {
			t.Fatalf("panic span = %#v", sink.spans)
		}
		if !strings.Contains(registry.Gather(), `method="GET",route="/health/live",status="500"`) {
			t.Fatalf("panic metric missing:\n%s", registry.Gather())
		}
		if !strings.Contains(registry.Gather(), `etherview_http_panics_total{method="GET",route="/health/live"} 1`) {
			t.Fatalf("dedicated panic metric missing:\n%s", registry.Gather())
		}
		if strings.Contains(logs.String(), "operational-panic-secret") || !strings.Contains(logs.String(), `"error_type":"string"`) {
			t.Fatalf("panic log was not type-only: %s", logs.String())
		}
	})

	t.Run("committed stream", func(t *testing.T) {
		registry := NewRegistry("test", "api")
		sink := &recordingSink{}
		var logs bytes.Buffer
		handler := HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
			_, _ = response.Write([]byte("partial-safe"))
			response.(http.Flusher).Flush()
			panic("committed-panic-secret")
		}), HTTPOptions{
			Registry: registry, Logger: NewLogger(LoggerOptions{Writer: &logs}), SpanSink: sink,
			Route: func(*http.Request) string { return "/api/v1/events" },
		})
		response := httptest.NewRecorder()
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
		}()
		if recovered != http.ErrAbortHandler {
			t.Fatalf("recovered = %#v, want http.ErrAbortHandler", recovered)
		}
		if response.Code != http.StatusAccepted || response.Body.String() != "partial-safe" {
			t.Fatalf("committed response was rewritten: status=%d body=%q", response.Code, response.Body.String())
		}
		if len(sink.spans) != 1 || sink.spans[0].StatusCode != "error" || sink.spans[0].Attributes["http.response.status_code"] != "202" {
			t.Fatalf("committed panic span = %#v", sink.spans)
		}
		if !strings.Contains(registry.Gather(), `method="GET",route="/api/v1/events",status="202"`) {
			t.Fatalf("committed wire status was not preserved:\n%s", registry.Gather())
		}
		if !strings.Contains(registry.Gather(), `etherview_http_panics_total{method="GET",route="/api/v1/events"} 1`) {
			t.Fatalf("committed panic signal missing:\n%s", registry.Gather())
		}
		if strings.Contains(response.Body.String(), "status") || strings.Contains(logs.String(), "committed-panic-secret") {
			t.Fatalf("committed panic leaked or appended an envelope: body=%q logs=%s", response.Body.String(), logs.String())
		}
	})
}

func TestHTTPMiddlewareRecoversUncomparablePanicValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "slice", value: []byte("slice-panic-secret")},
		{name: "map", value: map[string]string{"secret": "map-panic-secret"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry("test", "api")
			var logs bytes.Buffer
			handler := HTTPMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(test.value)
			}), HTTPOptions{
				Registry: registry, Logger: NewLogger(LoggerOptions{Writer: &logs}),
				Route: func(*http.Request) string { return "/api/v1/status" },
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
			if response.Code != http.StatusInternalServerError || response.Body.String() != "{\"status\":\"error\"}\n" {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if strings.Contains(logs.String(), "panic-secret") || !strings.Contains(registry.Gather(), "etherview_http_panics_total") {
				t.Fatalf("uncomparable panic leaked or was not counted: %s\n%s", logs.String(), registry.Gather())
			}
		})
	}
}

func TestHTTPMiddlewarePassesThroughAbortHandlerSentinel(t *testing.T) {
	registry := NewRegistry("test", "api")
	var logs bytes.Buffer
	handler := HTTPMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), HTTPOptions{
		Registry: registry, Logger: NewLogger(LoggerOptions{Writer: &logs}),
		Route: func(*http.Request) string { return "/api/v1/events" },
	})
	response := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	}()
	if recovered != http.ErrAbortHandler || response.Body.Len() != 0 {
		t.Fatalf("recovered=%#v status=%d body=%q", recovered, response.Code, response.Body.String())
	}
	if strings.Contains(registry.Gather(), "etherview_http_panics_total{") || strings.Contains(logs.String(), "panic recovered") {
		t.Fatalf("intentional abort was treated as a handler panic:\n%s\n%s", registry.Gather(), logs.String())
	}
}

func TestHTTPMiddlewareBoundsUnknownMethodEverywhere(t *testing.T) {
	registry := NewRegistry("test", "api")
	sink := &recordingSink{}
	var logs bytes.Buffer
	handler := HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}), HTTPOptions{
		Registry: registry, Logger: NewLogger(LoggerOptions{Writer: &logs}), SpanSink: sink,
		Route: func(*http.Request) string { return "/health/live" },
	})
	request := httptest.NewRequest("SUPER-SECRET-METHOD-123", "/health/live", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if len(sink.spans) != 1 || sink.spans[0].Name != "OTHER /health/live" || sink.spans[0].Attributes["http.request.method"] != "OTHER" {
		t.Fatalf("bounded method span = %#v", sink.spans)
	}
	if !strings.Contains(registry.Gather(), `method="OTHER",route="/health/live",status="204"`) {
		t.Fatalf("bounded method metric missing:\n%s", registry.Gather())
	}
	if strings.Contains(logs.String(), "SUPER-SECRET") || !strings.Contains(logs.String(), `"method":"OTHER"`) {
		t.Fatalf("raw method reached logs: %s", logs.String())
	}
}

func TestMuxRoutePatternDistinguishesOperationalRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /metrics", func(http.ResponseWriter, *http.Request) {})
	for _, test := range []struct {
		method string
		path   string
		want   string
	}{
		{method: http.MethodGet, path: "/health/live", want: "/health/live"},
		{method: http.MethodPost, path: "/metrics", want: "method_not_allowed"},
		{method: http.MethodGet, path: "/not-registered/secret", want: "unmatched"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if got := MuxRoutePattern(mux, request); got != test.want {
			t.Errorf("MuxRoutePattern(%s %s)=%q, want %q", test.method, test.path, got, test.want)
		}
	}
}

func TestHTTPServerErrorLogDiscardsNetHTTPPanicText(t *testing.T) {
	var logs synchronizedBuffer
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("net-http-server-panic-secret")
	}))
	server.Config.ErrorLog = HTTPServerErrorLog(NewLogger(LoggerOptions{Writer: &logs}))
	server.Start()
	response, err := server.Client().Get(server.URL)
	if err == nil {
		_ = response.Body.Close()
	}
	server.Close()
	if !strings.Contains(logs.String(), `"error_code":"http_server_error"`) {
		t.Fatalf("stable server error event missing: %s", logs.String())
	}
	for _, forbidden := range []string{"net-http-server-panic-secret", "goroutine", "observability_test.go"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("server logger leaked %q: %s", forbidden, logs.String())
		}
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func TestOperationalMetricLabelsUseClosedStateMappings(t *testing.T) {
	for _, status := range []string{"queued", "leased", "running", "succeeded", "done", "failed", "cancelled"} {
		if got := boundedJobStatus(status); got != status {
			t.Fatalf("boundedJobStatus(%q) = %q", status, got)
		}
	}
	for _, stage := range []string{"proxy", "abi", "token", "stats", "trace", "nft-metadata", "verification"} {
		if got := boundedJobStage(stage); got != stage {
			t.Fatalf("boundedJobStage(%q) = %q", stage, got)
		}
	}
	for versioned, want := range map[string]string{
		"proxy@1": "proxy", "abi@1": "abi", "token@1": "token", "stats@2": "stats", "trace@1": "trace",
	} {
		if got := boundedJobStage(versioned); got != want {
			t.Fatalf("boundedJobStage(%q) = %q, want %q", versioned, got, want)
		}
	}
	for _, unsupported := range []string{"proxy@2", "abi@2", "token@2", "stats@1", "trace@2"} {
		if got := boundedJobStage(unsupported); got != "other" {
			t.Fatalf("boundedJobStage(%q) = %q, want other", unsupported, got)
		}
	}
	for _, operation := range []string{"repair", "reindex"} {
		if got := boundedMaintenanceOperation(operation); got != operation {
			t.Fatalf("boundedMaintenanceOperation(%q) = %q", operation, got)
		}
	}
	for _, value := range []string{"pending-replay-42", "https://rpc.invalid/key", strings.Repeat("x", 200)} {
		if boundedJobStatus(value) != "other" || boundedJobStage(value) != "other" || boundedMaintenanceOperation(value) != "other" ||
			boundedRPCPurpose(value) != "other" || boundedRPCResult(value) != "other" || boundedRateDecision(value) != "other" ||
			boundedSyncHaltReason(value) != "other" {
			t.Fatalf("uncontrolled label %q was not collapsed", value)
		}
	}
}

type recordingSink struct {
	spans []Span
}

func (sink *recordingSink) ExportSpan(_ context.Context, span Span) error {
	sink.spans = append(sink.spans, span)
	return nil
}
