package config

import (
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
			IPFSGateway string `yaml:"ipfs_gateway"`
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
			NodePath       string            `yaml:"node_path"`
			WrapperPath    string            `yaml:"wrapper_path"`
			ManifestPath   string            `yaml:"manifest_path"`
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
		preview.Verification.NodePath != defaultVerificationNodePath ||
		preview.Verification.WrapperPath != defaultVerificationWrapperPath ||
		preview.Verification.ManifestPath != defaultVerificationManifestPath ||
		preview.Verification.CatalogURLs["solidity"] != "auto" {
		t.Fatalf(
			"Preview compiler cache=%q node=%q wrapper=%q manifest=%q catalog=%v",
			preview.Verification.CacheDirectory,
			preview.Verification.NodePath,
			preview.Verification.WrapperPath,
			preview.Verification.ManifestPath,
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
	if preview.Features.Sourcify {
		t.Fatal("Preview Sourcify must remain disabled")
	}
}
