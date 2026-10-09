package verify

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVyperDynamicRequiresValidatedStartup(t *testing.T) {
	compiler := &VyperCompiler{Catalog: &CompilerCatalog{}}
	if _, err := compiler.Resolve(t.Context(), LanguageVyper, "0.4.3"); !errors.Is(err, ErrVyperRuntimeUnavailable) {
		t.Fatalf("resolved before startup: %v", err)
	}
	digest := sha256.Sum256([]byte("test"))
	provenance := CompilerProvenance{Kind: CompilerVyper, Digest: digest, ExecutorDigest: digest, ExecutorKind: VyperDynamicExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy, CatalogGeneration: 1, Platform: CompilerPlatformPythonWheel}
	if _, err := compiler.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, nil); !errors.Is(err, ErrVyperRuntimeUnavailable) {
		t.Fatalf("compiled before startup: %v", err)
	}
}

func TestVyperHostCannotChangeAfterStartup(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	compiler.PackagePath = ""
	if err := compiler.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := compiler.checkedHostIdentity(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(compiler.Path)
	makeRuntimeWritable(t, root)
	library := filepath.Join(root, "lib", "libatomic.so.1")
	if err := os.Chmod(library, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, []byte("different host dependency"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(library, 0444); err != nil {
		t.Fatal(err)
	}
	mutateTestManifest(t, root, func(m *solcJSRuntimeManifest) {
		m.Files[1] = testManifestFile(t, root, library, "library", "libatomic.so.1")
	})
	makeRuntimeReadOnly(t, root)
	// Even a self-consistent replacement must not be adopted by a running parent.
	if _, _, err := compiler.hostIdentity(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := compiler.checkedHostIdentity(); !errors.Is(err, ErrVyperRuntimeUnavailable) {
		t.Fatalf("accepted replacement host: %v", err)
	}
	if compiler.Ready() {
		t.Fatal("changed host remained ready")
	}
}

func TestVyperFailedStartupIsNotReady(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compiler.ValidateRuntime(ctx); err == nil {
		t.Fatal("cancelled startup passed")
	}
	if compiler.Ready() {
		t.Fatal("failed startup remained ready")
	}
}

func TestVyperSEARejectsPolicyChanges(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	args, err := compiler.wasmArguments([]string{"--self-test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range []string{"--allow-net", "--allow-fs-read=/", "--allow-fs-write=/", "--max-old-space-size=256", "--wasm-max-mem-pages=8192"} {
		t.Run(option, func(t *testing.T) {
			changed := append([]string(nil), args...)
			changed[0] += " " + option
			command := exec.CommandContext(t.Context(), compiler.Path, changed...)
			command.Env = []string{}
			output, err := command.CombinedOutput()
			if err == nil || !strings.HasPrefix(string(output), "compiler runtime failed\n") {
				t.Fatalf("policy accepted: %v %s", err, output)
			}
		})
	}
}

func TestVyperSEARejectsHostSources(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	raw, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret.vy", "/etc/passwd", "a/../../secret.vy", "a\\secret.vy"} {
		t.Run(path, func(t *testing.T) {
			input := strings.ReplaceAll(string(raw), "A.vy", strings.ReplaceAll(path, "\\", "\\\\"))
			if _, err := compiler.Compile(t.Context(), LanguageVyper, "0.4.3", []byte(input)); err == nil {
				t.Fatal("host source accepted")
			}
		})
	}
}

func TestVyperMissingSharedDependencyFailsClosed(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	if err := os.Chmod(compiler.SharedPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(compiler.SharedPath, "pycryptodome-3.21.0-cp36-abi3-pyodide_2025_0_wasm32.whl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(compiler.SharedPath, 0555); err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(t.Context(), LanguageVyper, "0.4.3", input); !errors.Is(err, ErrVyperRuntimeUnavailable) {
		t.Fatalf("missing dependency: %v", err)
	}
	if compiler.Ready() {
		t.Fatal("missing dependency remained ready")
	}
}
