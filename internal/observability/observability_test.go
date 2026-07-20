package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	)
	line := output.String()
	for _, forbidden := range []string{"user", "pass", "provider-secret", "Bearer value"} {
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
	handler := HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusCreated)
	}), HTTPOptions{Registry: registry, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Now: now})
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

type recordingSink struct {
	spans []Span
}

func (sink *recordingSink) ExportSpan(_ context.Context, span Span) error {
	sink.spans = append(sink.spans, span)
	return nil
}
