package config

import (
	"strings"
	"testing"
)

func TestERC4337ConfigurationRequiresExplicitNonOverlappingEntries(t *testing.T) {
	t.Parallel()
	base := Default()
	base.Features.UserOperations = true
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "requires at least one") {
		t.Fatalf("missing EntryPoint error = %v", err)
	}

	end := uint64(19)
	base.ERC4337.EntryPoints = []ERC4337EntryPointConfig{
		{Address: "0x0000000071727De22E5E9d8BAf0edAc6f37da032", Version: "0.7", FromBlock: 10, ToBlock: &end},
		{Address: "0x0000000071727de22e5e9d8baf0edac6f37da032", Version: "0.8", FromBlock: 20},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid ERC-4337 config: %v", err)
	}

	overlap := base
	overlap.ERC4337.EntryPoints = append([]ERC4337EntryPointConfig(nil), base.ERC4337.EntryPoints...)
	overlap.ERC4337.EntryPoints[1].FromBlock = 19
	if err := overlap.Validate(); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestERC4337ConfigurationRejectsInertOrUnknownEntries(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.ERC4337.EntryPoints = []ERC4337EntryPointConfig{{
		Address: "0x433709009B8330FDa32311DF1C2AFA402eD8D009", Version: "1.0",
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires features.user_operations") ||
		!strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("invalid config error = %v", err)
	}
}

func TestERC4337ConfigurationRejectsZeroEntryPoint(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Features.UserOperations = true
	cfg.ERC4337.EntryPoints = []ERC4337EntryPointConfig{{
		Address: "0x0000000000000000000000000000000000000000", Version: "0.9",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "non-zero") {
		t.Fatalf("zero EntryPoint error = %v", err)
	}
}

func TestERC4337FeatureEnvironmentOverride(t *testing.T) {
	t.Parallel()
	cfg := Default()
	lookup := func(name string) (string, bool) {
		if name == "ETHERVIEW_FEATURE_USER_OPERATIONS" {
			return "true", true
		}
		return "", false
	}
	if err := applyBooleanEnvironment(&cfg, lookup); err != nil {
		t.Fatal(err)
	}
	if !cfg.Features.UserOperations {
		t.Fatal("user_operations feature was not enabled")
	}
}

func TestERC4337EntryPointEnvironmentJSON(t *testing.T) {
	t.Parallel()
	cfg := Default()
	lookup := func(name string) (string, bool) {
		if name == "ETHERVIEW_ERC4337_ENTRY_POINTS" {
			return `[{"address":"0x433709009B8330FDa32311DF1C2AFA402eD8D009","version":"0.9","from_block":7}]`, true
		}
		return "", false
	}
	if err := applyStringEnvironment(&cfg, lookup, func(string) ([]byte, error) { return nil, nil }, false); err != nil {
		t.Fatal(err)
	}
	if len(cfg.ERC4337.EntryPoints) != 1 || cfg.ERC4337.EntryPoints[0].FromBlock != 7 {
		t.Fatalf("entries=%+v", cfg.ERC4337.EntryPoints)
	}
	if _, err := parseERC4337EntryPoints(`[{"unknown":true}]`); err == nil {
		t.Fatal("unknown EntryPoint field was accepted")
	}
}
