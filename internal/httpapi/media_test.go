package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

const mediaTestAddress = "0x1111111111111111111111111111111111111111"

type recordingMediaSource struct {
	uri          string
	err          error
	currentErr   error
	stale        bool
	calls        int
	currentCalls int
}

func (source *recordingMediaSource) SelectNFTImage(_ context.Context, _ ethrpc.Address, _ string) (metadata.NFTImageSelection, error) {
	source.calls++
	return metadata.NFTImageSelection{URI: source.uri}, source.err
}

func (source *recordingMediaSource) NFTImageCurrent(_ context.Context, _ ethrpc.Address, _ string, _ metadata.NFTImageSelection) (bool, error) {
	source.currentCalls++
	return !source.stale, source.currentErr
}

type recordingMediaFetcher struct {
	result metadata.Result
	err    error
	uri    string
}

func (fetcher *recordingMediaFetcher) Fetch(_ context.Context, uri string, _ metadata.Kind) (metadata.Result, error) {
	fetcher.uri = uri
	return fetcher.result, fetcher.err
}

type fixedMediaResolver struct {
	addresses []net.IPAddr
	err       error
}

func (resolver fixedMediaResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, resolver.err
}

type mediaAuthRepository struct {
	record auth.APIKey
}

func (repository *mediaAuthRepository) Put(_ context.Context, record auth.APIKey) error {
	repository.record = record
	return nil
}

func (repository *mediaAuthRepository) ByPrefix(_ context.Context, prefix string) (auth.APIKey, error) {
	if repository.record.Prefix != prefix {
		return auth.APIKey{}, auth.ErrInvalidAPIKey
	}
	return repository.record, nil
}

func (repository *mediaAuthRepository) Revoke(_ context.Context, prefix string, at time.Time) error {
	if repository.record.Prefix != prefix {
		return auth.ErrInvalidAPIKey
	}
	repository.record.RevokedAt = &at
	return nil
}

func (repository *mediaAuthRepository) List(context.Context) ([]auth.APIKey, error) {
	return []auth.APIKey{repository.record}, nil
}

func newMediaTestHandler(t *testing.T, source metadata.NFTImageSource, fetcher metadata.Fetcher, authenticated bool, logger *slog.Logger) http.Handler {
	t.Helper()
	var proxy *metadata.MediaProxy
	if fetcher != nil {
		var err error
		proxy, err = metadata.NewMediaProxy(fetcher)
		if err != nil {
			t.Fatal(err)
		}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	}
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, NFTMediaSource: source,
		NFTMediaProxy: proxy, Logger: logger,
		RequestID: func() string { return "media-request" }, Now: func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if authenticated {
		repository := &mediaAuthRepository{}
		manager := auth.Manager{Repository: repository, Pepper: []byte(strings.Repeat("p", 32))}
		issued, err := manager.Create(t.Context(), "media-test", 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		authenticatedHandler := manager.Middleware(true, handler)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-API-Key", issued.Token)
			authenticatedHandler.ServeHTTP(w, r)
		})
	}
	return handler
}

func TestNFTMediaReturnsOnlyValidatedBytesWithStrictHeaders(t *testing.T) {
	sourceURI := "https://metadata-secret.example.invalid/image.png"
	resolvedURI := "https://cdn-secret.example.invalid/content/42"
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	source := &recordingMediaSource{uri: sourceURI}
	fetcher := &recordingMediaFetcher{result: metadata.Result{
		URL: resolvedURI, ContentType: "image/png", Body: body,
	}}
	var logs bytes.Buffer
	handler := newMediaTestHandler(t, source, fetcher, true, slog.New(slog.NewTextHandler(&logs, nil)))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), body) {
		t.Fatalf("status=%d body=%x", recorder.Code, recorder.Body.Bytes())
	}
	if source.calls != 1 || source.currentCalls != 1 || fetcher.uri != sourceURI {
		t.Fatalf("source calls=%d current=%d fetched=%q", source.calls, source.currentCalls, fetcher.uri)
	}
	wantHeaders := map[string]string{
		"Cache-Control":                "no-store, max-age=0",
		"Content-Type":                 "image/png",
		"Content-Length":               strconv.Itoa(len(body)),
		"Content-Disposition":          `inline; filename="nft-media.png"`,
		"Cross-Origin-Resource-Policy": "same-origin",
		"X-Content-Type-Options":       "nosniff",
		"X-Etherview-Media-State":      "available",
	}
	for name, want := range wantHeaders {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy=%q", csp)
	}
	if recorder.Header().Get("ETag") != "" {
		t.Error("no-store media response exposed an ETag")
	}
	for name, values := range recorder.Header() {
		joined := name + ":" + strings.Join(values, ",")
		if strings.Contains(joined, "metadata-secret") || strings.Contains(joined, "cdn-secret") {
			t.Fatalf("source URI leaked in response header %q", joined)
		}
	}
	if strings.Contains(logs.String(), "metadata-secret") || strings.Contains(logs.String(), "cdn-secret") {
		t.Fatalf("source URI leaked in logs: %s", logs.String())
	}
}

func TestNFTMediaValidatesIdentityBeforeLookup(t *testing.T) {
	source := &recordingMediaSource{uri: "https://example.invalid/image.png"}
	fetcher := &recordingMediaFetcher{}
	handler := newMediaTestHandler(t, source, fetcher, true, nil)
	for _, test := range []struct {
		path string
		code string
	}{
		{"/api/v1/nfts/0x12/42/media", "invalid_address"},
		{"/api/v1/nfts/" + mediaTestAddress + "/01/media", "invalid_token_id"},
		{"/api/v1/nfts/" + mediaTestAddress + "/" + strings.Repeat("9", 79) + "/media", "invalid_token_id"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.code) {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}
	if source.calls != 0 || source.currentCalls != 0 || fetcher.uri != "" {
		t.Fatalf("invalid identity reached source=%d fetch=%q", source.calls, fetcher.uri)
	}
}

func TestNFTMediaRejectsSelectionThatBecomesNoncanonicalDuringFetch(t *testing.T) {
	source := &recordingMediaSource{uri: "https://example.invalid/image.png", stale: true}
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}
	fetcher := &recordingMediaFetcher{result: metadata.Result{
		URL: source.uri, ContentType: "image/png", Body: body,
	}}
	handler := newMediaTestHandler(t, source, fetcher, true, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusConflict || recorder.Header().Get("X-Etherview-Media-State") != "noncanonical" ||
		!strings.Contains(recorder.Body.String(), "nft_media_noncanonical") {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestNFTMediaMapsPersistedMetadataStatesWithoutLeakingSource(t *testing.T) {
	tests := []struct {
		err    error
		status int
		state  string
		code   string
	}{
		{metadata.ErrMediaSourceNotFound, http.StatusNotFound, "not_found", "nft_metadata_not_found"},
		{metadata.ErrMediaImageNotFound, http.StatusNotFound, "not_found", "nft_media_not_found"},
		{metadata.ErrMediaSourcePending, http.StatusServiceUnavailable, "pending", "nft_metadata_pending"},
		{metadata.ErrMediaSourceUnavailable, http.StatusServiceUnavailable, "unavailable", "nft_media_unavailable"},
		{metadata.ErrMediaSourceUnsafe, http.StatusUnprocessableEntity, "unsafe", "nft_media_unsafe"},
		{metadata.ErrMediaSourceError, http.StatusServiceUnavailable, "error", "nft_metadata_error"},
		{metadata.ErrMediaSourceNoncanonical, http.StatusConflict, "noncanonical", "nft_media_noncanonical"},
	}
	for _, test := range tests {
		source := &recordingMediaSource{err: test.err}
		handler := newMediaTestHandler(t, source, &recordingMediaFetcher{}, true, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
		if recorder.Code != test.status || recorder.Header().Get("X-Etherview-Media-State") != test.state || !strings.Contains(recorder.Body.String(), test.code) {
			t.Fatalf("err=%v status=%d state=%q body=%s", test.err, recorder.Code, recorder.Header().Get("X-Etherview-Media-State"), recorder.Body.String())
		}
		if !strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("err=%v missing no-store", test.err)
		}
	}
}

func TestNFTMediaDisabledAndConfiguredAuthenticationAreExplicit(t *testing.T) {
	disabled := newMediaTestHandler(t, nil, nil, true, nil)
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("X-Etherview-Media-State") != "disabled" || !strings.Contains(recorder.Body.String(), "nft_media_disabled") {
		t.Fatalf("disabled status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	unauthenticatedDeployment := newMediaTestHandler(t, &recordingMediaSource{}, &recordingMediaFetcher{}, false, nil)
	recorder = httptest.NewRecorder()
	unauthenticatedDeployment.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("X-Etherview-Media-State") != "unauthorized" || !strings.Contains(recorder.Body.String(), "api_key_required") {
		t.Fatalf("auth status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestNFTMediaSecurityHeadersPrecedeAuthenticationAndRateRejections(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		handler := NFTMediaSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, httptest.NewRequest(http.MethodGet, "/", nil), status, "rejected", "rejected", nil)
		}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
		if recorder.Code != status || recorder.Header().Get("Cache-Control") != "no-store, max-age=0" ||
			recorder.Header().Get("X-Content-Type-Options") != "nosniff" ||
			recorder.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" ||
			!strings.Contains(recorder.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("status=%d response=%d headers=%v", status, recorder.Code, recorder.Header())
		}
	}
	adjacent := httptest.NewRecorder()
	NFTMediaSecurityMiddleware(http.NotFoundHandler()).ServeHTTP(adjacent,
		httptest.NewRequest(http.MethodGet, "/api/v1/nfts/not-a-media-route", nil))
	if adjacent.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("adjacent route received media headers: %v", adjacent.Header())
	}
}

func TestNFTMediaRejectsPrivateNetworkAndDisguisedHTML(t *testing.T) {
	privateClient, err := metadata.New(metadata.Policy{}, fixedMediaResolver{
		addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		source  *recordingMediaSource
		fetcher metadata.Fetcher
	}{
		"private network": {
			source:  &recordingMediaSource{uri: "https://private-secret.example.invalid/image.png"},
			fetcher: privateClient,
		},
		"HTML disguised as PNG": {
			source: &recordingMediaSource{uri: "https://untrusted.example.invalid/image.png"},
			fetcher: &recordingMediaFetcher{result: metadata.Result{
				URL: "https://untrusted.example.invalid/image.png", ContentType: "image/png", Body: []byte("<html>not an image</html>"),
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := newMediaTestHandler(t, test.source, test.fetcher, true, slog.New(slog.NewTextHandler(&logs, nil)))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
			if recorder.Code != http.StatusUnprocessableEntity || recorder.Header().Get("X-Etherview-Media-State") != "unsafe" || !strings.Contains(recorder.Body.String(), "nft_media_unsafe") {
				t.Fatalf("status=%d state=%q body=%s", recorder.Code, recorder.Header().Get("X-Etherview-Media-State"), recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "example.invalid") || strings.Contains(logs.String(), "example.invalid") {
				t.Fatalf("source URI leaked: response=%s logs=%s", recorder.Body.String(), logs.String())
			}
		})
	}
}

func TestNFTMediaRejectsOversizedUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 32)...))
	}))
	defer server.Close()
	client, err := metadata.New(metadata.Policy{
		Timeout: time.Second, MaxBytes: 8, AllowHTTP: true, UnsafeAllowPrivateNetworks: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := newMediaTestHandler(t, &recordingMediaSource{uri: server.URL}, client, true, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusRequestEntityTooLarge || recorder.Header().Get("X-Etherview-Media-State") != "too_large" || !strings.Contains(recorder.Body.String(), "nft_media_too_large") {
		t.Fatalf("status=%d state=%q body=%s", recorder.Code, recorder.Header().Get("X-Etherview-Media-State"), recorder.Body.String())
	}
}

func TestNFTMediaMapsTypedTemporaryFailure(t *testing.T) {
	fetcher := &recordingMediaFetcher{err: &metadata.FetchError{Kind: metadata.FailureTemporary, Err: errors.New("secret upstream URL")}}
	handler := newMediaTestHandler(t, &recordingMediaSource{uri: "https://secret.example.invalid/media"}, fetcher, true, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+mediaTestAddress+"/42/media", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") == "" || recorder.Header().Get("X-Etherview-Media-State") != "temporary" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("fetch error leaked: %s", recorder.Body.String())
	}
}
