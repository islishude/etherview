package httpapi

import (
	"context"
	"errors"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/userauth"
	"github.com/islishude/etherview/internal/watchlist"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeWatchlist struct {
	users     []string
	exportErr error
}

func (f *fakeWatchlist) List(_ context.Context, user string) ([]gen.AddressWatch, error) {
	f.users = append(f.users, user)
	return []gen.AddressWatch{}, nil
}
func (f *fakeWatchlist) Save(_ context.Context, user, _ string, _ gen.WatchInput) (gen.AddressWatch, error) {
	f.users = append(f.users, user)
	return gen.AddressWatch{}, nil
}
func (f *fakeWatchlist) Delete(_ context.Context, user, _ string) error {
	f.users = append(f.users, user)
	return nil
}
func (f *fakeWatchlist) PushSubscriptions(_ context.Context, user, session string) (gen.PushSubscriptions, error) {
	f.users = append(f.users, user)
	return gen.PushSubscriptions{Items: []gen.BrowserPushSubscription{}}, nil
}
func (f *fakeWatchlist) SavePushSubscription(_ context.Context, user, session string, input gen.PushSubscriptionInput) (gen.BrowserPushSubscription, error) {
	f.users = append(f.users, user)
	return gen.BrowserPushSubscription{}, nil
}
func (f *fakeWatchlist) DeletePushSubscription(_ context.Context, user, session, id string) error {
	f.users = append(f.users, user)
	return nil
}
func (f *fakeWatchlist) Export(_ context.Context, user string, _ gen.AddressExportRequest) (watchlist.Export, error) {
	f.users = append(f.users, user)
	return watchlist.Export{Bytes: []byte("block_number\r\n1\r\n"), BlockNumber: "1", BlockHash: "0x" + strings.Repeat("11", 32)}, f.exportErr
}
func TestWatchlistSessionCSRFAndCSVBoundary(t *testing.T) {
	fake := &fakeWatchlist{}
	authentication := &fakeUserAuthenticator{authentication: SessionAuthentication{Session: userauth.Session{User: authTestUser(time.Now(), userauth.RoleUser)}, validate: func(s string) error {
		if s == "csrf" {
			return nil
		}
		return userauth.ErrCSRFInvalid
	}}}
	handler := enabledAuthHandler(t, authentication, &fakeUserAdministration{})
	handler.watchlist = fake
	for _, test := range []struct {
		name, method, path, body, csrf string
		cookie                         bool
		want                           int
	}{
		{"anonymous", "GET", "/users/me/watchlist", "", "", false, 401},
		{"authenticated", "GET", "/users/me/watchlist", "", "", true, 200},
		{"missing csrf", "POST", "/users/me/push-subscriptions", `{"endpoint":"https://push.example/a","p256dh":"key","auth":"key","language":"en"}`, "", true, 403},
		{"subscribe", "POST", "/users/me/push-subscriptions", `{"endpoint":"https://push.example/a","p256dh":"key","auth":"key","language":"en"}`, "csrf", true, 200},
		{"export", "POST", "/users/me/exports/address-activity", `{"address":"` + testAddress + `","kind":"transaction","direction":"both","from":"2026-09-01T00:00:00Z","to":"2026-09-02T00:00:00Z"}`, "csrf", true, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/api/v1"+test.path, strings.NewReader(test.body))
			req.Header.Set("Origin", testAuthOrigin)
			req.Header.Set("X-CSRF-Token", test.csrf)
			req.Header.Set("Content-Type", "application/json")
			if test.cookie {
				req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: strings.Repeat("t", 43)})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private response is cacheable")
			}
			if test.name == "export" && !strings.HasPrefix(response.Header().Get("Content-Type"), "text/csv") {
				t.Fatal("not csv")
			}
			if test.name == "export" {
				mediaType, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
				want := strings.ToLower(testAddress) + "-2026-09-01T00-00-00Z-2026-09-02T00-00-00Z-transaction.csv"
				if err != nil || mediaType != "attachment" || params["filename"] != want {
					t.Fatalf("incorrect CSV filename: %q", response.Header().Get("Content-Disposition"))
				}
			}
		})
	}
	for _, u := range fake.users {
		if u != testUserID {
			t.Fatalf("wrong owner %q", u)
		}
	}
	for _, e := range []error{context.DeadlineExceeded, watchlist.ErrExportLimit, watchlist.ErrUnavailable, errors.New("secret database URL")} {
		fake.exportErr = e
		req := httptest.NewRequest("POST", "/api/v1/users/me/exports/address-activity", strings.NewReader(`{"address":"`+testAddress+`","kind":"transaction","direction":"both","from":"2026-09-01T00:00:00Z","to":"2026-09-02T00:00:00Z"}`))
		req.Header.Set("Origin", testAuthOrigin)
		req.Header.Set("X-CSRF-Token", "csrf")
		req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: strings.Repeat("t", 43)})
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code == 200 || strings.Contains(out.Body.String(), "secret") || out.Header().Get("Content-Disposition") != "" {
			t.Fatalf("partial/sensitive export %d %s", out.Code, out.Body.String())
		}
	}
}
