package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestBytecodeDBConfigurationBoundsAndDependency(t *testing.T) {
	defaults := Default()
	if defaults.BytecodeDB.Enabled || defaults.BytecodeDB.UnsafeAllowPrivateNetworks || defaults.BytecodeDB.MaxConcurrency != 2 || defaults.BytecodeDB.Cooldown != 24*time.Hour {
		t.Fatal("unexpected defaults")
	}
	if err := validateBytecodeDB(defaults.BytecodeDB, false); err != nil {
		t.Fatal(err)
	}
	enabled := defaults.BytecodeDB
	enabled.Enabled = true
	if err := validateBytecodeDB(enabled, false); err == nil {
		t.Fatal("verification dependency missing")
	}
	if err := validateBytecodeDB(enabled, true); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BytecodeDBConfig){
		func(c *BytecodeDBConfig) { c.BaseURL = "http://localhost" },
		func(c *BytecodeDBConfig) { c.BaseURL = "https://example.com?secret=1" },
		func(c *BytecodeDBConfig) { c.Timeout = 3 * time.Minute },
		func(c *BytecodeDBConfig) { c.MaxConcurrency = 0 },
		func(c *BytecodeDBConfig) { c.RequestInterval = 0 },
		func(c *BytecodeDBConfig) { c.MaxResponseBytes = 65 << 20 },
		func(c *BytecodeDBConfig) { c.Cooldown = 0 },
	} {
		cfg := defaults.BytecodeDB
		mutate(&cfg)
		if err := validateBytecodeDB(cfg, true); err == nil {
			t.Fatalf("invalid bounds accepted: %+v", cfg)
		}
	}
}

func TestBytecodeDBEnvironmentOverrides(t *testing.T) {
	cfg := Default()
	values := map[string]string{
		"ETHERVIEW_BYTECODE_DB_ENABLED": "true", "ETHERVIEW_BYTECODE_DB_BASE_URL": "https://bytecode.example/api",
		"ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS": "true",
		"ETHERVIEW_BYTECODE_DB_TIMEOUT":                       "12s", "ETHERVIEW_BYTECODE_DB_MAX_RESPONSE_BYTES": "1048576",
		"ETHERVIEW_BYTECODE_DB_MAX_CONCURRENCY": "3", "ETHERVIEW_BYTECODE_DB_REQUEST_INTERVAL": "2s", "ETHERVIEW_BYTECODE_DB_COOLDOWN": "48h",
	}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	if err := applyStringEnvironment(&cfg, lookup, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := applyNumericEnvironment(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	if err := applyDurationEnvironment(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	if err := applyBooleanEnvironment(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	c := cfg.BytecodeDB
	if !c.Enabled || !c.UnsafeAllowPrivateNetworks || c.BaseURL != "https://bytecode.example/api" || c.Timeout != 12*time.Second || c.MaxResponseBytes != 1<<20 || c.MaxConcurrency != 3 || c.RequestInterval != 2*time.Second || c.Cooldown != 48*time.Hour {
		t.Fatalf("overrides=%+v", c)
	}
}

func TestBytecodeDBPrivateNetworkConfigurationIsolation(t *testing.T) {
	const key = "ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS"
	cfg := Default()
	decoder := yaml.NewDecoder(strings.NewReader("bytecode_db:\n  unsafe_allow_private_networks: true\n"))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil || !cfg.BytecodeDB.UnsafeAllowPrivateNetworks {
		t.Fatalf("YAML opt-in: %v", err)
	}
	if cfg.Metadata.UnsafeAllowPrivateNetworks || cfg.Verification.UnsafeAllowPrivateDownloadNetworks {
		t.Fatal("bytecode exception changed another client's policy")
	}
	for _, value := range []string{"false", "true", "sometimes"} {
		err := applyBooleanEnvironment(&cfg, func(name string) (string, bool) { return value, name == key })
		if value == "sometimes" {
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("invalid boolean error = %v", err)
			}
		} else if err != nil || cfg.BytecodeDB.UnsafeAllowPrivateNetworks != (value == "true") {
			t.Fatalf("environment override %q: %v", value, err)
		}
	}
	cfg = Default()
	cfg.Metadata.UnsafeAllowPrivateNetworks = true
	cfg.Verification.UnsafeAllowPrivateDownloadNetworks = true
	if cfg.BytecodeDB.UnsafeAllowPrivateNetworks {
		t.Fatal("other exceptions enabled bytecode private-network access")
	}
}
