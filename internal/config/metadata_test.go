package config

import (
	"strings"
	"testing"
	"time"
)

func TestMetadataConfigurationEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	cfg := Default()
	values := map[string]string{
		envPrefix + "METADATA_FETCH_TIMEOUT":      "3s",
		envPrefix + "METADATA_MAX_DOCUMENT_BYTES": "1048576",
		envPrefix + "METADATA_MAX_REDIRECTS":      "2",
		envPrefix + "METADATA_IPFS_GATEWAY":       "https://ipfs.example.invalid/base",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	if err := applyEnvironment(&cfg, lookup, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Metadata.FetchTimeout != 3*time.Second || cfg.Metadata.MaxDocumentBytes != 1<<20 ||
		cfg.Metadata.MaxRedirects != 2 || cfg.Metadata.IPFSGateway != values[envPrefix+"METADATA_IPFS_GATEWAY"] {
		t.Fatalf("metadata overrides = %+v", cfg.Metadata)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate metadata overrides: %v", err)
	}
}

func TestMetadataConfigurationIsStrictlyBounded(t *testing.T) {
	t.Parallel()
	mutations := []func(*Config){
		func(cfg *Config) { cfg.Metadata.FetchTimeout = 99 * time.Millisecond },
		func(cfg *Config) { cfg.Metadata.FetchTimeout = time.Minute + time.Nanosecond },
		func(cfg *Config) { cfg.Metadata.MaxDocumentBytes = 0 },
		func(cfg *Config) { cfg.Metadata.MaxDocumentBytes = 2<<20 + 1 },
		func(cfg *Config) { cfg.Metadata.MaxRedirects = 0 },
		func(cfg *Config) { cfg.Metadata.MaxRedirects = 11 },
		func(cfg *Config) { cfg.Metadata.IPFSGateway = "http://ipfs.example.invalid" },
		func(cfg *Config) { cfg.Metadata.IPFSGateway = "https://user:secret@ipfs.example.invalid" },
	}
	for index, mutate := range mutations {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "metadata.") {
			t.Fatalf("invalid metadata config %d passed: %+v, error=%v", index, cfg.Metadata, err)
		}
	}
}
