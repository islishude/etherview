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
		envPrefix + "METADATA_FETCH_TIMEOUT":                 "3s",
		envPrefix + "METADATA_MAX_DOCUMENT_BYTES":            "1048576",
		envPrefix + "METADATA_MAX_REDIRECTS":                 "2",
		envPrefix + "METADATA_IPFS_GATEWAY":                  "https://ipfs.example.invalid/base",
		envPrefix + "METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS": "true",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	if err := applyEnvironment(&cfg, lookup, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Metadata.FetchTimeout != 3*time.Second || cfg.Metadata.MaxDocumentBytes != 1<<20 ||
		cfg.Metadata.MaxRedirects != 2 || cfg.Metadata.IPFSGateway != values[envPrefix+"METADATA_IPFS_GATEWAY"] ||
		!cfg.Metadata.UnsafeAllowPrivateNetworks {
		t.Fatalf("metadata overrides = %+v", cfg.Metadata)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate metadata overrides: %v", err)
	}
}

func TestMetadataPrivateNetworkExceptionIsMetadataWorkerOnly(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.Metadata.UnsafeAllowPrivateNetworks {
		t.Fatal("metadata private-network exception must default off")
	}
	cfg.Metadata.UnsafeAllowPrivateNetworks = true
	cfg.Features.NFTMetadata = true
	cfg.Database.URL = "postgres://localhost/etherview"
	cfg.RPC.Endpoints = []RPCEndpoint{{Name: "state", URL: "http://localhost:8545", Purposes: []string{"state"}}}
	if err := cfg.ValidateForRoles([]string{"metadata"}); err != nil {
		t.Fatalf("metadata-only exception failed validation: %v", err)
	}
	for _, roles := range [][]string{{"api"}, {"all"}, {"metadata", "api"}} {
		if err := cfg.ValidateForRoles(roles); err == nil ||
			!strings.Contains(err.Error(), "requires a metadata-only NFT metadata worker") {
			t.Fatalf("roles %v exception error = %v", roles, err)
		}
	}
}

func TestMetadataPrivateNetworkEnvironmentIsStrictBoolean(t *testing.T) {
	t.Parallel()
	cfg := Default()
	err := applyEnvironment(&cfg, func(name string) (string, bool) {
		return "sometimes", name == envPrefix+"METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS"
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS") {
		t.Fatalf("invalid metadata private-network boolean error = %v", err)
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
