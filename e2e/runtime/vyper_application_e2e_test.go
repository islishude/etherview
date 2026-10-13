//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Assert production matrix identity before exercising actual locked PyPI downloads.
func configureVyperApplicationFixture(t *testing.T, h *harness) {
	t.Helper()
	_ = readVyperMatrixEvidence(t, h.root)
}

type vyperMatrixCase struct {
	Cases          int    `json:"cases"`
	FixturesSHA256 string `json:"fixtures_sha256"`
	PackageSHA256  string `json:"package_sha256"`
	SharedSHA256   string `json:"shared_sha256"`
	ExecutorSHA256 string `json:"executor_sha256"`
}

type vyperMatrixEvidence struct {
	NativeLinux    bool                       `json:"native_linux"`
	ImageID        string                     `json:"image_id"`
	HostSHA256     string                     `json:"host_sha256"`
	SharedRuntimes map[string]string          `json:"shared_runtimes"`
	Descriptors    map[string]string          `json:"descriptors"`
	Matrix         map[string]vyperMatrixCase `json:"matrix"`
	Monolith       bool                       `json:"monolith"`
	Split          bool                       `json:"split"`
}

func readVyperMatrixEvidence(t *testing.T, root string) vyperMatrixEvidence {
	t.Helper()
	var evidence vyperMatrixEvidence
	raw, err := os.ReadFile(filepath.Join(root, ".local/vyper-releases", "matrix-"+runtime.GOARCH+".json"))
	if err != nil {
		t.Fatalf("run the production Vyper matrix before E2E: %v", err)
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	image, err := exec.CommandContext(t.Context(), dockerCommand(), "image", "inspect", "--format", "{{.Id}}", valueOrDefault("IMAGE", "etherview:local")).Output()
	if err != nil {
		t.Fatal(err)
	}
	versions := applicationVyperVersions(t)
	if strings.TrimSpace(string(image)) != evidence.ImageID || len(evidence.Descriptors) != len(versions) || len(evidence.Matrix) != len(versions) {
		t.Fatal("incomplete or stale production Vyper matrix")
	}
	for _, version := range versions {
		if _, ok := evidence.Matrix[version]; !ok {
			t.Fatalf("missing Vyper matrix version %s", version)
		}
		if evidence.Descriptors["vyper-"+version+"-emscripten-wasm32.json"] == "" {
			t.Fatalf("missing Vyper descriptor %s", version)
		}
	}
	validateVyperRuntimeEvidence(t, root, evidence)
	return evidence
}

func validateVyperRuntimeEvidence(t *testing.T, root string, evidence vyperMatrixEvidence) {
	t.Helper()
	var policy map[string]struct {
		SharedSHA256 string   `json:"shared_sha256"`
		Versions     []string `json:"versions"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "compiler/vyper/wasm/runtime-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy) != 2 || len(evidence.SharedRuntimes) != len(policy) {
		t.Fatal("incomplete shared runtime evidence")
	}
	count := 0
	for id, spec := range policy {
		if evidence.SharedRuntimes[id] != spec.SharedSHA256 {
			t.Fatal("stale shared runtime evidence")
		}
		for _, version := range spec.Versions {
			if evidence.Matrix[version].SharedSHA256 != spec.SharedSHA256 {
				t.Fatalf("incorrect runtime evidence for %s", version)
			}
			count++
		}
	}
	if count != len(evidence.Matrix) {
		t.Fatal("incomplete runtime version evidence")
	}
}

func writeVyperReleaseAcceptance(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, ".local/vyper-releases")
	platform := "linux-" + runtime.GOARCH
	evidence := readVyperMatrixEvidence(t, root)
	for name, expected := range evidence.Descriptors {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != expected {
			t.Fatal("Vyper descriptor changed during E2E")
		}
	}
	// A native matrix alone cannot make a macOS-orchestrated E2E run native.
	evidence.NativeLinux = evidence.NativeLinux && runtime.GOOS == "linux"
	evidence.Monolith, evidence.Split = true, true
	raw, err := json.Marshal(map[string]vyperMatrixEvidence{platform: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "acceptance-"+runtime.GOARCH+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
