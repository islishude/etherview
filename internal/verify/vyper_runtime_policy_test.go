package verify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVyperRuntimePolicyCoverage(t *testing.T) {
	modern, legacy := 0, 0
	for version, id := range vyperVersionRuntime {
		if _, ok := VyperVersionCapabilities(version); !ok {
			t.Fatalf("unsupported policy version %s", version)
		}
		spec, ok := vyperRuntimeFor(version)
		if !ok || spec.ID != id {
			t.Fatal("policy selection mismatch")
		}
		switch id {
		case "modern":
			modern++
		case "legacy":
			legacy++
		default:
			t.Fatal("unknown runtime")
		}
	}
	if modern != 4 || legacy != 22 {
		t.Fatalf("coverage %d/%d", modern, legacy)
	}
	for _, v := range []string{"0.4.4", "0.5.0", "0.3.5", "latest"} {
		if _, ok := vyperRuntimeFor(v); ok {
			t.Fatalf("unknown version accepted %s", v)
		}
	}
}

func TestVyperLegacyMissingPreventsStartup(t *testing.T) {
	c := newVyperTestCompiler(t)
	legacy := filepath.Join(c.SharedPath, "legacy")
	if err := os.Chmod(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(legacy, "shared-manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateRuntime(t.Context()); err == nil || c.Ready() {
		t.Fatal("missing legacy runtime accepted")
	}
}

func TestVyperCrossRuntimePackageRejected(t *testing.T) {
	c := newVyperTestCompiler(t)
	c.Version = "0.3.10"
	// Keep the version, digest and directory valid so only the runtime metadata
	// distinguishes this package from one that could be installed for legacy.
	path := filepath.Join(c.PackagePath, "package-manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest vyperWASMManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = c.Version
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	c.ManifestDigest = sha256.Sum256(raw)
	if _, err := c.validateHelper(); !errors.Is(err, ErrVyperRuntimeUnavailable) {
		t.Fatalf("modern package accepted for legacy version: %v", err)
	}
}

func TestVyperBothRuntimesEnforceExecutionBounds(t *testing.T) {
	fixture := newVyperTestCompiler(t)
	for _, id := range []string{"modern", "legacy"} {
		t.Run(id, func(t *testing.T) {
			spec := vyperRuntimeSpecs[id]
			probe := &VyperCompiler{Path: fixture.Path, SharedPath: fixture.SharedPath, Version: spec.Version}
			identity, err := probe.validateHelper()
			if err != nil {
				t.Fatal(err)
			}
			probe.Timeout = 50 * time.Millisecond
			if _, err := probe.runExpected(t.Context(), []string{"--self-test"}, nil, identity); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout did not terminate child: %v", err)
			}
			probe.Timeout = 10 * time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			completed := make(chan error, 1)
			go func() {
				_, err := probe.runExpected(ctx, []string{"--self-test"}, nil, identity)
				completed <- err
			}()
			pid := waitForVyperChild(t, probe.Path)
			cancel()
			if err := <-completed; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation did not terminate child: %v", err)
			}
			process, err := os.FindProcess(pid)
			if err != nil || !compilerProcessGroupTerminated(process) {
				t.Fatal("cancelled child process group remains alive")
			}
			t.Cleanup(func() { _ = process.Release() })
			probe.MaxInputBytes = 16
			if _, err := probe.runExpected(t.Context(), []string{"--self-test"}, make([]byte, 17), identity); err == nil {
				t.Fatal("oversize input accepted")
			}
			// A fresh child must recover after both termination paths. Its self-test
			// verifies WASM memory, host and sibling reads, writes, net and spawn.
			if _, err := probe.runExpected(t.Context(), []string{"--self-test"}, nil, identity); err != nil {
				t.Fatalf("isolated runtime failed after cleanup: %v", err)
			}
		})
	}
}

func TestVyperRuntimeChangeDisablesParent(t *testing.T) {
	for _, id := range []string{"modern", "legacy"} {
		t.Run(id, func(t *testing.T) {
			compiler := newVyperTestCompiler(t)
			compiler.PackagePath = ""
			if err := compiler.ValidateRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(compiler.SharedPath, id, "shared-manifest.json")
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"python":"unexpected"}`), 0444); err != nil {
				t.Fatal(err)
			}
			if _, _, err := compiler.checkedHostIdentity("0.4.3"); !errors.Is(err, ErrVyperRuntimeUnavailable) {
				t.Fatalf("runtime replacement accepted: %v", err)
			}
			if compiler.Ready() {
				t.Fatal("changed runtime remained ready")
			}
		})
	}
}

func TestVyperRuntimeDirectoriesCannotBeExchanged(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	root := compiler.SharedPath
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, move := range [][2]string{{"modern", "swap"}, {"legacy", "modern"}, {"swap", "legacy"}} {
		if err := os.Rename(filepath.Join(root, move[0]), filepath.Join(root, move[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0555); err != nil {
		t.Fatal(err)
	}
	if err := compiler.ValidateRuntime(t.Context()); err == nil || compiler.Ready() {
		t.Fatal("exchanged runtime directories accepted")
	}
}

// Observe the actual SEA before cancelling, so cancellation cannot pass merely
// by occurring during the parent's manifest hashing before process creation.
func waitForVyperChild(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := exec.CommandContext(t.Context(), "ps", "-axo", "pid=,command=").Output()
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			if strings.Contains(line, path) && strings.Contains(line, "--vyper-self-test") {
				pid, err := strconv.Atoi(strings.Fields(line)[0])
				if err != nil {
					t.Fatal(err)
				}
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("SEA child was not observed before cancellation")
	return 0
}
