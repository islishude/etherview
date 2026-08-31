package webui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
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
	if !bytes.Contains(stylesheet, []byte("tailwindcss")) ||
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
			assertShellResponse(t, response)
			if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
				t.Error("response does not contain SPA root")
			}
		})
	}
}

func TestShellNonceIsFreshAndShellResponsesHaveNoETag(t *testing.T) {
	t.Parallel()

	shell := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Accept", "text/html")
		response := httptest.NewRecorder()
		NewHandler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want %d", method, path, response.Code, http.StatusOK)
		}
		return response
	}

	first := shell(http.MethodGet, "/")
	second := shell(http.MethodGet, "/")
	deepLink := shell(http.MethodGet, "/contracts/verified")
	firstNonce := assertShellResponse(t, first)
	secondNonce := assertShellResponse(t, second)
	deepLinkNonce := assertShellResponse(t, deepLink)
	if firstNonce == secondNonce {
		t.Fatalf("consecutive shell responses reused CSP nonce %q", firstNonce)
	}
	if firstNonce == deepLinkNonce || secondNonce == deepLinkNonce {
		t.Fatalf("deep-link shell reused a prior CSP nonce %q", deepLinkNonce)
	}

	head := shell(http.MethodHead, "/")
	assertShellHeaders(t, head.Header())
	if head.Body.Len() != 0 {
		t.Errorf("HEAD shell body length = %d, want 0", head.Body.Len())
	}
}

func TestShellNonceFailuresFailClosed(t *testing.T) {
	t.Parallel()

	validShell := fstest.MapFS{
		"index.html": {Data: []byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`)},
	}
	failedGenerators := &handler{
		assets: validShell,
		nonceGenerator: func() (string, error) {
			return "", fmt.Errorf("test RNG failure")
		},
	}
	failedInjection := &handler{
		assets: fstest.MapFS{
			"index.html": {Data: []byte(`<div id="root"></div>`)},
		},
		nonceGenerator: func() (string, error) {
			return strings.Repeat("A", 43), nil
		},
	}

	for name, shellHandler := range map[string]*handler{
		"rng":       failedGenerators,
		"injection": failedInjection,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", "text/html")
			shellHandler.ServeHTTP(response, request)

			if response.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			if got := response.Header().Get("Cache-Control"); got != noStoreCache {
				t.Errorf("Cache-Control = %q, want %q", got, noStoreCache)
			}
			assertSecurityHeaders(t, response.Header())
			assertNoCSPNonce(t, response.Header())
			if strings.Contains(response.Body.String(), `<div id="root"></div>`) {
				t.Error("failed shell response contains SPA content")
			}
		})
	}
}

func TestEmbeddedSVGBrandIconIsTheFavicon(t *testing.T) {
	t.Parallel()

	index, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}
	match := regexp.MustCompile(`<link rel="icon" type="image/svg\+xml" href="([^"]+)"`).FindSubmatch(index)
	if len(match) != 2 {
		t.Fatal("embedded index does not declare the SVG brand icon as its favicon")
	}

	assetPath := string(match[1])
	if !strings.HasPrefix(assetPath, "/assets/") ||
		!isHashedAsset(strings.TrimPrefix(assetPath, "/")) {
		t.Fatalf("favicon path %q is not a local content-hashed asset", assetPath)
	}

	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, assetPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("favicon status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("favicon Content-Type = %q, want %q", got, "image/svg+xml")
	}
	if got := response.Header().Get("Cache-Control"); got != immutableCache {
		t.Errorf("favicon Cache-Control = %q, want %q", got, immutableCache)
	}
	assertSecurityHeaders(t, response.Header())
	assertNoCSPNonce(t, response.Header())
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
	if got := response.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("asset CSP = %q, want baseline %q", got, contentSecurityPolicy)
	}

	conditional := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	NewHandler().ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Errorf("conditional status = %d, want %d", notModified.Code, http.StatusNotModified)
	}
	if got := notModified.Header().Get("Cache-Control"); got != immutableCache {
		t.Errorf("conditional Cache-Control = %q, want %q", got, immutableCache)
	}
	assertSecurityHeaders(t, notModified.Header())
	if got := notModified.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("conditional asset CSP = %q, want baseline %q", got, contentSecurityPolicy)
	}

	headRequest := httptest.NewRequest(http.MethodHead, "/"+asset, nil)
	headResponse := httptest.NewRecorder()
	NewHandler().ServeHTTP(headResponse, headRequest)
	if headResponse.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want %d", headResponse.Code, http.StatusOK)
	}
	if headResponse.Body.Len() != 0 {
		t.Errorf("HEAD body length = %d, want 0", headResponse.Body.Len())
	}
	if got := headResponse.Header().Get("ETag"); got != etag {
		t.Errorf("HEAD ETag = %q, want %q", got, etag)
	}
	assertSecurityHeaders(t, headResponse.Header())
	if got := headResponse.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("HEAD asset CSP = %q, want baseline %q", got, contentSecurityPolicy)
	}
}

func TestHashedAssetNegotiatesPrecompressedRepresentationsAndPreservesRanges(t *testing.T) {
	t.Parallel()
	identity := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	gzip := []byte("prebuilt-gzip")
	brotli := []byte("prebuilt-brotli")
	name := "assets/chunk-12345678.js"
	assets := fstest.MapFS{
		"index.html":      {Data: []byte(`<div id="root"></div>`)},
		name:              {Data: identity},
		name + ".gz":      {Data: gzip},
		name + ".br":      {Data: brotli},
		assetManifestFile: {Data: []byte(`{"internal":true}`)},
	}
	handler := &handler{
		assets: assets,
		metadata: map[string]assetMetadata{
			name: {
				Identity: testAssetRepresentation(name, identity),
				Gzip:     new(testAssetRepresentation(name+".gz", gzip)),
				Brotli:   new(testAssetRepresentation(name+".br", brotli)),
			},
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	request.Header.Set("Accept-Encoding", "gzip, br")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "br" ||
		!bytes.Equal(response.Body.Bytes(), brotli) {
		t.Fatalf("brotli response status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.Bytes())
	}
	if !strings.Contains(strings.Join(response.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatalf("asset Vary = %v", response.Header().Values("Vary"))
	}
	brotliETag := response.Header().Get("ETag")

	conditional := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	conditional.Header.Set("Accept-Encoding", "br")
	conditional.Header.Set("If-None-Match", brotliETag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified ||
		notModified.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("conditional compressed response=%d headers=%v", notModified.Code, notModified.Header())
	}

	gzipRequest := httptest.NewRequest(http.MethodHead, "/"+name, nil)
	gzipRequest.Header.Set("Accept-Encoding", "br;q=0, gzip")
	gzipResponse := httptest.NewRecorder()
	handler.ServeHTTP(gzipResponse, gzipRequest)
	if gzipResponse.Code != http.StatusOK || gzipResponse.Header().Get("Content-Encoding") != "gzip" ||
		gzipResponse.Body.Len() != 0 {
		t.Fatalf("gzip HEAD response=%d headers=%v body=%d", gzipResponse.Code, gzipResponse.Header(), gzipResponse.Body.Len())
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	rangeRequest.Header.Set("Accept-Encoding", "br")
	rangeRequest.Header.Set("Range", "bytes=1-3")
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent ||
		rangeResponse.Header().Get("Content-Encoding") != "" ||
		rangeResponse.Body.String() != "123" {
		t.Fatalf("range response=%d headers=%v body=%q", rangeResponse.Code, rangeResponse.Header(), rangeResponse.Body.String())
	}

	notAcceptable := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	notAcceptable.Header.Set("Accept-Encoding", "identity;q=0, br;q=0, gzip;q=0")
	notAcceptableResponse := httptest.NewRecorder()
	handler.ServeHTTP(notAcceptableResponse, notAcceptable)
	if notAcceptableResponse.Code != http.StatusNotAcceptable {
		t.Fatalf("not acceptable status=%d", notAcceptableResponse.Code)
	}

	for _, internal := range []string{"/" + name + ".gz", "/" + name + ".br", "/" + assetManifestFile} {
		internalResponse := httptest.NewRecorder()
		handler.ServeHTTP(internalResponse, httptest.NewRequest(http.MethodGet, internal, nil))
		if internalResponse.Code != http.StatusNotFound {
			t.Errorf("internal asset %s status=%d", internal, internalResponse.Code)
		}
	}
}

func testAssetRepresentation(name string, contents []byte) assetRepresentation {
	digest := sha256.Sum256(contents)
	return assetRepresentation{
		Path: name, Bytes: int64(len(contents)), SHA256: hex.EncodeToString(digest[:]),
	}
}

func TestOnlyViteContentHashedAssetsAreImmutable(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"assets/index-BR0k1Xmr.js":           true,
		"assets/index-Abc-1234.js":           true,
		"assets/index_Abc_1234.css":          false,
		"assets/MetricChart-BHcCzZxN.js":     true,
		"assets/index-too-short.js":          false,
		"assets/logo-not-a-build-hash.svg":   false,
		"assets/nested/file-BR0k1Xmr.js":     false,
		"favicon-BR0k1Xmr.ico":               false,
		"assets/nested/file-BR0k1Xmr.js.map": false,
	} {
		if got := isHashedAsset(name); got != want {
			t.Errorf("isHashedAsset(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestOnlyExactHashedFilesReceiveImmutableCaching(t *testing.T) {
	t.Parallel()

	assets := fstest.MapFS{
		"index.html":                       {Data: []byte(`<div id="root"></div>`)},
		"assets/chunk-Abc-1234.js":         {Data: []byte("hashed")},
		"assets/logo-not-a-build-hash.svg": {Data: []byte("mutable")},
		"assets/nested/chunk-BR0k1Xmr.js":  {Data: []byte("nested")},
		"assets/chunk-BR0k1Xmr.js.map":     {Data: []byte("source map")},
	}
	handler := &handler{assets: assets}

	for _, test := range []struct {
		name      string
		path      string
		wantCache string
	}{
		{name: "base64url hash", path: "/assets/chunk-Abc-1234.js", wantCache: immutableCache},
		{name: "unhashed file", path: "/assets/logo-not-a-build-hash.svg", wantCache: "public, max-age=0, must-revalidate"},
		{name: "nested file", path: "/assets/nested/chunk-BR0k1Xmr.js", wantCache: "public, max-age=0, must-revalidate"},
		{name: "source map", path: "/assets/chunk-BR0k1Xmr.js.map", wantCache: "public, max-age=0, must-revalidate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Header().Get("Cache-Control"); got != test.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, test.wantCache)
			}
			assertSecurityHeaders(t, response.Header())
			assertNoCSPNonce(t, response.Header())
		})
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
		"/robots.txt",
		"/module.wasm",
		"/feed.xml",
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
		assertNoCSPNonce(t, response.Header())
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
		{name: "specific refusal overrides wildcard", method: http.MethodGet, path: "/blocks/1", accept: "text/html;q=0, */*;q=1", want: http.StatusNotFound},
		{name: "type refusal overrides wildcard", method: http.MethodGet, path: "/blocks/1", accept: "text/*;q=0, */*;q=1", want: http.StatusNotFound},
		{name: "invalid quality", method: http.MethodGet, path: "/blocks/1", accept: "text/html;q=NaN", want: http.StatusNotFound},
		{name: "out of range quality", method: http.MethodGet, path: "/blocks/1", accept: "text/html;q=2", want: http.StatusNotFound},
		{name: "html wildcard", method: http.MethodGet, path: "/blocks/1", accept: "application/json, text/*;q=0.5", want: http.StatusOK},
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

func assertShellResponse(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	nonce := assertShellHeaders(t, response.Header())
	match := regexp.MustCompile(`<meta name="` + cspNonceMetaName + `" content="([A-Za-z0-9_-]{43})">`).FindStringSubmatch(response.Body.String())
	if len(match) != 2 {
		t.Fatalf("shell HTML does not contain one valid %s meta nonce", cspNonceMetaName)
	}
	if match[1] != nonce {
		t.Fatalf("shell meta nonce = %q, CSP nonce = %q", match[1], nonce)
	}
	return nonce
}

func assertShellHeaders(t *testing.T, header http.Header) string {
	t.Helper()
	if got := header.Get("Cache-Control"); got != noStoreCache {
		t.Errorf("shell Cache-Control = %q, want %q", got, noStoreCache)
	}
	if got := header.Get("ETag"); got != "" {
		t.Errorf("shell ETag = %q, want empty", got)
	}
	matches := regexp.MustCompile(`'nonce-([A-Za-z0-9_-]{43})'`).FindAllStringSubmatch(header.Get("Content-Security-Policy"), -1)
	if len(matches) != 1 {
		t.Fatalf("shell CSP does not contain exactly one valid nonce: %q", header.Get("Content-Security-Policy"))
	}
	return matches[0][1]
}

func assertNoCSPNonce(t *testing.T, header http.Header) {
	t.Helper()
	if strings.Contains(header.Get("Content-Security-Policy"), "'nonce-") {
		t.Errorf("non-shell CSP unexpectedly contains a nonce: %q", header.Get("Content-Security-Policy"))
	}
}
