package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
)

const (
	testUserID      = "74e5f6a2-30ef-4d7f-93b8-e430f1fdfac4"
	testChallengeID = "4958525d-f31e-4fd8-adf7-ae2902f7ebda"
	testAddress     = "0x52908400098527886E0F7030069857D2E4169EE7"
	testAuthOrigin  = "https://explorer.example"
)

type fakeUserAuthenticator struct {
	challenge       userauth.Challenge
	login           userauth.LoginResult
	authentication  SessionAuthentication
	createErr       error
	verifyErr       error
	authenticateErr error
	logoutErr       error
	createCalls     int
	verifyCalls     int
	authCalls       int
	logoutCalls     int
}

func (fake *fakeUserAuthenticator) CreateChallenge(
	context.Context,
	string,
) (userauth.Challenge, error) {
	fake.createCalls++
	return fake.challenge, fake.createErr
}

func (fake *fakeUserAuthenticator) VerifyChallenge(
	context.Context,
	string,
	string,
) (userauth.LoginResult, error) {
	fake.verifyCalls++
	return fake.login, fake.verifyErr
}

func (fake *fakeUserAuthenticator) Authenticate(
	context.Context,
	string,
) (SessionAuthentication, error) {
	fake.authCalls++
	return fake.authentication, fake.authenticateErr
}

func (fake *fakeUserAuthenticator) Logout(context.Context, string) (bool, error) {
	fake.logoutCalls++
	return true, fake.logoutErr
}

type fakeUserAdministration struct {
	user         userauth.User
	users        []userauth.User
	updateResult userauth.AdminUserUpdateResult
	revoked      uint64
	err          error
	displayName  *string
	pageAfter    *userauth.UserPageAfter
	pageLimit    int
	update       userauth.AdminUserUpdate
	updateUserID string
	revokeUserID string
}

func (fake *fakeUserAdministration) UserByID(context.Context, string) (userauth.User, error) {
	return fake.user, fake.err
}

func (fake *fakeUserAdministration) UserByAddress(context.Context, string) (userauth.User, error) {
	return fake.user, fake.err
}

func (fake *fakeUserAdministration) UpdateDisplayName(
	_ context.Context,
	_ string,
	displayName *string,
	_ time.Time,
) (userauth.User, error) {
	fake.displayName = displayName
	return fake.user, fake.err
}

func (fake *fakeUserAdministration) Users(
	_ context.Context,
	after *userauth.UserPageAfter,
	limit int,
) ([]userauth.User, error) {
	fake.pageAfter, fake.pageLimit = after, limit
	return fake.users, fake.err
}

func (fake *fakeUserAdministration) UpdateUser(
	_ context.Context,
	id string,
	update userauth.AdminUserUpdate,
	_ time.Time,
) (userauth.AdminUserUpdateResult, error) {
	fake.updateUserID, fake.update = id, update
	return fake.updateResult, fake.err
}

func (fake *fakeUserAdministration) RevokeAllSessions(
	_ context.Context,
	id string,
	_ time.Time,
) (uint64, error) {
	fake.revokeUserID = id
	return fake.revoked, fake.err
}

func TestUserAuthFeatureOffReturnsTypedUnavailable(t *testing.T) {
	t.Parallel()
	handler := testHandler(t, fakeReader{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	handler.ServeHTTP(recorder, request)
	assertAuthError(t, recorder, http.StatusServiceUnavailable, "user_auth_unavailable")
}

func TestAuthChallengeRequiresExactOriginAndStrictJSON(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	authenticator := &fakeUserAuthenticator{challenge: userauth.Challenge{
		ID: testChallengeID, ChainID: 11155111, Address: testAddress,
		Message: "canonical SIWE message", IssuedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
	}}
	handler := enabledAuthHandler(t, authenticator, &fakeUserAdministration{})

	for _, test := range []struct {
		name   string
		origin []string
		body   string
		status int
		code   string
	}{
		{"missing origin", nil, `{"address":"` + testAddress + `"}`, http.StatusForbidden, "origin_invalid"},
		{"wrong origin", []string{"https://evil.example"}, `{"address":"` + testAddress + `"}`, http.StatusForbidden, "origin_invalid"},
		{"duplicate origin", []string{testAuthOrigin, testAuthOrigin}, `{"address":"` + testAddress + `"}`, http.StatusForbidden, "origin_invalid"},
		{"duplicate json key", []string{testAuthOrigin}, `{"address":"` + testAddress + `","address":"` + testAddress + `"}`, http.StatusBadRequest, "invalid_auth_request"},
		{"valid", []string{testAuthOrigin}, `{"address":"` + testAddress + `"}`, http.StatusCreated, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost, "/api/v1/auth/challenge", strings.NewReader(test.body),
			)
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			handler.ServeHTTP(recorder, request)
			if test.code != "" {
				assertAuthError(t, recorder, test.status, test.code)
				return
			}
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", recorder.Header().Get("Cache-Control"))
			}
			var response gen.AuthChallengeResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Data.ChallengeId.String() != testChallengeID ||
				response.Data.Message != "canonical SIWE message" ||
				!response.Data.ExpiresAt.Equal(now.Add(5*time.Minute)) {
				t.Fatalf("response=%+v", response)
			}
		})
	}
	if authenticator.createCalls != 1 {
		t.Fatalf("create calls=%d", authenticator.createCalls)
	}
}

func TestAuthVerifySetsBoundedSessionCookie(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 2, 3, 4, 0, time.UTC)
	user := authTestUser(now, userauth.RoleUser)
	authenticator := &fakeUserAuthenticator{login: userauth.LoginResult{
		Session: userauth.Session{
			ID: "92564860-e50b-4162-91c0-f32179432da8", User: user,
			CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
		},
		Credentials: userauth.Credentials{
			Token: strings.Repeat("t", 43), CSRFToken: strings.Repeat("c", 43),
		},
	}}
	handler := enabledAuthHandler(t, authenticator, &fakeUserAdministration{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/auth/verify",
		strings.NewReader(`{"challenge_id":"`+testChallengeID+`","signature":"0x`+strings.Repeat("11", 65)+`"}`),
	)
	request.Header.Set("Origin", testAuthOrigin)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != userauth.SessionCookieName ||
		cookie.Value != strings.Repeat("t", 43) ||
		cookie.Path != "/api/v1" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" ||
		!cookie.Expires.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("cookie=%+v", cookie)
	}
	var response gen.AuthSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.Authenticated || response.Data.CsrfToken == nil ||
		*response.Data.CsrfToken != strings.Repeat("c", 43) ||
		response.Data.User == nil || response.Data.User.Id.String() != testUserID {
		t.Fatalf("response=%+v", response)
	}
}

func TestAuthSessionInvalidCookieIsAnonymousAndCleared(t *testing.T) {
	t.Parallel()
	authenticator := &fakeUserAuthenticator{authenticateErr: userauth.ErrSessionInvalid}
	handler := enabledAuthHandler(t, authenticator, &fakeUserAdministration{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "invalid"})
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"authenticated":false`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 ||
		cookies[0].Path != "/api/v1" || !cookies[0].Secure {
		t.Fatalf("clear cookies=%+v", cookies)
	}
}

func TestAPIKeyCannotAuthorizeUserOrAdministratorRoutes(t *testing.T) {
	t.Parallel()
	authenticator := &fakeUserAuthenticator{}
	administration := &fakeUserAdministration{}
	handler := enabledAuthHandler(t, authenticator, administration)

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name: "current user", method: http.MethodPatch, path: "/api/v1/users/me",
			body: `{"display_name":"API key is not a user"}`,
		},
		{
			name: "administrator", method: http.MethodGet, path: "/api/v1/admin/users",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				test.method, test.path, strings.NewReader(test.body),
			)
			request.Header.Set("X-API-Key", "ev_valid-looking-but-not-authority")
			if test.method != http.MethodGet {
				request.Header.Set("Origin", testAuthOrigin)
				request.Header.Set("X-CSRF-Token", "not-session-bound")
			}
			handler.ServeHTTP(recorder, request)
			assertAuthError(
				t, recorder, http.StatusUnauthorized, "authentication_required",
			)
		})
	}
	if authenticator.authCalls != 0 || administration.pageLimit != 0 ||
		administration.displayName != nil {
		t.Fatalf(
			"API key crossed user boundary: auth=%d page_limit=%d display=%v",
			authenticator.authCalls, administration.pageLimit,
			administration.displayName,
		)
	}
}

func TestAuthenticatedWritesRequireCSRFAndPreserveNullableProfile(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 3, 4, 5, 0, time.UTC)
	user := authTestUser(now, userauth.RoleUser)
	authenticator := &fakeUserAuthenticator{authentication: SessionAuthentication{
		Session:   userauth.Session{ID: testChallengeID, User: user, ExpiresAt: now.Add(time.Hour)},
		CSRFToken: "csrf",
		validate: func(value string) error {
			if value == "csrf" {
				return nil
			}
			return userauth.ErrCSRFInvalid
		},
	}}
	administration := &fakeUserAdministration{user: user}
	handler := enabledAuthHandler(t, authenticator, administration)

	for _, test := range []struct {
		name   string
		csrf   []string
		status int
		code   string
	}{
		{"missing", nil, http.StatusForbidden, "csrf_invalid"},
		{"duplicate", []string{"csrf", "csrf"}, http.StatusForbidden, "csrf_invalid"},
		{"wrong", []string{"wrong"}, http.StatusForbidden, "csrf_invalid"},
		{"valid", []string{"csrf"}, http.StatusOK, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPatch, "/api/v1/users/me",
				strings.NewReader(`{"display_name":null}`),
			)
			request.Header.Set("Origin", testAuthOrigin)
			for _, token := range test.csrf {
				request.Header.Add("X-CSRF-Token", token)
			}
			request.AddCookie(&http.Cookie{
				Name: userauth.SessionCookieName, Value: "session-token",
			})
			handler.ServeHTTP(recorder, request)
			if test.code != "" {
				assertAuthError(t, recorder, test.status, test.code)
				return
			}
			if recorder.Code != http.StatusOK || administration.displayName != nil {
				t.Fatalf("status=%d display_name=%v body=%s", recorder.Code, administration.displayName, recorder.Body.String())
			}
		})
	}
}

func TestAdminUserBoundaryAndCursor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 4, 5, 6, 0, time.UTC)
	admin := authTestUser(now, userauth.RoleAdmin)
	second := authTestUser(now.Add(-time.Minute), userauth.RoleUser)
	second.ID = "91dd854b-455e-4aba-94c1-b034166a3f8d"
	authenticator := &fakeUserAuthenticator{authentication: SessionAuthentication{
		Session:  userauth.Session{User: admin, ExpiresAt: now.Add(time.Hour)},
		validate: func(string) error { return nil },
	}}
	administration := &fakeUserAdministration{users: []userauth.User{admin, second}}
	handler := enabledAuthHandler(t, authenticator, administration)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?limit=1", nil)
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session-token"})
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || administration.pageLimit != 2 {
		t.Fatalf("status=%d limit=%d body=%s", recorder.Code, administration.pageLimit, recorder.Body.String())
	}
	var page gen.UserListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Meta.NextCursor == nil {
		t.Fatalf("page=%+v", page)
	}
	administration.users = nil
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodGet, "/api/v1/admin/users?limit=1&cursor="+*page.Meta.NextCursor, nil,
	)
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session-token"})
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || administration.pageAfter == nil ||
		administration.pageAfter.ID != admin.ID ||
		!administration.pageAfter.CreatedAt.Equal(admin.CreatedAt) {
		t.Fatalf("status=%d after=%+v body=%s", recorder.Code, administration.pageAfter, recorder.Body.String())
	}

	nonAdmin := admin
	nonAdmin.Role = userauth.RoleUser
	authenticator.authentication.Session.User = nonAdmin
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	request.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session-token"})
	handler.ServeHTTP(recorder, request)
	assertAuthError(t, recorder, http.StatusForbidden, "admin_required")
}

func TestChallengeErrorsHaveStableCodes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", userauth.ErrChallengeInvalid, http.StatusBadRequest, "challenge_invalid"},
		{"expired", userauth.ErrChallengeExpired, http.StatusGone, "challenge_expired"},
		{"consumed", userauth.ErrChallengeConsumed, http.StatusConflict, "challenge_consumed"},
		{"signature", userauth.ErrSignatureInvalid, http.StatusUnauthorized, "signature_invalid"},
		{"disabled", userauth.ErrUserDisabled, http.StatusForbidden, "user_disabled"},
		{"unavailable", errors.New("database secret should not escape"), http.StatusServiceUnavailable, "user_auth_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticator := &fakeUserAuthenticator{verifyErr: test.err}
			handler := enabledAuthHandler(t, authenticator, &fakeUserAdministration{})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost, "/api/v1/auth/verify",
				strings.NewReader(`{"challenge_id":"`+testChallengeID+`","signature":"0x`+strings.Repeat("11", 65)+`"}`),
			)
			request.Header.Set("Origin", testAuthOrigin)
			handler.ServeHTTP(recorder, request)
			assertAuthError(t, recorder, test.status, test.code)
			if strings.Contains(recorder.Body.String(), "database secret") {
				t.Fatalf("nested error escaped: %s", recorder.Body.String())
			}
		})
	}
}

func enabledAuthHandler(
	t *testing.T,
	authenticator UserAuthenticator,
	administration UserAdministration,
) *Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	cfg.Server.PublicURL = testAuthOrigin
	cfg.Features.UserAuth = true
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		UserAuth: authenticator, UserAdministration: administration,
		BillingReader: &fakeBillingReader{},
		RequestID:     func() string { return "auth-request" },
		Now: func() time.Time {
			return time.Date(2026, 7, 26, 5, 6, 7, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func authTestUser(now time.Time, role userauth.Role) userauth.User {
	return userauth.User{
		ID: testUserID, ChainID: 11155111, Address: testAddress,
		Role: role, Status: userauth.StatusActive,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		LastLoginAt: &now,
	}
}

func assertAuthError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var response gen.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != code {
		t.Fatalf("error=%+v want code=%q", response.Error, code)
	}
}
