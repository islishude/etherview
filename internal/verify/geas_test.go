package verify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGeasCompilerValidatesIdentityAndCompilesPinnedEntrypoint(t *testing.T) {
	helper := buildTestGeasHelper(t)
	compiler := &GeasCompiler{Path: helper, Timeout: 10 * time.Second}
	if err := compiler.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	provenance, err := compiler.Resolve(t.Context(), LanguageGeas, GeasCompilerVersion)
	if err != nil {
		t.Fatal(err)
	}
	if provenance.Kind != CompilerGeas || provenance.Platform != CompilerPlatformGoModule ||
		provenance.ExecutorKind != GeasExecutorKind || provenance.ExecutionPolicy != TrustedSubprocessPolicy ||
		provenance.CatalogGeneration != 0 || provenance.ArtifactURL != "" {
		t.Fatalf("provenance = %+v", provenance)
	}
	response, err := compiler.CompileGeasEntrypointPinned(
		t.Context(), GeasCompilerVersion, provenance,
		map[string]string{
			"system/main.eas":  "#include \"../common/value.eas\"\npush VALUE\n",
			"common/value.eas": "#define VALUE = 1\n",
			"unused.eas":       "push 2\n",
		},
		"system/main.eas",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Successful || response.Bytecode != "0x6001" || len(response.Sources) != 2 ||
		response.Sources[0] != "common/value.eas" || response.Sources[1] != "system/main.eas" {
		t.Fatalf("response = %#v", response)
	}

	conflicting := provenance
	conflicting.ExecutorDigest[0] ^= 0xff
	if _, err := compiler.CompileGeasEntrypointPinned(
		t.Context(), GeasCompilerVersion, conflicting,
		map[string]string{"main.eas": "push 1"}, "main.eas",
	); !errors.Is(err, ErrCompilerProvenanceConflict) {
		t.Fatalf("conflicting provenance error = %v", err)
	}
}

func TestGeasCompilerRejectsWritableHelperAndCancellation(t *testing.T) {
	helper := buildTestGeasHelper(t)
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := validateGeasHelper(helper); err == nil {
		t.Fatal("writable helper was accepted")
	}
	if err := os.Chmod(helper, 0o555); err != nil {
		t.Fatal(err)
	}
	compiler := &GeasCompiler{Path: helper, Timeout: 10 * time.Second}
	if err := compiler.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	provenance, err := compiler.Resolve(t.Context(), LanguageGeas, GeasCompilerVersion)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = compiler.CompileGeasEntrypointPinned(
		ctx, GeasCompilerVersion, provenance,
		map[string]string{"main.eas": "push 1"}, "main.eas",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled compilation error = %v", err)
	}
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = compiler.CompileGeasEntrypointPinned(
		t.Context(), GeasCompilerVersion, provenance,
		map[string]string{"main.eas": "push 1"}, "main.eas",
	)
	if err == nil {
		t.Fatal("post-validation helper mode change was not detected")
	}
	if compiler.Ready() {
		t.Fatal("compiler remained available after helper identity changed")
	}
}

func TestGeasPlatformDoesNotExtendSolcCatalogPlatforms(t *testing.T) {
	t.Parallel()
	if validCompilerPlatform(CompilerPlatformGoModule) {
		t.Fatal("Go module identity became an accepted solc catalog platform")
	}
	if provenance := testGeasProvenance(); !provenance.valid() {
		t.Fatalf("valid Geas provenance was rejected: %+v", provenance)
	}
}

func buildTestGeasHelper(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "etherview-geas-compiler")
	command := exec.Command("go", "build", "-trimpath", "-o", helper, "./cmd/etherview-geas-compiler")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Geas helper: %v: %s", err, output)
	}
	if err := os.Chmod(helper, 0o555); err != nil {
		t.Fatal(err)
	}
	return helper
}
