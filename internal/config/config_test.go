package config

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNormalizeRoles(t *testing.T) {
	t.Parallel()
	got, err := NormalizeRoles([]string{"trace,api", "api"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "trace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	all, err := NormalizeRoles([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, allowedRoles) {
		t.Fatalf("all expanded to %v", all)
	}
	if _, err := NormalizeRoles([]string{"api", "unknown"}); err == nil {
		t.Fatal("expected unknown role error")
	}
}

func TestLoadEnvironmentAndSecretFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "database-url")
	if err := os.WriteFile(secretPath, []byte("postgres://example/db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This case exercises the file-only input and must not inherit an unrelated
	// inline value from the caller's environment.
	inlineDatabaseURL, inlineDatabaseURLSet := os.LookupEnv("ETHERVIEW_DATABASE_URL")
	if err := os.Unsetenv("ETHERVIEW_DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if inlineDatabaseURLSet {
			_ = os.Setenv("ETHERVIEW_DATABASE_URL", inlineDatabaseURL)
		} else {
			_ = os.Unsetenv("ETHERVIEW_DATABASE_URL")
		}
	})
	t.Setenv("ETHERVIEW_DATABASE_URL_FILE", secretPath)
	t.Setenv("ETHERVIEW_CHAIN_ID", "11155111")
	t.Setenv("ETHERVIEW_ROLES", "api,sync")
	t.Setenv("ETHERVIEW_RPC_URLS", "https://rpc.example, wss://ws.example")
	t.Setenv("ETHERVIEW_API_KEY_PEPPER", strings.Repeat("p", 32))
	t.Setenv("ETHERVIEW_BACKFILL_WORKERS", "8")
	t.Setenv("ETHERVIEW_BACKFILL_BATCH_BLOCKS", "128")
	t.Setenv("ETHERVIEW_MEMPOOL_POLL_INTERVAL", "1500ms")
	t.Setenv("ETHERVIEW_MEMPOOL_RETENTION", "15m")
	t.Setenv("ETHERVIEW_MEMPOOL_MAX_TRANSACTIONS", "1234")
	t.Setenv("ETHERVIEW_MEMPOOL_MAX_RESPONSE_BYTES", "8388608")
	t.Setenv("ETHERVIEW_MAINTENANCE_INTERVAL", "5m")
	t.Setenv("ETHERVIEW_MAINTENANCE_SEARCH_RETENTION_GENERATIONS", "2500")
	t.Setenv("ETHERVIEW_MAINTENANCE_ADAPTER_DELETE_BATCH", "55")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.URL != "postgres://example/db" || cfg.Chain.ID != 11155111 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.RPC.Endpoints) != 2 || cfg.RPC.Endpoints[1].URL != "wss://ws.example" {
		t.Fatalf("unexpected endpoints: %#v", cfg.RPC.Endpoints)
	}
	if cfg.Runtime.BackfillWorkers != 8 || cfg.Runtime.BackfillBatchBlocks != 128 ||
		len(cfg.Security.APIKeyPepper) != 32 {
		t.Fatalf("unexpected runtime/security override: %#v", cfg)
	}
	if cfg.Mempool.PollInterval.String() != "1.5s" || cfg.Mempool.Retention.String() != "15m0s" ||
		cfg.Mempool.MaxTransactions != 1234 || cfg.Mempool.MaxResponseBytes != 8<<20 {
		t.Fatalf("unexpected mempool override: %#v", cfg.Mempool)
	}
	if cfg.Maintenance.Interval != 5*time.Minute || cfg.Maintenance.SearchRetentionGenerations != 2500 ||
		cfg.Maintenance.AdapterDeleteBatch != 55 {
		t.Fatalf("unexpected maintenance override: %#v", cfg.Maintenance)
	}
}

func TestRPCEnvironmentSupportsPurposeAndRateStructuredSecret(t *testing.T) {
	t.Setenv("ETHERVIEW_RPC_URLS", `[
		{"name":"live","url":"https://live.example","purposes":["head"],"max_requests_per_second":25},
		{"name":"history","url":"https://history.example","purposes":["history","state"],"max_requests_per_second":100}
	]`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RPC.Endpoints) != 2 ||
		cfg.RPC.Endpoints[0].Name != "live" ||
		cfg.RPC.Endpoints[0].MaxRequests != 25 ||
		cfg.RPC.Endpoints[1].Name != "history" ||
		!slices.Equal(cfg.RPC.Endpoints[1].Purposes, []string{"history", "state"}) {
		t.Fatalf("structured RPC endpoints = %#v", cfg.RPC.Endpoints)
	}
}

func TestRPCEnvironmentRejectsEndpointPersistenceAndRateBounds(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    string
	}{
		{
			name: "endpoint name exceeds durable bound",
			payload: `[{"name":"` + strings.Repeat("n", 129) +
				`","url":"https://rpc.example","purposes":["mempool"]}]`,
			want: "between 1 and 128 trimmed bytes",
		},
		{
			name:    "endpoint name is not canonical",
			payload: `[{"name":" live ","url":"https://rpc.example","purposes":["head"]}]`,
			want:    "between 1 and 128 trimmed bytes",
		},
		{
			name: "request rate exceeds nanosecond cadence",
			payload: `[{"name":"live","url":"https://rpc.example","purposes":["head"],` +
				`"max_requests_per_second":1000000001}]`,
			want: "between 0 and 1000000000",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ETHERVIEW_RPC_URLS", test.payload)
			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}

	t.Setenv("ETHERVIEW_RPC_URLS",
		`[{"name":"live","url":"https://rpc.example","purposes":["head"],`+
			`"max_requests_per_second":1000000000}]`)
	if _, err := Load(""); err != nil {
		t.Fatalf("maximum request rate rejected: %v", err)
	}
}

func TestRPCEnvironmentRejectsMalformedStructuredSecretWithoutEchoingIt(t *testing.T) {
	for _, value := range []string{
		`[{"name":"rpc","url":"https://user:top-secret@example","purposes":["head"]}`,
		`[{"name":"rpc","url":"https://user:top-secret@example","purposes":["head"],"unknown":"top-secret"}]`,
		`[]`,
	} {
		t.Run("", func(t *testing.T) {
			t.Setenv("ETHERVIEW_RPC_URLS", value)
			_, err := Load("")
			if err == nil || strings.Contains(err.Error(), "top-secret") {
				t.Fatalf("malformed structured RPC error = %v", err)
			}
		})
	}
}

func TestRuntimeWorkerAndBackfillConfigurationIsStrictlyBounded(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Runtime.WorkerCount = maximumRuntimeWorkerCount
	cfg.Runtime.BackfillWorkers = maximumRuntimeBackfillWorkers
	cfg.Runtime.BackfillBatchBlocks = maximumRuntimeBackfillBatchBlocks
	if err := cfg.Validate(); err != nil {
		t.Fatalf("maximum runtime bounds rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		field  string
		mutate func(*Config)
	}{
		{name: "zero worker count", field: "runtime.worker_count", mutate: func(cfg *Config) {
			cfg.Runtime.WorkerCount = 0
		}},
		{name: "excessive worker count", field: "runtime.worker_count", mutate: func(cfg *Config) {
			cfg.Runtime.WorkerCount = maximumRuntimeWorkerCount + 1
		}},
		{name: "zero backfill workers", field: "runtime.backfill_workers", mutate: func(cfg *Config) {
			cfg.Runtime.BackfillWorkers = 0
		}},
		{name: "excessive backfill workers", field: "runtime.backfill_workers", mutate: func(cfg *Config) {
			cfg.Runtime.BackfillWorkers = maximumRuntimeBackfillWorkers + 1
		}},
		{name: "zero backfill batch", field: "runtime.backfill_batch_blocks", mutate: func(cfg *Config) {
			cfg.Runtime.BackfillBatchBlocks = 0
		}},
		{name: "excessive backfill batch", field: "runtime.backfill_batch_blocks", mutate: func(cfg *Config) {
			cfg.Runtime.BackfillBatchBlocks = maximumRuntimeBackfillBatchBlocks + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("invalid runtime config passed validation: %#v, error=%v", cfg.Runtime, err)
			}
		})
	}
}

func TestMempoolConfigurationIsStrictlyBounded(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Mempool.PollInterval = 0 },
		func(cfg *Config) { cfg.Mempool.Retention = cfg.Mempool.PollInterval },
		func(cfg *Config) { cfg.Mempool.Retention = 25 * time.Hour },
		func(cfg *Config) { cfg.Mempool.MaxTransactions = 200_001 },
		func(cfg *Config) { cfg.Mempool.MaxResponseBytes = 32<<20 + 1 },
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "mempool.") {
			t.Fatalf("invalid mempool config passed validation: %#v, error=%v", cfg.Mempool, err)
		}
	}
}

func TestMaintenanceConfigurationIsStrictlyBounded(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Maintenance.Interval = time.Second - 1 },
		func(cfg *Config) { cfg.Maintenance.Interval = 24*time.Hour + 1 },
		func(cfg *Config) { cfg.Maintenance.SearchRetentionGenerations = 999 },
		func(cfg *Config) { cfg.Maintenance.SearchRetentionGenerations = 10_000_001 },
		func(cfg *Config) { cfg.Maintenance.AdapterDeleteBatch = 0 },
		func(cfg *Config) { cfg.Maintenance.AdapterDeleteBatch = 10_001 },
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "maintenance.") {
			t.Fatalf("invalid maintenance config passed validation: %#v, error=%v", cfg.Maintenance, err)
		}
	}
}

func TestObservabilityConfigurationIsExplicitAndBounded(t *testing.T) {
	t.Parallel()
	if Default().Observability.OTLPTraceEndpoint != "" {
		t.Fatal("OTLP tracing must be disabled by default")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "empty environment", mutate: func(cfg *Config) { cfg.Observability.Environment = "" }, want: "environment"},
		{name: "sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = 1.01 }, want: "trace_sample_ratio"},
		{name: "nan sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = math.NaN() }, want: "trace_sample_ratio"},
		{name: "infinite sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = math.Inf(1) }, want: "trace_sample_ratio"},
		{name: "short export timeout", mutate: func(cfg *Config) { cfg.Observability.TraceExportTimeout = 99 * time.Millisecond }, want: "trace_export_timeout"},
		{name: "short refresh", mutate: func(cfg *Config) { cfg.Observability.MetricsRefreshInterval = time.Millisecond }, want: "metrics_refresh_interval"},
		{name: "credential endpoint", mutate: func(cfg *Config) { cfg.Observability.OTLPTraceEndpoint = "https://user:secret@otel.example:4318" }, want: "otlp_trace_endpoint"},
		{name: "endpoint query", mutate: func(cfg *Config) { cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318?key=secret" }, want: "otlp_trace_endpoint"},
		{name: "endpoint path", mutate: func(cfg *Config) { cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318/private" }, want: "otlp_trace_endpoint"},
		{name: "endpoint fragment", mutate: func(cfg *Config) { cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318#secret" }, want: "otlp_trace_endpoint"},
		{name: "implicit insecure HTTP", mutate: func(cfg *Config) { cfg.Observability.OTLPTraceEndpoint = "http://otel.example:4318" }, want: "otlp_trace_insecure"},
		{name: "insecure HTTPS", mutate: func(cfg *Config) {
			cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318"
			cfg.Observability.OTLPTraceInsecure = true
		}, want: "otlp_trace_insecure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "observability."+test.want) {
				t.Fatalf("invalid observability config passed: %#v error=%v", cfg.Observability, err)
			}
		})
	}
	for _, cfg := range []ObservabilityConfig{
		{
			Environment: "production", OTLPTraceEndpoint: "https://otel.example:4318",
			TraceSampleRatio: 0.25, TraceExportTimeout: time.Second,
			MetricsRefreshInterval: 10 * time.Second,
		},
		{
			Environment: "staging", OTLPTraceEndpoint: "http://otel.monitoring.svc:4318",
			OTLPTraceInsecure: true, TraceSampleRatio: 1, TraceExportTimeout: time.Second,
			MetricsRefreshInterval: 10 * time.Second,
		},
	} {
		root := Default()
		root.Observability = cfg
		if err := root.Validate(); err != nil {
			t.Fatalf("valid observability config failed: %v", err)
		}
	}
}

func TestObservabilityEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	cfg := Default()
	values := map[string]string{
		"ETHERVIEW_OBSERVABILITY_ENVIRONMENT": "staging",
		"ETHERVIEW_OTLP_TRACE_ENDPOINT":       "http://otel.monitoring.svc:4318",
		"ETHERVIEW_OTLP_TRACE_INSECURE":       "true",
		"ETHERVIEW_TRACE_SAMPLE_RATIO":        "0.5",
		"ETHERVIEW_TRACE_EXPORT_TIMEOUT":      "3s",
		"ETHERVIEW_METRICS_REFRESH_INTERVAL":  "20s",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	if err := applyEnvironment(&cfg, lookup, os.ReadFile); err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.Environment != "staging" || cfg.Observability.OTLPTraceEndpoint != "http://otel.monitoring.svc:4318" ||
		!cfg.Observability.OTLPTraceInsecure || cfg.Observability.TraceSampleRatio != 0.5 ||
		cfg.Observability.TraceExportTimeout != 3*time.Second || cfg.Observability.MetricsRefreshInterval != 20*time.Second {
		t.Fatalf("observability environment was not applied: %#v", cfg.Observability)
	}
}

func TestExternalAdapterConfigurationIsHTTPSAndBounded(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Features.Pricing = true },
		func(cfg *Config) { cfg.Adapters.PriceBaseURL = "http://price.example/v1" },
		func(cfg *Config) { cfg.Adapters.NameBaseURL = "https://user:secret@name.example/v1" },
		func(cfg *Config) { cfg.Adapters.FetchTimeout = 0 },
		func(cfg *Config) { cfg.Adapters.MaxResponseBytes = 8<<20 + 1 },
		func(cfg *Config) { cfg.Adapters.MaxRedirects = 6 },
		func(cfg *Config) { cfg.Adapters.PriceFreshness = 25 * time.Hour },
		func(cfg *Config) { cfg.Adapters.NameFreshness = 31 * 24 * time.Hour },
		func(cfg *Config) { cfg.Adapters.FailureTTL = 2 * time.Hour },
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "adapters.") {
			t.Fatalf("invalid adapter config passed: %#v error=%v", cfg.Adapters, err)
		}
	}
	cfg := Default()
	cfg.Features.Pricing = true
	cfg.Adapters.PriceBaseURL = "https://price.example/v1"
	cfg.Adapters.NameBaseURL = "https://name.example/v1"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !validS3Bucket("123") {
		t.Fatal("purely numeric, non-IP S3 bucket was rejected")
	}
}

func TestSourcifyConfigurationIsHTTPSBoundedAndExplicit(t *testing.T) {
	t.Parallel()
	if Default().Features.Sourcify {
		t.Fatal("Sourcify must be disabled by default")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "HTTP URL", mutate: func(cfg *Config) { cfg.Sourcify.BaseURL = "http://sourcify.example/server" }, want: "base_url"},
		{name: "credentials", mutate: func(cfg *Config) { cfg.Sourcify.BaseURL = "https://user:secret@sourcify.example/server" }, want: "base_url"},
		{name: "query", mutate: func(cfg *Config) { cfg.Sourcify.BaseURL = "https://sourcify.example/server?token=secret" }, want: "base_url"},
		{name: "fragment", mutate: func(cfg *Config) { cfg.Sourcify.BaseURL = "https://sourcify.example/server#fragment" }, want: "base_url"},
		{name: "escaped traversal", mutate: func(cfg *Config) { cfg.Sourcify.BaseURL = "https://sourcify.example/%2e%2e/server" }, want: "base_url"},
		{name: "short timeout", mutate: func(cfg *Config) { cfg.Sourcify.Timeout = 99 * time.Millisecond }, want: "timeout"},
		{name: "long timeout", mutate: func(cfg *Config) { cfg.Sourcify.Timeout = 2*time.Minute + 1 }, want: "timeout"},
		{name: "empty request bound", mutate: func(cfg *Config) { cfg.Sourcify.MaxRequestBytes = 0 }, want: "max_request_bytes"},
		{name: "large request bound", mutate: func(cfg *Config) { cfg.Sourcify.MaxRequestBytes = 64<<20 + 1 }, want: "max_request_bytes"},
		{name: "empty response bound", mutate: func(cfg *Config) { cfg.Sourcify.MaxResponseBytes = 0 }, want: "max_response_bytes"},
		{name: "large response bound", mutate: func(cfg *Config) { cfg.Sourcify.MaxResponseBytes = 64<<20 + 1 }, want: "max_response_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sourcify."+test.want) {
				t.Fatalf("invalid Sourcify config passed: %#v error=%v", cfg.Sourcify, err)
			}
		})
	}

	cfg := Default()
	cfg.Features.Sourcify = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "features.sourcify requires public verification") {
		t.Fatalf("unexpected Sourcify dependency error: %v", err)
	}
	cfg.Features.Verification = true
	cfg.Security.PublicVerification = true
	cfg.Security.CompilerSandbox = "container"
	cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid enabled Sourcify config failed: %v", err)
	}
}

func TestSourcifyEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	cfg := Default()
	values := map[string]string{
		"ETHERVIEW_FEATURE_SOURCIFY":            "true",
		"ETHERVIEW_SOURCIFY_BASE_URL":           "https://sourcify.example/v2",
		"ETHERVIEW_SOURCIFY_TIMEOUT":            "17s",
		"ETHERVIEW_SOURCIFY_MAX_REQUEST_BYTES":  "123456",
		"ETHERVIEW_SOURCIFY_MAX_RESPONSE_BYTES": "654321",
	}
	lookup := func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
	if err := applyEnvironment(&cfg, lookup, os.ReadFile); err != nil {
		t.Fatal(err)
	}
	if !cfg.Features.Sourcify || cfg.Sourcify.BaseURL != "https://sourcify.example/v2" ||
		cfg.Sourcify.Timeout != 17*time.Second || cfg.Sourcify.MaxRequestBytes != 123456 ||
		cfg.Sourcify.MaxResponseBytes != 654321 {
		t.Fatalf("Sourcify environment was not applied: feature=%v config=%#v", cfg.Features.Sourcify, cfg.Sourcify)
	}
}

func TestOptionalAcceleratorConfigurationIsStrictAndPostgresOnlyByDefault(t *testing.T) {
	t.Parallel()
	if err := Default().Validate(); err != nil {
		t.Fatalf("PostgreSQL-only defaults are invalid: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "namespace", mutate: func(cfg *Config) { cfg.Adapters.Namespace = "bad namespace" }},
		{name: "nats scheme", mutate: func(cfg *Config) { cfg.Adapters.NATSURL = "https://nats.example" }},
		{name: "redis scheme", mutate: func(cfg *Config) { cfg.Adapters.RedisURL = "http://redis.example" }},
		{name: "s3 credentials", mutate: func(cfg *Config) {
			cfg.Adapters.S3Endpoint = "https://s3.example"
			cfg.Adapters.S3Bucket = "cache"
			cfg.Adapters.S3AccessKey = "only-access"
		}},
		{name: "s3 endpoint userinfo", mutate: func(cfg *Config) {
			cfg.Adapters.S3Endpoint = "https://user:secret@s3.example"
			cfg.Adapters.S3Bucket = "cache"
		}},
		{name: "s3 bucket", mutate: func(cfg *Config) {
			cfg.Adapters.S3Endpoint = "https://s3.example"
			cfg.Adapters.S3Bucket = "Invalid_Bucket"
		}},
		{name: "s3 bucket leading dot", mutate: func(cfg *Config) {
			cfg.Adapters.S3Endpoint = "https://s3.example"
			cfg.Adapters.S3Bucket = ".cache"
		}},
		{name: "s3 bucket IPv4", mutate: func(cfg *Config) {
			cfg.Adapters.S3Endpoint = "https://s3.example"
			cfg.Adapters.S3Bucket = "192.0.2.1"
		}},
		{name: "operation timeout", mutate: func(cfg *Config) { cfg.Adapters.OperationTimeout = 0 }},
		{name: "cache ttl", mutate: func(cfg *Config) { cfg.Adapters.RedisCacheTTL = 0 }},
		{name: "blob limit", mutate: func(cfg *Config) { cfg.Adapters.S3MaxObjectBytes = 64<<20 + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "adapters.") {
				t.Fatalf("invalid accelerator config passed: %#v error=%v", cfg.Adapters, err)
			}
		})
	}
	cfg := Default()
	cfg.Adapters.NATSURL = "nats://user:secret@nats.example:4222"
	cfg.Adapters.RedisURL = "rediss://user:secret@redis.example:6379/0"
	cfg.Adapters.S3Endpoint = "https://s3.example"
	cfg.Adapters.S3Bucket = "etherview-cache"
	cfg.Adapters.S3AccessKey = "access"
	cfg.Adapters.S3SecretKey = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAcceleratorSecretsSupportFileEnvironment(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "s3-secret")
	if err := os.WriteFile(secretPath, []byte("top-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHERVIEW_S3_SECRET_KEY_FILE", secretPath)
	t.Setenv("ETHERVIEW_S3_ACCESS_KEY", "access")
	t.Setenv("ETHERVIEW_S3_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("ETHERVIEW_S3_BUCKET", "etherview-cache")
	t.Setenv("ETHERVIEW_S3_PATH_STYLE", "true")
	t.Setenv("ETHERVIEW_ADAPTER_OPERATION_TIMEOUT", "250ms")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters.S3SecretKey != "top-secret" || cfg.Adapters.S3AccessKey != "access" ||
		!cfg.Adapters.S3PathStyle || cfg.Adapters.OperationTimeout != 250*time.Millisecond {
		t.Fatalf("accelerator environment was not applied: %#v", cfg.Adapters)
	}
}

func TestValidateAggregatesErrorsAndDoesNotRequireGenesisDuringBootstrap(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Chain.ID = 0
	cfg.Chain.GenesisHash = "0x1234"
	cfg.Security.PublicVerification = true
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, fragment := range []string{"chain.id", "genesis_hash", "public verification"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q lacks %q", err, fragment)
		}
	}
}

func TestValueAndFileAreMutuallyExclusive(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_URL", "postgres://inline")
	t.Setenv("ETHERVIEW_DATABASE_URL_FILE", "/tmp/ignored")
	if _, err := Load(""); err == nil {
		t.Fatal("expected mutually-exclusive secret error")
	}
}

func TestValidateForRolesRequiresRuntimeDependencies(t *testing.T) {
	t.Parallel()
	cfg := Default()
	err := cfg.ValidateForRoles([]string{"all"})
	if err == nil || !strings.Contains(err.Error(), "database.url") || !strings.Contains(err.Error(), "rpc endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.RPC.Endpoints = []RPCEndpoint{{Name: "primary", URL: "http://localhost:8545", Purposes: []string{"all"}}}
	if err := cfg.ValidateForRoles([]string{"all"}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIOnlyRoleKeepsStateRPCOptional(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatal(err)
	}
	cfg.RPC.Endpoints = []RPCEndpoint{{Name: "state", URL: "https://rpc.example", Purposes: []string{"state"}}}
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIVerificationReadsRequireAPIKeyAuthentication(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Features.Verification = true
	if err := cfg.ValidateForRoles([]string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "verification reads require API key authentication") {
		t.Fatalf("unexpected missing verification read authentication error: %v", err)
	}
	if err := cfg.ValidateForRoles([]string{"verify"}); err == nil ||
		!strings.Contains(err.Error(), "configured compiler sandbox") {
		t.Fatalf("verify-only role bypassed its independent sandbox requirement: %v", err)
	}
	cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatalf("authenticated API-only verification reads failed validation: %v", err)
	}
}

func TestEnrichRoleRequiresRPCForBlockPinnedTokenDetection(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	if err := cfg.ValidateForRoles([]string{"enrich"}); err == nil || !strings.Contains(err.Error(), "rpc endpoint") {
		t.Fatalf("unexpected missing RPC error: %v", err)
	}
	cfg.RPC.Endpoints = []RPCEndpoint{{Name: "state", URL: "http://localhost:8545", Purposes: []string{"state"}}}
	if err := cfg.ValidateForRoles([]string{"enrich"}); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationWorkerRequiresPinnedCompilerAllowlist(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Features.Verification = true
	cfg.Security.CompilerSandbox = "container"
	if err := cfg.ValidateForRoles([]string{"verify"}); err == nil || !strings.Contains(err.Error(), "verification.images") {
		t.Fatalf("unexpected missing image error: %v", err)
	}
	cfg.Verification.Images = map[string]map[string]string{
		"solidity": {"0.8.30": "registry.example/solc@sha256:" + strings.Repeat("a", 64)},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateForRoles([]string{"verify"}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicVerificationRequiresAPIKeyAndContainerIsolation(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Security.PublicVerification = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "container compiler sandbox") || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("unexpected public verification error: %v", err)
	}
}

func TestContainerSandboxRejectsUntrustedRuntimeExecutable(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Security.CompilerSandbox = "container"
	cfg.Verification.ContainerRuntime = "/tmp/docker-wrapper"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be docker or podman") {
		t.Fatalf("unexpected runtime validation error: %v", err)
	}
}

func TestCompilerAllowlistRejectsUnpinnedOrInsecureArtifacts(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
		"solidity": {"0.8.30": {URL: "http://compiler.example/solc", SHA256: "bad"}},
	}
	cfg.Verification.Images = map[string]map[string]string{
		"vyper": {"0.4.0": "registry.example/vyper:latest"},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "HTTPS") || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("unexpected compiler allowlist error: %v", err)
	}
}

func TestCompilerAllowlistRejectsNoncanonicalSupplyChainInputs(t *testing.T) {
	t.Parallel()
	validDigest := strings.Repeat("a", 64)
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{
			name: "zero artifact digest",
			edit: func(cfg *Config) {
				cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
					"solidity": {"0.8.30": {URL: "https://compiler.example/solc", SHA256: strings.Repeat("0", 64)}},
				}
			},
			want: "invalid SHA-256",
		},
		{
			name: "uppercase artifact digest",
			edit: func(cfg *Config) {
				cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
					"solidity": {"0.8.30": {URL: "https://compiler.example/solc", SHA256: strings.Repeat("A", 64)}},
				}
			},
			want: "invalid SHA-256",
		},
		{
			name: "fragmented artifact URL",
			edit: func(cfg *Config) {
				cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
					"solidity": {"0.8.30": {URL: "https://compiler.example/solc#fragment", SHA256: validDigest}},
				}
			},
			want: "absolute HTTPS URL",
		},
		{
			name: "oversized artifact",
			edit: func(cfg *Config) {
				cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
					"solidity": {"0.8.30": {URL: "https://compiler.example/solc", SHA256: validDigest, MaxBytes: 1<<30 + 1}},
				}
			},
			want: "max_bytes",
		},
		{
			name: "invalid compiler version",
			edit: func(cfg *Config) {
				cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
					"solidity": {"../../solc": {URL: "https://compiler.example/solc", SHA256: validDigest}},
				}
			},
			want: "invalid version",
		},
		{
			name: "zero image digest",
			edit: func(cfg *Config) {
				cfg.Verification.Images = map[string]map[string]string{
					"vyper": {"0.4.0": "registry.example/vyper@sha256:" + strings.Repeat("0", 64)},
				}
			},
			want: "invalid digest",
		},
		{
			name: "ambiguous image digest",
			edit: func(cfg *Config) {
				cfg.Verification.Images = map[string]map[string]string{
					"vyper": {"0.4.0": "registry.example/vyper@sha256:" + validDigest + "@sha256:" + validDigest},
				}
			},
			want: "pinned by SHA-256",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.edit(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestCompilerContainerResourceLimitsAreValidated(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		memory string
		cpus   string
		want   string
	}{
		{name: "unbounded memory", memory: "0", cpus: "1", want: "container_memory"},
		{name: "too little memory", memory: "63m", cpus: "1", want: "container_memory"},
		{name: "too much memory", memory: "17g", cpus: "1", want: "container_memory"},
		{name: "invalid CPUs", memory: "512m", cpus: "all", want: "container_cpus"},
		{name: "zero CPUs", memory: "512m", cpus: "0", want: "container_cpus"},
		{name: "too many CPUs", memory: "512m", cpus: "65", want: "container_cpus"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Verification.ContainerMemory = test.memory
			cfg.Verification.ContainerCPUs = test.cpus
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestProcessCompilerRoleRequiresAbsoluteCache(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Features.Verification = true
	cfg.Security.CompilerSandbox = "process"
	cfg.Verification.CacheDirectory = "relative/cache"
	cfg.Verification.Artifacts = map[string]map[string]CompilerArtifact{
		"solidity": {"0.8.30": {
			URL: "https://compiler.example/solc", SHA256: strings.Repeat("a", 64), MaxBytes: 100 << 20,
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateForRoles([]string{"verify"}); err == nil || !strings.Contains(err.Error(), "absolute clean path") {
		t.Fatalf("unexpected cache path error: %v", err)
	}
}
