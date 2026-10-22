package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type ingressRepository struct {
	*MemoryRepository
	reads int
}

func (r *ingressRepository) ByPrefix(ctx context.Context, prefix string) (APIKey, error) {
	r.reads++
	return r.MemoryRepository.ByPrefix(ctx, prefix)
}

func TestCredentialIngressPrecedesAuthenticationAndPreservesKeyQuota(t *testing.T) {
	for _, valid := range []bool{false, true} {
		repo := &ingressRepository{MemoryRepository: NewMemoryRepository()}
		manager := Manager{Repository: repo, Pepper: []byte(strings.Repeat("p", 32))}
		key, err := manager.Create(t.Context(), "ingress", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		token := key.Token
		if !valid {
			replacement := byte('A')
			if token[15] == replacement {
				replacement = 'B'
			}
			token = token[:15] + string(replacement) + token[16:]
		} // preserve valid token syntax, change its digest
		now := time.Now()
		rates := RateMiddleware{Limiter: NewMemoryLimiter(func() time.Time { return now }), Ingress: Limit{Rate: 2, Burst: 2}, Anonymous: Limit{Rate: 1, Burst: 1}}
		proxies, err := NewTrustedProxySet([]string{"10.0.0.0/8"})
		if err != nil {
			t.Fatal(err)
		}
		rates.TrustedProxies = proxies
		served := 0
		h := rates.WrapWithAuthentication(manager, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served++; w.WriteHeader(204) }))
		for i := range 100 {
			r := httptest.NewRequest("GET", "/api/v1/blocks", nil)
			r.RemoteAddr = "10.0.0.1:4321"
			r.Header.Set("X-Forwarded-For", "198.51.100.1")
			r.Header.Set("X-API-Key", token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 429
			if i < 2 && !valid {
				want = 401
			}
			if i == 0 && valid {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("valid=%t request=%d got=%d want=%d body=%s", valid, i, w.Code, want, w.Body.String())
			}
			if want == 429 && w.Header().Get("Retry-After") == "" {
				t.Fatal("missing retry")
			}
		}
		wantServed := 0
		if valid {
			wantServed = 1
		}
		if repo.reads != 2 || served != wantServed {
			t.Fatalf("reads=%d served=%d", repo.reads, served)
		}
		// A different client behind the same trusted proxy has independent ingress.
		r := httptest.NewRequest("GET", "/api/v1/blocks", nil)
		r.RemoteAddr = "10.0.0.1:4321"
		r.Header.Set("X-Forwarded-For", "198.51.100.2")
		r.Header.Set("X-API-Key", token)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if repo.reads != 3 {
			t.Fatal("proxy collapsed clients")
		}
	}
}

type unreadCredentialBody struct{ t *testing.T }

func (b unreadCredentialBody) Read([]byte) (int, error) {
	b.t.Fatal("credential body read before admission")
	return 0, nil
}
func (unreadCredentialBody) Close() error { return nil }
func TestIngressRejectsBeforeCompatibilityBodyParsing(t *testing.T) {
	now := time.Now()
	rates := RateMiddleware{Limiter: NewMemoryLimiter(func() time.Time { return now }), Ingress: Limit{Rate: 1, Burst: 1}, Anonymous: Limit{Rate: 1, Burst: 1}}
	h := rates.WrapWithAuthentication(Manager{}, http.NotFoundHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/status", nil))
	r := httptest.NewRequest("POST", "/v2/api", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = unreadCredentialBody{t: t}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
}
