package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestOTLPConfigurationRejectsUnsafeOrInconsistentValues(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		insecure   bool
		sampleRate float64
	}{
		{name: "empty endpoint", sampleRate: 1},
		{name: "credentials", endpoint: "https://user:secret@otel.example:4318", sampleRate: 1},
		{name: "path", endpoint: "https://otel.example:4318/private", sampleRate: 1},
		{name: "query", endpoint: "https://otel.example:4318?header=secret", sampleRate: 1},
		{name: "fragment", endpoint: "https://otel.example:4318#secret", sampleRate: 1},
		{name: "wrong scheme", endpoint: "grpc://otel.example:4317", sampleRate: 1},
		{name: "implicit plaintext", endpoint: "http://otel.example:4318", sampleRate: 1},
		{name: "insecure https", endpoint: "https://otel.example:4318", insecure: true, sampleRate: 1},
		{name: "negative sample", endpoint: "https://otel.example:4318", sampleRate: -0.1},
		{name: "large sample", endpoint: "https://otel.example:4318", sampleRate: 1.1},
		{name: "nan sample", endpoint: "https://otel.example:4318", sampleRate: math.NaN()},
		{name: "infinite sample", endpoint: "https://otel.example:4318", sampleRate: math.Inf(1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewTelemetry(t.Context(), TelemetryOptions{
				Endpoint: test.endpoint, Insecure: test.insecure, SampleRatio: test.sampleRate,
				ExportTimeout: time.Second,
			})
			if err == nil {
				t.Fatal("unsafe OpenTelemetry configuration was accepted")
			}
			for _, secret := range []string{"user:secret", "header=secret", "#secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("validation error leaked endpoint details: %v", err)
				}
			}
		})
	}
}

func TestOTLPHTTPTracingExportsAndPropagatesW3CIdentity(t *testing.T) {
	var (
		mu          sync.Mutex
		exportPath  string
		exportBody  []byte
		contentType string
		collectorID string
	)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-otel-token=otel-test-secret")
	collector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read OTLP body: %v", err)
		}
		mu.Lock()
		exportPath = request.URL.Path
		exportBody = body
		contentType = request.Header.Get("Content-Type")
		collectorID = request.Header.Get("X-Otel-Token")
		mu.Unlock()
		response.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	var logs bytes.Buffer
	logger := NewLogger(LoggerOptions{Writer: &logs})
	telemetry, err := NewTelemetry(t.Context(), TelemetryOptions{
		Endpoint: collector.URL, Insecure: true, SampleRatio: 1,
		ExportTimeout: 2 * time.Second, Service: "etherview", Version: "test",
		Environment: "test", Role: "api", InstanceID: "test-instance", Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		logger.InfoContext(request.Context(), "inside traced request")
		response.WriteHeader(http.StatusNoContent)
	}), HTTPOptions{Registry: NewRegistry("test", "api"), Logger: logger, Telemetry: telemetry})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	traceparent := response.Header().Get("traceparent")
	if !strings.HasPrefix(traceparent, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || !strings.HasSuffix(traceparent, "-01") {
		t.Fatalf("response did not preserve the sampled remote trace: %q", traceparent)
	}
	if !strings.Contains(logs.String(), `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`) {
		t.Fatalf("structured logs are not correlated with OTel context: %s", logs.String())
	}
	if strings.Contains(logs.String(), "otel-test-secret") {
		t.Fatalf("structured logs leaked the collector header: %s", logs.String())
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := telemetry.Run(shutdownCtx); err == nil {
		t.Fatal("telemetry service did not return the supervisor cancellation")
	}
	mu.Lock()
	defer mu.Unlock()
	if exportPath != "/v1/traces" || len(exportBody) == 0 || contentType != "application/x-protobuf" || collectorID != "otel-test-secret" {
		t.Fatalf("OTLP export path=%q bytes=%d content-type=%q secret-header-present=%t",
			exportPath, len(exportBody), contentType, collectorID != "")
	}
}

func TestRemoteSampledTraceUsesIndependentServerRandomness(t *testing.T) {
	var exports atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		exports.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	randomValues := append(bytes.Repeat([]byte{0xff}, 8), make([]byte, 8)...)
	telemetry, err := NewTelemetry(t.Context(), TelemetryOptions{
		Endpoint: collector.URL, Insecure: true, SampleRatio: 0.5,
		ExportTimeout: 2 * time.Second, Service: "etherview", Role: "api",
		remoteSampleReader: bytes.NewReader(randomValues),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}), HTTPOptions{Registry: NewRegistry("test", "api"), Telemetry: telemetry})
	const replayedLowTailParent = "00-10000000000000000000000000000000-00f067aa0ba902b7-01"
	for index, expectedFlag := range []string{"-00", "-01"} {
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("traceparent", replayedLowTailParent)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		traceparent := response.Header().Get("traceparent")
		if !strings.HasPrefix(traceparent, "00-10000000000000000000000000000000-") || !strings.HasSuffix(traceparent, expectedFlag) {
			t.Fatalf("decision %d traceparent=%q, want preserved identity and flag %q", index, traceparent, expectedFlag)
		}
	}
	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = telemetry.Run(shutdownCtx)
	if exports.Load() != 1 {
		t.Fatalf("OTLP export requests = %d, want one independently sampled replay", exports.Load())
	}
}

func TestIndependentRatioSamplerBoundariesFailureAndTraceState(t *testing.T) {
	traceID, err := oteltrace.TraceIDFromHex("10000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	traceState, err := oteltrace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: oteltrace.FlagsSampled,
		TraceState: traceState, Remote: true,
	})
	parameters := sdktrace.SamplingParameters{
		ParentContext: oteltrace.ContextWithRemoteSpanContext(t.Context(), parent), TraceID: traceID,
	}
	for _, test := range []struct {
		name     string
		sampler  *independentRatioSampler
		decision sdktrace.SamplingDecision
	}{
		{name: "zero exact", sampler: &independentRatioSampler{ratio: 0, random: iotest.ErrReader(errors.New("unused"))}, decision: sdktrace.Drop},
		{name: "one exact", sampler: &independentRatioSampler{ratio: 1, random: iotest.ErrReader(errors.New("unused"))}, decision: sdktrace.RecordAndSample},
		{name: "random failure drops", sampler: &independentRatioSampler{ratio: 0.5, random: iotest.ErrReader(errors.New("random unavailable"))}, decision: sdktrace.Drop},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := test.sampler.ShouldSample(parameters)
			if result.Decision != test.decision || result.Tracestate.String() != "vendor=value" {
				t.Fatalf("result=%#v trace-state=%q", result, result.Tracestate.String())
			}
		})
	}

	source := append(make([]byte, 8), bytes.Repeat([]byte{0xff}, 8)...)
	sampler := &independentRatioSampler{ratio: 0.5, random: bytes.NewReader(source)}
	first, second := sampler.ShouldSample(parameters), sampler.ShouldSample(parameters)
	if first.Decision != sdktrace.RecordAndSample || second.Decision != sdktrace.Drop {
		t.Fatalf("replayed trace ID determined decisions: first=%v second=%v", first.Decision, second.Decision)
	}
}

func TestOTLPExporterFailureIsRedactedAndNonFatal(t *testing.T) {
	var logs bytes.Buffer
	logger := NewLogger(LoggerOptions{Writer: &logs})
	telemetry, err := NewTelemetry(t.Context(), TelemetryOptions{
		Endpoint: "http://127.0.0.1:1", Insecure: true, SampleRatio: 1,
		ExportTimeout: 100 * time.Millisecond, Service: "etherview", Environment: "test", Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, finish := telemetry.startHTTP(t.Context(), http.Header{}, "GET /", http.MethodGet, "/")
	logger.InfoContext(ctx, "request still succeeds")
	finish("ok", map[string]string{"http.response.status_code": "200"})
	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = telemetry.Run(shutdownCtx)
	if !strings.Contains(logs.String(), "request still succeeds") {
		t.Fatalf("request path did not continue: %s", logs.String())
	}
	if strings.Contains(logs.String(), "127.0.0.1:1") {
		t.Fatalf("exporter failure leaked endpoint details: %s", logs.String())
	}
}
