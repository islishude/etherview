package genesis

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type remoteStubResolver struct {
	addresses []net.IPAddr
	err       error
	calls     atomic.Int32
}

func (resolver *remoteStubResolver) LookupIPAddr(
	context.Context,
	string,
) ([]net.IPAddr, error) {
	resolver.calls.Add(1)
	return resolver.addresses, resolver.err
}

func TestParseRemoteGenesisURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "default port", rawURL: "https://example.com/genesis.json"},
		{name: "explicit 443", rawURL: "https://example.com:443/network/genesis.json"},
		{name: "maximum length", rawURL: "https://example.com/" + strings.Repeat("a", 4096-len("https://example.com/"))},
		{name: "empty", rawURL: "", wantErr: true},
		{name: "above maximum length", rawURL: "https://example.com/" + strings.Repeat("a", 4097-len("https://example.com/")), wantErr: true},
		{name: "untrimmed", rawURL: " https://example.com/genesis.json", wantErr: true},
		{name: "HTTP", rawURL: "http://example.com/genesis.json", wantErr: true},
		{name: "userinfo", rawURL: "https://operator@example.com/genesis.json", wantErr: true},
		{name: "query", rawURL: "https://example.com/genesis.json?v=1", wantErr: true},
		{name: "empty query", rawURL: "https://example.com/genesis.json?", wantErr: true},
		{name: "fragment", rawURL: "https://example.com/genesis.json#sha", wantErr: true},
		{name: "empty fragment", rawURL: "https://example.com/genesis.json#", wantErr: true},
		{name: "non-default port", rawURL: "https://example.com:8443/genesis.json", wantErr: true},
		{name: "empty explicit port", rawURL: "https://example.com:/genesis.json", wantErr: true},
		{name: "opaque", rawURL: "https:genesis.json", wantErr: true},
		{name: "parent path", rawURL: "https://example.com/a/../genesis.json", wantErr: true},
		{name: "escaped parent", rawURL: "https://example.com/a/%2e%2e/genesis.json", wantErr: true},
		{name: "double escaped parent", rawURL: "https://example.com/a/%252e%252e/genesis.json", wantErr: true},
		{name: "escaped slash parent", rawURL: "https://example.com/a%2f..%2fgenesis.json", wantErr: true},
		{name: "double escaped slash parent", rawURL: "https://example.com/a%252f..%252fgenesis.json", wantErr: true},
		{name: "backslash parent", rawURL: `https://example.com/a\..\genesis.json`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseRemoteGenesisURL(test.rawURL)
			if test.wantErr {
				assertRemoteFailure(
					t,
					err,
					remoteFailureFailed,
					"genesis_remote_url_invalid",
				)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseRemoteGenesisDigest(t *testing.T) {
	t.Parallel()
	document := []byte(`{"config":{"chainId":1}}`)
	valid := fmt.Sprintf("%x", sha256.Sum256(document))
	tests := []struct {
		name    string
		value   string
		wantNil bool
		wantErr bool
	}{
		{name: "optional", value: "", wantNil: true},
		{name: "valid", value: valid},
		{name: "uppercase", value: strings.ToUpper(valid), wantErr: true},
		{name: "zero", value: strings.Repeat("0", sha256.Size*2), wantErr: true},
		{name: "short", value: valid[:len(valid)-1], wantErr: true},
		{name: "not hex", value: strings.Repeat("z", sha256.Size*2), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			digest, err := parseRemoteGenesisDigest(test.value)
			if test.wantErr {
				assertRemoteFailure(
					t,
					err,
					remoteFailureFailed,
					"genesis_remote_checksum_invalid",
				)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (digest == nil) != test.wantNil {
				t.Fatalf("digest nil = %t, want %t", digest == nil, test.wantNil)
			}
		})
	}
}

func TestNewRemoteSourceRejectsTimeoutOutsideBoundary(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{
		minimumRemoteFetchTimeout - time.Nanosecond,
		maximumRemoteFetchTimeout + time.Nanosecond,
	} {
		_, err := newRemoteSource("https://example.com/genesis.json", "", timeout)
		assertRemoteFailure(
			t,
			err,
			remoteFailureFailed,
			"genesis_remote_timeout_invalid",
		)
	}
	for _, timeout := range []time.Duration{
		minimumRemoteFetchTimeout,
		maximumRemoteFetchTimeout,
	} {
		if _, err := newRemoteSource("https://example.com/genesis.json", "", timeout); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteSourceFetchAcceptsAllowlistedJSONMediaTypes(t *testing.T) {
	t.Parallel()
	document := []byte(`{"config":{"chainId":777},"alloc":{}}`)
	expected := sha256.Sum256(document)
	mediaTypes := []string{
		"application/json",
		"application/vnd.ethereum.genesis+json; charset=utf-8",
		"application/octet-stream",
		"text/plain; charset=utf-8",
	}
	for _, mediaType := range mediaTypes {
		t.Run(mediaType, func(t *testing.T) {
			t.Parallel()
			source, server := newRemoteSourceTestServer(t, "", func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				if request.Method != http.MethodGet {
					t.Errorf("method = %s", request.Method)
				}
				if got := request.Header.Get("Accept-Encoding"); got != "identity" {
					t.Errorf("Accept-Encoding = %q", got)
				}
				if got := request.Header.Get("User-Agent"); got != "etherview-genesis/1" {
					t.Errorf("User-Agent = %q", got)
				}
				if got := request.Header.Get("Authorization"); got != "" {
					t.Errorf("Authorization = %q", got)
				}
				if got := request.Header.Get("Cookie"); got != "" {
					t.Errorf("Cookie = %q", got)
				}
				writer.Header().Set("Content-Type", mediaType)
				_, _ = writer.Write(document)
			})
			defer server.Close()

			result, err := source.fetch(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Bytes) != string(document) {
				t.Fatalf("document = %q", result.Bytes)
			}
			if result.SHA256 != expected {
				t.Fatalf("digest = %x, want %x", result.SHA256, expected)
			}
		})
	}
}

func TestRemoteSourceFetchValidatesChecksum(t *testing.T) {
	t.Parallel()
	document := []byte(`{"config":{"chainId":777},"alloc":{}}`)
	correct := fmt.Sprintf("%x", sha256.Sum256(document))
	tests := []struct {
		name     string
		checksum string
		wantCode string
	}{
		{name: "match", checksum: correct},
		{name: "mismatch", checksum: strings.Repeat("1", sha256.Size*2), wantCode: "genesis_remote_checksum_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, server := newRemoteSourceTestServer(t, test.checksum, func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(document)
			})
			defer server.Close()
			result, err := source.fetch(t.Context())
			if test.wantCode != "" {
				assertRemoteFailure(t, err, remoteFailureFailed, test.wantCode)
				if result.Bytes != nil || result.SHA256 != ([sha256.Size]byte{}) {
					t.Fatal("failed fetch returned a document")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoteSourceFetchValidatesChecksumBeforeJSON(t *testing.T) {
	t.Parallel()
	source, server := newRemoteSourceTestServer(
		t,
		strings.Repeat("1", sha256.Size*2),
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"invalid":`))
		},
	)
	defer server.Close()
	_, err := source.fetch(t.Context())
	assertRemoteFailure(
		t,
		err,
		remoteFailureFailed,
		"genesis_remote_checksum_mismatch",
	)
}

func TestRemoteSourceFetchRejectsRedirectWithoutFollowing(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	source, server := newRemoteSourceTestServer(t, "", func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/redirected" {
			redirected.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		http.Redirect(writer, request, "/redirected", http.StatusFound)
	})
	defer server.Close()
	_, err := source.fetch(t.Context())
	assertRemoteFailure(
		t,
		err,
		remoteFailureFailed,
		"genesis_remote_redirect_not_allowed",
	)
	if redirected.Load() != 0 {
		t.Fatalf("redirect target received %d requests", redirected.Load())
	}
}

func TestRemoteSourceFetchIgnoresEnvironmentProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		proxyHits.Add(1)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)

	source, server := newRemoteSourceTestServer(t, "", func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	})
	defer server.Close()
	if _, err := source.fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if proxyHits.Load() != 0 {
		t.Fatalf("environment proxy received %d requests", proxyHits.Load())
	}
}

func TestRemoteSourceFetchRejectsAnyNonPublicDNSAnswerBeforeDial(t *testing.T) {
	t.Parallel()
	resolver := &remoteStubResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("127.0.0.1")},
	}}
	var dialCalls atomic.Int32
	source, err := newRemoteSourceWithOptions(
		"https://example.com/genesis.json",
		"",
		time.Second,
		remoteSourceOptions{
			resolver: resolver,
			dialContext: func(context.Context, string, string) (net.Conn, error) {
				dialCalls.Add(1)
				return nil, errors.New("must not dial")
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.fetch(t.Context())
	assertRemoteFailure(
		t,
		err,
		remoteFailureFailed,
		"genesis_remote_address_not_public",
	)
	if dialCalls.Load() != 0 {
		t.Fatalf("dial called %d times", dialCalls.Load())
	}
}

func TestRemoteSourceFetchClassifiesUnavailableDNSWithoutLeakingError(t *testing.T) {
	t.Parallel()
	resolver := &remoteStubResolver{err: errors.New("resolver leaked secret.internal")}
	source, err := newRemoteSourceWithOptions(
		"https://example.com/genesis.json",
		"",
		time.Second,
		remoteSourceOptions{resolver: resolver},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.fetch(t.Context())
	assertRemoteFailure(
		t,
		err,
		remoteFailureUnavailable,
		"genesis_remote_dns_unavailable",
	)
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.com") {
		t.Fatalf("error leaked hostile detail: %v", err)
	}
}

func TestRemoteSourceFetchClassifiesHTTPStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status   int
		wantKind remoteFailureKind
		wantCode string
	}{
		{status: http.StatusNotFound, wantKind: remoteFailureFailed, wantCode: "genesis_remote_http_status"},
		{status: http.StatusTooManyRequests, wantKind: remoteFailureUnavailable, wantCode: "genesis_remote_http_unavailable"},
		{status: http.StatusServiceUnavailable, wantKind: remoteFailureUnavailable, wantCode: "genesis_remote_http_unavailable"},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			source, server := newRemoteSourceTestServer(t, "", func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte("secret response"))
			})
			defer server.Close()
			_, err := source.fetch(t.Context())
			assertRemoteFailure(t, err, test.wantKind, test.wantCode)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked response: %v", err)
			}
		})
	}
}

func TestRemoteSourceFetchRejectsHostileContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		encoding    string
		body        string
		wantCode    string
	}{
		{
			name:     "missing content type",
			body:     `{}`,
			wantCode: "genesis_remote_content_type",
		},
		{
			name:        "unsupported content type",
			contentType: "text/html",
			body:        `{}`,
			wantCode:    "genesis_remote_content_type",
		},
		{
			name:        "empty JSON suffix subtype",
			contentType: "application/+json",
			body:        `{}`,
			wantCode:    "genesis_remote_content_type",
		},
		{
			name:        "compressed",
			contentType: "application/json",
			encoding:    "gzip",
			body:        `{}`,
			wantCode:    "genesis_remote_content_encoding",
		},
		{
			name:        "invalid JSON",
			contentType: "application/json",
			body:        `{"secret":`,
			wantCode:    "genesis_remote_json_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, server := newRemoteSourceTestServer(t, "", func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				if test.contentType != "" {
					writer.Header().Set("Content-Type", test.contentType)
				} else {
					// A nil field suppresses net/http's automatic content sniffing.
					writer.Header()["Content-Type"] = nil
				}
				if test.encoding != "" {
					writer.Header().Set("Content-Encoding", test.encoding)
				}
				_, _ = writer.Write([]byte(test.body))
			})
			defer server.Close()
			_, err := source.fetch(t.Context())
			assertRemoteFailure(t, err, remoteFailureFailed, test.wantCode)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked response: %v", err)
			}
		})
	}
}

func TestRemoteSourceFetchEnforcesDeclaredAndStreamingLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		maximum  int64
		wantCode string
	}{
		{
			name: "declared",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Length", "17")
				writer.WriteHeader(http.StatusOK)
			},
			maximum:  16,
			wantCode: "genesis_remote_too_large",
		},
		{
			name: "streaming",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				writer.(http.Flusher).Flush()
				_, _ = writer.Write([]byte(strings.Repeat("x", 17)))
			},
			maximum:  16,
			wantCode: "genesis_remote_too_large",
		},
		{
			name: "truncated read",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Length", "10")
				_, _ = writer.Write([]byte(`{}`))
			},
			maximum:  16,
			wantCode: "genesis_remote_read_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, server := newRemoteSourceTestServer(t, "", test.handler)
			defer server.Close()
			source.maximumBytes = test.maximum
			_, err := source.fetch(t.Context())
			wantKind := remoteFailureFailed
			if test.name == "truncated read" {
				wantKind = remoteFailureUnavailable
			}
			assertRemoteFailure(t, err, wantKind, test.wantCode)
		})
	}
}

func TestRemoteSourceFetchTimeoutIsUnavailable(t *testing.T) {
	t.Parallel()
	source, server := newRemoteSourceTestServer(t, "", func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		select {
		case <-request.Context().Done():
		case <-time.After(time.Second):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{}`))
		}
	})
	defer server.Close()
	source.http.Timeout = 20 * time.Millisecond
	_, err := source.fetch(t.Context())
	assertRemoteFailure(
		t,
		err,
		remoteFailureUnavailable,
		"genesis_remote_unavailable",
	)
}

func newRemoteSourceTestServer(
	t *testing.T,
	expectedSHA256 string,
	handler http.HandlerFunc,
) (*remoteSource, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	resolver := &remoteStubResolver{addresses: []net.IPAddr{{
		IP: net.ParseIP("93.184.216.34"),
	}}}
	dialer := &net.Dialer{Timeout: time.Second}
	source, err := newRemoteSourceWithOptions(
		"https://example.com/genesis.json",
		expectedSHA256,
		time.Second,
		remoteSourceOptions{
			resolver: resolver,
			dialContext: func(
				ctx context.Context,
				network string,
				address string,
			) (net.Conn, error) {
				if address != "93.184.216.34:443" {
					return nil, fmt.Errorf("unexpected direct dial target %q", address)
				}
				return dialer.DialContext(ctx, network, server.Listener.Addr().String())
			},
			tlsClientConfig: &tls.Config{ //nolint:gosec
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			},
		},
	)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return source, server
}

func assertRemoteFailure(
	t *testing.T,
	err error,
	wantKind remoteFailureKind,
	wantCode string,
) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s failure", wantCode)
	}
	kind, code, ok := remoteErrorDetails(err)
	if !ok {
		t.Fatalf("error type = %T, want *remoteFetchError", err)
	}
	if kind != wantKind || code != wantCode {
		t.Fatalf("failure = (%q, %q), want (%q, %q)", kind, code, wantKind, wantCode)
	}
	if err.Error() != wantCode {
		t.Fatalf("error text = %q, want stable code %q", err, wantCode)
	}
}
