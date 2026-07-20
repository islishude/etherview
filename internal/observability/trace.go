package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TraceContext is compatible with the W3C trace-context fields used by
// OpenTelemetry propagators. It deliberately contains no exporter state.
type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Sampled      bool
}

// Span is the exporter-neutral record produced when a traced operation ends.
type Span struct {
	TraceContext
	Name       string
	StartedAt  time.Time
	Duration   time.Duration
	StatusCode string
	Attributes map[string]string
}

// SpanSink is an optional hook for an OpenTelemetry bridge or another trace
// exporter. Export failures must never fail the request being observed.
type SpanSink interface {
	ExportSpan(context.Context, Span) error
}

type traceContextKey struct{}

// TraceFromContext returns the active trace identity, if one is present.
func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	value, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return value, ok
}

// ParseTraceparent validates the W3C version 00 traceparent representation.
func ParseTraceparent(value string) (TraceContext, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" {
		return TraceContext{}, errors.New("unsupported traceparent")
	}
	if !validHexID(parts[1], 32) || !validHexID(parts[2], 16) {
		return TraceContext{}, errors.New("invalid traceparent identifiers")
	}
	flags, err := hex.DecodeString(parts[3])
	if err != nil || len(flags) != 1 {
		return TraceContext{}, errors.New("invalid traceparent flags")
	}
	return TraceContext{TraceID: parts[1], ParentSpanID: parts[2], Sampled: flags[0]&1 == 1}, nil
}

// Traceparent formats the active span as a W3C version 00 traceparent value.
func (trace TraceContext) Traceparent() string {
	flags := "00"
	if trace.Sampled {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", trace.TraceID, trace.SpanID, flags)
}

// StartSpan starts an in-process span, accepting a valid remote parent when
// supplied. The finish callback is safe to invoke with a nil attribute map.
func StartSpan(
	ctx context.Context,
	name string,
	remoteTraceparent string,
	sink SpanSink,
	now func() time.Time,
) (context.Context, TraceContext, func(status string, attributes map[string]string)) {
	if now == nil {
		now = time.Now
	}
	parent, parentErr := ParseTraceparent(remoteTraceparent)
	traceID := parent.TraceID
	if traceID == "" {
		traceID = randomHex(16)
	}
	trace := TraceContext{
		TraceID:      traceID,
		SpanID:       randomHex(8),
		ParentSpanID: parent.ParentSpanID,
		Sampled:      parent.Sampled,
	}
	if parentErr != nil {
		trace.Sampled = true
	}
	startedAt := now()
	spanCtx := context.WithValue(ctx, traceContextKey{}, trace)
	finish := func(status string, attributes map[string]string) {
		if sink == nil {
			return
		}
		span := Span{
			TraceContext: trace,
			Name:         name,
			StartedAt:    startedAt,
			Duration:     now().Sub(startedAt),
			StatusCode:   status,
			Attributes:   attributes,
		}
		_ = sink.ExportSpan(spanCtx, span)
	}
	return spanCtx, trace, finish
}

func validHexID(value string, expectedLength int) bool {
	if len(value) != expectedLength || strings.Trim(value, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func randomHex(byteLength int) string {
	value := make([]byte, byteLength)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Sprintf("observability: system random source failed: %v", err))
	}
	return hex.EncodeToString(value)
}
