package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func startProxy(t *testing.T, handler http.Handler) (*httptest.Server, *http.Transport) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	transport := proxyTransport()
	t.Cleanup(transport.CloseIdleConnections)
	server := httptest.NewServer(gatewayHandler(u, transport))
	t.Cleanup(server.Close)
	return server, transport
}

func TestProxyContract(t *testing.T) {
	server, _ := startProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "ipfs.preview.test" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("incorrect forwarding boundary")
		}
		w.Header().Set("X-URI", r.RequestURI)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "content")
	}))
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/ipfs/cid/file%20name?format=raw&x=a%2Fb", 206},
		{"GET", "/ipfs/cid/100%25.txt", 206},
		{"GET", "/ipfs/cid/file?filename=a;b&raw=%zz", 206},
		{"HEAD", "/ipfs/cid/file", 206}, {"POST", "/ipfs/cid", 403},
		{"GET", "/api/v0/version", 404}, {"GET", "/", 404}, {"GET", "/ipfs/../api/v0/version", 404},
		{"GET", "/ipfs/%2e%2e/api", 404}, {"GET", "/ipfs/cid/%2f..%2fapi", 404},
		{"GET", "/ipfs/cid/%252e%252e/api", 404}, {"GET", "/ipfs/cid/%5capi", 404},
		{"GET", "/%69pfs/cid", 404},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			req, _ := http.NewRequest(test.method, server.URL+test.path, nil)
			req.Header.Set("Forwarded", "host=secret")
			req.Header.Set("X-Forwarded-For", "spoof")
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != test.status {
				t.Fatalf("status %d want %d", resp.StatusCode, test.status)
			}
			if test.status == 206 && resp.Header.Get("X-URI") != test.path {
				t.Fatal("URI changed")
			}
			if test.method == "HEAD" && len(body) != 0 {
				t.Fatal("HEAD body")
			}
		})
	}
}

func TestStreamingAndCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	server, _ := startProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/ipfs/cid", nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data := make([]byte, 5)
	if _, err = io.ReadFull(resp.Body, data); err != nil || string(data) != "first" {
		t.Fatalf("stream: %s %v", data, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream was not cancelled")
	}
}

type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("http://secret:credential@private.invalid failure")
}
func TestRedactedError(t *testing.T) {
	u, _ := url.Parse("http://private.invalid")
	server := httptest.NewServer(gatewayHandler(u, errorTransport{}))
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/ipfs/cid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 502 || string(body) != "upstream_unavailable\n" {
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, body)
	}

}

func TestHeaderTimeout(t *testing.T) {
	server, transport := startProxy(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	transport.ResponseHeaderTimeout = 20 * time.Millisecond
	resp, err := server.Client().Get(server.URL + "/ipfs/cid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 502 {
		t.Fatal(resp.StatusCode)
	}
}

func TestIdleConnectionDeadlines(t *testing.T) {
	for _, write := range []bool{false, true} {
		a, b := net.Pipe()
		c := &idleConn{Conn: a, timeout: 20 * time.Millisecond}
		var err error
		if write {
			_, err = c.Write([]byte("x"))
		} else {
			_, err = c.Read(make([]byte, 1))
		}
		_ = a.Close()
		_ = b.Close()
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("expected idle timeout: %v", err)
		}
	}
}

func TestInvalidRequests(t *testing.T) {
	u, _ := url.Parse("http://unused")
	for _, upgrade := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/ipfs/cid", strings.NewReader("body"))
		if upgrade {
			r = httptest.NewRequest("GET", "/ipfs/cid", nil)
			r.Header.Set("Upgrade", "websocket")
		}
		w := httptest.NewRecorder()
		gatewayHandler(u, errorTransport{}).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestStalledUpstreamBody(t *testing.T) {
	server, transport := startProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err == nil {
			conn.(*idleConn).timeout = 30 * time.Millisecond
		}
		return conn, err
	}
	resp, err := server.Client().Get(server.URL + "/ipfs/cid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err == nil || string(body) != "first" {
		t.Fatalf("stalled stream not terminated: %q %v", body, err)
	}
}

// A client which never reads must not hold a streaming response indefinitely.
func TestStalledDownstream(t *testing.T) {
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writer := &streamWriter{ResponseWriter: w, idle: 30 * time.Millisecond}
		block := make([]byte, 64<<10)
		for range 1024 {
			if _, err := writer.Write(block); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("expected write timeout: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("downstream blocked indefinitely")
	}
}
