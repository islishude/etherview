package observability

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPOptions configures low-cardinality request metrics, JSON request logs,
// and exporter-neutral trace hooks.
type HTTPOptions struct {
	Registry *Registry
	Logger   *slog.Logger
	SpanSink SpanSink
	Now      func() time.Time
	Route    func(*http.Request) string
}

// HTTPMiddleware instruments the supplied application handler. The default
// route classifier never uses a raw identifier as a metric label.
func HTTPMiddleware(next http.Handler, options HTTPOptions) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	if options.Registry == nil {
		options.Registry = NewRegistry("unknown", "unknown")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Route == nil {
		options.Route = RouteLabel
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := options.Now()
		route := options.Route(request)
		ctx, trace, finish := StartSpan(request.Context(), request.Method+" "+route, request.Header.Get("traceparent"), options.SpanSink, options.Now)
		response.Header().Set("traceparent", trace.Traceparent())
		writer := &statusWriter{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(writer, request.WithContext(ctx))
		duration := options.Now().Sub(startedAt)
		options.Registry.ObserveHTTPRequest(request.Method, route, writer.status, duration)
		status := "ok"
		if writer.status >= http.StatusInternalServerError {
			status = "error"
		}
		finish(status, map[string]string{
			"http.request.method":       request.Method,
			"http.route":                route,
			"http.response.status_code": strconv.Itoa(writer.status),
		})
		options.Logger.InfoContext(ctx, "HTTP request completed",
			"method", request.Method,
			"route", route,
			"status", writer.status,
			"duration_ms", duration.Milliseconds(),
			"trace_id", trace.TraceID,
			"span_id", trace.SpanID,
		)
	})
}

// RouteLabel maps the current public and operational routes to a bounded set.
func RouteLabel(request *http.Request) string {
	if request.Pattern != "" {
		return request.Pattern
	}
	path := request.URL.Path
	if path == "/" || path == "/v2/api" || path == "/metrics" || path == "/health/live" || path == "/health/ready" {
		return path
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) >= 3 && segments[0] == "api" && segments[1] == "v1" {
		switch segments[2] {
		case "blocks":
			if len(segments) == 3 {
				return "/api/v1/blocks"
			}
			return "/api/v1/blocks/{id}"
		case "transactions":
			return "/api/v1/transactions/{hash}"
		case "addresses":
			return "/api/v1/addresses/{address}"
		case "search", "status", "config", "events":
			return "/api/v1/" + segments[2]
		default:
			return "/api/v1/other"
		}
	}
	if len(segments) > 0 && segments[0] == "assets" {
		return "/assets/*"
	}
	return "unmatched"
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *statusWriter) Flush() {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// MountMetrics registers the Prometheus endpoint on an operational ServeMux.
func MountMetrics(mux *http.ServeMux, registry *Registry) {
	if mux == nil || registry == nil {
		return
	}
	mux.Handle("GET /metrics", registry.Handler())
}
