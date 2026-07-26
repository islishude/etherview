package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapturedResponseBoundsBodyHeadersAndOptionalInterfaces(t *testing.T) {
	t.Parallel()
	body := newCapturedResponse(4, 1024)
	if count, err := body.Write([]byte("12345")); count != 5 || err == nil {
		t.Fatalf("overflow write=(%d,%v)", count, err)
	}
	if err := body.finish(); err != errCapturedBodyLimit {
		t.Fatalf("body finish=%v", err)
	}

	headers := newCapturedResponse(1024, 8)
	headers.Header().Set("X-Large", strings.Repeat("x", 32))
	headers.WriteHeader(http.StatusOK)
	if err := headers.finish(); err != errCapturedHeaderLimit {
		t.Fatalf("header finish=%v", err)
	}

	for _, invoke := range []func(*capturedResponse){
		func(response *capturedResponse) { response.Flush() },
		func(response *capturedResponse) { _, _, _ = response.Hijack() },
		func(response *capturedResponse) { _ = response.Push("/", nil) },
	} {
		response := newCapturedResponse(1024, 1024)
		invoke(response)
		if err := response.finish(); err != errCapturedStreaming {
			t.Fatalf("optional interface finish=%v", err)
		}
	}
}

func TestReleaseCapturedResponseFiltersUnsafeAndForgedHeaders(t *testing.T) {
	t.Parallel()
	capture := newCapturedResponse(1024, 4096)
	capture.Header().Set("Content-Type", "application/json")
	capture.Header().Set("Set-Cookie", "secret=value")
	capture.Header().Set("Connection", "X-Hop")
	capture.Header().Set("X-Hop", "secret")
	capture.Header()["connection"] = []string{"x-lower-hop"}
	capture.Header()["x-lower-hop"] = []string{"secret"}
	capture.Header().Set("Payment-Response", "forged")
	capture.Header().Set("Payment-Future", "forged")
	capture.Header().Set("X-Request-ID", "forged")
	capture.WriteHeader(http.StatusCreated)
	_, _ = capture.Write([]byte(`{"ok":true}`))
	if err := capture.finish(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-ID", "trusted")
	recorder.Header().Set("Cache-Control", "no-store")
	releaseCapturedResponse(recorder, capture)
	if recorder.Code != http.StatusCreated || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/json" ||
		recorder.Header().Get("X-Request-ID") != "trusted" ||
		recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("safe headers=%v", recorder.Header())
	}
	for _, name := range []string{
		"Set-Cookie", "Connection", "X-Hop", "X-Lower-Hop",
		"Payment-Response", "Payment-Future",
	} {
		if value := recorder.Header().Get(name); value != "" {
			t.Fatalf("%s leaked as %q", name, value)
		}
	}
}
