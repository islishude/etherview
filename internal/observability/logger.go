// Package observability provides structured logging, Prometheus exposition,
// and W3C trace-context hooks without making an exporter a correctness
// dependency.
package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

const redactedValue = "[REDACTED]"

// LoggerOptions describes stable process identity attached to every log line.
type LoggerOptions struct {
	Writer      io.Writer
	Level       slog.Leveler
	Service     string
	Version     string
	Environment string
	AddSource   bool
}

// NewLogger returns a JSON logger that redacts secret-bearing attributes and
// URL credentials before records reach the output handler.
func NewLogger(options LoggerOptions) *slog.Logger {
	writer := options.Writer
	if writer == nil {
		writer = os.Stderr
	}
	level := options.Level
	if level == nil {
		level = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		AddSource: options.AddSource,
		Level:     level,
	})
	logger := slog.New(&redactingHandler{next: handler})
	attributes := make([]any, 0, 6)
	if options.Service != "" {
		attributes = append(attributes, "service", options.Service)
	}
	if options.Version != "" {
		attributes = append(attributes, "version", options.Version)
	}
	if options.Environment != "" {
		attributes = append(attributes, "environment", options.Environment)
	}
	return logger.With(attributes...)
}

type redactingHandler struct {
	next slog.Handler
}

func (handler *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	traceIDSet := false
	spanIDSet := false
	record.Attrs(func(attribute slog.Attr) bool {
		traceIDSet = traceIDSet || attribute.Key == "trace_id"
		spanIDSet = spanIDSet || attribute.Key == "span_id"
		clean.AddAttrs(redactAttribute(attribute))
		return true
	})
	if trace, ok := TraceFromContext(ctx); ok {
		if !traceIDSet {
			clean.AddAttrs(slog.String("trace_id", trace.TraceID))
		}
		if !spanIDSet {
			clean.AddAttrs(slog.String("span_id", trace.SpanID))
		}
	}
	return handler.next.Handle(ctx, clean)
}

func (handler *redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		clean = append(clean, redactAttribute(attribute))
	}
	return &redactingHandler{next: handler.next.WithAttrs(clean)}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: handler.next.WithGroup(name)}
}

func redactAttribute(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if attribute.Value.Kind() == slog.KindAny {
		if err, ok := attribute.Value.Any().(error); ok {
			attribute.Value = slog.StringValue(fmt.Sprintf("%T", err))
			return attribute
		}
	}
	if isSensitiveKey(attribute.Key) {
		attribute.Value = slog.StringValue(redactedValue)
		return attribute
	}
	if attribute.Value.Kind() == slog.KindGroup {
		children := attribute.Value.Group()
		clean := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			clean = append(clean, redactAttribute(child))
		}
		attribute.Value = slog.GroupValue(clean...)
		return attribute
	}
	if isURLKey(attribute.Key) && attribute.Value.Kind() == slog.KindString {
		attribute.Value = slog.StringValue(redactURL(attribute.Value.String()))
	}
	return attribute
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, marker := range []string{
		"api_key", "authorization", "cookie", "credential", "database_url",
		"header", "panic", "password", "pepper", "private_key", "rpc_url",
		"secret", "token",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func isURLKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "url") || strings.Contains(key, "endpoint")
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redactedValue
	}
	// RPC providers commonly place credentials in userinfo, query parameters,
	// or the URL path. Only the origin is safe for routine logs.
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}
