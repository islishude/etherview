package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/turnstile"
	"github.com/islishude/etherview/internal/userauth"
)

type challengeValidator func(context.Context, string, string) error

func (f challengeValidator) Verify(ctx context.Context, token, action string) error {
	return f(ctx, token, action)
}

func TestTurnstileBeforeProtectedWork(t *testing.T) {
	for _, path := range []string{"/verification-jobs/local", "/verification-jobs/sourcify-import", "/verification-jobs/sourcify", "/verification-jobs/sourcify-from-etherscan", "/users/me/exports/address-activity"} {
		for _, tc := range []struct {
			name, token   string
			err           error
			status, calls int
		}{
			{"missing", "", nil, 403, 0},
			{"duplicate", "token", nil, 403, 0},
			{"oversized", strings.Repeat("x", 2049), nil, 403, 0},
			{"invalid", "token", turnstile.ErrInvalid, 403, 1},
			{"upstream", "token", errors.New("https://upstream.example/private?secret=secret-value"), 503, 1},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				h, _, service := browserVerificationHandler(t)
				h.cfg.Turnstile.VerificationEnabled = true
				h.cfg.Turnstile.CSVExportEnabled = true
				watch := &fakeWatchlist{}
				h.watchlist = watch
				calls := 0
				h.turnstile = challengeValidator(func(_ context.Context, token, action string) error {
					calls++
					want := "verification"
					if strings.Contains(path, "exports") {
						want = "csv_export"
					}
					if action != want || token != tc.token {
						t.Fatal("wrong challenge")
					}
					return tc.err
				})
				req := httptest.NewRequest("POST", "/api/v1"+path, unreadVerificationBody{t})
				req.Header.Set("Origin", testAuthOrigin)
				req.Header.Set("X-CSRF-Token", "csrf")
				req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
				if tc.token != "" {
					req.Header.Set("X-Turnstile-Token", tc.token)
					if tc.name == "duplicate" {
						req.Header.Add("X-Turnstile-Token", tc.token)
					}
				}
				out := httptest.NewRecorder()
				h.ServeHTTP(out, req)
				if out.Code != tc.status || calls != tc.calls || service.calls != 0 || len(watch.users) != 0 {
					t.Fatalf("status=%d calls=%d body=%s", out.Code, calls, out.Body.String())
				}
				if strings.Contains(out.Body.String(), "secret-value") || out.Header().Get("Content-Disposition") != "" {
					t.Fatal("sensitive/CSV failure")
				}
			})
		}
	}
}

func TestTurnstileSwitchesAndAcceptedExport(t *testing.T) {
	for _, verify := range []bool{false, true} {
		for _, csv := range []bool{false, true} {
			h, _, _ := browserVerificationHandler(t)
			h.cfg.Turnstile.VerificationEnabled = verify
			h.cfg.Turnstile.CSVExportEnabled = csv
			watch := &fakeWatchlist{}
			h.watchlist = watch
			calls := 0
			h.turnstile = challengeValidator(func(_ context.Context, token, action string) error {
				calls++
				if action != "csv_export" || token != "token" {
					t.Fatal("incorrect validation")
				}
				return nil
			})
			req := httptest.NewRequest("POST", "/api/v1/users/me/exports/address-activity", strings.NewReader(`{"address":"`+testAddress+`","kind":"transaction","direction":"both","from":"2026-09-01T00:00:00Z","to":"2026-09-02T00:00:00Z"}`))
			req.Header.Set("Origin", testAuthOrigin)
			req.Header.Set("X-CSRF-Token", "csrf")
			req.Header.Set("X-Turnstile-Token", "token")
			req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			want := 0
			if csv {
				want = 1
			}
			if out.Code != 200 || calls != want || len(watch.users) != 1 {
				t.Fatalf("status=%d calls=%d %s", out.Code, calls, out.Body.String())
			}
			// Watch reads and job reads never consume a challenge.
			for _, path := range []string{"/users/me/watchlist", "/verification-jobs/123e4567-e89b-42d3-a456-426614174000"} {
				req = httptest.NewRequest("GET", "/api/v1"+path, nil)
				req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
				out = httptest.NewRecorder()
				h.ServeHTTP(out, req)
				if calls != want {
					t.Fatal("read consumed token")
				}
			}
		}
	}
}

func TestAuthenticationPrecedesTurnstile(t *testing.T) {
	h, _, _ := browserVerificationHandler(t)
	h.cfg.Turnstile.VerificationEnabled = true
	h.turnstile = challengeValidator(func(context.Context, string, string) error { t.Fatal("authentication bypass"); return nil })
	for _, cookie := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/api/v1/verification-jobs/local", unreadVerificationBody{t})
		r.Header.Set("Origin", testAuthOrigin)
		r.Header.Set("X-Turnstile-Token", "token")
		if cookie {
			r.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}

func TestTurnstilePublicConfigRedaction(t *testing.T) {
	h, _, _ := browserVerificationHandler(t)
	h.cfg.Turnstile.VerificationEnabled = true
	h.cfg.Turnstile.SiteKey = "public-widget"
	h.cfg.Turnstile.SecretKey = "private-widget"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/config", nil))
	if !strings.Contains(w.Body.String(), "public-widget") || strings.Contains(w.Body.String(), "private-widget") {
		t.Fatal(w.Body.String())
	}
}

func TestTurnstileAcceptedBrowserSubmission(t *testing.T) {
	h, _, service := browserVerificationHandler(t)
	h.cfg.Turnstile.VerificationEnabled = true
	calls := 0
	h.turnstile = challengeValidator(func(_ context.Context, token, action string) error {
		calls++
		if token != "valid-token" || action != "verification" {
			t.Fatal("incorrect challenge")
		}
		return nil
	})
	body := `{"address":"` + testAddress + `","language":"solidity","compiler_version":"0.8.30","input_kind":"standard_json","input":{"sources":{"A.sol":{"content":"contract A {}"}}}}`
	req := httptest.NewRequest("POST", "/api/v1/verification-jobs/local", strings.NewReader(body))
	req.Header.Set("Origin", testAuthOrigin)
	req.Header.Set("X-CSRF-Token", "csrf")
	req.Header.Set("X-Turnstile-Token", "valid-token")
	req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 202 || calls != 1 || service.calls != 1 {
		t.Fatalf("status=%d calls=%d jobs=%d body=%s", out.Code, calls, service.calls, out.Body.String())
	}
}
