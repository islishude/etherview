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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateSolcJSRuntimeManifest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		executorPath, _ := writeTestSolcJSRuntime(t)
		digest, err := validateSolcJSRuntimeManifest(executorPath)
		if err != nil {
			t.Fatalf("validate runtime: %v", err)
		}
		if digest == [sha256.Size]byte{} {
			t.Fatal("runtime manifest digest is empty")
		}
	})

	t.Run("executor tamper", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		makeRuntimeWritable(t, root)
		if err := os.Chmod(executorPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(executorPath, []byte("tampered"), 0o555); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(executorPath, 0o555); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("unexpected tamper result: %v", err)
		}
	})

	t.Run("writable manifest", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		manifestPath := filepath.Join(root, solcJSRuntimeManifestName)
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "manifest is unsafe") {
			t.Fatalf("unexpected writable manifest result: %v", err)
		}
	})

	t.Run("configured executable mismatch", func(t *testing.T) {
		executorPath, _ := writeTestSolcJSRuntime(t)
		if _, err := validateSolcJSRuntimeManifest(executorPath + "-other"); err == nil ||
			!strings.Contains(err.Error(), "executor manifest entry is inconsistent") {
			t.Fatalf("unexpected executable mismatch result: %v", err)
		}
	})

	t.Run("noncanonical manifest", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		manifestPath := filepath.Join(root, solcJSRuntimeManifestName)
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		makeRuntimeWritable(t, root)
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, append([]byte(" "), raw...), 0o444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(manifestPath, 0o444); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "not canonical") {
			t.Fatalf("unexpected canonicalization result: %v", err)
		}
	})

	t.Run("unmanifested file", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		makeRuntimeWritable(t, root)
		if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("x"), 0o444); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "unmanifested file") {
			t.Fatalf("unexpected complete-tree result: %v", err)
		}
	})

	t.Run("path escape", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		mutateTestManifest(t, root, func(manifest *solcJSRuntimeManifest) {
			manifest.Files[1].Path = "../libatomic.so.1"
		})
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "manifest file is invalid") {
			t.Fatalf("unexpected path-escape result: %v", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		libraryPath := filepath.Join(root, "lib", "libatomic.so.1")
		makeRuntimeWritable(t, root)
		if err := os.Remove(libraryPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(executorPath, libraryPath); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "runtime file is unsafe") {
			t.Fatalf("unexpected symbolic-link result: %v", err)
		}
	})

	t.Run("unknown manifest field", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		manifestPath := filepath.Join(root, solcJSRuntimeManifestName)
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.Replace(raw, []byte(`{"schema":`), []byte(`{"unknown":true,"schema":`), 1)
		makeRuntimeWritable(t, root)
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, raw, 0o444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(manifestPath, 0o444); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "decode solc-js runtime manifest") {
			t.Fatalf("unexpected unknown-field result: %v", err)
		}
	})

	t.Run("duplicated dependency", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		mutateTestManifest(t, root, func(manifest *solcJSRuntimeManifest) {
			manifest.Dependencies = append(
				manifest.Dependencies,
				manifest.Dependencies[len(manifest.Dependencies)-1],
			)
		})
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "dependency is invalid") {
			t.Fatalf("unexpected duplicate-dependency result: %v", err)
		}
	})

	t.Run("missing private dependency file", func(t *testing.T) {
		executorPath, root := writeTestSolcJSRuntime(t)
		makeRuntimeWritable(t, root)
		if err := os.Remove(filepath.Join(root, "lib", "libatomic.so.1")); err != nil {
			t.Fatal(err)
		}
		makeRuntimeReadOnly(t, root)
		if _, err := validateSolcJSRuntimeManifest(executorPath); err == nil ||
			!strings.Contains(err.Error(), "runtime file is unsafe") {
			t.Fatalf("unexpected missing-library result: %v", err)
		}
	})
}

func TestSolcJSCompilerRequiresExplicitRelocatableExecutor(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		compiler := &SolcJSCompiler{
			Catalog: &CompilerCatalog{},
			Cache: &CompilerCache{
				Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker,
			},
		}
		if err := compiler.ValidateRuntime(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "executor path must be absolute and clean") {
			t.Fatalf("unexpected missing runtime path result: %v", err)
		}
	})

	t.Run("relocated runtime", func(t *testing.T) {
		executorPath, _ := writeTestSolcJSRuntime(t)
		compiler := &SolcJSCompiler{
			Catalog: &CompilerCatalog{},
			Cache: &CompilerCache{
				Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker,
			},
			ExecutorPath:   executorPath,
			Timeout:        time.Second,
			MaxOutputBytes: 1 << 20,
		}
		if err := compiler.ValidateRuntime(context.Background()); err != nil {
			t.Fatalf("validate relocated solc-js runtime: %v", err)
		}
		if !compiler.Ready() {
			t.Fatal("relocated solc-js runtime was not marked ready")
		}
	})
}

func TestSolcJSArtifactNodeOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), `compiler "quoted" path.js`)
	got, err := solcJSArtifactNodeOptions(path)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path)
	want := `--node-options=--allow-fs-read="` + wantPath + `"`
	if got != want {
		t.Fatalf("node options = %q, want %q", got, want)
	}
	for _, invalid := range []string{
		"relative.js",
		filepath.Join(t.TempDir(), "compiler*.js"),
		filepath.Join(t.TempDir(), "compiler,other.js"),
		filepath.Join(t.TempDir(), "compiler\nother.js"),
	} {
		if _, err := solcJSArtifactNodeOptions(invalid); err == nil {
			t.Fatalf("unsafe artifact path passed: %q", invalid)
		}
	}
}

func TestValidateSolcJSSelfTest(t *testing.T) {
	valid := []byte(`{"schema":"etherview-solcjs-sea-self-test-v1","sea":true,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true}`)
	if err := validateSolcJSSelfTest(valid); err != nil {
		t.Fatalf("valid self-test failed: %v", err)
	}
	for _, invalid := range [][]byte{
		append(append([]byte(nil), valid...), '\n'),
		[]byte(`{"schema":"etherview-solcjs-sea-self-test-v1","sea":false,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true}`),
		[]byte(`{"schema":"etherview-solcjs-sea-self-test-v1","sea":true,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true,"extra":true}`),
	} {
		if err := validateSolcJSSelfTest(invalid); err == nil {
			t.Fatalf("invalid self-test passed: %s", invalid)
		}
	}
}

func TestSolcJSCompileInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX executables are used for process-boundary regression")
	}
	markerRoot := t.TempDir()
	argumentsPath := filepath.Join(markerRoot, "arguments")
	environmentPath := filepath.Join(markerRoot, "library-path")
	executor := writeFakeExecutor(t, fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nprintf '%%s' \"$LD_LIBRARY_PATH\" > %s\nprintf '{}'\n",
		shellQuote(argumentsPath), shellQuote(environmentPath),
	))
	compiler := fakeSolcJSCompiler(executor)
	artifactPath := filepath.Join(markerRoot, "compiler artifact.js")
	if err := os.WriteFile(artifactPath, []byte("fixture"), 0o400); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Dir(executor)
	if _, err := compiler.run(
		context.Background(), runtimeRoot, artifactPath,
		"0.8.36+commit.8a079791", []byte(`{}`), false,
	); err != nil {
		t.Fatalf("run fake SEA: %v", err)
	}
	rawArguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	wantOptions, err := solcJSArtifactNodeOptions(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := strings.Join([]string{
		wantOptions, "--compile", artifactPath, "0.8.36+commit.8a079791", "",
	}, "\n")
	if string(rawArguments) != wantArguments {
		t.Fatalf("SEA arguments = %q, want %q", rawArguments, wantArguments)
	}
	rawEnvironment, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(rawEnvironment), filepath.Join(runtimeRoot, "lib"); got != want {
		t.Fatalf("LD_LIBRARY_PATH = %q, want %q", got, want)
	}
}

func TestSolcJSProcessBoundsAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX executables are used for process-boundary regression")
	}
	t.Run("output limit", func(t *testing.T) {
		fakeExecutor := writeFakeExecutor(t, "#!/bin/sh\nprintf '{\"output\":\"%0800d\"}' 0\n")
		compiler := fakeSolcJSCompiler(fakeExecutor)
		compiler.MaxOutputBytes = 128
		if _, err := compiler.run(
			context.Background(), filepath.Dir(fakeExecutor), fakeExecutor, "0.8.36",
			[]byte(`{}`), false,
		); err == nil || err.Error() != "compiler output exceeds size limit" {
			t.Fatalf("unexpected output-limit result: %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		fakeExecutor := writeFakeExecutor(t, "#!/bin/sh\n/bin/sleep 30\n")
		compiler := fakeSolcJSCompiler(fakeExecutor)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := compiler.run(
			ctx, filepath.Dir(fakeExecutor), fakeExecutor, "0.8.36", []byte(`{}`), false,
		); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected timeout result: %v", err)
		}
	})

	t.Run("cancellation removes private directory", func(t *testing.T) {
		root := t.TempDir()
		markerPath := filepath.Join(root, "cwd")
		fakeExecutor := writeFakeExecutor(
			t,
			fmt.Sprintf("#!/bin/sh\npwd > %s\nwhile :; do /bin/sleep 1; done\n", shellQuote(markerPath)),
		)
		compiler := fakeSolcJSCompiler(fakeExecutor)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := compiler.run(
				ctx, filepath.Dir(fakeExecutor), fakeExecutor, "0.8.36", []byte(`{}`), false,
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

func writeTestSolcJSRuntime(t *testing.T) (string, string) {
	t.Helper()
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	libraryRoot := filepath.Join(runtimeRoot, "lib")
	if err := os.MkdirAll(libraryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(runtimeRoot, 0o755)
		_ = os.Chmod(libraryRoot, 0o755)
	})
	executorPath := filepath.Join(runtimeRoot, "etherview-solcjs")
	libraryPath := filepath.Join(libraryRoot, "libatomic.so.1")
	selfTest := `{"schema":"etherview-solcjs-sea-self-test-v1","sea":true,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true}`
	if err := os.WriteFile(
		executorPath, []byte("#!/bin/sh\nprintf '%s' '"+selfTest+"'\n"), 0o555,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libraryPath, []byte("library"), 0o444); err != nil {
		t.Fatal(err)
	}
	manifest := solcJSRuntimeManifest{
		Schema: solcJSRuntimeSchema, NodeVersion: solcJSNodeVersion,
		WrapperPackage: solcJSWrapperPackage, BundleBuilder: solcJSBundleBuilder,
		SEA: solcJSRuntimeManifestSEA{
			MainFormat: "commonjs", ExecArgv: append([]string(nil), solcJSFixedExecArgv...),
			ExecArgvExtension: "cli",
		},
		ELFInterpreter: "/lib/ld-test.so.1",
		Dependencies: []solcJSRuntimeManifestDependency{
			{
				SONAME: "libatomic.so.1", Provider: "runtime", Path: "lib/libatomic.so.1",
				Package: "libatomic1", PackageVersion: "1", PackageArchitecture: "test",
				LicenseSHA256: strings.Repeat("1", sha256.Size*2),
			},
			{
				SONAME: "libc.so.6", Provider: "base", Path: "/lib/libc.so.6",
				Package: "libc6", PackageVersion: "1", PackageArchitecture: "test",
				LicenseSHA256: strings.Repeat("2", sha256.Size*2),
			},
		},
		Files: []solcJSRuntimeManifestFile{
			testManifestFile(t, runtimeRoot, executorPath, "executor", ""),
			testManifestFile(t, runtimeRoot, libraryPath, "library", "libatomic.so.1"),
		},
	}
	writeTestManifest(t, runtimeRoot, manifest)
	makeRuntimeReadOnly(t, runtimeRoot)
	return executorPath, runtimeRoot
}

func testManifestFile(
	t *testing.T,
	root string,
	path string,
	kind string,
	soname string,
) solcJSRuntimeManifestFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return solcJSRuntimeManifestFile{
		Path: filepath.ToSlash(relativePath), Kind: kind, SONAME: soname,
		SHA256: hex.EncodeToString(digest[:]),
	}
}

func mutateTestManifest(
	t *testing.T,
	root string,
	mutate func(*solcJSRuntimeManifest),
) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, solcJSRuntimeManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest solcJSRuntimeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	makeRuntimeWritable(t, root)
	writeTestManifest(t, root, manifest)
	makeRuntimeReadOnly(t, root)
}

func writeTestManifest(t *testing.T, root string, manifest solcJSRuntimeManifest) {
	t.Helper()
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, solcJSRuntimeManifestName)
	if _, err := os.Stat(manifestPath); err == nil {
		if err := os.Chmod(manifestPath, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(manifestPath, raw.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o444); err != nil {
		t.Fatal(err)
	}
}

func makeRuntimeWritable(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func makeRuntimeReadOnly(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(root, "lib"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
}

func writeFakeExecutor(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "etherview-solcjs")
	if err := os.WriteFile(path, []byte(script), 0o555); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeSolcJSCompiler(fakeExecutor string) *SolcJSCompiler {
	return &SolcJSCompiler{
		ExecutorPath: fakeExecutor, MaxOutputBytes: 1 << 20,
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
