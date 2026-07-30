package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateSolcJSRuntimeManifestRejectsTampering(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		nodePath, wrapperPath, manifestPath := writeTestSolcJSRuntime(t)
		digest, root, err := validateSolcJSRuntimeManifest(
			nodePath, wrapperPath, manifestPath,
		)
		if err != nil {
			t.Fatalf("validate runtime: %v", err)
		}
		if digest == [sha256.Size]byte{} || root != filepath.Dir(wrapperPath) {
			t.Fatalf("digest=%x root=%q", digest, root)
		}
	})

	t.Run("runtime file", func(t *testing.T) {
		nodePath, wrapperPath, manifestPath := writeTestSolcJSRuntime(t)
		if err := os.Chmod(wrapperPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wrapperPath, []byte("tampered"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(wrapperPath, 0o444); err != nil {
			t.Fatal(err)
		}
		if _, _, err := validateSolcJSRuntimeManifest(
			nodePath, wrapperPath, manifestPath,
		); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("unexpected tamper result: %v", err)
		}
	})

	t.Run("writable manifest", func(t *testing.T) {
		nodePath, wrapperPath, manifestPath := writeTestSolcJSRuntime(t)
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := validateSolcJSRuntimeManifest(
			nodePath, wrapperPath, manifestPath,
		); err == nil || !strings.Contains(err.Error(), "manifest is unsafe") {
			t.Fatalf("unexpected writable manifest result: %v", err)
		}
	})

	t.Run("noncanonical manifest", func(t *testing.T) {
		nodePath, wrapperPath, manifestPath := writeTestSolcJSRuntime(t)
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
		raw = append([]byte(" "), raw...)
		if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(manifestPath, 0o444); err != nil {
			t.Fatal(err)
		}
		if _, _, err := validateSolcJSRuntimeManifest(
			nodePath, wrapperPath, manifestPath,
		); err == nil || !strings.Contains(err.Error(), "not canonical") {
			t.Fatalf("unexpected canonicalization result: %v", err)
		}
	})
}

func TestSolcJSWrapperCompilesExactLongVersionWithoutImports(t *testing.T) {
	nodePath, wrapperPath, artifactPath := localSolcJSRuntime(t)
	compiler := &SolcJSCompiler{
		NodePath:       nodePath,
		WrapperPath:    wrapperPath,
		MaxOutputBytes: 4 << 20,
	}
	input := []byte(`{
		"language":"Solidity",
		"sources":{"Contract.sol":{"content":"contract Contract { function answer() external pure returns (uint256) { return 42; } }"}},
		"settings":{"outputSelection":{"*":{"*":["abi","evm.bytecode.object"]}}}
	}`)
	output, err := compiler.run(
		context.Background(),
		filepath.Dir(wrapperPath),
		artifactPath,
		"0.8.36+commit.8a079791",
		input,
		false,
	)
	if err != nil {
		t.Fatalf("compile exact solc-js: %v", err)
	}
	var result struct {
		Contracts map[string]map[string]json.RawMessage `json:"contracts"`
		Errors    []struct {
			Severity string `json:"severity"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode compiler output: %v", err)
	}
	for _, compilerError := range result.Errors {
		if compilerError.Severity == "error" {
			t.Fatalf("unexpected compiler errors: %s", output)
		}
	}
	if len(result.Contracts["Contract.sol"]["Contract"]) == 0 {
		t.Fatalf("compiled contract is missing: %s", output)
	}

	if _, err := compiler.run(
		context.Background(),
		filepath.Dir(wrapperPath),
		artifactPath,
		"0.8.36+commit.deadbeef",
		input,
		false,
	); err == nil || err.Error() != "compiler failed" {
		t.Fatalf("unexpected version mismatch result: %v", err)
	}

	importInput := []byte(`{
		"language":"Solidity",
		"sources":{"Contract.sol":{"urls":["file:///etc/passwd"]}},
		"settings":{"outputSelection":{"*":{"*":["abi"]}}}
	}`)
	output, err = compiler.run(
		context.Background(),
		filepath.Dir(wrapperPath),
		artifactPath,
		"0.8.36+commit.8a079791",
		importInput,
		false,
	)
	if err != nil {
		t.Fatalf("compile rejected import input: %v", err)
	}
	if !bytes.Contains(output, []byte("File import callback not supported")) {
		t.Fatalf("compiler unexpectedly resolved an import: %s", output)
	}
}

func TestSolcJSRuntimePermissionSelfTestAndWriteDenial(t *testing.T) {
	nodePath, wrapperPath, _ := localSolcJSRuntime(t)
	compiler := &SolcJSCompiler{
		NodePath:       nodePath,
		WrapperPath:    wrapperPath,
		MaxOutputBytes: 1 << 20,
	}
	output, err := compiler.run(
		context.Background(),
		filepath.Dir(wrapperPath),
		"",
		"",
		nil,
		true,
	)
	if err != nil {
		t.Fatalf("runtime permission self-test: %v", err)
	}
	if string(output) != solcJSSelfTestOutput {
		t.Fatalf("self-test output=%q", output)
	}

	root := t.TempDir()
	deniedWrapper := filepath.Join(root, "denied.mjs")
	escapedPath := filepath.Join(root, "must-not-exist")
	script := fmt.Sprintf(
		`import { writeFileSync } from "node:fs"; writeFileSync(%q, "denied");`,
		escapedPath,
	)
	if err := os.WriteFile(deniedWrapper, []byte(script), 0o444); err != nil {
		t.Fatal(err)
	}
	compiler.WrapperPath = deniedWrapper
	if _, err := compiler.run(
		context.Background(), root, deniedWrapper, "0.8.36", []byte(`{}`), false,
	); err == nil || err.Error() != "compiler failed" {
		t.Fatalf("unexpected permission result: %v", err)
	}
	if _, err := os.Stat(escapedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("permission model wrote %q: %v", escapedPath, err)
	}
}

func TestSolcJSProcessBoundsAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX executables are used for process-boundary regression")
	}
	t.Run("output limit", func(t *testing.T) {
		fakeNode := writeFakeNode(t, "#!/bin/sh\nprintf '{\"output\":\"%0800d\"}' 0\n")
		compiler := fakeSolcJSCompiler(fakeNode)
		compiler.MaxOutputBytes = 128
		if _, err := compiler.run(
			context.Background(), filepath.Dir(fakeNode), fakeNode, "0.8.36",
			[]byte(`{}`), false,
		); err == nil || err.Error() != "compiler output exceeds size limit" {
			t.Fatalf("unexpected output-limit result: %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		fakeNode := writeFakeNode(t, "#!/bin/sh\nsleep 30\n")
		compiler := fakeSolcJSCompiler(fakeNode)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := compiler.run(
			ctx, filepath.Dir(fakeNode), fakeNode, "0.8.36", []byte(`{}`), false,
		); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected timeout result: %v", err)
		}
	})

	t.Run("cancellation removes private directory", func(t *testing.T) {
		root := t.TempDir()
		markerPath := filepath.Join(root, "cwd")
		fakeNode := writeFakeNode(
			t,
			fmt.Sprintf("#!/bin/sh\npwd > %s\nwhile :; do sleep 1; done\n", shellQuote(markerPath)),
		)
		compiler := fakeSolcJSCompiler(fakeNode)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := compiler.run(
				ctx, filepath.Dir(fakeNode), fakeNode, "0.8.36", []byte(`{}`), false,
			)
			result <- err
		}()
		waitForTestFile(t, markerPath)
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation result: %v", err)
		}
		raw, err := os.ReadFile(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		privateDirectory := strings.TrimSpace(string(raw))
		if _, err := os.Stat(privateDirectory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private compiler directory still exists: %q (%v)", privateDirectory, err)
		}
	})
}

func writeTestSolcJSRuntime(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(runtimeRoot, 0o755)
	})
	nodePath := filepath.Join(root, "node")
	wrapperPath := filepath.Join(runtimeRoot, "compile.mjs")
	packagePath := filepath.Join(runtimeRoot, "package.json")
	lockPath := filepath.Join(runtimeRoot, "package-lock.json")
	emptyPath := filepath.Join(runtimeRoot, "empty-runtime-file")
	for path, contents := range map[string]string{
		nodePath:    "#!/bin/sh\nexit 0\n",
		wrapperPath: "export {};\n",
		packagePath: `{"dependencies":{"solc":"0.8.36"}}`,
		lockPath:    `{"lockfileVersion":3}`,
		emptyPath:   "",
	} {
		mode := os.FileMode(0o444)
		if path == nodePath {
			mode = 0o555
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	files := []solcJSRuntimeManifestFile{
		testManifestFile(t, nodePath, "node"),
		testManifestFile(t, wrapperPath, "runtime/compile.mjs"),
		testManifestFile(t, emptyPath, "runtime/empty-runtime-file"),
		testManifestFile(t, lockPath, "runtime/package-lock.json"),
		testManifestFile(t, packagePath, "runtime/package.json"),
	}
	manifest := solcJSRuntimeManifest{
		Schema:         solcJSRuntimeSchema,
		NodeVersion:    solcJSNodeVersion,
		WrapperPackage: solcJSWrapperPackage,
		Files:          files,
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runtimeRoot, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, raw.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runtimeRoot, 0o555); err != nil {
		t.Fatal(err)
	}
	return nodePath, wrapperPath, manifestPath
}

func testManifestFile(t *testing.T, path, logicalPath string) solcJSRuntimeManifestFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return solcJSRuntimeManifestFile{
		Path:        path,
		LogicalPath: logicalPath,
		SHA256:      hex.EncodeToString(digest[:]),
	}
}

func localSolcJSRuntime(t *testing.T) (string, string, string) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	nodePath, err = filepath.Abs(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(nodePath, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != solcJSNodeVersion {
		t.Skipf("Node %s is required for the solc-js runtime regression", solcJSNodeVersion)
	}
	wrapperPath, err := filepath.Abs(filepath.Join("..", "..", "compiler", "compile.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	artifactPath, err := filepath.Abs(
		filepath.Join("..", "..", "compiler", "node_modules", "solc", "soljson.js"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{wrapperPath, artifactPath} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Skip("run make compiler-install before the solc-js runtime regression")
		}
	}
	return nodePath, wrapperPath, artifactPath
}

func writeFakeNode(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(path, []byte(script), 0o555); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeSolcJSCompiler(fakeNode string) *SolcJSCompiler {
	return &SolcJSCompiler{
		NodePath:       fakeNode,
		WrapperPath:    filepath.Join(filepath.Dir(fakeNode), "compile.mjs"),
		MaxOutputBytes: 1 << 20,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
