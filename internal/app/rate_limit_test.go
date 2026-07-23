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
	key string
}

func (limiter *appCaptureLimiter) Allow(_ context.Context, key string, _ auth.Limit) (bool, time.Duration) {
	limiter.key = key
	return true, 0
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
	if response.Code != http.StatusNoContent || limiter.key != "anonymous:198.51.100.7" {
		t.Fatalf("status=%d limiter key=%q body=%s", response.Code, limiter.key, response.Body.String())
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
