package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
	"github.com/islishude/etherview/internal/verify"
)

type unreadVerificationBody struct{ t *testing.T }

func (body unreadVerificationBody) Read([]byte) (int, error) {
	body.t.Fatal("unauthorized request read its body")
	return 0, io.EOF
}
func (unreadVerificationBody) Close() error { return nil }

type browserVerificationService struct {
	verificationV2Service
	calls int
}

func (service *browserVerificationService) SubmitV2(ctx context.Context, request verify.SubmissionV2) (verify.VerificationJob, bool, error) {
	service.calls++
	service.job.RequestV2 = &request
	service.job.Kind = request.Kind
	return service.verificationV2Service.SubmitV2(ctx, request)
}

func browserVerificationHandler(t *testing.T) (*Handler, *fakeUserAuthenticator, *browserVerificationService) {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 1
	cfg.Server.PublicURL = testAuthOrigin
	cfg.Features.UserAuth, cfg.Features.Verification, cfg.Features.Sourcify = true, true, true
	cfg.Security.PublicVerification = true
	authentication := &fakeUserAuthenticator{authentication: SessionAuthentication{
		Session: userauth.Session{User: authTestUser(time.Now(), userauth.RoleUser)},
		validate: func(token string) error {
			if token == "csrf" {
				return nil
			}
			return userauth.ErrCSRFInvalid
		},
	}}
	service := &browserVerificationService{}
	service.job = verify.VerificationJob{
		ID: "123e4567-e89b-42d3-a456-426614174000", Kind: verify.JobAddress, Status: verify.JobQueued,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}, UserAuth: authentication, UserAdministration: &fakeUserAdministration{}, BillingReader: &fakeBillingReader{},
		VerificationReader: service, VerificationSubmitter: service,
		VerificationTargets: verificationTargetResolver{target: verify.VerificationTarget{ChainID: 1, Address: testAddress}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, authentication, service
}

func TestBrowserVerificationAuthenticationBeforeWork(t *testing.T) {
	for _, path := range []string{"/verification-jobs/local", "/verification-jobs/sourcify-import", "/verification-jobs/sourcify", "/verification-jobs/sourcify-from-etherscan"} {
		for _, test := range []struct {
			name, origin, csrf string
			cookie             bool
			err                error
			status             int
		}{
			{name: "API key only", origin: testAuthOrigin, csrf: "csrf", status: 401},
			{name: "missing origin", cookie: true, csrf: "csrf", status: 403},
			{name: "wrong origin", origin: "https://evil.test", cookie: true, csrf: "csrf", status: 403},
			{name: "missing CSRF", origin: testAuthOrigin, cookie: true, status: 403},
			{name: "wrong CSRF", origin: testAuthOrigin, cookie: true, csrf: "wrong", status: 403},
			{name: "expired", origin: testAuthOrigin, cookie: true, csrf: "csrf", err: userauth.ErrSessionInvalid, status: 401},
			{name: "disabled", origin: testAuthOrigin, cookie: true, csrf: "csrf", err: userauth.ErrUserDisabled, status: 403},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				handler, authentication, service := browserVerificationHandler(t)
				authentication.authenticateErr = test.err
				request := httptest.NewRequest("POST", "/api/v1"+path, unreadVerificationBody{t})
				request.Header.Set("Origin", test.origin)
				request.Header.Set("X-CSRF-Token", test.csrf)
				if test.cookie {
					request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
				}
				manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
				key, err := manager.Create(t.Context(), "test", 100, 100)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("X-API-Key", key.Token)
				response := httptest.NewRecorder()
				manager.Middleware(false, handler).ServeHTTP(response, request)
				if response.Code != test.status || service.calls != 0 {
					t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), service.calls)
				}
			})
		}
	}
}

func TestBrowserVerificationSubmissionAndRedactedRead(t *testing.T) {
	handler, _, service := browserVerificationHandler(t)
	body := `{"address":"` + testAddress + `","language":"solidity","compiler_version":"0.8.30","input_kind":"standard_json","input":{"sources":{"A.sol":{"content":"private-source"}}}}`
	request := httptest.NewRequest("POST", "/api/v1/verification-jobs/local", strings.NewReader(body))
	request.Header.Set("Origin", testAuthOrigin)
	request.Header.Set("X-CSRF-Token", "csrf")
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 202 || service.calls != 1 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-source") || !strings.Contains(response.Body.String(), `"chain_id":"1"`) || !strings.Contains(response.Body.String(), `"address":`) {
		t.Fatal(response.Body.String())
	}
	service.job.Outcome = json.RawMessage(`{"kind":"verification_success","sources":{"A.sol":"private-source"},"settings":{"secret":"upstream-secret"},"compilation_artifacts":{"ast":"private-source"},"runtime_match":{"match_type":"full","transformations":[],"values":{}}}`)
	handler.cfg.Features.Verification = false
	handler.cfg.Security.PublicVerification = false
	request = httptest.NewRequest("GET", "/api/v1/verification-jobs/"+service.job.ID, nil)
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"match_type":"full"`) {
		t.Fatal(response.Body.String())
	}
	for _, secret := range []string{"private-source", "upstream-secret", "sources", "compilation_artifacts"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	request.SetPathValue("id", "bad")
	request.URL.Path = "/api/v1/verification-jobs/bad"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal(response.Body.String())
	}
}

func TestBrowserVerificationCookieCannotAuthorizeNative(t *testing.T) {
	handler, _, service := browserVerificationHandler(t)
	for _, path := range []string{"/contracts/" + testAddress + "/verification", "/verifier/sourcify", "/verifier/sourcify/from-etherscan"} {
		request := httptest.NewRequest("POST", "/api/v1"+path, unreadVerificationBody{t})
		request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
		request.Header.Set("Origin", testAuthOrigin)
		request.Header.Set("X-CSRF-Token", "csrf")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 401 || service.calls != 0 {
			t.Fatal(response.Body.String())
		}
	}
}

func TestBrowserVerificationSourcifyFlowsAndLimits(t *testing.T) {
	for _, test := range []struct {
		path, body string
		kind       verify.JobKind
	}{
		{"sourcify-import", `{"address":"` + testAddress + `","chain_id":"9007199254740993"}`, verify.JobAddress},
		{"sourcify", `{"address":"` + testAddress + `","chain_id":"1","files":{"A.sol":"private-source"}}`, verify.JobSourcify},
		{"sourcify-from-etherscan", `{"address":"` + testAddress + `","chain_id":"1","etherscan_api_key":"upstream-secret"}`, verify.JobSourcifyFromEtherscan},
	} {
		t.Run(test.path, func(t *testing.T) {
			handler, _, service := browserVerificationHandler(t)
			importer := &sourcifyImporterStub{}
			handler.sourcifyImporter = importer
			send := func(body string) *httptest.ResponseRecorder {
				request := httptest.NewRequest("POST", "/api/v1/verification-jobs/"+test.path, strings.NewReader(body))
				request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
				request.Header.Set("Origin", testAuthOrigin)
				request.Header.Set("X-CSRF-Token", "csrf")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				return response
			}
			response := send(test.body)
			if response.Code != 202 || service.calls != 1 || service.submission.Kind != test.kind {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.path == "sourcify-import" && (importer.calls != 1 || importer.sourceChain != 9007199254740993) {
				t.Fatal("source-chain identity lost")
			}
			if !strings.Contains(response.Body.String(), `"address":`) || strings.Contains(response.Body.String(), "private-source") || strings.Contains(response.Body.String(), "upstream-secret") {
				t.Fatal(response.Body.String())
			}
			handler.maxVerificationBody = 10
			if response = send(test.body); response.Code != 400 || service.calls != 1 {
				t.Fatal(response.Body.String())
			}
			handler.cfg.Features.Sourcify = false
			if response = send(test.body); response.Code != 503 || service.calls != 1 {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestBrowserVerificationReadsRequireSessionAndIngressBudget(t *testing.T) {
	handler, authentication, service := browserVerificationHandler(t)
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: []byte(strings.Repeat("p", 32))}
	key, err := manager.Create(t.Context(), "test", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	rates := auth.RateMiddleware{Limiter: auth.NewMemoryLimiter(time.Now), Ingress: auth.Limit{Rate: 1, Burst: 1}, Anonymous: auth.Limit{Rate: 100, Burst: 100}}
	protected := rates.WrapWithAuthentication(manager, handler)
	for index, status := range []int{401, 429} {
		request := httptest.NewRequest("GET", "/api/v1/verification-jobs/"+service.job.ID, nil)
		request.Header.Set("X-API-Key", key.Token)
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, request)
		if response.Code != status || authentication.authCalls != 0 {
			t.Fatalf("request=%d status=%d auth=%d", index, response.Code, authentication.authCalls)
		}
	}
}

func TestBrowserVerificationDisabledBeforeParsing(t *testing.T) {
	for _, feature := range []string{"authentication", "verification", "public_submission"} {
		t.Run(feature, func(t *testing.T) {
			handler, _, service := browserVerificationHandler(t)
			switch feature {
			case "authentication":
				handler.cfg.Features.UserAuth = false
			case "verification":
				handler.cfg.Features.Verification = false
			case "public_submission":
				handler.cfg.Security.PublicVerification = false
			}
			request := httptest.NewRequest("POST", "/api/v1/verification-jobs/local", unreadVerificationBody{t})
			request.Header.Set("Origin", testAuthOrigin)
			request.Header.Set("X-CSRF-Token", "csrf")
			request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 503 || service.calls != 0 {
				t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), service.calls)
			}
		})
	}
}
