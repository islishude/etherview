package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHealthcheckUsesBoundedLoopbackReadinessEndpointWithoutBackend(t *testing.T) {
	observed := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		observed <- request.Method + " " + request.URL.Path
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	var stderr bytes.Buffer
	program := Program{Stdout: &bytes.Buffer{}, Stderr: &stderr}
	if code := program.Run(
		context.Background(),
		[]string{"healthcheck", "--url", server.URL + "/health/ready", "--timeout=1s"},
	); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if request := <-observed; request != "GET /health/ready" {
		t.Fatalf("request = %q", request)
	}
}

func TestHealthcheckRejectsUnreadyAndRedirectResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "unready", status: http.StatusServiceUnavailable},
		{name: "redirect", status: http.StatusTemporaryRedirect},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if test.status == http.StatusTemporaryRedirect {
					response.Header().Set("Location", "/health/ready")
				}
				response.WriteHeader(test.status)
			}))
			t.Cleanup(server.Close)

			var stderr bytes.Buffer
			program := Program{Stdout: &bytes.Buffer{}, Stderr: &stderr}
			code := program.Run(context.Background(), []string{
				"healthcheck", "--url=" + server.URL + "/health/ready",
			})
			want := "health endpoint returned status " + strconv.Itoa(test.status)
			if code != 1 || !strings.Contains(stderr.String(), want) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestHealthcheckRejectsUnsafeOrUnboundedInputs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "remote host", args: []string{"--url=http://example.com:9090/health/ready"}},
		{name: "DNS host", args: []string{"--url=http://localhost:9090/health/ready"}},
		{name: "HTTPS", args: []string{"--url=https://127.0.0.1:9090/health/ready"}},
		{name: "credentials", args: []string{"--url=http://user:secret@127.0.0.1:9090/health/ready"}},
		{name: "query", args: []string{"--url=http://127.0.0.1:9090/health/ready?secret=value"}},
		{name: "wrong path", args: []string{"--url=http://127.0.0.1:9090/metrics"}},
		{name: "missing port", args: []string{"--url=http://127.0.0.1/health/ready"}},
		{name: "zero timeout", args: []string{"--timeout=0s"}},
		{name: "long timeout", args: []string{"--timeout=11s"}},
		{name: "positional", args: []string{"extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			program := Program{Stdout: &bytes.Buffer{}, Stderr: &stderr}
			args := append([]string{"healthcheck"}, test.args...)
			if code := program.Run(context.Background(), args); code != 1 || stderr.Len() == 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}
