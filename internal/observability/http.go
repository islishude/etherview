package observability

import (
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// HTTPOptions configures low-cardinality request metrics, JSON request logs,
// and exporter-neutral trace hooks.
type HTTPOptions struct {
	Registry      *Registry
	Logger        *slog.Logger
	Telemetry     *Telemetry
	SpanSink      SpanSink
	Now           func() time.Time
	Route         func(*http.Request) string
	PanicResponse func(http.ResponseWriter, *http.Request, string)
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
		writer := &statusWriter{ResponseWriter: response, status: http.StatusOK}
		var (
			ctx    = request.Context()
			method = boundedMethod(request.Method)
			route  = "unmatched"
			trace  TraceContext
			finish = func(string, map[string]string) {}
		)
		defer func() {
			recovered := recover()
			abort := false
			if recovered != nil {
				abortHandler := IsHTTPAbortHandlerPanic(recovered)
				committed := writer.ResponseCommitted()
				requestID := selectedRequestID(writer.Header().Get("X-Request-ID"), trace.TraceID)
				if !abortHandler && !committed {
					if options.PanicResponse != nil {
						options.PanicResponse(writer, request.WithContext(ctx), requestID)
					} else {
						writeOperationalPanicResponse(writer, requestID)
					}
				}
				if !abortHandler {
					writer.MarkPanicked()
					options.Logger.ErrorContext(ctx, "HTTP handler panic recovered",
						"error_code", "http_handler_panic",
						"error_type", fmt.Sprintf("%T", recovered),
						"method", method,
						"route", route,
					)
				}
				abort = abortHandler || committed
			}
			duration := options.Now().Sub(startedAt)
			options.Registry.ObserveHTTPRequest(method, route, writer.status, duration)
			if writer.panicked {
				options.Registry.RecordHTTPPanic(method, route)
			}
			status := "ok"
			if writer.status >= http.StatusInternalServerError || writer.panicked {
				status = "error"
			}
			spanValues := map[string]string{
				"http.request.method":       method,
				"http.route":                route,
				"http.response.status_code": strconv.Itoa(writer.status),
			}
			if writer.panicked {
				spanValues["error.type"] = "http_handler_panic"
			}
			finish(status, spanValues)
			options.Logger.InfoContext(ctx, "HTTP request completed",
				"method", method,
				"route", route,
				"status", writer.status,
				"result", requestResult(writer.panicked),
				"duration_ms", duration.Milliseconds(),
				"trace_id", trace.TraceID,
				"span_id", trace.SpanID,
			)
			if abort {
				panic(http.ErrAbortHandler)
			}
		}()

		route = boundedRoute(options.Route(request))
		if options.Telemetry != nil {
			ctx, trace, finish = options.Telemetry.startHTTP(ctx, request.Header, method+" "+route, method, route)
		} else {
			ctx, trace, finish = StartSpan(ctx, method+" "+route, request.Header.Get("traceparent"), options.SpanSink, options.Now)
		}
		response.Header().Set("traceparent", trace.Traceparent())
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// IsHTTPAbortHandlerPanic recognizes net/http's exact private abort sentinel
// without invoking hostile Error or Is methods. The comparability guard keeps
// panic values such as slices, maps, and non-comparable error implementations
// from triggering a second panic inside recovery.
func IsHTTPAbortHandlerPanic(value any) bool {
	valueType := reflect.TypeOf(value)
	return valueType != nil && valueType.Comparable() && value == http.ErrAbortHandler
}

func selectedRequestID(responseValue, traceID string) string {
	responseValue = strings.TrimSpace(responseValue)
	if responseValue != "" && len(responseValue) <= 128 {
		return responseValue
	}
	if traceID != "" {
		return traceID
	}
	return randomHex(16)
}

func requestResult(panicked bool) string {
	if panicked {
		return "panic"
	}
	return "completed"
}

func writeOperationalPanicResponse(writer http.ResponseWriter, requestID string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Request-ID", requestID)
	writer.WriteHeader(http.StatusInternalServerError)
	_, _ = writer.Write([]byte("{\"status\":\"error\"}\n"))
}

// RouteLabel returns only the pattern selected by a Go 1.22 ServeMux. Callers
// that instrument outside the mux should pass MuxRoutePattern explicitly.
func RouteLabel(request *http.Request) string {
	return normalizeMuxPattern(request.Pattern)
}

// MuxRoutePattern asks the registered ServeMux for its matching pattern and
// never derives labels from request identifiers. Go returns an empty pattern
// for both 404 and 405 responses, so a bounded method probe distinguishes a
// known path with the wrong method without duplicating the route table.
func MuxRoutePattern(mux *http.ServeMux, request *http.Request) string {
	if mux == nil || request == nil {
		return "unmatched"
	}
	_, pattern := mux.Handler(request)
	selected := normalizeMuxPattern(pattern)
	if selected != "" && selected != "/" {
		return selected
	}
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		if method == request.Method {
			continue
		}
		candidate := request.Clone(request.Context())
		candidate.Method = method
		_, candidatePattern := mux.Handler(candidate)
		candidateRoute := normalizeMuxPattern(candidatePattern)
		if candidateRoute != "" && candidateRoute != "/" {
			return "method_not_allowed"
		}
	}
	if selected == "/" {
		return selected
	}
	return "unmatched"
}

func normalizeMuxPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}
	if method, route, found := strings.Cut(pattern, " "); found && boundedMethod(method) == method && strings.HasPrefix(route, "/") {
		return route
	}
	return pattern
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	panicked    bool
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// ResponseCommitted lets an inner boundary avoid appending a second envelope
// after headers or a streaming body have already reached the client.
func (writer *statusWriter) ResponseCommitted() bool { return writer.wroteHeader }

// MarkPanicked records a stable failure signal without rewriting the HTTP
// status already placed on the wire.
func (writer *statusWriter) MarkPanicked() { writer.panicked = true }

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
