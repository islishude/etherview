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

	"gopkg.in/yaml.v3"
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
	if _, err := NormalizeRoles([]string{"verify"}); err == nil ||
		!strings.Contains(err.Error(), "use the api role") {
		t.Fatalf("removed verify role error = %v", err)
	}
}

func TestDefaultCompilerCatalogUsesAutomaticSolidityPlatform(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if got := cfg.Verification.CatalogURLs["solidity"]; got != "auto" {
		t.Fatalf("default Solidity catalog = %q, want auto", got)
	}
	if err := validateCompilerCatalogConfig(cfg.Verification); err != nil {
		t.Fatalf("automatic catalog configuration is invalid: %v", err)
	}
}

func TestVerificationRuntimePathsDefaultAndEnvironmentOverride(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.Verification.NodePath != defaultVerificationNodePath ||
		cfg.Verification.WrapperPath != defaultVerificationWrapperPath ||
		cfg.Verification.ManifestPath != defaultVerificationManifestPath {
		t.Fatalf("unexpected default verification runtime paths: %#v", cfg.Verification)
	}

	decoder := yaml.NewDecoder(strings.NewReader("verification:\n  timeout: 3m\n"))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Verification.NodePath != defaultVerificationNodePath ||
		cfg.Verification.WrapperPath != defaultVerificationWrapperPath ||
		cfg.Verification.ManifestPath != defaultVerificationManifestPath {
		t.Fatalf("legacy YAML cleared verification runtime defaults: %#v", cfg.Verification)
	}

	overrides := map[string]string{
		"ETHERVIEW_VERIFICATION_NODE_PATH":     "/custom/bin/node",
		"ETHERVIEW_VERIFICATION_WRAPPER_PATH":  "/custom/runtime/compile.mjs",
		"ETHERVIEW_VERIFICATION_MANIFEST_PATH": "/custom/runtime/runtime-manifest.json",
	}
	if err := applyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := overrides[key]
		return value, ok
	}, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Verification.NodePath != overrides["ETHERVIEW_VERIFICATION_NODE_PATH"] ||
		cfg.Verification.WrapperPath != overrides["ETHERVIEW_VERIFICATION_WRAPPER_PATH"] ||
		cfg.Verification.ManifestPath != overrides["ETHERVIEW_VERIFICATION_MANIFEST_PATH"] {
		t.Fatalf("verification runtime environment override was not applied: %#v", cfg.Verification)
	}
}

func TestVerificationWorkerRequiresExplicitAbsoluteCleanRuntimePaths(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		configure func(*VerificationConfig)
		field     string
	}{
		{
			name: "empty node",
			configure: func(verification *VerificationConfig) {
				verification.NodePath = ""
			},
			field: "verification.node_path",
		},
		{
			name: "relative wrapper",
			configure: func(verification *VerificationConfig) {
				verification.WrapperPath = "runtime/compile.mjs"
			},
			field: "verification.wrapper_path",
		},
		{
			name: "unclean manifest",
			configure: func(verification *VerificationConfig) {
				verification.ManifestPath = "/opt/etherview/compiler/../runtime-manifest.json"
			},
			field: "verification.manifest_path",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Database.URL = "postgres://localhost/etherview"
			cfg.Features.Verification = true
			cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
			test.configure(&cfg.Verification)
			if err := cfg.ValidateForRoles([]string{"api"}); err == nil ||
				!strings.Contains(err.Error(), test.field) {
				t.Fatalf("unexpected runtime path validation error: %v", err)
			}
		})
	}
}

func TestWalletAddChainConfiguration(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Chain.NativeSymbol = "TST"
	cfg.Wallet.AddChain = WalletAddChainConfig{
		RPCURLs:           []string{"https://rpc.public.example", "http://localhost:8545"},
		BlockExplorerURLs: []string{"https://explorer.example"},
		IconURLs:          []string{"https://assets.example/network.png"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid wallet add-chain configuration: %v", err)
	}

	for _, test := range []struct {
		name string
		url  string
	}{
		{name: "http", url: "http://rpc.example"},
		{name: "HTTP loopback address", url: "http://127.0.0.1:8545"},
		{name: "HTTP internal hostname", url: "http://reth:8545"},
		{name: "non-HTTP localhost", url: "ftp://localhost:8545"},
		{name: "credentials", url: "https://user:secret@rpc.example"},
		{name: "query", url: "https://rpc.example/?key=secret"},
		{name: "fragment", url: "https://rpc.example/#network"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := cfg
			invalid.Wallet.AddChain.RPCURLs = []string{test.url}
			if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "wallet.add_chain.rpc_urls") {
				t.Fatalf("invalid public URL passed: %q error=%v", test.url, err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{
			name: "HTTP localhost block explorer",
			mutate: func(invalid *Config) {
				invalid.Wallet.AddChain.BlockExplorerURLs = []string{"http://localhost:8080"}
			},
			field: "wallet.add_chain.block_explorer_urls",
		},
		{
			name: "HTTP localhost icon",
			mutate: func(invalid *Config) {
				invalid.Wallet.AddChain.IconURLs = []string{"http://localhost:8080/icon.png"}
			},
			field: "wallet.add_chain.icon_urls",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := cfg
			test.mutate(&invalid)
			if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("invalid public metadata URL passed: error=%v", err)
			}
		})
	}

	metadataOnly := Default()
	metadataOnly.Wallet.AddChain.IconURLs = []string{"https://assets.example/network.png"}
	if err := metadataOnly.Validate(); err == nil || !strings.Contains(err.Error(), "requires at least one rpc_url") {
		t.Fatalf("metadata-only wallet config error = %v", err)
	}

	tooMany := cfg
	tooMany.Wallet.AddChain.RPCURLs = make([]string, 6)
	for index := range tooMany.Wallet.AddChain.RPCURLs {
		tooMany.Wallet.AddChain.RPCURLs[index] = "https://rpc.example"
	}
	if err := tooMany.Validate(); err == nil || !strings.Contains(err.Error(), "at most 5") {
		t.Fatalf("unbounded wallet URL list error = %v", err)
	}
}

func TestPreviewPublicOriginMatchesBrowserAndWalletMetadata(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../deploy/preview.config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Server struct {
			PublicURL string `yaml:"public_url"`
		} `yaml:"server"`
		Wallet struct {
			AddChain struct {
				BlockExplorerURLs []string `yaml:"block_explorer_urls"`
			} `yaml:"add_chain"`
		} `yaml:"wallet"`
	}
	if err := yaml.Unmarshal(data, &preview); err != nil {
		t.Fatal(err)
	}
	const browserOrigin = "https://etherview.localhost:8080"
	if preview.Server.PublicURL != browserOrigin {
		t.Fatalf("Preview public URL = %q, want documented browser origin %q", preview.Server.PublicURL, browserOrigin)
	}
	if !reflect.DeepEqual(preview.Wallet.AddChain.BlockExplorerURLs, []string{browserOrigin}) {
		t.Fatalf(
			"Preview wallet block explorer URLs = %#v, want public origin",
			preview.Wallet.AddChain.BlockExplorerURLs,
		)
	}
}

func TestWalletAddChainEnvironment(t *testing.T) {
	cfg := Default()
	values := map[string]string{
		"ETHERVIEW_WALLET_ADD_CHAIN_RPC_URLS":            "https://one.example, https://two.example",
		"ETHERVIEW_WALLET_ADD_CHAIN_BLOCK_EXPLORER_URLS": "https://explorer.example",
		"ETHERVIEW_WALLET_ADD_CHAIN_ICON_URLS":           "https://assets.example/icon.png",
	}
	if err := applyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Wallet.AddChain.RPCURLs, []string{"https://one.example", "https://two.example"}) ||
		!reflect.DeepEqual(cfg.Wallet.AddChain.BlockExplorerURLs, []string{"https://explorer.example"}) ||
		!reflect.DeepEqual(cfg.Wallet.AddChain.IconURLs, []string{"https://assets.example/icon.png"}) {
		t.Fatalf("unexpected wallet environment config: %#v", cfg.Wallet.AddChain)
	}
}

func TestRemovedCompilerEnvironmentIsRejected(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"ETHERVIEW_COMPILER_SANDBOX",
		"ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT",
		"ETHERVIEW_VERIFICATION_RUNNER_IMAGE",
		"ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			err := applyEnvironment(&cfg, func(key string) (string, bool) {
				return "removed", key == name
			}, nil)
			if err == nil || !strings.Contains(err.Error(), "no longer supported") {
				t.Fatalf("%s error=%v", name, err)
			}
		})
	}
}

func TestPreviewCompilerDownloadNetworkExceptionIsExplicitAndAPIScoped(t *testing.T) {
	t.Parallel()
	cfg := Default()
	err := applyEnvironment(&cfg, func(key string) (string, bool) {
		if key == "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS" {
			return "true", true
		}
		return "", false
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Verification.UnsafeAllowPrivateDownloadNetworks {
		t.Fatal("explicit compiler download network exception was not applied")
	}
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Features.Verification = true
	cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatalf("API-scoped compiler download exception failed validation: %v", err)
	}
	if err := cfg.ValidateForRoles([]string{"sync"}); err == nil ||
		!strings.Contains(err.Error(), "requires an api verification worker") {
		t.Fatalf("non-API compiler download exception error = %v", err)
	}
}

func TestLoadEnvironmentAndSecretFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "database-url")
	readSecretPath := filepath.Join(dir, "database-read-url")
	if err := os.WriteFile(secretPath, []byte("postgres://example/db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readSecretPath, []byte("postgres://read-example/db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This case exercises the file-only input and must not inherit an unrelated
	// inline value from the caller's environment.
	unsetHostEnvironment(t, "ETHERVIEW_DATABASE_URL")
	unsetHostEnvironment(t, "ETHERVIEW_DATABASE_READ_URL")
	t.Setenv("ETHERVIEW_DATABASE_URL_FILE", secretPath)
	t.Setenv("ETHERVIEW_DATABASE_READ_URL_FILE", readSecretPath)
	t.Setenv("ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS", "10")
	t.Setenv("ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS", "1")
	t.Setenv("ETHERVIEW_CHAIN_ID", "11155111")
	t.Setenv("ETHERVIEW_CHAIN_GENESIS_FILE", "/var/lib/etherview/genesis.json")
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
	if cfg.Database.URL != "postgres://example/db" ||
		cfg.Database.ReadURL != "postgres://read-example/db" ||
		cfg.Database.ReadMaxConnections != 10 || cfg.Database.ReadMinConnections != 1 ||
		cfg.Chain.ID != 11155111 ||
		cfg.Chain.GenesisFile != "/var/lib/etherview/genesis.json" {
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

func TestLoadReadDatabaseYAML(t *testing.T) {
	for _, name := range []string{
		"ETHERVIEW_DATABASE_READ_URL",
		"ETHERVIEW_DATABASE_READ_URL_FILE",
		"ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS",
		"ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS",
	} {
		unsetHostEnvironment(t, name)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`database:
  read_url: postgres://yaml-reader.example/etherview
  read_max_connections: 8
  read_min_connections: 3
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.ReadURL != "postgres://yaml-reader.example/etherview" ||
		cfg.Database.ReadMaxConnections != 8 || cfg.Database.ReadMinConnections != 3 {
		t.Fatalf("read database YAML was not applied: %#v", cfg.Database)
	}
}

func TestLoadSyncProgressLogIntervalYAML(t *testing.T) {
	unsetHostEnvironment(t, "ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`observability:
  sync_progress_log_interval: 45s
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.SyncProgressLogInterval != 45*time.Second {
		t.Fatalf("sync progress YAML interval = %s, want 45s", cfg.Observability.SyncProgressLogInterval)
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
	if got := Default().Observability; got.OTLPTraceEndpoint != "" ||
		got.LogLevel != "info" || got.LogFormat != "json" ||
		got.SyncProgressLogInterval != 30*time.Second {
		t.Fatalf("unexpected observability defaults: %#v", got)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "empty environment", mutate: func(cfg *Config) { cfg.Observability.Environment = "" }, want: "environment"},
		{name: "empty log level", mutate: func(cfg *Config) { cfg.Observability.LogLevel = "" }, want: "log_level"},
		{name: "uppercase log level", mutate: func(cfg *Config) { cfg.Observability.LogLevel = "INFO" }, want: "log_level"},
		{name: "numeric log level", mutate: func(cfg *Config) { cfg.Observability.LogLevel = "DEBUG-4" }, want: "log_level"},
		{name: "spaced log level", mutate: func(cfg *Config) { cfg.Observability.LogLevel = " info" }, want: "log_level"},
		{name: "empty log format", mutate: func(cfg *Config) { cfg.Observability.LogFormat = "" }, want: "log_format"},
		{name: "uppercase log format", mutate: func(cfg *Config) { cfg.Observability.LogFormat = "JSON" }, want: "log_format"},
		{name: "spaced log format", mutate: func(cfg *Config) { cfg.Observability.LogFormat = "text " }, want: "log_format"},
		{name: "sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = 1.01 }, want: "trace_sample_ratio"},
		{name: "nan sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = math.NaN() }, want: "trace_sample_ratio"},
		{name: "infinite sample ratio", mutate: func(cfg *Config) { cfg.Observability.TraceSampleRatio = math.Inf(1) }, want: "trace_sample_ratio"},
		{name: "short export timeout", mutate: func(cfg *Config) { cfg.Observability.TraceExportTimeout = 99 * time.Millisecond }, want: "trace_export_timeout"},
		{name: "short refresh", mutate: func(cfg *Config) { cfg.Observability.MetricsRefreshInterval = time.Millisecond }, want: "metrics_refresh_interval"},
		{name: "short sync progress log", mutate: func(cfg *Config) { cfg.Observability.SyncProgressLogInterval = time.Millisecond }, want: "sync_progress_log_interval"},
		{name: "long sync progress log", mutate: func(cfg *Config) { cfg.Observability.SyncProgressLogInterval = time.Hour + 1 }, want: "sync_progress_log_interval"},
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
			Environment: "production", LogLevel: "debug", LogFormat: "text",
			OTLPTraceEndpoint: "https://otel.example:4318",
			TraceSampleRatio:  0.25, TraceExportTimeout: time.Second,
			MetricsRefreshInterval: 10 * time.Second, SyncProgressLogInterval: time.Minute,
		},
		{
			Environment: "staging", LogLevel: "error", LogFormat: "json",
			OTLPTraceEndpoint: "http://otel.monitoring.svc:4318",
			OTLPTraceInsecure: true, TraceSampleRatio: 1, TraceExportTimeout: time.Second,
			MetricsRefreshInterval: 10 * time.Second, SyncProgressLogInterval: time.Second,
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
		"ETHERVIEW_OBSERVABILITY_ENVIRONMENT":  "staging",
		"ETHERVIEW_LOG_LEVEL":                  "debug",
		"ETHERVIEW_LOG_FORMAT":                 "text",
		"ETHERVIEW_OTLP_TRACE_ENDPOINT":        "http://otel.monitoring.svc:4318",
		"ETHERVIEW_OTLP_TRACE_INSECURE":        "true",
		"ETHERVIEW_TRACE_SAMPLE_RATIO":         "0.5",
		"ETHERVIEW_TRACE_EXPORT_TIMEOUT":       "3s",
		"ETHERVIEW_METRICS_REFRESH_INTERVAL":   "20s",
		"ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL": "45s",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	if err := applyEnvironment(&cfg, lookup, os.ReadFile); err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.Environment != "staging" ||
		cfg.Observability.LogLevel != "debug" || cfg.Observability.LogFormat != "text" ||
		cfg.Observability.OTLPTraceEndpoint != "http://otel.monitoring.svc:4318" ||
		!cfg.Observability.OTLPTraceInsecure || cfg.Observability.TraceSampleRatio != 0.5 ||
		cfg.Observability.TraceExportTimeout != 3*time.Second ||
		cfg.Observability.MetricsRefreshInterval != 20*time.Second ||
		cfg.Observability.SyncProgressLogInterval != 45*time.Second {
		t.Fatalf("observability environment was not applied: %#v", cfg.Observability)
	}
}

func TestObservabilityEnvironmentRejectsNonCanonicalLoggingValues(t *testing.T) {
	t.Parallel()
	for name, values := range map[string]map[string]string{
		"level whitespace":  {"ETHERVIEW_LOG_LEVEL": " info"},
		"level uppercase":   {"ETHERVIEW_LOG_LEVEL": "INFO"},
		"format whitespace": {"ETHERVIEW_LOG_FORMAT": "json "},
		"format uppercase":  {"ETHERVIEW_LOG_FORMAT": "TEXT"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			lookup := func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			}
			if err := applyEnvironment(&cfg, lookup, os.ReadFile); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("non-canonical logging configuration passed: %#v", cfg.Observability)
			}
		})
	}
}

func TestLoadWithOverridesAppliesLoggingBeforeValidation(t *testing.T) {
	t.Setenv("ETHERVIEW_LOG_LEVEL", "INVALID")
	t.Setenv("ETHERVIEW_LOG_FORMAT", "INVALID")
	level, format := "debug", "text"
	cfg, err := LoadWithOverrides("", Overrides{
		LogLevel: &level, LogFormat: &format,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.LogLevel != level || cfg.Observability.LogFormat != format {
		t.Fatalf("logging overrides were not applied: %#v", cfg.Observability)
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

func TestGenesisFileRequiresAbsolutePathAndBlockZeroStart(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Chain.GenesisFile = "genesis.json"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative genesis file error = %v", err)
	}
	cfg.Chain.GenesisFile = "/var/lib/etherview/genesis.json"
	cfg.Chain.StartBlock = 1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "start_block=0") {
		t.Fatalf("non-zero start genesis file error = %v", err)
	}
	cfg.Chain.StartBlock = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid genesis file config: %v", err)
	}
}

func TestServerTLSConfiguration(t *testing.T) {
	t.Parallel()
	valid := Default()
	valid.Server.PublicURL = "https://explorer.example"
	valid.Server.TLSCertFile = "/run/etherview-tls/tls.crt"
	valid.Server.TLSKeyFile = "/run/etherview-tls/tls.key"
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid server TLS configuration: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "certificate only",
			mutate: func(cfg *Config) {
				cfg.Server.TLSCertFile = "/run/etherview-tls/tls.crt"
			},
			want: "must be configured together",
		},
		{
			name: "key only",
			mutate: func(cfg *Config) {
				cfg.Server.TLSKeyFile = "/run/etherview-tls/tls.key"
			},
			want: "must be configured together",
		},
		{
			name: "relative certificate",
			mutate: func(cfg *Config) {
				cfg.Server.TLSCertFile = "tls.crt"
				cfg.Server.TLSKeyFile = "/run/etherview-tls/tls.key"
			},
			want: "tls_cert_file must be an absolute path",
		},
		{
			name: "relative key",
			mutate: func(cfg *Config) {
				cfg.Server.TLSCertFile = "/run/etherview-tls/tls.crt"
				cfg.Server.TLSKeyFile = "tls.key"
			},
			want: "tls_key_file must be an absolute path",
		},
		{
			name: "HTTP public origin",
			mutate: func(cfg *Config) {
				cfg.Server.PublicURL = "http://localhost:8080"
				cfg.Server.TLSCertFile = "/run/etherview-tls/tls.crt"
				cfg.Server.TLSKeyFile = "/run/etherview-tls/tls.key"
			},
			want: "public_url must use HTTPS",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("TLS validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServerTLSEnvironment(t *testing.T) {
	cfg := Default()
	values := map[string]string{
		"ETHERVIEW_SERVER_PUBLIC_URL":    "https://explorer.example",
		"ETHERVIEW_SERVER_TLS_CERT_FILE": "/run/etherview-tls/tls.crt",
		"ETHERVIEW_SERVER_TLS_KEY_FILE":  "/run/etherview-tls/tls.key",
	}
	if err := applyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PublicURL != values["ETHERVIEW_SERVER_PUBLIC_URL"] ||
		cfg.Server.TLSCertFile != values["ETHERVIEW_SERVER_TLS_CERT_FILE"] ||
		cfg.Server.TLSKeyFile != values["ETHERVIEW_SERVER_TLS_KEY_FILE"] {
		t.Fatalf("unexpected server TLS environment: %#v", cfg.Server)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("environment server TLS configuration: %v", err)
	}
}

func TestGenesisRemoteSourceConfiguration(t *testing.T) {
	t.Parallel()
	if Default().Chain.GenesisFetchTimeout != time.Minute {
		t.Fatalf("default Genesis fetch timeout = %s", Default().Chain.GenesisFetchTimeout)
	}
	validDigest := strings.Repeat("a", 64)
	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "file and URL",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisFile = "/var/lib/etherview/genesis.json"
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
			},
			want: "mutually exclusive",
		},
		{
			name: "URL with non-zero start",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
				cfg.Chain.StartBlock = 1
			},
			want: "start_block=0",
		},
		{
			name:   "untrimmed URL",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = " https://genesis.example/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "HTTP URL",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "http://genesis.example/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "relative URL",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "opaque URL",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https:genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "credentials",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://user:secret@genesis.example/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "query",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/genesis.json?token=secret" },
			want:   "chain.genesis_url",
		},
		{
			name:   "empty query",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/genesis.json?" },
			want:   "chain.genesis_url",
		},
		{
			name:   "fragment",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/genesis.json#fragment" },
			want:   "chain.genesis_url",
		},
		{
			name:   "empty fragment",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/genesis.json#" },
			want:   "chain.genesis_url",
		},
		{
			name: "URL above maximum length",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/" +
					strings.Repeat("a", 4097-len("https://genesis.example/"))
			},
			want: "chain.genesis_url",
		},
		{
			name:   "non-standard port",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example:8443/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "empty explicit port",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example:/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "escaped traversal",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/%2e%2e/genesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "escaped traversal separator",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/safe%2f%2e%2e%2fgenesis.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "escaped null",
			mutate: func(cfg *Config) { cfg.Chain.GenesisURL = "https://genesis.example/genesis%00.json" },
			want:   "chain.genesis_url",
		},
		{
			name:   "digest without URL",
			mutate: func(cfg *Config) { cfg.Chain.GenesisSHA256 = validDigest },
			want:   "chain.genesis_sha256",
		},
		{
			name: "short digest",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
				cfg.Chain.GenesisSHA256 = "abcd"
			},
			want: "chain.genesis_sha256",
		},
		{
			name: "uppercase digest",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
				cfg.Chain.GenesisSHA256 = strings.Repeat("A", 64)
			},
			want: "chain.genesis_sha256",
		},
		{
			name: "zero digest",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
				cfg.Chain.GenesisSHA256 = strings.Repeat("0", 64)
			},
			want: "chain.genesis_sha256",
		},
		{
			name: "non-hex digest",
			mutate: func(cfg *Config) {
				cfg.Chain.GenesisURL = "https://genesis.example/genesis.json"
				cfg.Chain.GenesisSHA256 = strings.Repeat("z", 64)
			},
			want: "chain.genesis_sha256",
		},
		{
			name:   "short timeout",
			mutate: func(cfg *Config) { cfg.Chain.GenesisFetchTimeout = time.Second - 1 },
			want:   "chain.genesis_fetch_timeout",
		},
		{
			name:   "long timeout",
			mutate: func(cfg *Config) { cfg.Chain.GenesisFetchTimeout = 5*time.Minute + 1 },
			want:   "chain.genesis_fetch_timeout",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid Genesis source config passed: %#v error=%v", cfg.Chain, err)
			}
		})
	}

	for _, rawURL := range []string{
		"https://genesis.example/genesis.json",
		"https://genesis.example:443/genesis.json",
	} {
		cfg := Default()
		cfg.Chain.GenesisURL = rawURL
		cfg.Chain.GenesisSHA256 = validDigest
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid Genesis URL config %q: %v", rawURL, err)
		}
	}
	for _, timeout := range []time.Duration{time.Second, 5 * time.Minute} {
		cfg := Default()
		cfg.Chain.GenesisFetchTimeout = timeout
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid Genesis fetch timeout %s: %v", timeout, err)
		}
	}
}

func TestLoadGenesisRemoteSourceEnvironment(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	t.Setenv("ETHERVIEW_CHAIN_GENESIS_FILE", "")
	t.Setenv("ETHERVIEW_CHAIN_GENESIS_URL", "https://genesis.example/genesis.json")
	t.Setenv("ETHERVIEW_CHAIN_GENESIS_SHA256", validDigest)
	t.Setenv("ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT", "45s")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chain.GenesisURL != "https://genesis.example/genesis.json" ||
		cfg.Chain.GenesisSHA256 != validDigest || cfg.Chain.GenesisFetchTimeout != 45*time.Second {
		t.Fatalf("Genesis source environment was not applied: %#v", cfg.Chain)
	}
}

func TestValueAndFileAreMutuallyExclusive(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_URL", "postgres://inline")
	t.Setenv("ETHERVIEW_DATABASE_URL_FILE", "/tmp/ignored")
	if _, err := Load(""); err == nil {
		t.Fatal("expected mutually-exclusive secret error")
	}
}

func TestReadURLAndReadFileAreMutuallyExclusive(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_READ_URL", "postgres://read-inline")
	t.Setenv("ETHERVIEW_DATABASE_READ_URL_FILE", "/tmp/ignored")
	if _, err := Load(""); err == nil {
		t.Fatal("expected read url mutually-exclusive secret error")
	}
}

func TestReadDatabaseConnectionBoundsFallbackToWriterDefaults(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Database.ReadURL = "postgres://read-replica/etherview"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected read database with fallback defaults to validate: %v", err)
	}
}

func TestReadDatabaseConnectionBoundsRejectInvalidConfiguration(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		readMax int32
		readMin int32
	}{
		{name: "negative maximum", readMax: -1},
		{name: "negative minimum", readMin: -1},
		{name: "explicit minimum exceeds maximum", readMax: 1, readMin: 2},
		{name: "inherited writer minimum exceeds read maximum", readMax: 1, readMin: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Database.URL = "postgres://localhost/etherview"
			cfg.Database.ReadMaxConnections = test.readMax
			cfg.Database.ReadMinConnections = test.readMin
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "read connection bounds") {
				t.Fatalf("unexpected read connection validation result: %v", err)
			}
		})
	}
}

func TestValidateForRolesRejectsMalformedReadDatabaseURL(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Database.ReadURL = "postgres://"
	if err := cfg.ValidateForRoles([]string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "database.read_url must be an absolute postgres URL") {
		t.Fatalf("unexpected malformed read URL validation result: %v", err)
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
	cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatalf("authenticated API verification worker failed validation: %v", err)
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

func TestPublicVerificationRequiresFeatureAndAPIKey(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Security.PublicVerification = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "features.verification") ||
		!strings.Contains(err.Error(), "API key") {
		t.Fatalf("unexpected public verification error: %v", err)
	}
}

func TestRemovedCompilerYAMLFieldsAreUnknown(t *testing.T) {
	t.Parallel()
	for _, document := range []string{
		"security:\n  compiler_sandbox: remote\n",
		"verification:\n  runner_endpoint: http://compiler-runner:8091\n",
		"verification:\n  runner_image: image@sha256:deadbeef\n",
		"verification:\n  artifacts: {}\n",
	} {
		var cfg Config
		decoder := yaml.NewDecoder(strings.NewReader(document))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err == nil {
			t.Fatalf("removed compiler field was accepted: %s", document)
		}
	}
}

func TestVerificationWorkerRequiresAbsoluteCache(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.Features.Verification = true
	cfg.Verification.CacheDirectory = "relative/cache"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Security.APIKeyPepper = strings.Repeat("p", 32)
	if err := cfg.ValidateForRoles([]string{"api"}); err == nil || !strings.Contains(err.Error(), "absolute clean path") {
		t.Fatalf("unexpected cache path error: %v", err)
	}
}

func unsetHostEnvironment(t *testing.T, name string) {
	t.Helper()
	value, set := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if set {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}
