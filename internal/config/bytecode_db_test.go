package config

import (
	"testing"
	"time"
)

func TestBytecodeDBConfigurationBoundsAndDependency(t *testing.T) {
	defaults := Default()
	if defaults.BytecodeDB.Enabled || defaults.BytecodeDB.MaxConcurrency != 2 || defaults.BytecodeDB.Cooldown != 24*time.Hour {
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
		"ETHERVIEW_BYTECODE_DB_TIMEOUT": "12s", "ETHERVIEW_BYTECODE_DB_MAX_RESPONSE_BYTES": "1048576",
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
	if !c.Enabled || c.BaseURL != "https://bytecode.example/api" || c.Timeout != 12*time.Second || c.MaxResponseBytes != 1<<20 || c.MaxConcurrency != 3 || c.RequestInterval != 2*time.Second || c.Cooldown != 48*time.Hour {
		t.Fatalf("overrides=%+v", c)
	}
}
