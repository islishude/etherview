package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	t.Setenv("ETHERVIEW_DATABASE_URL_FILE", secretPath)
	t.Setenv("ETHERVIEW_CHAIN_ID", "11155111")
	t.Setenv("ETHERVIEW_ROLES", "api,sync")
	t.Setenv("ETHERVIEW_RPC_URLS", "https://rpc.example, wss://ws.example")
	t.Setenv("ETHERVIEW_API_KEY_PEPPER", strings.Repeat("p", 32))
	t.Setenv("ETHERVIEW_BACKFILL_WORKERS", "8")
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
	if cfg.Runtime.BackfillWorkers != 8 || len(cfg.Security.APIKeyPepper) != 32 {
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
