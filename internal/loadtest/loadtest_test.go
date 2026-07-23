package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPercentileUsesNearestRank(t *testing.T) {
	t.Parallel()
	samples := []time.Duration{
		time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond,
		4 * time.Millisecond, 100 * time.Millisecond,
	}
	if got := percentile(samples, 0.50); got != 3*time.Millisecond {
		t.Fatalf("p50 = %s", got)
	}
	if got := percentile(samples, 0.95); got != 100*time.Millisecond {
		t.Fatalf("p95 = %s", got)
	}
}

func TestValidateRejectsCredentialedOriginsAndCrossOriginPaths(t *testing.T) {
	t.Parallel()
	base := Config{
		BaseURL: "https://example.test", Paths: []string{"/api/v1/status"},
		Rate: 1, Duration: time.Second, Concurrency: 1,
		RequestTimeout: time.Second, MaximumP95: time.Second,
		MaximumErrorRate: 0.1, MinimumThroughputRatio: 0.5,
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "credentials", mutate: func(cfg *Config) { cfg.BaseURL = "https://user:secret@example.test" }},
		{name: "protocol relative", mutate: func(cfg *Config) { cfg.Paths = []string{"//other.test/path"} }},
		{name: "absolute path", mutate: func(cfg *Config) { cfg.Paths = []string{"https://other.test/path"} }},
		{name: "fragment", mutate: func(cfg *Config) { cfg.Paths = []string{"/api/v1/status#secret"} }},
		{name: "API key query", mutate: func(cfg *Config) {
			cfg.Paths = []string{"/v2/api?module=account&%61piKey=top-secret"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			test.mutate(&cfg)
			if _, err := validate(&cfg); err == nil {
				t.Fatal("invalid load target was accepted")
			}
		})
	}
}

func TestRunReportsBoundedPublicLoadAndLag(t *testing.T) {
	t.Parallel()
	var statusRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "read-key" {
			t.Error("load API key header is missing")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status" {
			statusRequests.Add(1)
			_, _ = w.Write([]byte(`{"data":{"lag":"1","core_ready":true,"backfill_complete":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{
		BaseURL: server.URL, Paths: []string{"/api/v1/blocks?limit=1"},
		Rate: 20, Duration: 250 * time.Millisecond, Concurrency: 2,
		RequestTimeout: time.Second, MaximumP95: time.Second,
		MaximumErrorRate: 0, MinimumThroughputRatio: 0.50, MaximumLag: 2,
		APIKey: "read-key", Profile: "unit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 5 || report.Errors != 0 || report.CoreLag != 1 ||
		!report.CoreReady || !report.BackfillComplete {
		t.Fatalf("report = %+v", report)
	}
	if statusRequests.Load() != 1 {
		t.Fatalf("status requests = %d", statusRequests.Load())
	}
	for key := range report.StatusCounts {
		if strings.Contains(key, "read-key") {
			t.Fatal("API key leaked into report")
		}
	}
}

func TestRunContinuesPastTheResultBuffer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"data":{"lag":"0","core_ready":true,"backfill_complete":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{
		BaseURL: server.URL, Paths: []string{"/api/v1/blocks?limit=1"},
		Rate: 100, Duration: 200 * time.Millisecond, Concurrency: 1,
		RequestTimeout: time.Second, MaximumP95: time.Second,
		MaximumErrorRate: 0, MinimumThroughputRatio: 0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 20 {
		t.Fatalf("requests = %d", report.Requests)
	}
}

func TestRunDropsSaturatedAdmissionsAndRemainsWallClockBounded(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"lag":"0","core_ready":true,"backfill_complete":true}}`))
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	started := time.Now()
	report, err := Run(context.Background(), Config{
		BaseURL: server.URL, Paths: []string{"/slow"},
		Rate: 100, Duration: 200 * time.Millisecond, Concurrency: 1,
		RequestTimeout: 75 * time.Millisecond, MaximumP95: time.Second,
		MaximumErrorRate: 0, MinimumThroughputRatio: 0.01,
	})
	if err == nil || report.Errors == 0 {
		t.Fatalf("saturated load report=%+v error=%v", report, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("saturated load ran for %s", elapsed)
	}
	if report.Requests != 20 {
		t.Fatalf("requests=%d, want scheduled 20", report.Requests)
	}
}

func TestRunDoesNotFollowRedirectsWithAPIKey(t *testing.T) {
	t.Parallel()
	var redirectedRequests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	defer receiver.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"lag":"0","core_ready":true,"backfill_complete":true}}`))
			return
		}
		http.Redirect(w, r, receiver.URL, http.StatusFound)
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{
		BaseURL: server.URL, Paths: []string{"/redirect"},
		Rate: 1, Duration: 100 * time.Millisecond, Concurrency: 1,
		RequestTimeout: time.Second, MaximumP95: time.Second,
		MaximumErrorRate: 1, MinimumThroughputRatio: 0.01,
		APIKey: "must-not-cross-origin",
	})
	if err == nil || !strings.Contains(err.Error(), "throughput") {
		t.Fatalf("redirect-only load error = %v", err)
	}
	if report.Errors != 1 || report.StatusCounts["302"] != 1 {
		t.Fatalf("redirect report = %+v", report)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatal("load client followed a redirect with the API key")
	}
}

func TestValidateRejectsProfilesBeyondMemoryBound(t *testing.T) {
	t.Parallel()
	cfg := Config{
		BaseURL: "https://example.test", Paths: []string{"/api/v1/status"},
		Rate: 100_000, Duration: 11 * time.Second, Concurrency: 1,
		RequestTimeout: time.Second, MaximumP95: time.Second,
		MaximumErrorRate: 0.1, MinimumThroughputRatio: 0.5,
	}
	if _, err := validate(&cfg); err == nil {
		t.Fatal("unbounded load profile was accepted")
	}
}

func TestEvaluateReportsEveryThresholdViolation(t *testing.T) {
	t.Parallel()
	err := evaluate(Config{
		Rate: 500, MaximumErrorRate: 0.001, MaximumP95: 500 * time.Millisecond,
		MinimumThroughputRatio: 0.95, MaximumLag: 2,
	}, Report{
		Requests: 100, Errors: 2, ErrorRate: 0.02,
		ThroughputRPS: 400, P95Milliseconds: 600, CoreLag: 3,
	})
	if err == nil {
		t.Fatal("threshold violations were accepted")
	}
	for _, fragment := range []string{
		"error rate", "p95", "throughput", "core lag", "core is not ready", "backfill is not complete",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not include %q", err, fragment)
		}
	}
}

func TestEvaluateUsesStrictP70LatencyAndErrorBoundaries(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Rate: 500, MaximumErrorRate: 0.001, MaximumP95: 500 * time.Millisecond,
		MinimumThroughputRatio: 0.95, MaximumLag: 2,
	}
	report := Report{
		Requests: 1000, Successes: 999, Errors: 1, ErrorRate: 0.001,
		ThroughputRPS: 500, P95Milliseconds: 500, CoreLag: 2,
		CoreReady: true, BackfillComplete: true,
	}
	err := evaluate(cfg, report)
	if err == nil || !strings.Contains(err.Error(), "error rate") || !strings.Contains(err.Error(), "p95") {
		t.Fatalf("strict threshold error = %v", err)
	}
	report.ErrorRate = 0
	report.Errors = 0
	report.Successes = report.Requests
	report.P95Milliseconds = 499.999
	if err := evaluate(cfg, report); err != nil {
		t.Fatalf("below-threshold report rejected: %v", err)
	}
}

func TestReadRuntimeStatusRejectsUnreadyAndMalformedResponses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "core not ready",
			payload: `{"data":{"lag":"0","core_ready":false,"backfill_complete":true}}`,
			want:    "core is not ready",
		},
		{
			name:    "backfill incomplete",
			payload: `{"data":{"lag":"0","core_ready":true,"backfill_complete":false}}`,
			want:    "backfill is not complete",
		},
		{
			name:    "null lag",
			payload: `{"data":{"lag":null,"core_ready":true,"backfill_complete":true}}`,
			want:    "status lag is invalid",
		},
		{
			name:    "numeric lag",
			payload: `{"data":{"lag":0,"core_ready":true,"backfill_complete":true}}`,
			want:    "status lag is invalid",
		},
		{
			name:    "missing readiness",
			payload: `{"data":{"lag":"0","core_ready":true}}`,
			want:    "status readiness fields are missing",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/status" {
					_, _ = w.Write([]byte(test.payload))
					return
				}
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			defer server.Close()

			report, err := Run(context.Background(), Config{
				BaseURL: server.URL, Paths: []string{"/api/v1/blocks?limit=1"},
				Rate: 1, Duration: 100 * time.Millisecond, Concurrency: 1,
				RequestTimeout: time.Second, MaximumP95: time.Second,
				MaximumErrorRate: 0, MinimumThroughputRatio: 0.01,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("report=%+v error=%v, want %q", report, err, test.want)
			}
		})
	}
}
