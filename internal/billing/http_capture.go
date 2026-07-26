package billing

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"
)

var (
	errCapturedBodyLimit   = errors.New("billing captured response body exceeded its limit")
	errCapturedHeaderLimit = errors.New("billing captured response headers exceeded their limit")
	errCapturedStreaming   = errors.New("billing captured response attempted unsupported streaming")
)

type capturedResponse struct {
	header          http.Header
	snapshot        http.Header
	body            bytes.Buffer
	status          int
	maxBodyBytes    int64
	maxHeaderBytes  int
	bodyOverflow    bool
	headerOverflow  bool
	streamViolation bool
}

func newCapturedResponse(maxBodyBytes int64, maxHeaderBytes int) *capturedResponse {
	return &capturedResponse{
		header:         make(http.Header),
		maxBodyBytes:   maxBodyBytes,
		maxHeaderBytes: maxHeaderBytes,
	}
}

func (capture *capturedResponse) Header() http.Header {
	return capture.header
}

func (capture *capturedResponse) WriteHeader(status int) {
	if capture.status != 0 {
		return
	}
	capture.status = status
	capture.snapshotHeaders()
}

func (capture *capturedResponse) Write(value []byte) (int, error) {
	if capture.status == 0 {
		capture.WriteHeader(http.StatusOK)
	}
	remaining := capture.maxBodyBytes - int64(capture.body.Len())
	if remaining < int64(len(value)) {
		capture.bodyOverflow = true
		if remaining > 0 {
			_, _ = capture.body.Write(value[:remaining])
		}
		return len(value), errCapturedBodyLimit
	}
	_, _ = capture.body.Write(value)
	return len(value), nil
}

// Flush, Hijack, and Push deliberately satisfy optional interfaces so an
// attempted use is recorded as a hard capture violation rather than silently
// changing behavior or escaping the settlement gate.
func (capture *capturedResponse) Flush() {
	capture.streamViolation = true
	if capture.status == 0 {
		capture.WriteHeader(http.StatusOK)
	}
}

func (capture *capturedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	capture.streamViolation = true
	return nil, nil, errCapturedStreaming
}

func (capture *capturedResponse) Push(string, *http.PushOptions) error {
	capture.streamViolation = true
	return errCapturedStreaming
}

func (capture *capturedResponse) snapshotHeaders() {
	if capture.snapshot != nil {
		return
	}
	capture.snapshot = capture.header.Clone()
	if headerBytes(capture.snapshot) > capture.maxHeaderBytes {
		capture.headerOverflow = true
	}
}

func (capture *capturedResponse) finish() error {
	if capture.status == 0 {
		capture.WriteHeader(http.StatusOK)
	}
	// Mutations after WriteHeader are not part of net/http's committed
	// response, but count them toward the hostile-memory boundary.
	if headerBytes(capture.header) > capture.maxHeaderBytes {
		capture.headerOverflow = true
	}
	switch {
	case capture.streamViolation:
		return errCapturedStreaming
	case capture.headerOverflow:
		return errCapturedHeaderLimit
	case capture.bodyOverflow:
		return errCapturedBodyLimit
	default:
		return nil
	}
}

func headerBytes(header http.Header) int {
	total := 0
	for name, values := range header {
		total += len(name) + 2
		for _, value := range values {
			total += len(value) + 2
		}
	}
	return total
}

func releaseCapturedResponse(destination http.ResponseWriter, capture *capturedResponse) {
	source := capture.snapshot
	if source == nil {
		source = capture.header
	}
	connectionHeaders := connectionHeaderNames(source)
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if forbiddenCapturedHeader(canonical, connectionHeaders) {
			continue
		}
		destination.Header().Del(canonical)
		for _, value := range values {
			destination.Header().Add(canonical, value)
		}
	}
	destination.WriteHeader(capture.status)
	_, _ = destination.Write(capture.body.Bytes())
}

func connectionHeaderNames(header http.Header) map[string]struct{} {
	result := make(map[string]struct{})
	for name, values := range header {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for token := range strings.SplitSeq(value, ",") {
				canonical := http.CanonicalHeaderKey(strings.TrimSpace(token))
				if canonical != "" {
					result[canonical] = struct{}{}
				}
			}
		}
	}
	return result
}

func forbiddenCapturedHeader(name string, connectionHeaders map[string]struct{}) bool {
	if _, ok := connectionHeaders[name]; ok {
		return true
	}
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding",
		"Upgrade", "Set-Cookie", "Content-Length",
		"X-Request-Id", "Cache-Control", "X-Content-Type-Options",
		"Content-Security-Policy", "Referrer-Policy", "Permissions-Policy",
		"Access-Control-Allow-Origin", "Access-Control-Expose-Headers",
		"Access-Control-Allow-Credentials", "Vary":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(name), "payment-")
	}
}
