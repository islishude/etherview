package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPreviewEnablesPublicVerificationAndNFTMetadata(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../deploy/preview.config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Metadata struct {
			IPFSGateway                string `yaml:"ipfs_gateway"`
			UnsafeAllowPrivateNetworks bool   `yaml:"unsafe_allow_private_networks"`
		} `yaml:"metadata"`
		Features struct {
			Verification bool `yaml:"verification"`
			Sourcify     bool `yaml:"sourcify"`
			NFTMetadata  bool `yaml:"nft_metadata"`
		} `yaml:"features"`
		Security struct {
			PublicVerification bool `yaml:"public_verification"`
		} `yaml:"security"`
		Verification struct {
			CacheDirectory string            `yaml:"cache_directory"`
			ExecutorPath   string            `yaml:"executor_path"`
			CatalogURLs    map[string]string `yaml:"catalog_urls"`
		} `yaml:"verification"`
	}
	if err := yaml.Unmarshal(data, &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Features.Verification || !preview.Security.PublicVerification {
		t.Fatal("Preview public verification must be enabled")
	}
	if preview.Verification.CacheDirectory != "/var/lib/etherview/compilers/cache" ||
		preview.Verification.ExecutorPath != defaultVerificationExecutorPath ||
		preview.Verification.CatalogURLs["solidity"] != "auto" {
		t.Fatalf(
			"Preview compiler cache=%q executor=%q catalog=%v",
			preview.Verification.CacheDirectory,
			preview.Verification.ExecutorPath,
			preview.Verification.CatalogURLs,
		)
	}
	text := string(data)
	for _, removed := range []string{
		"compiler_sandbox", "runner_endpoint", "runner_image", "vyper",
	} {
		if strings.Contains(text, removed) {
			t.Fatalf("Preview config retains removed field %q", removed)
		}
	}
	if !preview.Features.NFTMetadata || preview.Metadata.IPFSGateway != "https://ipfs.io" {
		t.Fatalf(
			"Preview NFT metadata = %v gateway = %q, want enabled public HTTPS gateway",
			preview.Features.NFTMetadata,
			preview.Metadata.IPFSGateway,
		)
	}
	if preview.Metadata.UnsafeAllowPrivateNetworks {
		t.Fatal("Preview YAML must keep the metadata private-network exception disabled")
	}
	if preview.Features.Sourcify {
		t.Fatal("Preview Sourcify must remain disabled")
	}
}

func TestPreviewGenesisFundsGethUnlockedDeveloperAccount(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../deploy/preview.genesis.json")
	if err != nil {
		t.Fatal(err)
	}
	var genesis struct {
		Alloc map[string]struct {
			Balance string `json:"balance"`
		} `json:"alloc"`
	}
	if err := json.Unmarshal(data, &genesis); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{
		"71562b71999873db5b286df957af199ec94617f7",
		"f39fd6e51aad88f6f4ce6ab8827279cfffb92266",
	} {
		allocation, ok := genesis.Alloc[address]
		if !ok || allocation.Balance == "" || allocation.Balance == "0x0" {
			t.Fatalf("Preview Genesis allocation %s = %#v", address, allocation)
		}
	}
}
