package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
)

type appCaptureLimiter struct {
	key   string
	calls int
	limit auth.Limit
}

func (limiter *appCaptureLimiter) Allow(_ context.Context, key string, limit auth.Limit) (bool, time.Duration) {
	limiter.key = key
	limiter.calls++
	limiter.limit = limit
	return true, 0
}

func TestProtectPublicAPIBillingUsesTheSameTrustedProxyAwareQuota(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.APIBilling = true
	cfg.Security.TrustedProxies = []string{"10.0.0.0/8"}
	limiter := &appCaptureLimiter{}
	handler, err := (&Backend{}).protectPublicAPI(
		nil, cfg, nil, limiter,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil)
	request.RemoteAddr = "10.0.0.2:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || limiter.calls != 1 ||
		limiter.key != "anonymous:198.51.100.7" ||
		limiter.limit.Rate != cfg.Security.AnonymousRate ||
		limiter.limit.Burst != cfg.Security.AnonymousBurst {
		t.Fatalf(
			"status=%d calls=%d key=%q limit=%#v",
			response.Code, limiter.calls, limiter.key, limiter.limit,
		)
	}
}

func TestProtectPublicAPIWiresTrustedProxyIdentity(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Security.TrustedProxies = []string{"10.0.0.0/8"}
	limiter := &appCaptureLimiter{}
	handler, err := (&Backend{}).protectPublicAPI(nil, cfg, nil, limiter, http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	request.RemoteAddr = "10.0.0.2:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || limiter.key != "anonymous:198.51.100.7" ||
		limiter.calls != 1 {
		t.Fatalf("status=%d limiter key=%q calls=%d body=%s", response.Code, limiter.key, limiter.calls, response.Body.String())
	}
}

func TestProtectPublicAPIFeatureOffUsesExactlyOriginalQuota(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	if cfg.Features.APIBilling || cfg.Features.X402Topups {
		t.Fatal("billing unexpectedly enabled by default")
	}
	limiter := &appCaptureLimiter{}
	handler, err := (&Backend{}).protectPublicAPI(
		nil, cfg, nil, limiter,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusNoContent || limiter.calls != 1 {
		t.Fatalf("status=%d limiter calls=%d", response.Code, limiter.calls)
	}
}

func TestProtectPublicAPIRejectsInvalidTrustedProxyWithoutServing(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Security.TrustedProxies = []string{"proxy.example"}
	handler, err := (&Backend{}).protectPublicAPI(nil, cfg, nil, &appCaptureLimiter{}, http.NotFoundHandler())
	if err == nil || handler != nil {
		t.Fatalf("handler=%v error=%v", handler, err)
	}
}
