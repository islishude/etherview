package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) (Manager, *MemoryRepository) {
	t.Helper()
	repository := NewMemoryRepository()
	counter := byte(0)
	manager := Manager{
		Repository: repository,
		Pepper:     bytes.Repeat([]byte{7}, 32),
		Now:        func() time.Time { return time.Unix(123, 0) },
		Random: func(target []byte) (int, error) {
			for index := range target {
				target[index] = counter
				counter++
			}
			return len(target), nil
		},
	}
	return manager, repository
}

func TestCreateAuthenticateAndRevoke(t *testing.T) {
	t.Parallel()
	manager, repository := testManager(t)
	issued, err := manager.Create(context.Background(), "partner", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Token, "evk_") || strings.Contains(string(issued.Record.Digest), issued.Token) {
		t.Fatal("unexpected token or digest")
	}
	record, err := manager.Authenticate(context.Background(), issued.Token)
	if err != nil || record.Name != "partner" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	if _, err := manager.Authenticate(context.Background(), issued.Token+"x"); err != ErrInvalidAPIKey {
		t.Fatalf("got %v", err)
	}
	if err := repository.Revoke(context.Background(), record.Prefix, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(context.Background(), issued.Token); err != ErrRevokedAPIKey {
		t.Fatalf("got %v", err)
	}
}

func TestRotateAtomicallyReplacesActiveKeyAndPreservesPolicy(t *testing.T) {
	t.Parallel()
	manager, repository := testManager(t)
	issued, err := manager.Create(context.Background(), "partner", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := manager.Rotate(context.Background(), issued.Record.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Token == issued.Token || replacement.Record.Prefix == issued.Record.Prefix {
		t.Fatalf("rotation reused credential identity: old=%s new=%s", issued.Record.Prefix, replacement.Record.Prefix)
	}
	if replacement.Record.Name != issued.Record.Name || replacement.Record.Rate != issued.Record.Rate || replacement.Record.Burst != issued.Record.Burst {
		t.Fatalf("replacement policy changed: old=%+v new=%+v", issued.Record, replacement.Record)
	}
	if _, err := manager.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrRevokedAPIKey) {
		t.Fatalf("old token authentication error = %v", err)
	}
	if authenticated, err := manager.Authenticate(context.Background(), replacement.Token); err != nil || authenticated.Prefix != replacement.Record.Prefix {
		t.Fatalf("replacement authentication record=%+v error=%v", authenticated, err)
	}
	if _, err := manager.Rotate(context.Background(), issued.Record.Prefix); !errors.Is(err, ErrRevokedAPIKey) {
		t.Fatalf("rotating revoked key error = %v", err)
	}
	items, err := repository.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("rotated repository items=%+v error=%v", items, err)
	}
	for _, item := range items {
		if len(item.Digest) != 0 {
			t.Fatalf("list exposed key digest for %s", item.Prefix)
		}
	}
}

func TestConcurrentRotationLeavesExactlyOneReplacementActive(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	manager := Manager{Repository: repository, Pepper: bytes.Repeat([]byte{9}, 32)}
	issued, err := manager.Create(context.Background(), "concurrent", 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		issued IssuedAPIKey
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			replacement, rotateErr := manager.Rotate(context.Background(), issued.Record.Prefix)
			results <- result{issued: replacement, err: rotateErr}
		}()
	}
	var successes, revoked int
	for range 2 {
		outcome := <-results
		switch {
		case outcome.err == nil:
			successes++
			if _, err := manager.Authenticate(context.Background(), outcome.issued.Token); err != nil {
				t.Fatalf("authenticate winning replacement: %v", err)
			}
		case errors.Is(outcome.err, ErrRevokedAPIKey):
			revoked++
		default:
			t.Fatalf("unexpected rotation error: %v", outcome.err)
		}
	}
	if successes != 1 || revoked != 1 {
		t.Fatalf("rotation outcomes successes=%d revoked=%d", successes, revoked)
	}
	items, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, item := range items {
		if item.RevokedAt == nil {
			active++
		}
	}
	if len(items) != 2 || active != 1 {
		t.Fatalf("rotated items=%+v active=%d", items, active)
	}
}

func TestAPIKeySecretMayContainUnderscore(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	randomCalls := 0
	manager := Manager{
		Repository: repository,
		Pepper:     []byte(strings.Repeat("p", 32)),
		Random: func(target []byte) (int, error) {
			randomCalls++
			for index := range target {
				target[index] = byte(index + randomCalls)
			}
			if randomCalls == 2 {
				target[0], target[1], target[2] = 0xff, 0xff, 0xff
			}
			return len(target), nil
		},
	}
	issued, err := manager.Create(context.Background(), "underscore", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.SplitN(issued.Token, "_", 3)[2], "_") {
		t.Fatalf("test token does not exercise underscore secret: %q", issued.Token)
	}
	if _, err := manager.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("authenticate underscore-bearing secret: %v", err)
	}
}

func TestMiddlewareRejectsAmbiguousAndRequiresKey(t *testing.T) {
	t.Parallel()
	manager, _ := testManager(t)
	issued, err := manager.Create(context.Background(), "partner", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = IdentityFrom(r.Context()).Authenticated
	})
	handler := manager.Middleware(true, next)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?apikey=other", nil)
	request.Header.Set("X-API-Key", issued.Token)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-API-Key", issued.Token)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !seen {
		t.Fatalf("status=%d seen=%v", recorder.Code, seen)
	}
}

func TestMiddlewareLimitsQueryAPIKeysToEtherscanBoundary(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	manager := Manager{Repository: repository, Pepper: bytes.Repeat([]byte{7}, 32)}
	issued, err := manager.Create(context.Background(), "query-scope", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IdentityFrom(r.Context()).Authenticated {
			t.Fatal("authenticated identity missing")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := manager.Middleware(true, next)

	native := httptest.NewRecorder()
	handler.ServeHTTP(native, httptest.NewRequest(http.MethodGet,
		"/api/v1/blocks?apikey="+url.QueryEscape(issued.Token), nil))
	if native.Code != http.StatusBadRequest || !strings.Contains(native.Body.String(), "api_key_query_not_allowed") {
		t.Fatalf("native query credential status=%d body=%s", native.Code, native.Body.String())
	}

	compatibility := httptest.NewRecorder()
	handler.ServeHTTP(compatibility, httptest.NewRequest(http.MethodGet,
		"/v2/api?module=account&action=balance&apikey="+url.QueryEscape(issued.Token), nil))
	if compatibility.Code != http.StatusNoContent {
		t.Fatalf("Etherscan query credential status=%d body=%s", compatibility.Code, compatibility.Body.String())
	}

	header := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.Header.Set("X-API-Key", issued.Token)
	handler.ServeHTTP(header, request)
	if header.Code != http.StatusNoContent {
		t.Fatalf("native header credential status=%d body=%s", header.Code, header.Body.String())
	}
}

func TestMiddlewareAuthenticatesBoundedEtherscanPOSTFormWithoutConsumingBody(t *testing.T) {
	t.Parallel()
	manager, _ := testManager(t)
	issued, err := manager.Create(context.Background(), "form-key", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"apikey":     {issued.Token},
		"module":     {"contract"},
		"action":     {"verifysourcecode"},
		"sourceCode": {"contract A { string constant x = \"body-preserved\"; }"},
	}
	body := values.Encode()
	seen := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if identity := IdentityFrom(r.Context()); !identity.Authenticated || identity.Prefix != issued.Record.Prefix {
			t.Fatalf("identity=%+v", identity)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse preserved form: %v", err)
		}
		if r.Form.Get("sourceCode") != values.Get("sourceCode") || r.Form.Get("apikey") != issued.Token {
			t.Fatalf("preserved form=%v", r.Form)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := manager.Middleware(true, next)
	request := httptest.NewRequest(http.MethodPost, "/v2/api", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || !seen {
		t.Fatalf("status=%d seen=%t body=%s", recorder.Code, seen, recorder.Body.String())
	}
}

func TestMiddlewareEtherscanCredentialPriorityAndConflicts(t *testing.T) {
	t.Parallel()
	manager, _ := testManager(t)
	first, err := manager.Create(context.Background(), "first", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(context.Background(), "second", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		header     string
		query      []string
		form       []string
		wantStatus int
	}{
		{name: "header", header: first.Token, wantStatus: http.StatusNoContent},
		{name: "query", query: []string{first.Token}, wantStatus: http.StatusNoContent},
		{name: "form", form: []string{first.Token}, wantStatus: http.StatusNoContent},
		{name: "same credential in every source", header: first.Token, query: []string{first.Token}, form: []string{first.Token}, wantStatus: http.StatusNoContent},
		{name: "header query conflict", header: first.Token, query: []string{second.Token}, wantStatus: http.StatusBadRequest},
		{name: "header form conflict", header: first.Token, form: []string{second.Token}, wantStatus: http.StatusBadRequest},
		{name: "query form conflict", query: []string{first.Token}, form: []string{second.Token}, wantStatus: http.StatusBadRequest},
		{name: "duplicate query", query: []string{first.Token, first.Token}, wantStatus: http.StatusBadRequest},
		{name: "duplicate form", form: []string{first.Token, first.Token}, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if identity := IdentityFrom(r.Context()); !identity.Authenticated || identity.Prefix != first.Record.Prefix {
					t.Fatalf("identity=%+v", identity)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			query := url.Values{}
			for _, credential := range test.query {
				query.Add("apikey", credential)
			}
			path := "/v2/api"
			if encoded := query.Encode(); encoded != "" {
				path += "?" + encoded
			}
			method := http.MethodGet
			var body io.Reader
			if test.form != nil {
				method = http.MethodPost
				values := url.Values{"module": {"contract"}}
				for _, credential := range test.form {
					values.Add("apikey", credential)
				}
				body = strings.NewReader(values.Encode())
			}
			request := httptest.NewRequest(method, path, body)
			if test.form != nil {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if test.header != "" {
				request.Header.Set("X-API-Key", test.header)
			}
			recorder := httptest.NewRecorder()
			manager.Middleware(true, next).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if called != (test.wantStatus == http.StatusNoContent) {
				t.Fatalf("called=%t status=%d", called, recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), first.Token) || strings.Contains(recorder.Body.String(), second.Token) {
				t.Fatalf("response leaked API key: %s", recorder.Body.String())
			}
		})
	}
}

func TestMiddlewareLimitsEtherscanFormKeyToExactPOSTBoundaryAndSize(t *testing.T) {
	t.Parallel()
	manager, _ := testManager(t)
	issued, err := manager.Create(context.Background(), "bounded-form", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := manager.Middleware(true, next)
	form := url.Values{"apikey": {issued.Token}}.Encode()
	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "native post", method: http.MethodPost, path: "/api/v1/blocks"},
		{name: "compatibility get body", method: http.MethodGet, path: "/v2/api"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(form))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || called {
			t.Fatalf("%s status=%d called=%t body=%s", test.name, recorder.Code, called, recorder.Body.String())
		}
	}

	limited := manager
	limited.MaxCompatibilityFormBodyBytes = 32
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/api", strings.NewReader(form+"&sourceCode="+strings.Repeat("x", 64)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	limited.Middleware(true, next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || called || !strings.Contains(recorder.Body.String(), "api_key_form_too_large") || strings.Contains(recorder.Body.String(), issued.Token) {
		t.Fatalf("oversized status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v2/api", strings.NewReader("apikey=%zz"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	manager.Middleware(true, next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || called || !strings.Contains(recorder.Body.String(), "invalid_api_key_form") {
		t.Fatalf("malformed status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestBoundaryErrorsUseNativeAndEtherscanEnvelopes(t *testing.T) {
	t.Parallel()
	manager, _ := testManager(t)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected request reached application handler")
	})
	handler := manager.Middleware(false, next)

	native := httptest.NewRecorder()
	nativeRequest := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	nativeRequest.Header.Set("X-API-Key", "invalid")
	nativeRequest.Header.Set("X-Request-ID", "native-request")
	handler.ServeHTTP(native, nativeRequest)
	if native.Code != http.StatusUnauthorized || native.Header().Get("X-Request-ID") != "native-request" {
		t.Fatalf("native status=%d headers=%v body=%s", native.Code, native.Header(), native.Body.String())
	}
	var nativeEnvelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(native.Body.Bytes(), &nativeEnvelope); err != nil {
		t.Fatal(err)
	}
	if nativeEnvelope.Error.Code != "invalid_api_key" || nativeEnvelope.Error.RequestID != "native-request" {
		t.Fatalf("native envelope=%+v", nativeEnvelope)
	}

	compatibility := httptest.NewRecorder()
	compatibilityRequest := httptest.NewRequest(http.MethodGet, "/v2/api?apikey=query-key", nil)
	compatibilityRequest.Header.Set("X-API-Key", "header-key")
	handler.ServeHTTP(compatibility, compatibilityRequest)
	if compatibility.Code != http.StatusBadRequest || compatibility.Header().Get("X-Request-ID") == "" {
		t.Fatalf("compatibility status=%d headers=%v body=%s", compatibility.Code, compatibility.Header(), compatibility.Body.String())
	}
	var compatibilityEnvelope struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(compatibility.Body.Bytes(), &compatibilityEnvelope); err != nil {
		t.Fatal(err)
	}
	if compatibilityEnvelope.Status != "0" || compatibilityEnvelope.Message != "NOTOK" || !strings.Contains(compatibilityEnvelope.Result, "ambiguous_api_key") {
		t.Fatalf("compatibility envelope=%+v", compatibilityEnvelope)
	}
}

type recordingRateObserver struct {
	decisions []string
}

func (observer *recordingRateObserver) RecordRateLimit(decision string) {
	observer.decisions = append(observer.decisions, decision)
}

func TestRateLimitErrorsPreserveBoundaryEnvelope(t *testing.T) {
	t.Parallel()
	limiter := NewMemoryLimiter(func() time.Time { return time.Unix(123, 0) })
	observer := &recordingRateObserver{}
	handler := RateMiddleware{
		Limiter:   limiter,
		Anonymous: Limit{Rate: 1, Burst: 1},
		Observer:  observer,
	}.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		name       string
		path       string
		native     bool
		remoteAddr string
	}{
		{name: "native", path: "/api/v1/status", native: true, remoteAddr: "192.0.2.1:1000"},
		{name: "etherscan", path: "/v2/api", remoteAddr: "192.0.2.2:1000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := httptest.NewRequest(http.MethodGet, test.path, nil)
			first.RemoteAddr = test.remoteAddr
			handler.ServeHTTP(httptest.NewRecorder(), first)

			second := httptest.NewRequest(http.MethodGet, test.path, nil)
			second.RemoteAddr = test.remoteAddr
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, second)
			if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "1" || recorder.Header().Get("X-Request-ID") == "" {
				t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			if test.native {
				var envelope struct {
					Error struct {
						Code      string `json:"code"`
						RequestID string `json:"request_id"`
					} `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Code != "rate_limit_exceeded" || envelope.Error.RequestID == "" {
					t.Fatalf("native envelope=%+v", envelope)
				}
				return
			}
			var envelope struct {
				Status  string `json:"status"`
				Message string `json:"message"`
				Result  string `json:"result"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Status != "0" || envelope.Message != "NOTOK" || !strings.Contains(envelope.Result, "rate_limit_exceeded") {
				t.Fatalf("compatibility envelope=%+v", envelope)
			}
		})
	}
	if got := strings.Join(observer.decisions, ","); got != "allowed,rejected,allowed,rejected" {
		t.Fatalf("rate decisions=%q", got)
	}
}

func TestMemoryLimiterRefills(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	limiter := NewMemoryLimiter(func() time.Time { return now })
	limit := Limit{Rate: 2, Burst: 2}
	if ok, _ := limiter.Allow(context.Background(), "key", limit); !ok {
		t.Fatal("first request denied")
	}
	if ok, _ := limiter.Allow(context.Background(), "key", limit); !ok {
		t.Fatal("second request denied")
	}
	if ok, retry := limiter.Allow(context.Background(), "key", limit); ok || retry != 500*time.Millisecond {
		t.Fatalf("ok=%v retry=%s", ok, retry)
	}
	now = now.Add(500 * time.Millisecond)
	if ok, _ := limiter.Allow(context.Background(), "key", limit); !ok {
		t.Fatal("refilled request denied")
	}
}
