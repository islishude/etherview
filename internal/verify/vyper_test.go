package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestVyperManifestIntegrity(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	helper := filepath.Join(root, "etherview-vyper")
	data := []byte("fixture helper bytes")
	if err := os.WriteFile(helper, data, 0o555); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	manifest := map[string]any{"schema": vyperRuntimeSchema, "python": "3.13.15", "vyper": VyperCompilerVersion, "pyinstaller": "6.22.2", "compiler_sha256": VyperCompilerSHA256, "lock_sha256": vyperDependencyLockSHA256, "files": []map[string]string{{"path": "etherview-vyper", "sha256": hex.EncodeToString(digest[:])}}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), encoded, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := validateVyperHelper(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := validateVyperHelper(helper); err == nil {
		t.Fatal("accepted non-executable helper")
	}
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := validateVyperHelper(helper); err == nil {
		t.Fatal("accepted writable executable")
	}
	if err := os.WriteFile(helper, []byte("changed helper bytes"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := validateVyperHelper(helper); err == nil {
		t.Fatal("accepted changed helper bytes")
	}
}

func newVyperTestCompiler(t *testing.T) *VyperCompiler {
	t.Helper()
	path := os.Getenv("VYPER_EXECUTOR_TEST_PATH")
	if path == "" {
		path, _ = filepath.Abs("../../.local/vyper/runtime/etherview-vyper")
		if _, err := os.Stat(path); err != nil {
			t.Skip("built Vyper runtime required; run make compiler-install")
		}
	}
	compiler := &VyperCompiler{Path: path, Timeout: 10 * time.Second}
	if err := compiler.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	return compiler
}

func TestVyperRuntime(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	input := `{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256:\n    return 42\n"}},"settings":{"search_paths":["."],"optimize":"gas","outputSelection":{"A.vy":["abi","metadata","layout","evm.bytecode.object","evm.deployedBytecode.object","evm.methodIdentifiers","userdoc","devdoc"]}}}`
	first, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/compiler/vyper/plain.input.output.json")
	if err != nil || !equalJSONValue(first, fixture) {
		t.Fatalf("real compiler differs from pinned fixture: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("nondeterministic compiler output")
	}
	var result struct {
		Compiler  string                     `json:"compiler"`
		Contracts map[string]json.RawMessage `json:"contracts"`
	}
	if json.Unmarshal(first, &result) != nil || result.Compiler != "vyper-0.4.3" || len(result.Contracts) != 1 {
		t.Fatalf("invalid result: %s", first)
	}
	provenance, err := compiler.Resolve(context.Background(), LanguageVyper, VyperCompilerVersion)
	if err != nil {
		t.Fatal(err)
	}
	provenance.ExecutorDigest[0] ^= 1
	if _, err = compiler.CompilePinned(context.Background(), LanguageVyper, VyperCompilerVersion, provenance, []byte(input)); err != ErrCompilerProvenanceConflict {
		t.Fatalf("changed provenance: %v", err)
	}
	if _, err = compiler.Compile(context.Background(), LanguageVyper, "0.4.2", []byte(input)); err != ErrCompilerVersionUnavailable {
		t.Fatalf("wrong version: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = compiler.Compile(ctx, LanguageVyper, VyperCompilerVersion, []byte(input)); err == nil {
		t.Fatal("canceled compile succeeded")
	}
}

func TestVyperManifestRejectsUnsafeInputs(t *testing.T) {
	for _, path := range []string{"", "relative/etherview-vyper", filepath.Join(t.TempDir(), "etherview-vyper")} {
		if _, err := validateVyperHelper(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestVyperUnavailableRuntimeDoesNotRetryCatalog(t *testing.T) {
	compiler := &VyperCompiler{}
	_, err := compiler.Resolve(context.Background(), LanguageVyper, VyperCompilerVersion)
	if err != ErrVyperRuntimeUnavailable || transientCompilerError(err) {
		t.Fatalf("unavailable fixed runtime must terminate the claimed attempt: %v", err)
	}
}

func TestVyperRuntimeTimeoutCleansProcessAndCanCompileAgain(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	original, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Language string `json:"language"`
		Sources  map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(original, &document); err != nil {
		t.Fatal(err)
	}
	source := document.Sources["A.vy"]
	source.Content = strings.Repeat("@external\ndef slow() -> uint256:\n    return 1\n", 20000)
	document.Sources["A.vy"] = source
	large, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	compiler.Timeout = 50 * time.Millisecond
	if _, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, large); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded compile error=%v", err)
	}
	compiler.Timeout = 10 * time.Second
	if _, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, original); err != nil {
		t.Fatalf("compile after process cleanup: %v", err)
	}
}

func TestVyperRuntimeHonorsConfiguredInputLimit(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	compiler.MaxInputBytes = 16 << 20
	raw, err := json.Marshal(map[string]any{
		"language": "Vyper",
		"sources":  map[string]any{"A.vy": map[string]string{"content": "@external\ndef value() -> uint256:\n    return 42\n#" + strings.Repeat("x", (5<<20)+1024)}},
		"settings": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := PrepareVyperStandardJSON(raw, "A.vy", compiler.MaxInputBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) <= defaultCompilerInputBytes {
		t.Fatal("fixture must exceed the old hardcoded limit")
	}
	compiler.MaxInputBytes = len(input)
	output, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, input)
	if err != nil {
		t.Fatalf("compile at configured input boundary: %v", err)
	}
	var result struct {
		Contracts map[string]json.RawMessage `json:"contracts"`
	}
	if json.Unmarshal(output, &result) != nil || len(result.Contracts) != 1 {
		t.Fatal("large valid input did not compile")
	}
	compiler.MaxInputBytes--
	if _, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, input); err == nil {
		t.Fatal("input beyond configured boundary was accepted")
	}
	// A configured ceiling is not an allocation request: this exceeds the
	// Linux helper's address-space limit but the actual input is tiny.
	compiler.MaxInputBytes = 1 << 30
	small, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(context.Background(), LanguageVyper, VyperCompilerVersion, small); err != nil {
		t.Fatalf("small input with a large configured ceiling: %v", err)
	}
}

func TestVyperHelperRejectsInvalidLimits(t *testing.T) {
	compiler := newVyperTestCompiler(t)
	input, err := os.ReadFile("testdata/compiler/vyper/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--compile"},
		{"--compile", "0", "1024"},
		{"--compile", "-1", "1024"},
		{"--compile", "01", "1024"},
		{"--compile", "1024", "0"},
		{"--compile", "18446744073709551615", "1024"},
		{"--compile", strconv.Itoa(len(input) - 1), strconv.Itoa(defaultCompilerOutputBytes)},
		{"--compile", strconv.Itoa(len(input)), "1"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, compiler.Path, args...)
			command.Env = []string{}
			command.Stdin = strings.NewReader(string(input))
			output, err := command.CombinedOutput()
			if err == nil || string(output) != "compiler runtime failed\n" {
				t.Fatalf("invalid/ exceeded limits: error=%v output=%s", err, output)
			}
		})
	}
}
