package observability

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TelemetryOptions configures an optional OTLP/HTTP trace pipeline. Endpoint
// is an already validated origin without credentials or path; an empty value
// must be handled by the caller by not constructing Telemetry at all.
type TelemetryOptions struct {
	Endpoint      string
	Insecure      bool
	SampleRatio   float64
	ExportTimeout time.Duration
	Service       string
	Version       string
	Environment   string
	Role          string
	InstanceID    string
	Logger        *slog.Logger
	// remoteSampleReader is a test seam. Production always uses crypto/rand.
	remoteSampleReader io.Reader
}

// Telemetry owns the OpenTelemetry provider and is also a supervisor service.
// Its Run method waits for process cancellation and flushes the SDK within the
// configured export timeout, so exporter shutdown shares the normal bounded
// component-drain path.
type Telemetry struct {
	provider      *sdktrace.TracerProvider
	tracer        trace.Tracer
	propagator    propagation.TraceContext
	exportTimeout time.Duration
	logger        *slog.Logger
	shutdown      atomic.Bool
}

// NewTelemetry starts an OTLP/HTTP exporter and SDK provider. It performs no
// collector reachability check: an exporter outage is an observability loss,
// not a readiness or request-serving dependency.
func NewTelemetry(ctx context.Context, options TelemetryOptions) (*Telemetry, error) {
	if options.Endpoint == "" {
		return nil, errors.New("OpenTelemetry endpoint is empty")
	}
	parsed, err := url.Parse(options.Endpoint)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("OpenTelemetry endpoint is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Scheme == "http" && !options.Insecure || parsed.Scheme == "https" && options.Insecure {
		return nil, errors.New("OpenTelemetry endpoint transport is inconsistent")
	}
	if math.IsNaN(options.SampleRatio) || math.IsInf(options.SampleRatio, 0) || options.SampleRatio < 0 || options.SampleRatio > 1 {
		return nil, errors.New("OpenTelemetry sample ratio is invalid")
	}
	if options.ExportTimeout <= 0 {
		options.ExportTimeout = 5 * time.Second
	}
	if options.Service == "" {
		options.Service = "etherview"
	}
	if options.InstanceID == "" {
		options.InstanceID = uuid.NewString()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	// Install the redacted error boundary before constructing the exporter so
	// malformed environment-sourced headers cannot reach the SDK's default
	// stderr handler during initialization.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(exportErr error) {
		options.Logger.Warn("OpenTelemetry export failed; application work continues",
			"error_code", "otel_export_failed",
			"error_type", fmt.Sprintf("%T", exportErr),
		)
	}))

	exporterOptions := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(parsed.Host),
		otlptracehttp.WithTimeout(options.ExportTimeout),
	}
	if options.Insecure {
		exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		// The exporter may include environment-sourced headers or endpoint
		// details in its nested error. Keep construction failures on the same
		// stable redacted boundary as asynchronous export failures.
		return nil, fmt.Errorf("initialize OTLP trace exporter: %T", err)
	}

	attributes := []attribute.KeyValue{
		attribute.String("service.name", options.Service),
		attribute.String("service.instance.id", options.InstanceID),
		attribute.String("service.version", options.Version),
		attribute.String("deployment.environment.name", options.Environment),
		attribute.String("etherview.runtime.roles", options.Role),
	}
	ratioSampler := sdktrace.TraceIDRatioBased(options.SampleRatio)
	remoteSampleReader := options.remoteSampleReader
	if remoteSampleReader == nil {
		remoteSampleReader = cryptorand.Reader
	}
	remoteSampler := &independentRatioSampler{ratio: options.SampleRatio, random: remoteSampleReader}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attributes...)),
		// A public caller's sampled traceparent is an identity/provenance input,
		// not authority to choose a trace ID that deterministically passes the
		// server's sampler. Keep local sampled children inherited and remote
		// unsampled parents off. Remote sampled parents receive an independent
		// cryptographic Bernoulli decision whose long-run expectation is the
		// configured ratio; the ratio is not a per-client hard quota.
		sdktrace.WithSampler(sdktrace.ParentBased(
			ratioSampler,
			sdktrace.WithRemoteParentSampled(remoteSampler),
		)),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithExportTimeout(options.ExportTimeout),
			sdktrace.WithBatchTimeout(time.Second),
		),
	)
	return &Telemetry{
		provider: provider, tracer: provider.Tracer("github.com/islishude/etherview"),
		propagator: propagation.TraceContext{}, exportTimeout: options.ExportTimeout,
		logger: options.Logger,
	}, nil
}

// independentRatioSampler prevents a remote caller from selecting or replaying
// a trace ID that deterministically passes TraceIDRatioBased. It samples from a
// fresh server-side random value for every decision and fails closed if that
// value cannot be read.
type independentRatioSampler struct {
	ratio  float64
	random io.Reader
	mu     sync.Mutex
}

func (sampler *independentRatioSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	result := sdktrace.SamplingResult{
		Decision:   sdktrace.Drop,
		Tracestate: trace.SpanContextFromContext(parameters.ParentContext).TraceState(),
	}
	if sampler == nil || sampler.ratio <= 0 || (sampler.random == nil && sampler.ratio < 1) {
		return result
	}
	if sampler.ratio >= 1 {
		result.Decision = sdktrace.RecordAndSample
		return result
	}
	var raw [8]byte
	sampler.mu.Lock()
	_, err := io.ReadFull(sampler.random, raw[:])
	sampler.mu.Unlock()
	if err != nil {
		return result
	}
	// Use the high 53 bits so conversion to float64 is exact and produces a
	// uniform value in [0,1). The decision is independent of parameters.TraceID.
	randomValue := binary.BigEndian.Uint64(raw[:]) >> 11
	if float64(randomValue)/float64(uint64(1)<<53) < sampler.ratio {
		result.Decision = sdktrace.RecordAndSample
	}
	return result
}

func (sampler *independentRatioSampler) Description() string {
	if sampler == nil {
		return "IndependentRatioBased{0}"
	}
	return fmt.Sprintf("IndependentRatioBased{%g}", sampler.ratio)
}

func (*Telemetry) Name() string { return "opentelemetry-traces" }

// Run blocks until shutdown, then flushes the provider. Export failure is
// logged as a stable degraded signal and never replaces the process result.
func (telemetry *Telemetry) Run(ctx context.Context) error {
	if telemetry == nil || telemetry.provider == nil {
		return errors.New("run nil OpenTelemetry provider")
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetry.exportTimeout)
	defer cancel()
	telemetry.Shutdown(shutdownCtx)
	return ctx.Err()
}

// Shutdown closes the SDK within a caller-provided budget. It is safe to call
// after Run and lets startup wiring close a provider if component assembly
// fails after the exporter was constructed but before the supervisor starts.
func (telemetry *Telemetry) Shutdown(ctx context.Context) {
	if telemetry == nil || telemetry.provider == nil {
		return
	}
	// The assembly cleanup and supervisor may race at the handoff boundary.
	// Only the first owner starts SDK shutdown; later calls return immediately
	// instead of extending the supervisor's bounded drain while it is in flight.
	if !telemetry.shutdown.CompareAndSwap(false, true) {
		return
	}
	if err := telemetry.provider.Shutdown(ctx); err != nil {
		telemetry.logger.Warn("OpenTelemetry shutdown did not flush all spans",
			"error_code", "otel_shutdown_failed",
			"error_type", fmt.Sprintf("%T", err),
		)
	}
}

func (telemetry *Telemetry) startHTTP(
	ctx context.Context,
	header http.Header,
	name string,
	method string,
	route string,
) (context.Context, TraceContext, func(string, map[string]string)) {
	parentCtx := telemetry.propagator.Extract(ctx, propagation.HeaderCarrier(header))
	parent := trace.SpanContextFromContext(parentCtx)
	spanCtx, span := telemetry.tracer.Start(parentCtx, name,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.request.method", method),
			attribute.String("http.route", route),
		),
	)
	current := span.SpanContext()
	identity := TraceContext{
		TraceID: current.TraceID().String(), SpanID: current.SpanID().String(),
		Sampled: current.IsSampled(),
	}
	if parent.IsValid() {
		identity.ParentSpanID = parent.SpanID().String()
	}
	finish := func(status string, values map[string]string) {
		attributes := make([]attribute.KeyValue, 0, len(values))
		for key, value := range values {
			attributes = append(attributes, attribute.String(key, value))
		}
		span.SetAttributes(attributes...)
		if status == "error" {
			span.SetStatus(codes.Error, "HTTP server error")
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
	return spanCtx, identity, finish
}
