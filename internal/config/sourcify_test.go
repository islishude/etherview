package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSourcifyPrivateNetworkConfiguration(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.Sourcify.UnsafeAllowPrivateNetworks {
		t.Fatal("Sourcify network exception must default off")
	}
	decoder := yaml.NewDecoder(strings.NewReader("sourcify:\n  unsafe_allow_private_networks: true\n"))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil || !cfg.Sourcify.UnsafeAllowPrivateNetworks {
		t.Fatalf("YAML opt-in: %v", err)
	}
	const key = "ETHERVIEW_SOURCIFY_UNSAFE_ALLOW_PRIVATE_NETWORKS"
	for _, value := range []string{"false", "true", "sometimes"} {
		err := applyBooleanEnvironment(&cfg, func(name string) (string, bool) { return value, name == key })
		if value == "sometimes" {
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("invalid boolean error = %v", err)
			}
		} else if err != nil || cfg.Sourcify.UnsafeAllowPrivateNetworks != (value == "true") {
			t.Fatalf("environment override %q: %v", value, err)
		}
	}
	if cfg.BytecodeDB.UnsafeAllowPrivateNetworks || cfg.Metadata.UnsafeAllowPrivateNetworks || cfg.Verification.UnsafeAllowPrivateDownloadNetworks {
		t.Fatal("Sourcify exception changed another client's policy")
	}
	cfg = Default()
	cfg.BytecodeDB.UnsafeAllowPrivateNetworks = true
	cfg.Metadata.UnsafeAllowPrivateNetworks = true
	cfg.Verification.UnsafeAllowPrivateDownloadNetworks = true
	if cfg.Sourcify.UnsafeAllowPrivateNetworks {
		t.Fatal("other exceptions enabled Sourcify private-network access")
	}
}
