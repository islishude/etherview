//go:build previewgatewaye2e

// Package previewgateway tests the shipped Caddyfile against the real pinned image.
package previewgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type gateway struct {
	t           *testing.T
	id, binding string
	client      *http.Client
	roots       *x509.CertPool
	calls       atomic.Int64
	canceled    chan struct{}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	binary := os.Getenv("DOCKER")
	if binary == "" {
		binary = "docker"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, output)
	}
	return strings.TrimSpace(string(output))
}

func start(t *testing.T) *gateway {
	t.Helper()
	return startWithOrigins(t, "https://app.etherview.localhost http://localhost:5173 http://127.0.0.1:5173")
}

func startWithOrigins(t *testing.T, origins string) *gateway {
	t.Helper()
	g := &gateway{t: t, canceled: make(chan struct{}, 8)}
	cert, key, roots := certificate(t)
	g.roots = roots
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "-d", "--add-host", "host.docker.internal:host-gateway", "-p", "127.0.0.1::443", "-v", root + "/preview/Caddyfile:/etc/caddy/Caddyfile:ro", "-v", cert + ":/run/preview-tls/gateway.crt:ro", "-v", key + ":/run/preview-tls/gateway.key:ro", "-e", "PREVIEW_NODE_ALLOWED_ORIGINS=" + origins}
	for _, name := range []string{"APP", "IPFS", "GETH", "GETH_WS", "BEACON", "BUNDLER"} {
		upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g.calls.Add(1)
			if r.URL.Query().Get("disconnect") == "1" {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			if r.Header.Get("Range") == "bytes=0-1" {
				w.Header().Set("Content-Range", "bytes 0-1/4")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "da")
				return
			}

			if name == "GETH_WS" {
				conn, upgradeErr := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if upgradeErr != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				for {
					kind, data, readErr := conn.ReadMessage()
					if readErr != nil {
						return
					}
					if conn.WriteMessage(kind, data) != nil {
						return
					}
				}
			}
			w.Header().Set("X-Upstream", name)
			w.Header().Set("X-URI", r.RequestURI)
			w.Header().Set("X-Host", r.Host)
			if name != "BUNDLER" && (r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "") {
				t.Error("spoofed forwarding header")
			}
			if name == "GETH" || name == "BEACON" {
				if r.Header.Get("Origin") != "" {
					t.Error("node Origin forwarded upstream")
				}
				w.Header().Set("Access-Control-Allow-Origin", "https://upstream.invalid")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if strings.Contains(r.URL.Path, "headers-stall") {
				<-r.Context().Done()
				return
			}
			if strings.Contains(r.URL.Path, "stream") {
				if name == "BEACON" {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				_, _ = io.WriteString(w, "data: first\n\n")
				_ = http.NewResponseController(w).Flush()
				if strings.Contains(r.URL.Path, "progress-stream") {
					for range 7 {
						select {
						case <-r.Context().Done():
							return
						case <-time.After(5 * time.Second):
						}
						_, _ = io.WriteString(w, "data: progress\n\n")
						_ = http.NewResponseController(w).Flush()
					}
					return
				}
				if strings.Contains(r.URL.Path, "idle-stream") {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(65 * time.Second):
					}
					_, _ = io.WriteString(w, "data: second\n\n")
					_ = http.NewResponseController(w).Flush()
				}
				<-r.Context().Done()
				select {
				case g.canceled <- struct{}{}:
				default:
				}
				return
			}
			if strings.Contains(r.URL.Path, "flood") {
				block := make([]byte, 64<<10)
				for range 16384 {
					if _, writeErr := w.Write(block); writeErr != nil {
						select {
						case g.canceled <- struct{}{}:
						default:
						}
						return
					}
				}
				return
			}
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				return
			}
			if len(body) > 0 {
				_, _ = w.Write(body)
			} else {
				_, _ = io.WriteString(w, name)
			}
		}))
		listener, listenErr := net.Listen("tcp", "0.0.0.0:0")
		if listenErr != nil {
			t.Fatal(listenErr)
		}
		upstream.Listener = listener
		upstream.Start()
		t.Cleanup(upstream.Close)
		port := listener.Addr().(*net.TCPAddr).Port
		args = append(args, "-e", fmt.Sprintf("PREVIEW_%s_UPSTREAM=host.docker.internal:%d", name, port))
	}
	image := os.Getenv("PREVIEW_GATEWAY_IMAGE")
	if image == "" {
		image = "caddy:2.11.7-alpine"
	}
	g.id = docker(t, append(args, image)...)
	t.Cleanup(func() { docker(t, "rm", "-fv", g.id) })
	g.binding = docker(t, "port", g.id, "443/tcp")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, g.binding)
	}}
	g.client = &http.Client{Transport: transport, Timeout: 90 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, "https://app.etherview.localhost/", nil)
		resp, requestErr := g.client.Do(req)
		if requestErr == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Caddy readiness: %v; logs=%s", requestErr, docker(t, "logs", g.id))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return g
}

func (g *gateway) request(method, host, path, origin, body string) (*http.Response, []byte) {
	g.t.Helper()
	req, err := http.NewRequest(method, "https://"+host+".etherview.localhost"+path, strings.NewReader(body))
	if err != nil {
		g.t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type")
	}
	req.Header.Set("Forwarded", "secret")
	req.Header.Set("X-Forwarded-For", "spoof")
	resp, err := g.client.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		g.t.Fatal(err)
	}
	return resp, data
}

func TestRoutesAndBoundary(t *testing.T) {
	g := start(t)
	for _, test := range []struct {
		method, host, path, body, upstream string
		status                             int
	}{
		{"GET", "app", "/api/v1/config", "", "APP", 200},
		{"POST", "geth", "/", `[{"jsonrpc":"2.0","method":"eth_chainId","id":1}]`, "GETH", 200},
		{"GET", "beacon", "/eth/v1/node/version", "", "BEACON", 200},
		{"POST", "beacon", "/eth/v1/beacon/pool/attestations", "[]", "BEACON", 200},
		{"GET", "beacon", "/lighthouse/health", "", "", 404},
		{"GET", "geth", "/", "", "", 404},
		{"POST", "geth", "/engine", "{}", "", 404},
		{"GET", "unknown", "/", "", "", 0},
		{"GET", "ipfs", "/ipfs/cid/file%20name?format=raw&x=a%2Fb", "", "IPFS", 200},
		{"GET", "ipfs", "/ipfs/cid/100%25.txt", "", "IPFS", 200},
		{"GET", "ipfs", "/ipfs/cid/file?filename=a;b&raw=%zz", "", "IPFS", 200},
		{"HEAD", "ipfs", "/ipfs/cid/file", "", "IPFS", 200},
		{"POST", "ipfs", "/ipfs/cid", "", "", 403},
		{"GET", "ipfs", "/ipfs/cid", "body", "", 400},
		{"GET", "ipfs", "/api/v0/version", "", "", 404},
		{"GET", "ipfs", "/ipfs/../api/v0/version", "", "", 404},
		{"GET", "ipfs", "/ipfs/%2e%2e/api", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%2f..%2fapi", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%252e%252e/api", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%25%32%65%25%32%65/api", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%252%66api", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%25%35capi", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%252520", "", "", 404},
		{"GET", "ipfs", "/ipfs/cid/%5capi", "", "", 404},
		{"GET", "ipfs", "/%69pfs/cid", "", "", 404},
	} {
		t.Run(test.host+test.method+test.path, func(t *testing.T) {
			if test.status == 0 { // Unknown hosts must never reach an upstream (TLS may reject them).
				before := g.calls.Load()
				resp, err := g.client.Get("https://unknown.etherview.localhost/")
				if err == nil {
					_ = resp.Body.Close()
				}
				if g.calls.Load() != before {
					t.Fatal("unknown host proxied")
				}
				return
			}
			resp, data := g.request(test.method, test.host, test.path, "", test.body)
			if resp.StatusCode != test.status {
				t.Fatalf("status=%d want=%d body=%q", resp.StatusCode, test.status, data)
			}
			if test.upstream != "" {
				if resp.Header.Get("X-Upstream") != test.upstream || resp.Header.Get("X-URI") != test.path {
					t.Fatalf("incorrect forwarding: %v", resp.Header)
				}
				if test.method == "HEAD" && len(data) != 0 {
					t.Fatal("HEAD body")
				}
				if test.body != "" && !bytes.Equal(data, []byte(test.body)) {
					t.Fatal("body changed")
				}
			}
		})
	}
	for _, host := range []string{"geth", "beacon", "bundler"} {
		path := "/"
		if host == "beacon" {
			path = "/eth/v1/beacon/pool/attestations"
		}
		for _, origin := range []string{"", "https://app.etherview.localhost", "http://localhost:5173", "http://127.0.0.1:5173", "https://evil.invalid", "chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn", "moz-extension://preview-wallet", "null"} {
			for _, method := range []string{"POST", "OPTIONS"} {
				before := g.calls.Load()
				resp, _ := g.request(method, host, path, origin, "{}")
				allowed := host == "geth" || origin == "" || origin == "https://app.etherview.localhost" || origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173"
				want := 200
				if !allowed {
					want = 403
				} else if method == "OPTIONS" {
					want = 204
				}
				if resp.StatusCode != want {
					t.Fatalf("%s %s origin %q: status %d want %d", host, method, origin, resp.StatusCode, want)
				}
				if (!allowed || method == "OPTIONS") && g.calls.Load() != before {
					t.Fatal("rejected request or preflight reached upstream")
				}
				wantOrigin := ""
				if host == "geth" {
					wantOrigin = "*"
				} else if allowed {
					wantOrigin = origin
				}
				if resp.Header.Get("Access-Control-Allow-Origin") != wantOrigin || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
					t.Fatalf("%s CORS headers: %v", host, resp.Header)
				}
				if allowed && (origin != "" || host == "geth") {
					if resp.Header.Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" || resp.Header.Get("Access-Control-Allow-Headers") != "Content-Type, Accept, Eth-Consensus-Version" {
						t.Fatalf("%s preflight policy: %v", host, resp.Header)
					}
				}
			}
		}
	}
}

func TestTLSAndFailure(t *testing.T) {
	g := start(t)
	for _, test := range []struct {
		name, server string
		roots        *x509.CertPool
		max          uint16
	}{
		{"untrusted", "app.etherview.localhost", x509.NewCertPool(), tls.VersionTLS13},
		{"wrong-name", "wrong.invalid", g.roots, tls.VersionTLS13},
		{"old-tls", "app.etherview.localhost", g.roots, tls.VersionTLS11},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, err := tls.Dial("tcp", g.binding, &tls.Config{RootCAs: test.roots, ServerName: test.server, MinVersion: tls.VersionTLS10, MaxVersion: test.max})
			if err == nil {
				_ = conn.Close()
				t.Fatal("invalid TLS accepted")
			}
		})
	}
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		conn, err := tls.Dial("tcp", g.binding, &tls.Config{RootCAs: g.roots, ServerName: "app.etherview.localhost", MinVersion: version, MaxVersion: version})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}

}

func TestStreamingAndDeadlines(t *testing.T) {
	g := start(t)
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://ipfs.etherview.localhost/ipfs/cid/stream", nil)
		resp, err := g.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if _, err = bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case <-g.canceled:
		case <-time.After(3 * time.Second):
			t.Fatal("upstream not canceled")
		}
	})
	t.Run("header-timeout", func(t *testing.T) {
		started := time.Now()
		resp, data := g.request("GET", "ipfs", "/ipfs/cid/headers-stall", "", "")
		if resp.StatusCode != 502 || string(data) != "upstream_unavailable\n" {
			t.Fatalf("failure %d %q", resp.StatusCode, data)
		}
		if time.Since(started) > 35*time.Second {
			t.Fatal("header deadline not bounded")
		}
	})
	t.Run("body-timeout", func(t *testing.T) {
		resp, err := g.client.Get("https://ipfs.etherview.localhost/ipfs/cid/stream")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		if err == nil || !bytes.Contains(data, []byte("first")) {
			t.Fatalf("stalled body %q %v", data, err)
		}
	})
	t.Run("idle-websocket-and-sse", func(t *testing.T) {
		dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: g.roots}, NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, g.binding)
		}}
		conn, _, err := dialer.Dial("wss://geth.etherview.localhost/", http.Header{"Origin": []string{"https://app.etherview.localhost"}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()

		resp, err := g.client.Get("https://beacon.etherview.localhost/eth/v1/events/idle-stream")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		reader := bufio.NewReader(resp.Body)
		for i := range 3 {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				t.Fatal(readErr)
			}
			if i == 2 && !strings.Contains(line, "second") {
				t.Fatal(line)
			}
		}
		if err = conn.WriteMessage(websocket.TextMessage, []byte("after-idle")); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil || string(data) != "after-idle" {
			t.Fatalf("idle websocket %q %v", data, err)
		}
	})
	started := time.Now()
	docker(t, "stop", "-t", "15", g.id)
	if time.Since(started) > 14*time.Second {
		t.Fatal("unbounded shutdown")
	}
}

func TestRequestLimits(t *testing.T) {
	g := start(t)
	resp, _ := g.request("POST", "geth", "/", "", strings.Repeat("x", (32<<20)+1))
	if resp.StatusCode != 413 {
		t.Fatalf("oversize status %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", "https://ipfs.etherview.localhost/ipfs/cid", io.NopCloser(strings.NewReader("body")))
	req.ContentLength = -1
	before := g.calls.Load()
	response, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 400 || g.calls.Load() != before {
		t.Fatalf("unknown-length body forwarded: %d", response.StatusCode)
	}
}

func TestFailureIsolationAndRange(t *testing.T) {
	g := start(t)
	before := g.calls.Load()
	resp, data := g.request("POST", "geth", "/?disconnect=1", "", "{}")
	if resp.StatusCode != 502 || string(data) != "upstream_unavailable\n" || g.calls.Load() != before+1 {
		t.Fatalf("write failure retried or leaked: %d %q", resp.StatusCode, data)
	}
	resp, _ = g.request("GET", "app", "/", "", "")
	if resp.StatusCode != 200 {
		t.Fatal("node failure affected app")
	}
	req, _ := http.NewRequest("GET", "https://ipfs.etherview.localhost/ipfs/cid", nil)
	req.Header.Set("Range", "bytes=0-1")
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err = io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 206 || string(data) != "da" || resp.Header.Get("Content-Range") != "bytes 0-1/4" {
		t.Fatalf("range changed: %d %q %v", resp.StatusCode, data, err)
	}
	before = g.calls.Load()
	docker(t, "exec", g.id, "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8081/health")
	if g.calls.Load() != before {
		t.Fatal("gateway health depends on an upstream")
	}
	containerIP := docker(t, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", g.id)
	docker(t, "exec", g.id, "sh", "-c", "if wget -T 2 -q -O /dev/null --header='Host: 127.0.0.1:8081' http://"+containerIP+":8081/health; then exit 1; fi")
}

func TestStalledDownstream(t *testing.T) {
	g := start(t)
	conn, err := tls.Dial("tcp", g.binding, &tls.Config{RootCAs: g.roots, ServerName: "ipfs.etherview.localhost", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET /ipfs/cid/flood HTTP/1.1\r\nHost: ipfs.etherview.localhost\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	// Never read: the proxy must cancel its upstream after the blocked TLS write.
	select {
	case <-g.canceled:
	case <-time.After(40 * time.Second):
		t.Fatal("stalled downstream retained its upstream")
	}
}

func TestOriginOverride(t *testing.T) {
	g := startWithOrigins(t, "https://custom.example null")
	for _, host := range []string{"geth", "beacon", "bundler"} {
		path := "/"
		if host == "beacon" {
			path = "/eth/v1/beacon/pool/attestations"
		}
		for _, origin := range []string{"", "https://app.etherview.localhost", "http://localhost:5173", "https://custom.example", "null"} {
			response, _ := g.request("POST", host, path, origin, "{}")
			want := 403
			if host == "geth" || origin == "" || origin == "https://custom.example" {
				want = 200
			}
			if response.StatusCode != want {
				t.Fatalf("%s origin override %q: %d", host, origin, response.StatusCode)
			}
		}
	}
}

func TestGethWebSocketOrigins(t *testing.T) {
	g := startWithOrigins(t, "https://custom.example")
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: g.roots}, NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, g.binding)
	}}
	for _, origin := range []string{"", "https://app.etherview.localhost", "https://evil.invalid", "chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn", "moz-extension://preview-wallet", "null"} {
		t.Run(origin, func(t *testing.T) {
			headers := http.Header{}
			if origin != "" {
				headers.Set("Origin", origin)
			}
			conn, response, err := dialer.Dial("wss://geth.etherview.localhost/", headers)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if response.Header.Get("Access-Control-Allow-Origin") != "*" || response.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("WSS CORS headers: %v", response.Header)
			}
			if err = conn.WriteMessage(websocket.TextMessage, []byte("wallet")); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, data, err := conn.ReadMessage()
			if err != nil || string(data) != "wallet" {
				t.Fatalf("websocket echo %q: %v", data, err)
			}
		})
	}
}

func TestProgressingIPFSStream(t *testing.T) {
	g := start(t)
	started := time.Now()
	resp, data := g.request("GET", "ipfs", "/ipfs/cid/progress-stream", "", "")
	if resp.StatusCode != 200 || bytes.Count(data, []byte("data: progress")) != 7 {
		t.Fatalf("progressing IPFS stream interrupted: %d %q", resp.StatusCode, data)
	}
	if time.Since(started) < 35*time.Second {
		t.Fatal("fixture must outlive the 30-second idle timeout")
	}
}

func TestWebSocketUpgradeTokens(t *testing.T) {
	g := start(t)
	for _, test := range []struct {
		connection, upgrade string
		status              int
	}{
		{"Upgrade", "websocket", 101},
		{"upgrade", "websocket", 101},
		{"keep-alive, uPgRaDe", "WebSocket", 101},
		{"notUpgrade", "websocket", 404},
	} {
		t.Run(test.connection+test.upgrade, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", g.binding, &tls.Config{
				RootCAs: g.roots, ServerName: "geth.etherview.localhost", MinVersion: tls.VersionTLS12,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: geth.etherview.localhost\r\nOrigin: https://app.etherview.localhost\r\nConnection: %s\r\nUpgrade: %s\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", test.connection, test.upgrade)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != test.status {
				t.Fatalf("upgrade status %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}
