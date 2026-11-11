package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errRequestBudget = errors.New("API request budget exhausted")

// RequestBudget wraps authentication as well as the business handler. Work
// cooperatively observes one deadline; no detached handler goroutine survives it.
func RequestBudget(next http.Handler, budget time.Duration) http.Handler {
	if budget <= 0 {
		budget = 20 * time.Second
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api := strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/v2/api"
		stream := r.Method == http.MethodGet && (r.URL.Path == "/api/v1/events" || r.URL.Path == "/api/v1/home/stream")
		if !api || stream {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeoutCause(r.Context(), budget, errRequestBudget)
		defer cancel()
		request := r.WithContext(ctx)
		writer := &budgetWriter{ResponseWriter: w, request: request}
		next.ServeHTTP(writer, request)
		if !writer.committed && errors.Is(context.Cause(ctx), errRequestBudget) {
			writer.timeout()
		}
	})
}

type budgetWriter struct {
	http.ResponseWriter
	request   *http.Request
	committed bool
	timedOut  bool
}

func (w *budgetWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *budgetWriter) ResponseCommitted() bool     { return w.committed }
func (w *budgetWriter) MarkPanicked() {
	if observer, ok := w.ResponseWriter.(interface{ MarkPanicked() }); ok {
		observer.MarkPanicked()
	}
}
func (w *budgetWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	if errors.Is(context.Cause(w.request.Context()), errRequestBudget) {
		w.timeout()
		return
	}
	if status >= 200 {
		w.committed = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *budgetWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if w.timedOut || errors.Is(context.Cause(w.request.Context()), errRequestBudget) {
		return 0, context.DeadlineExceeded
	}
	return w.ResponseWriter.Write(body)
}
func (w *budgetWriter) timeout() {
	if w.committed {
		return
	}
	w.committed, w.timedOut = true, true
	w.Header().Del("Content-Length")
	if w.request.URL.Path == "/v2/api" {
		writeJSON(w.ResponseWriter, http.StatusGatewayTimeout, map[string]string{"status": "0", "message": "NOTOK", "result": "request_timeout"})
		return
	}
	request := w.request.WithContext(context.WithValue(w.request.Context(), requestIDKey{}, w.Header().Get("X-Request-ID")))
	writeError(w.ResponseWriter, request, http.StatusGatewayTimeout, "request_timeout", "request execution timed out", nil)
}
