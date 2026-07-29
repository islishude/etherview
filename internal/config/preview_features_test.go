package config

import (
	"os"
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
			PublicVerification bool   `yaml:"public_verification"`
			CompilerSandbox    string `yaml:"compiler_sandbox"`
		} `yaml:"security"`
		Verification struct {
			RunnerImage string `yaml:"runner_image"`
		} `yaml:"verification"`
	}
	if err := yaml.Unmarshal(data, &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Features.Verification || !preview.Security.PublicVerification {
		t.Fatal("Preview public verification must be enabled")
	}
	if preview.Security.CompilerSandbox != "container" {
		t.Fatalf("Preview compiler sandbox = %q, want container", preview.Security.CompilerSandbox)
	}
	if preview.Verification.RunnerImage != "" {
		t.Fatalf("Preview runner image must be injected by its preflight, got %q", preview.Verification.RunnerImage)
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
