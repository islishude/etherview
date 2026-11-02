package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type sourceManifest struct {
	Fingerprints []sourceFingerprint `json:"fingerprints"`
	Deployments  []deployment        `json:"deployments"`
}

type sourceFingerprint struct {
	Name             string `json:"name"`
	Variant          string `json:"variant"`
	Version          string `json:"version"`
	DeploymentType   string `json:"deploymentType"`
	RuntimeBytecode  string `json:"runtimeBytecode"`
	SourceRepository string `json:"sourceRepository"`
	SourceTag        string `json:"sourceTag"`
	SourceArtifact   string `json:"sourceArtifact"`
	CompilerVersion  string `json:"compilerVersion,omitempty"`
	PackageIntegrity string `json:"packageIntegrity,omitempty"`
}

type generatedManifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	Fingerprints  []fingerprint `json:"fingerprints"`
	Deployments   []deployment  `json:"deployments"`
}

type fingerprint struct {
	Name             string `json:"name"`
	Variant          string `json:"variant"`
	Version          string `json:"version"`
	DeploymentType   string `json:"deploymentType"`
	RuntimeCodeHash  string `json:"runtimeCodeHash"`
	RuntimeCodeBytes int    `json:"runtimeCodeBytes"`
	SourceRepository string `json:"sourceRepository"`
	SourceTag        string `json:"sourceTag"`
	SourceArtifact   string `json:"sourceArtifact"`
	CompilerVersion  string `json:"compilerVersion,omitempty"`
	PackageIntegrity string `json:"packageIntegrity,omitempty"`
}

type deployment struct {
	ChainID          string `json:"chainId"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	DeploymentType   string `json:"deploymentType"`
	Address          string `json:"address"`
	CodeHash         string `json:"codeHash"`
	ProxyIndexed     *bool  `json:"proxyIndexed,omitempty"`
	SourceRepository string `json:"sourceRepository"`
	SourceTag        string `json:"sourceTag"`
	SourceArtifact   string `json:"sourceArtifact"`
}

func main() {
	inputPath := flag.String("input", "", "source manifest path")
	outputPath := flag.String("output", "", "generated manifest path")
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		fatal(errors.New("safe manifest requires -input and -output"))
	}
	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fatal(fmt.Errorf("read source manifest: %w", err))
	}
	var source sourceManifest
	if err := json.Unmarshal(input, &source); err != nil {
		fatal(fmt.Errorf("decode source manifest: %w", err))
	}
	generated := generatedManifest{SchemaVersion: 1, Deployments: append([]deployment(nil), source.Deployments...)}
	seen := make(map[string]struct{}, len(source.Fingerprints))
	for _, item := range source.Fingerprints {
		runtime, err := decodeHex(item.RuntimeBytecode)
		if err != nil || len(runtime) == 0 {
			fatal(fmt.Errorf("decode %s %s runtime: %w", item.Name, item.Version, err))
		}
		hash := crypto.Keccak256Hash(runtime).Hex()
		if _, exists := seen[hash]; exists {
			fatal(fmt.Errorf("duplicate runtime code hash %s", hash))
		}
		seen[hash] = struct{}{}
		if item.Name == "" || item.Variant == "" || item.Version == "" || item.DeploymentType == "" ||
			item.SourceRepository == "" || item.SourceTag == "" || item.SourceArtifact == "" {
			fatal(errors.New("safe fingerprint source is incomplete"))
		}
		generated.Fingerprints = append(generated.Fingerprints, fingerprint{
			Name: item.Name, Variant: item.Variant, Version: item.Version,
			DeploymentType: item.DeploymentType, RuntimeCodeHash: hash,
			RuntimeCodeBytes: len(runtime), SourceRepository: item.SourceRepository,
			SourceTag: item.SourceTag, SourceArtifact: item.SourceArtifact,
			CompilerVersion: item.CompilerVersion, PackageIntegrity: item.PackageIntegrity,
		})
	}
	for _, item := range generated.Deployments {
		if item.ChainID == "" || item.Name == "" || item.Version == "" || item.DeploymentType == "" ||
			!common.IsHexAddress(item.Address) || item.SourceRepository == "" || item.SourceTag == "" ||
			item.SourceArtifact == "" {
			fatal(fmt.Errorf("safe deployment %s %s is incomplete", item.Name, item.Version))
		}
		if item.Kind != "singleton" && item.Kind != "factory" {
			fatal(fmt.Errorf("safe deployment %s has unsupported kind %s", item.Name, item.Kind))
		}
		codeHash, err := decodeHex(item.CodeHash)
		if err != nil || len(codeHash) != common.HashLength {
			fatal(fmt.Errorf("safe deployment %s has invalid code hash", item.Name))
		}
		if item.Kind == "factory" && item.ProxyIndexed == nil {
			fatal(fmt.Errorf("safe factory %s lacks event layout", item.Name))
		}
	}
	slices.SortFunc(generated.Fingerprints, func(left, right fingerprint) int {
		return strings.Compare(left.Version+left.DeploymentType+left.Name, right.Version+right.DeploymentType+right.Name)
	})
	slices.SortFunc(generated.Deployments, func(left, right deployment) int {
		return strings.Compare(left.ChainID+left.Kind+left.Version+left.DeploymentType+left.Name,
			right.ChainID+right.Kind+right.Version+right.DeploymentType+right.Name)
	})
	encoded, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		fatal(fmt.Errorf("encode generated manifest: %w", err))
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
		fatal(fmt.Errorf("write generated manifest: %w", err))
	}
}

func decodeHex(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return nil, errors.New("value is not even-length 0x-prefixed hex")
	}
	return hex.DecodeString(value[2:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
