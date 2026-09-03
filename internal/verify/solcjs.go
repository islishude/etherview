package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	SolcJSExecutorKind      = "node_solcjs_v1"
	TrustedSubprocessPolicy = "trusted_subprocess"

	solcJSRuntimeSchema       = "etherview-solcjs-sea-runtime-v1"
	solcJSSelfTestSchema      = "etherview-solcjs-sea-self-test-v1"
	solcJSNodeVersion         = "v26.8.1"
	solcJSWrapperPackage      = "solc@0.8.36"
	solcJSBundleBuilder       = "esbuild@0.28.2"
	solcJSRuntimeManifestName = "runtime-manifest.json"
	solcJSPrivateLibraries    = "lib"

	maxRuntimeManifestBytes = 2 << 20
	maxRuntimeManifestFiles = 4096
	maxRuntimeFileBytes     = int64(256 << 20)
	maxRuntimeTotalBytes    = int64(512 << 20)
)

var solcJSFixedExecArgv = []string{
	"--permission",
	"--disable-sigusr1",
	"--no-addons",
	"--no-global-search-paths",
	"--max-old-space-size=384",
}

type solcJSRuntimeManifest struct {
	Schema         string                            `json:"schema"`
	NodeVersion    string                            `json:"node_version"`
	WrapperPackage string                            `json:"wrapper_package"`
	BundleBuilder  string                            `json:"bundle_builder"`
	SEA            solcJSRuntimeManifestSEA          `json:"sea"`
	ELFInterpreter string                            `json:"elf_interpreter"`
	Dependencies   []solcJSRuntimeManifestDependency `json:"dependencies"`
	Files          []solcJSRuntimeManifestFile       `json:"files"`
}

type solcJSRuntimeManifestSEA struct {
	MainFormat        string   `json:"main_format"`
	UseSnapshot       bool     `json:"use_snapshot"`
	UseCodeCache      bool     `json:"use_code_cache"`
	ExecArgv          []string `json:"exec_argv"`
	ExecArgvExtension string   `json:"exec_argv_extension"`
}

type solcJSRuntimeManifestDependency struct {
	SONAME              string `json:"soname"`
	Provider            string `json:"provider"`
	Path                string `json:"path"`
	Package             string `json:"package"`
	PackageVersion      string `json:"package_version"`
	PackageArchitecture string `json:"package_architecture"`
	LicenseSHA256       string `json:"license_sha256"`
}

type solcJSRuntimeManifestFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SONAME string `json:"soname,omitempty"`
	SHA256 string `json:"sha256"`
}

type solcJSSelfTest struct {
	Schema         string   `json:"schema"`
	SEA            bool     `json:"sea"`
	NodeVersion    string   `json:"node_version"`
	WrapperPackage string   `json:"wrapper_package"`
	ExecArgv       []string `json:"exec_argv"`
	Permissions    string   `json:"permissions"`
	WriteDenied    bool     `json:"write_denied"`
}

// SolcJSCompiler executes checksum-authenticated official solc-js artifacts
// in one restricted, short-lived Node SEA process per Standard JSON input.
// The permission model is defense in depth for trusted compiler code, not a
// malicious-JavaScript sandbox.
type SolcJSCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	ExecutorPath   string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int

	mu             sync.RWMutex
	executorDigest [sha256.Size]byte
	ready          bool
}

func (compiler *SolcJSCompiler) paths() (string, string) {
	executorPath := compiler.ExecutorPath
	runtimeRoot := filepath.Dir(executorPath)
	return executorPath, runtimeRoot
}

func (compiler *SolcJSCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil || compiler.Catalog == nil || compiler.Cache == nil ||
		compiler.Cache.InstallLocker == nil {
		return errors.New("solc-js compiler runtime is incomplete")
	}
	executorPath, runtimeRoot := compiler.paths()
	digest, err := validateSolcJSRuntimeManifest(executorPath)
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
	output, err := compiler.run(selfTestContext, runtimeRoot, "", "", nil, true)
	if err != nil || validateSolcJSSelfTest(output) != nil {
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
	_, runtimeRoot := compiler.paths()
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
	executorPath, _ := compiler.paths()
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

	arguments := make([]string, 0, 5)
	if selfTest {
		arguments = append(arguments, "--self-test")
	} else {
		nodeOptions, err := solcJSArtifactNodeOptions(artifactPath)
		if err != nil {
			_ = cleanup()
			return nil, err
		}
		arguments = append(arguments, nodeOptions)
		arguments = append(arguments, "--compile", artifactPath, version)
	}
	command := exec.CommandContext(ctx, executorPath, arguments...)
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

func validateSolcJSSelfTest(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result solcJSSelfTest
	if err := decoder.Decode(&result); err != nil {
		return errors.New("decode solc-js SEA self-test")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("solc-js SEA self-test has trailing data")
	}
	expected := solcJSSelfTest{
		Schema: solcJSSelfTestSchema, SEA: true, NodeVersion: solcJSNodeVersion,
		WrapperPackage: solcJSWrapperPackage,
		ExecArgv:       append([]string(nil), solcJSFixedExecArgv...),
		Permissions:    "restricted", WriteDenied: true,
	}
	canonical, err := json.Marshal(result)
	expectedCanonical, expectedErr := json.Marshal(expected)
	if err != nil || expectedErr != nil || !bytes.Equal(raw, canonical) ||
		!bytes.Equal(canonical, expectedCanonical) {
		return errors.New("solc-js SEA self-test identity is invalid")
	}
	return nil
}

func solcJSArtifactNodeOptions(artifactPath string) (string, error) {
	if artifactPath == "" || !filepath.IsAbs(artifactPath) ||
		filepath.Clean(artifactPath) != artifactPath {
		return "", errors.New("solc-js compiler artifact path is unsafe")
	}
	var quoted strings.Builder
	quoted.WriteString(`--node-options=--allow-fs-read="`)
	for _, character := range artifactPath {
		if character < 0x20 || character == 0x7f || character == '*' || character == ',' {
			return "", errors.New("solc-js compiler artifact path is unsafe")
		}
		if character == '\\' || character == '"' {
			quoted.WriteByte('\\')
		}
		quoted.WriteRune(character)
	}
	quoted.WriteByte('"')
	return quoted.String(), nil
}

func validateSolcJSRuntimeManifest(executorPath string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if executorPath == "" || !filepath.IsAbs(executorPath) ||
		filepath.Clean(executorPath) != executorPath {
		return zero, errors.New("solc-js executor path must be absolute and clean")
	}
	runtimeRoot := filepath.Dir(executorPath)
	manifestPath := filepath.Join(runtimeRoot, solcJSRuntimeManifestName)
	libraryRoot := filepath.Join(runtimeRoot, solcJSPrivateLibraries)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() ||
		manifestInfo.Mode()&os.ModeSymlink != 0 ||
		manifestInfo.Mode().Perm()&0o222 != 0 ||
		manifestInfo.Size() < 1 ||
		manifestInfo.Size() > maxRuntimeManifestBytes {
		return zero, errors.New("solc-js runtime manifest is unsafe")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return zero, errors.New("read solc-js runtime manifest")
	}
	if int64(len(raw)) != manifestInfo.Size() {
		return zero, errors.New("solc-js runtime manifest exceeds configured bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest solcJSRuntimeManifest
	if err := decoder.Decode(&manifest); err != nil {
		return zero, errors.New("decode solc-js runtime manifest")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, errors.New("solc-js runtime manifest has trailing data")
	}
	if manifest.Schema != solcJSRuntimeSchema ||
		manifest.NodeVersion != solcJSNodeVersion ||
		manifest.WrapperPackage != solcJSWrapperPackage ||
		manifest.BundleBuilder != solcJSBundleBuilder ||
		manifest.SEA.MainFormat != "commonjs" ||
		manifest.SEA.UseSnapshot || manifest.SEA.UseCodeCache ||
		!slices.Equal(manifest.SEA.ExecArgv, solcJSFixedExecArgv) ||
		manifest.SEA.ExecArgvExtension != "cli" ||
		manifest.ELFInterpreter == "" || !filepath.IsAbs(manifest.ELFInterpreter) ||
		filepath.Clean(manifest.ELFInterpreter) != manifest.ELFInterpreter ||
		len(manifest.Dependencies) == 0 ||
		len(manifest.Dependencies) > maxRuntimeManifestFiles ||
		len(manifest.Files) < 1 ||
		len(manifest.Files) > maxRuntimeManifestFiles {
		return zero, errors.New("solc-js runtime manifest identity is invalid")
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil ||
		!bytes.Equal(raw, canonical.Bytes()) {
		return zero, errors.New("solc-js runtime manifest is not canonical")
	}
	for _, directory := range []string{runtimeRoot, libraryRoot} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm()&0o222 != 0 {
			return zero, errors.New("solc-js runtime directory is unsafe")
		}
	}
	runtimeDependencies := make(map[string]string)
	seenDependencies := make(map[string]struct{}, len(manifest.Dependencies))
	var previousSONAME string
	for _, dependency := range manifest.Dependencies {
		licenseDigest, digestErr := hex.DecodeString(dependency.LicenseSHA256)
		if dependency.SONAME == "" || strings.ContainsAny(dependency.SONAME, `/\\`) ||
			previousSONAME >= dependency.SONAME || dependency.Package == "" ||
			dependency.PackageVersion == "" || dependency.PackageArchitecture == "" ||
			dependency.LicenseSHA256 != strings.ToLower(dependency.LicenseSHA256) ||
			digestErr != nil || len(licenseDigest) != sha256.Size {
			return zero, errors.New("solc-js runtime dependency is invalid")
		}
		if _, duplicate := seenDependencies[dependency.SONAME]; duplicate {
			return zero, errors.New("solc-js runtime dependency is duplicated")
		}
		seenDependencies[dependency.SONAME] = struct{}{}
		previousSONAME = dependency.SONAME
		switch dependency.Provider {
		case "base":
			if !filepath.IsAbs(dependency.Path) || filepath.Clean(dependency.Path) != dependency.Path {
				return zero, errors.New("solc-js base dependency path is invalid")
			}
		case "runtime":
			expectedPath := filepath.ToSlash(filepath.Join(solcJSPrivateLibraries, dependency.SONAME))
			if dependency.Path != expectedPath {
				return zero, errors.New("solc-js private dependency path is invalid")
			}
			runtimeDependencies[dependency.SONAME] = dependency.Path
		default:
			return zero, errors.New("solc-js runtime dependency provider is invalid")
		}
	}

	seenPaths := make(map[string]struct{}, len(manifest.Files))
	seenRuntimeDependencies := make(map[string]struct{}, len(runtimeDependencies))
	var totalBytes int64
	var previousFilePath string
	for index, file := range manifest.Files {
		cleanPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path)))
		if file.Path == "" || filepath.IsAbs(file.Path) || cleanPath != file.Path ||
			file.Path == "." || file.Path == ".." || strings.HasPrefix(file.Path, "../") ||
			file.SHA256 != strings.ToLower(file.SHA256) {
			return zero, errors.New("solc-js runtime manifest file is invalid")
		}
		expectedDigest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(expectedDigest) != sha256.Size {
			return zero, errors.New("solc-js runtime manifest digest is invalid")
		}
		absolutePath := filepath.Join(runtimeRoot, filepath.FromSlash(file.Path))
		if !pathWithinRoot(runtimeRoot, absolutePath) {
			return zero, errors.New("solc-js runtime file escapes its root")
		}
		if _, duplicate := seenPaths[absolutePath]; duplicate {
			return zero, errors.New("solc-js runtime manifest path is duplicated")
		}
		seenPaths[absolutePath] = struct{}{}
		if index == 0 {
			if file.Path != filepath.Base(executorPath) || file.Kind != "executor" ||
				file.SONAME != "" || absolutePath != executorPath {
				return zero, errors.New("solc-js executor manifest entry is inconsistent")
			}
		} else {
			if file.Kind != "library" || file.SONAME == "" ||
				file.Path != filepath.ToSlash(filepath.Join(solcJSPrivateLibraries, file.SONAME)) ||
				previousFilePath >= file.Path || runtimeDependencies[file.SONAME] != file.Path {
				return zero, errors.New("solc-js private library entry is invalid")
			}
			seenRuntimeDependencies[file.SONAME] = struct{}{}
		}
		previousFilePath = file.Path
		info, err := os.Lstat(absolutePath)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm()&0o222 != 0 ||
			info.Size() > maxRuntimeFileBytes {
			return zero, errors.New("solc-js runtime file is unsafe")
		}
		if index == 0 && info.Mode().Perm()&0o111 == 0 {
			return zero, errors.New("solc-js executor is not executable")
		}
		if index > 0 && info.Mode().Perm()&0o111 != 0 {
			return zero, errors.New("solc-js private library is executable")
		}
		totalBytes += info.Size()
		if totalBytes > maxRuntimeTotalBytes {
			return zero, errors.New("solc-js runtime exceeds configured bounds")
		}
		actual, err := fileSHA256(absolutePath, maxRuntimeFileBytes)
		if err != nil || !bytes.Equal(actual[:], expectedDigest) {
			return zero, errors.New("solc-js runtime file checksum mismatch")
		}
	}
	if len(seenRuntimeDependencies) != len(runtimeDependencies) {
		return zero, errors.New("solc-js runtime manifest is missing a private dependency")
	}
	if err := verifyRuntimeTree(runtimeRoot, libraryRoot, manifestPath, seenPaths); err != nil {
		return zero, err
	}
	return sha256.Sum256(raw), nil
}

func verifyRuntimeTree(root, libraryRoot, manifestPath string, expected map[string]struct{}) error {
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
			if path != libraryRoot || info.Mode().Perm()&0o222 != 0 {
				return errors.New("solc-js runtime contains an unexpected directory")
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
