package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	SolcJSExecutorKind      = "node_solcjs_v1"
	TrustedSubprocessPolicy = "trusted_subprocess"

	solcJSRuntimeSchema  = "etherview-solcjs-runtime-v1"
	solcJSNodeVersion    = "v26.7.0"
	solcJSWrapperPackage = "solc@0.8.36"
	solcJSHeapMiB        = 384
	solcJSSelfTestOutput = `{"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","permissions":"restricted"}`

	maxRuntimeManifestBytes = 2 << 20
	maxRuntimeManifestFiles = 4096
	maxRuntimeFileBytes     = int64(256 << 20)
	maxRuntimeTotalBytes    = int64(512 << 20)
)

type solcJSRuntimeManifest struct {
	Schema         string                      `json:"schema"`
	NodeVersion    string                      `json:"node_version"`
	WrapperPackage string                      `json:"wrapper_package"`
	Files          []solcJSRuntimeManifestFile `json:"files"`
}

type solcJSRuntimeManifestFile struct {
	Path        string `json:"path"`
	LogicalPath string `json:"logical_path"`
	SHA256      string `json:"sha256"`
}

// SolcJSCompiler executes checksum-authenticated official solc-js artifacts
// in one restricted, short-lived Node process per Standard JSON input.
// The permission model is defense in depth for trusted compiler code, not a
// malicious-JavaScript sandbox.
type SolcJSCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	NodePath       string
	WrapperPath    string
	ManifestPath   string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int

	mu             sync.RWMutex
	executorDigest [sha256.Size]byte
	ready          bool
}

func (compiler *SolcJSCompiler) paths() (string, string, string) {
	return compiler.NodePath, compiler.WrapperPath, compiler.ManifestPath
}

func (compiler *SolcJSCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil || compiler.Catalog == nil || compiler.Cache == nil {
		return errors.New("solc-js compiler runtime is incomplete")
	}
	nodePath, wrapperPath, manifestPath := compiler.paths()
	digest, runtimeRoot, err := validateSolcJSRuntimeManifest(
		nodePath, wrapperPath, manifestPath,
	)
	if err != nil {
		compiler.markUnavailable()
		return err
	}
	if err := secureCompilerCacheRoot(compiler.Cache.Root); err != nil {
		compiler.markUnavailable()
		return err
	}
	selfTestContext, cancel := context.WithTimeout(ctx, min(compiler.timeout(), 10*time.Second))
	defer cancel()
	output, err := compiler.run(
		selfTestContext, runtimeRoot, "", "", nil, true,
	)
	if err != nil || string(output) != solcJSSelfTestOutput {
		compiler.markUnavailable()
		return errors.New("solc-js runtime self-test failed")
	}
	compiler.mu.Lock()
	compiler.executorDigest = digest
	compiler.ready = true
	compiler.mu.Unlock()
	return nil
}

func (compiler *SolcJSCompiler) markUnavailable() {
	if compiler == nil {
		return
	}
	compiler.mu.Lock()
	compiler.ready = false
	compiler.executorDigest = [sha256.Size]byte{}
	compiler.mu.Unlock()
}

func (compiler *SolcJSCompiler) Ready() bool {
	if compiler == nil {
		return false
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	return compiler.ready
}

func (compiler *SolcJSCompiler) CompilerAvailable(ctx context.Context) bool {
	if !compiler.Ready() || compiler.Catalog == nil {
		return false
	}
	_, err := compiler.Catalog.Versions(ctx, LanguageSolidity)
	return err == nil
}

func (compiler *SolcJSCompiler) runtimeDigest() ([sha256.Size]byte, error) {
	if compiler == nil {
		return [sha256.Size]byte{}, errors.New("solc-js compiler is unavailable")
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	if !compiler.ready || compiler.executorDigest == [sha256.Size]byte{} {
		return [sha256.Size]byte{}, errors.New("solc-js compiler runtime is unavailable")
	}
	return compiler.executorDigest, nil
}

func (compiler *SolcJSCompiler) Resolve(
	ctx context.Context,
	language Language,
	version string,
) (CompilerProvenance, error) {
	if compiler == nil || compiler.Catalog == nil {
		return CompilerProvenance{}, errors.New("solc-js compiler is unavailable")
	}
	if language != LanguageSolidity && language != LanguageYul {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	executorDigest, err := compiler.runtimeDigest()
	if err != nil {
		return CompilerProvenance{}, err
	}
	entry, err := compiler.Catalog.Lookup(ctx, language, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	if entry.Platform != CompilerPlatformEmscriptenWASM32 {
		return CompilerProvenance{}, errors.New("solc-js compiler artifact platform is invalid")
	}
	return CompilerProvenance{
		Kind:              CompilerSolcJS,
		Digest:            entry.ArtifactSHA256,
		ExecutorDigest:    executorDigest,
		ExecutorKind:      SolcJSExecutorKind,
		ExecutionPolicy:   TrustedSubprocessPolicy,
		CatalogGeneration: entry.GenerationID,
		Platform:          entry.Platform,
		ArtifactURL:       entry.ArtifactURL,
		ArtifactMaxBytes:  entry.MaxBytes,
	}, nil
}

func (compiler *SolcJSCompiler) Provenance(
	language Language,
	version string,
) (CompilerProvenance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return compiler.Resolve(ctx, language, version)
}

func (compiler *SolcJSCompiler) Compile(
	ctx context.Context,
	language Language,
	version string,
	input []byte,
) ([]byte, error) {
	provenance, err := compiler.Resolve(ctx, language, version)
	if err != nil {
		return nil, err
	}
	return compiler.CompilePinned(ctx, language, version, provenance, input)
}

func (compiler *SolcJSCompiler) CompilePinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	input []byte,
) ([]byte, error) {
	if compiler == nil || compiler.Cache == nil ||
		(language != LanguageSolidity && language != LanguageYul) {
		return nil, errors.New("solc-js compiler request is invalid")
	}
	executorDigest, err := compiler.runtimeDigest()
	if err != nil {
		return nil, err
	}
	if !provenance.valid() || provenance.Kind != CompilerSolcJS ||
		provenance.ExecutorDigest != executorDigest ||
		provenance.ExecutorKind != SolcJSExecutorKind ||
		provenance.ExecutionPolicy != TrustedSubprocessPolicy ||
		provenance.Platform != CompilerPlatformEmscriptenWASM32 {
		return nil, errors.New("solc-js compiler provenance is invalid")
	}
	maxInput := compiler.MaxInputBytes
	if maxInput <= 0 {
		maxInput = defaultCompilerInputBytes
	}
	if len(input) == 0 || len(input) > maxInput {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	entryLanguage := language
	if entryLanguage == LanguageYul {
		entryLanguage = LanguageSolidity
	}
	artifactPath, err := compiler.Cache.EnsureCatalogEntry(ctx, CatalogEntry{
		GenerationID:   provenance.CatalogGeneration,
		Language:       entryLanguage,
		Version:        normalizeCompilerVersion(version),
		Platform:       provenance.Platform,
		ArtifactURL:    provenance.ArtifactURL,
		ArtifactSHA256: provenance.Digest,
		MaxBytes:       provenance.ArtifactMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	_, wrapperPath, _ := compiler.paths()
	runtimeRoot := filepath.Dir(wrapperPath)
	runContext, cancel := context.WithTimeout(ctx, compiler.timeout())
	defer cancel()
	return compiler.run(
		runContext,
		runtimeRoot,
		artifactPath,
		normalizeCompilerVersion(version),
		input,
		false,
	)
}

func (compiler *SolcJSCompiler) timeout() time.Duration {
	if compiler.Timeout > 0 {
		return compiler.Timeout
	}
	return defaultCompilerTimeout
}

func (compiler *SolcJSCompiler) run(
	ctx context.Context,
	runtimeRoot string,
	artifactPath string,
	version string,
	input []byte,
	selfTest bool,
) ([]byte, error) {
	nodePath, wrapperPath, _ := compiler.paths()
	temporaryDirectory, err := os.MkdirTemp("", "etherview-solcjs-*")
	if err != nil {
		return nil, errors.New("create solc-js temporary directory")
	}
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return nil, errors.New("secure solc-js temporary directory")
	}
	cleanup := func() error {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			return ErrCompilerCleanup
		}
		return nil
	}

	arguments := []string{
		"--permission",
		"--disable-sigusr1",
		"--no-addons",
		"--no-global-search-paths",
		fmt.Sprintf("--max-old-space-size=%d", solcJSHeapMiB),
		"--allow-fs-read=" + runtimeRoot,
	}
	if artifactPath != "" {
		arguments = append(arguments, "--allow-fs-read="+artifactPath)
	}
	arguments = append(arguments, wrapperPath)
	if selfTest {
		arguments = append(arguments, "--self-test")
	} else {
		arguments = append(arguments, "--compile", artifactPath, version)
	}
	command := exec.CommandContext(ctx, nodePath, arguments...)
	command.Dir = temporaryDirectory
	command.Env = []string{
		"HOME=/nonexistent",
		"TMPDIR=" + temporaryDirectory,
		"LD_LIBRARY_PATH=" + filepath.Join(runtimeRoot, "lib"),
		"LANG=C",
		"LC_ALL=C",
	}
	command.Stdin = bytes.NewReader(input)
	maxOutput := compiler.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultCompilerOutputBytes
	}
	stdout, stderr := newLimitedBuffer(maxOutput), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	configureCompilerProcess(command)
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return killCompilerProcessGroup(command.Process)
	}
	command.WaitDelay = 2 * time.Second
	runErr := command.Run()
	lingering := !compilerProcessGroupTerminated(command.Process)
	if lingering {
		_ = killCompilerProcessGroup(command.Process)
		deadline := time.Now().Add(2 * time.Second)
		for !compilerProcessGroupTerminated(command.Process) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !compilerProcessGroupTerminated(command.Process) {
			_ = cleanup()
			return nil, ErrCompilerCleanup
		}
	}
	cleanupErr := cleanup()
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	if runErr != nil || lingering {
		if errors.Is(runErr, exec.ErrWaitDelay) {
			return nil, ErrCompilerCleanup
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	if selfTest {
		return append([]byte(nil), stdout.Bytes()...), nil
	}
	output := append([]byte(nil), stdout.Bytes()...)
	if !json.Valid(output) {
		return nil, errors.New("compiler returned invalid JSON")
	}
	return output, nil
}

func validateSolcJSRuntimeManifest(
	nodePath string,
	wrapperPath string,
	manifestPath string,
) ([sha256.Size]byte, string, error) {
	var zero [sha256.Size]byte
	for _, path := range []string{nodePath, wrapperPath, manifestPath} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return zero, "", errors.New("solc-js runtime paths must be absolute and clean")
		}
	}
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() ||
		manifestInfo.Mode()&os.ModeSymlink != 0 ||
		manifestInfo.Mode().Perm()&0o222 != 0 ||
		manifestInfo.Size() < 1 ||
		manifestInfo.Size() > maxRuntimeManifestBytes {
		return zero, "", errors.New("solc-js runtime manifest is unsafe")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return zero, "", errors.New("read solc-js runtime manifest")
	}
	if int64(len(raw)) != manifestInfo.Size() {
		return zero, "", errors.New("solc-js runtime manifest exceeds configured bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest solcJSRuntimeManifest
	if err := decoder.Decode(&manifest); err != nil {
		return zero, "", errors.New("decode solc-js runtime manifest")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, "", errors.New("solc-js runtime manifest has trailing data")
	}
	if manifest.Schema != solcJSRuntimeSchema ||
		manifest.NodeVersion != solcJSNodeVersion ||
		manifest.WrapperPackage != solcJSWrapperPackage ||
		len(manifest.Files) < 4 ||
		len(manifest.Files) > maxRuntimeManifestFiles {
		return zero, "", errors.New("solc-js runtime manifest identity is invalid")
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil ||
		!bytes.Equal(raw, canonical.Bytes()) {
		return zero, "", errors.New("solc-js runtime manifest is not canonical")
	}
	runtimeRoot := filepath.Dir(wrapperPath)
	rootInfo, err := os.Lstat(runtimeRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 ||
		rootInfo.Mode().Perm()&0o222 != 0 {
		return zero, "", errors.New("solc-js runtime root is unsafe")
	}
	seenLogical := make(map[string]struct{}, len(manifest.Files))
	seenPaths := make(map[string]struct{}, len(manifest.Files))
	var totalBytes int64
	var previousRuntimeLogicalPath string
	for index, file := range manifest.Files {
		if file.Path == "" || !filepath.IsAbs(file.Path) ||
			filepath.Clean(file.Path) != file.Path ||
			file.LogicalPath == "" ||
			file.SHA256 != strings.ToLower(file.SHA256) {
			return zero, "", errors.New("solc-js runtime manifest file is invalid")
		}
		expectedDigest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(expectedDigest) != sha256.Size {
			return zero, "", errors.New("solc-js runtime manifest digest is invalid")
		}
		if _, duplicate := seenLogical[file.LogicalPath]; duplicate {
			return zero, "", errors.New("solc-js runtime manifest logical path is duplicated")
		}
		if _, duplicate := seenPaths[file.Path]; duplicate {
			return zero, "", errors.New("solc-js runtime manifest path is duplicated")
		}
		seenLogical[file.LogicalPath] = struct{}{}
		seenPaths[file.Path] = struct{}{}
		if file.LogicalPath == "node" {
			if index != 0 || file.Path != nodePath {
				return zero, "", errors.New("solc-js runtime Node path is inconsistent")
			}
		} else {
			if !strings.HasPrefix(file.LogicalPath, "runtime/") ||
				!pathWithinRoot(runtimeRoot, file.Path) {
				return zero, "", errors.New("solc-js runtime file escapes its root")
			}
			relative, err := filepath.Rel(runtimeRoot, file.Path)
			expectedLogicalPath := "runtime/" + filepath.ToSlash(relative)
			if err != nil || file.LogicalPath != expectedLogicalPath ||
				previousRuntimeLogicalPath >= file.LogicalPath {
				return zero, "", errors.New("solc-js runtime file order is invalid")
			}
			previousRuntimeLogicalPath = file.LogicalPath
		}
		info, err := os.Lstat(file.Path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm()&0o222 != 0 ||
			info.Size() > maxRuntimeFileBytes {
			return zero, "", errors.New("solc-js runtime file is unsafe")
		}
		totalBytes += info.Size()
		if totalBytes > maxRuntimeTotalBytes {
			return zero, "", errors.New("solc-js runtime exceeds configured bounds")
		}
		actual, err := fileSHA256(file.Path, maxRuntimeFileBytes)
		if err != nil || !bytes.Equal(actual[:], expectedDigest) {
			return zero, "", errors.New("solc-js runtime file checksum mismatch")
		}
	}
	for logical, expected := range map[string]string{
		"node":                      nodePath,
		"runtime/compile.mjs":       wrapperPath,
		"runtime/package.json":      filepath.Join(runtimeRoot, "package.json"),
		"runtime/package-lock.json": filepath.Join(runtimeRoot, "package-lock.json"),
	} {
		if _, ok := seenLogical[logical]; !ok {
			return zero, "", fmt.Errorf("solc-js runtime manifest is missing %s", logical)
		}
		if _, ok := seenPaths[expected]; !ok {
			return zero, "", fmt.Errorf("solc-js runtime manifest is missing %s", expected)
		}
	}
	if err := verifyRuntimeTree(runtimeRoot, manifestPath, seenPaths); err != nil {
		return zero, "", err
	}
	return sha256.Sum256(raw), runtimeRoot, nil
}

func verifyRuntimeTree(root, manifestPath string, expected map[string]struct{}) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk solc-js runtime")
		}
		if path == root || path == manifestPath {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return errors.New("inspect solc-js runtime")
		}
		if entry.IsDir() {
			if info.Mode().Perm()&0o222 != 0 {
				return errors.New("solc-js runtime directory is writable")
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("solc-js runtime contains an unsafe entry")
		}
		if _, ok := expected[path]; !ok {
			return errors.New("solc-js runtime contains an unmanifested file")
		}
		return nil
	})
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func fileSHA256(path string, maximum int64) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer file.Close() //nolint:errcheck
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(file, maximum+1))
	if err != nil || written > maximum {
		return digest, errors.New("hash runtime file")
	}
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}
