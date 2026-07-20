package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedDistributionHasNoServerConfigurationOrExternalEntrypoints(t *testing.T) {
	t.Parallel()

	var stylesheet []byte
	err := fs.WalkDir(Assets(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, readErr := fs.ReadFile(Assets(), name)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range [][]byte{
			[]byte("ETHERVIEW_"),
			[]byte("VITE_"),
			[]byte("DATABASE_URL"),
			[]byte("RPC_URL"),
			[]byte("postgres://"),
		} {
			if bytes.Contains(contents, forbidden) {
				t.Errorf("embedded asset %s contains forbidden server configuration marker %q", name, forbidden)
			}
		}
		if strings.HasSuffix(name, ".css") {
			stylesheet = append(stylesheet, contents...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded distribution: %v", err)
	}
	if !bytes.Contains(stylesheet, []byte("tailwindcss v4.3.0")) ||
		!bytes.Contains(stylesheet, []byte(".min-h-screen")) ||
		!bytes.Contains(stylesheet, []byte(".rounded-ui-md")) {
		t.Fatal("embedded stylesheet does not contain the pinned Tailwind design primitives")
	}

	index, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}
	for _, external := range []string{`src="http://`, `src="https://`, `href="http://`, `href="https://`} {
		if bytes.Contains(index, []byte(external)) {
			t.Errorf("embedded index contains external entrypoint %q", external)
		}
	}
	if bytes.Contains(index, []byte("<style")) {
		t.Error("embedded index contains an inline style block rejected by the CSP")
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllSubmatch(index, -1) {
		target := string(match[1])
		if !strings.HasPrefix(target, "/assets/") || !isHashedAsset(strings.TrimPrefix(target, "/")) {
			t.Errorf("embedded index entrypoint %q is not a local content-hashed asset", target)
		}
	}
}

func TestIndexAndDeepLinks(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		path   string
		method string
	}{
		{name: "root", path: "/", method: http.MethodGet},
		{name: "nested route", path: "/blocks/123456", method: http.MethodGet},
		{name: "dotted entity", path: "/address/vitalik.eth", method: http.MethodGet},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Accept", "text/html")
			response := httptest.NewRecorder()

			NewHandler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Header().Get("Cache-Control"); got != noStoreCache {
				t.Errorf("Cache-Control = %q, want %q", got, noStoreCache)
			}
			assertSecurityHeaders(t, response.Header())
			if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
				t.Error("response does not contain SPA root")
			}
		})
	}
}

func TestHashedAssetCachingAndETag(t *testing.T) {
	t.Parallel()

	asset := firstHashedAsset(t)
	request := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != immutableCache {
		t.Errorf("Cache-Control = %q, want %q", got, immutableCache)
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}

	conditional := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	NewHandler().ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Errorf("conditional status = %d, want %d", notModified.Code, http.StatusNotModified)
	}
}

func TestOnlyViteContentHashedAssetsAreImmutable(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"assets/index-BR0k1Xmr.js":           true,
		"assets/StatsChart-BHcCzZxN.js":      true,
		"assets/index-too-short.js":          false,
		"assets/logo-not-a-build-hash.svg":   false,
		"favicon-BR0k1Xmr.ico":               false,
		"assets/nested/file-BR0k1Xmr.js.map": false,
	} {
		if got := isHashedAsset(name); got != want {
			t.Errorf("isHashedAsset(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestNoFallbackForReservedOrAssetRequests(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/v1/status",
		"/API/v1/status",
		"/v2/api",
		"/V2/API",
		"/health/ready",
		"/metrics",
		"/assets/missing.js",
		"/favicon.ico",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "text/html")
		response := httptest.NewRecorder()
		NewHandler().ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
		if got := response.Header().Get("Cache-Control"); got != noStoreCache {
			t.Errorf("%s Cache-Control = %q, want %q", path, got, noStoreCache)
		}
		assertSecurityHeaders(t, response.Header())
	}
}

func TestNonHTMLAndUnsafeRequestsAreRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		accept string
		want   int
	}{
		{name: "json navigation", method: http.MethodGet, path: "/blocks/1", accept: "application/json", want: http.StatusNotFound},
		{name: "explicitly refused html", method: http.MethodGet, path: "/blocks/1", accept: "text/html;q=0, application/json", want: http.StatusNotFound},
		{name: "head deep link", method: http.MethodHead, path: "/blocks/1", accept: "text/html", want: http.StatusNotFound},
		{name: "post", method: http.MethodPost, path: "/", accept: "text/html", want: http.StatusMethodNotAllowed},
		{name: "traversal", method: http.MethodGet, path: "/../index.html", accept: "text/html", want: http.StatusNotFound},
		{name: "backslash", method: http.MethodGet, path: `/assets\index.js`, accept: "text/html", want: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Accept", test.accept)
			response := httptest.NewRecorder()
			NewHandler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Errorf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func firstHashedAsset(t *testing.T) string {
	t.Helper()
	var selected string
	err := fs.WalkDir(Assets(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && isHashedAsset(name) && selected == "" {
			selected = name
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
	if selected == "" {
		t.Fatal("embedded distribution contains no hashed asset")
	}
	return selected
}

func assertSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for _, name := range []string{
		"Content-Security-Policy",
		"Cross-Origin-Resource-Policy",
		"Origin-Agent-Cluster",
		"Permissions-Policy",
		"Referrer-Policy",
		"Strict-Transport-Security",
		"X-DNS-Prefetch-Control",
		"X-Content-Type-Options",
		"X-Frame-Options",
	} {
		if header.Get(name) == "" {
			t.Errorf("security header %s is empty", name)
		}
	}
	policy := header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'none'", "script-src 'self'", "style-src 'self'",
		"connect-src 'self'", "object-src 'none'", "frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("Content-Security-Policy %q lacks %q", policy, directive)
		}
	}
	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'", "http:", "https:"} {
		if strings.Contains(policy, forbidden) {
			t.Errorf("Content-Security-Policy %q contains forbidden source %q", policy, forbidden)
		}
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
