package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type captureLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (limiter *captureLimiter) Allow(_ context.Context, key string, _ Limit) (bool, time.Duration) {
	limiter.mu.Lock()
	limiter.keys = append(limiter.keys, key)
	limiter.mu.Unlock()
	return true, 0
}

func (limiter *captureLimiter) lastKey() string {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.keys) == 0 {
		return ""
	}
	return limiter.keys[len(limiter.keys)-1]
}

func TestTrustedProxySetRequiresCanonicalAddressesAndPrefixes(t *testing.T) {
	t.Parallel()
	if _, err := NewTrustedProxySet([]string{
		"192.0.2.10",
		"10.0.0.0/8",
		"2001:db8::1",
		"2001:db8::/32",
	}); err != nil {
		t.Fatalf("canonical trusted proxies failed: %v", err)
	}
	for _, value := range []string{
		"",
		" proxy.example",
		"proxy.example",
		"192.0.2.1:443",
		"192.0.2.1/24",
		"2001:0DB8::1",
		"fe80::1%eth0",
		"::ffff:192.0.2.1",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := NewTrustedProxySet([]string{value}); err == nil {
				t.Fatalf("non-canonical trusted proxy %q passed", value)
			}
		})
	}
}

func TestAnonymousIdentityTrustsOnlyBoundedValidForwardedChains(t *testing.T) {
	t.Parallel()
	trusted, err := NewTrustedProxySet([]string{"10.0.0.0/8", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	tooMany := strings.TrimSuffix(strings.Repeat("192.0.2.1,", maxForwardedForHops+1), ",")
	oversized := strings.Repeat("1", maxForwardedForBytes+1)
	tests := []struct {
		name       string
		remote     string
		forwarded  []string
		wantClient string
	}{
		{
			name: "untrusted direct peer ignores spoofed header", remote: "203.0.113.9:443",
			forwarded: []string{"198.51.100.7"}, wantClient: "203.0.113.9",
		},
		{
			name: "trusted edge accepts client", remote: "10.0.0.2:443",
			forwarded: []string{"198.51.100.7"}, wantClient: "198.51.100.7",
		},
		{
			name: "walks trusted chain from right", remote: "10.0.0.2:443",
			forwarded: []string{"203.0.113.5, 198.51.100.9, 10.0.0.1"}, wantClient: "198.51.100.9",
		},
		{
			name: "multiple header lines preserve chain", remote: "10.0.0.2:443",
			forwarded: []string{"198.51.100.7", "10.0.0.1"}, wantClient: "198.51.100.7",
		},
		{
			name: "all trusted uses furthest hop", remote: "10.0.0.2:443",
			forwarded: []string{"10.0.0.9, 10.0.0.1"}, wantClient: "10.0.0.9",
		},
		{
			name: "malformed token rejects entire chain", remote: "10.0.0.2:443",
			forwarded: []string{"198.51.100.7, invalid, 10.0.0.1"}, wantClient: "10.0.0.2",
		},
		{
			name: "forwarded port is invalid", remote: "10.0.0.2:443",
			forwarded: []string{"198.51.100.7:8443"}, wantClient: "10.0.0.2",
		},
		{
			name: "too many hops rejected", remote: "10.0.0.2:443",
			forwarded: []string{tooMany}, wantClient: "10.0.0.2",
		},
		{
			name: "oversized header rejected", remote: "10.0.0.2:443",
			forwarded: []string{oversized}, wantClient: "10.0.0.2",
		},
		{
			name: "IPv6 chain is canonicalized", remote: "[2001:db8::2]:443",
			forwarded: []string{"2001:db9::7, 2001:db8::1"}, wantClient: "2001:db9::7",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limiter := &captureLimiter{}
			handler := RateMiddleware{
				Limiter: limiter, Anonymous: Limit{Rate: 1, Burst: 1}, TrustedProxies: trusted,
			}.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			request.RemoteAddr = test.remote
			for _, value := range test.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if got := limiter.lastKey(); got != "anonymous:"+test.wantClient {
				t.Fatalf("limiter key=%q want anonymous:%s", got, test.wantClient)
			}
		})
	}
}

func TestAuthenticatedRateIdentityIgnoresPeerAndForwardedHeaders(t *testing.T) {
	t.Parallel()
	trusted, err := NewTrustedProxySet([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	limiter := &captureLimiter{}
	handler := RateMiddleware{
		Limiter: limiter, Anonymous: Limit{Rate: 1, Burst: 1}, TrustedProxies: trusted,
	}.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	request.RemoteAddr = "10.0.0.2:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	request = request.WithContext(context.WithValue(request.Context(), identityKey{}, Identity{
		Authenticated: true,
		Prefix:        "stable-prefix",
		Rate:          7,
		Burst:         9,
	}))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got := limiter.lastKey(); got != "key:stable-prefix" {
		t.Fatalf("authenticated limiter key=%q", got)
	}
}

func TestMemoryLimiterBoundsAndExpiresAnonymousBucketsWithoutEvictingAPIKeys(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	limiter := NewMemoryLimiter(func() time.Time { return now })
	limiter.maxAnonymousBuckets = 2
	limiter.anonymousBucketTTL = time.Minute
	limit := Limit{Rate: 1, Burst: 1}

	if allowed, _ := limiter.Allow(context.Background(), "key:authenticated", limit); !allowed {
		t.Fatal("authenticated key first request was denied")
	}
	if allowed, _ := limiter.Allow(context.Background(), "key:authenticated", limit); allowed {
		t.Fatal("authenticated key did not retain its exhausted bucket")
	}
	for _, key := range []string{"anonymous:192.0.2.1", "anonymous:192.0.2.2", "anonymous:192.0.2.3"} {
		if allowed, _ := limiter.Allow(context.Background(), key, limit); !allowed {
			t.Fatalf("%s first request was denied", key)
		}
	}
	if limiter.anonymousBuckets != 2 || len(limiter.buckets) != 3 {
		t.Fatalf("bucket counts anonymous=%d total=%d", limiter.anonymousBuckets, len(limiter.buckets))
	}
	if allowed, _ := limiter.Allow(context.Background(), "key:authenticated", limit); allowed {
		t.Fatal("anonymous churn reset the authenticated key bucket")
	}

	now = now.Add(2 * time.Minute)
	if allowed, _ := limiter.Allow(context.Background(), "anonymous:192.0.2.4", limit); !allowed {
		t.Fatal("new anonymous request was denied after expiry")
	}
	if limiter.anonymousBuckets != 1 {
		t.Fatalf("expired anonymous buckets retained: %d", limiter.anonymousBuckets)
	}
	if _, exists := limiter.buckets["key:authenticated"]; !exists {
		t.Fatal("anonymous expiry removed authenticated key state")
	}
}

func TestMemoryLimiterConcurrentAnonymousCardinalityRemainsBounded(t *testing.T) {
	t.Parallel()
	limiter := NewMemoryLimiter(func() time.Time { return time.Unix(100, 0) })
	limiter.maxAnonymousBuckets = 64
	limit := Limit{Rate: 1, Burst: 1}
	var wait sync.WaitGroup
	for index := range 1_000 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			limiter.Allow(context.Background(), "anonymous:client-"+time.Duration(index).String(), limit)
		}()
	}
	wait.Wait()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.anonymousBuckets > limiter.maxAnonymousBuckets ||
		limiter.anonymousOrder.Len() != limiter.anonymousBuckets {
		t.Fatalf(
			"anonymous cardinality limit=%d buckets=%d order=%d",
			limiter.maxAnonymousBuckets,
			limiter.anonymousBuckets,
			limiter.anonymousOrder.Len(),
		)
	}
}
