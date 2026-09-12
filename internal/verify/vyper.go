package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	VyperCompilerVersion        = "0.4.3"
	VyperExecutorKind           = "etherview_vyper_v1"
	CompilerPlatformPythonWheel = "python-wheel"
	VyperCompilerSHA256         = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd"
	vyperDependencyLockSHA256   = "554dc6f7797a99df3d66f0d42a628bc1e48f11fd15231e1349f5948dccdb99d2"
	vyperRuntimeSchema          = "etherview-vyper-runtime-v2"
)

// A bundled helper cannot recover through a remote catalog refresh.
var ErrVyperRuntimeUnavailable = errors.New("vyper runtime unavailable")

type VyperCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	Version        string
	CompilerDigest [sha256.Size]byte
	ManifestDigest [sha256.Size]byte
	Path           string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	mu             sync.RWMutex
	identity       *vyperRuntimeIdentity
}
type vyperRuntimeIdentity struct{ compiler, executor [sha256.Size]byte }
type vyperRuntimeManifest struct {
	Schema         string            `json:"schema"`
	Python         string            `json:"python"`
	Vyper          string            `json:"vyper"`
	PyInstaller    string            `json:"pyinstaller"`
	Platform       string            `json:"platform"`
	CompilerSHA256 string            `json:"compiler_sha256"`
	LockSHA256     string            `json:"lock_sha256"`
	Dependencies   map[string]string `json:"dependencies"`
	Files          []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func (compiler *VyperCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil {
		return errors.New("vyper compiler is nil")
	}
	if compiler.Catalog != nil {
		if compiler.Cache == nil || compiler.Cache.InstallLocker == nil {
			return ErrVyperRuntimeUnavailable
		}
		return secureCompilerCacheRoot(compiler.Cache.Root)
	}
	compiler.markUnavailable()
	identity, err := compiler.validateHelper()
	if err != nil {
		return err
	}
	compiler.mu.Lock()
	compiler.identity = &identity
	compiler.mu.Unlock()
	output, err := compiler.run(ctx, []string{"--self-test"}, nil)
	if err != nil {
		compiler.markUnavailable()
		return errors.New("vyper helper self-test failed")
	}
	var response struct {
		Schema       string `json:"schema"`
		Version      string `json:"version"`
		Python       string `json:"python"`
		AccessDenied bool   `json:"access_denied"`
		Limits       bool   `json:"limits"`
	}
	if json.Unmarshal(output, &response) != nil || response.Schema != compiler.schema() || response.Version != compiler.version() || response.Python == "" || !response.AccessDenied || (runtime.GOOS == "linux" && !response.Limits) {
		compiler.markUnavailable()
		return errors.New("vyper helper self-test response is invalid")
	}
	return nil
}
func (compiler *VyperCompiler) markUnavailable() {
	compiler.mu.Lock()
	compiler.identity = nil
	compiler.mu.Unlock()
}
func (compiler *VyperCompiler) Ready() bool {
	if compiler == nil {
		return false
	}
	if compiler.Catalog != nil {
		return compiler.Cache != nil && compiler.Cache.InstallLocker != nil
	}
	_, ready := compiler.runtimeIdentity()
	return ready
}
func (compiler *VyperCompiler) CompilerAvailable(ctx context.Context) bool {
	if !compiler.Ready() {
		return false
	}
	if compiler.Catalog != nil {
		_, err := compiler.Catalog.Versions(ctx, LanguageVyper)
		return err == nil
	}
	return true
}
func (compiler *VyperCompiler) runtimeIdentity() (vyperRuntimeIdentity, bool) {
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	if compiler.identity == nil {
		return vyperRuntimeIdentity{}, false
	}
	return *compiler.identity, true
}
func (compiler *VyperCompiler) Resolve(ctx context.Context, language Language, version string) (CompilerProvenance, error) {
	if compiler != nil && compiler.Catalog != nil {
		return compiler.resolveDynamic(ctx, language, version)
	}
	if compiler == nil || language != LanguageVyper || normalizeCompilerVersion(version) != compiler.version() {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	identity, ready := compiler.runtimeIdentity()
	if !ready {
		return CompilerProvenance{}, ErrVyperRuntimeUnavailable
	}
	return CompilerProvenance{Kind: CompilerVyper, Digest: identity.compiler, ExecutorDigest: identity.executor, ExecutorKind: VyperExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy, Platform: CompilerPlatformPythonWheel}, nil
}
func (compiler *VyperCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	return compiler.Resolve(context.Background(), language, version)
}
func (compiler *VyperCompiler) Compile(ctx context.Context, language Language, version string, input []byte) ([]byte, error) {
	provenance, err := compiler.Resolve(ctx, language, version)
	if err != nil {
		return nil, err
	}
	return compiler.CompilePinned(ctx, language, version, provenance, input)
}
func (compiler *VyperCompiler) CompilePinned(ctx context.Context, language Language, version string, provenance CompilerProvenance, input []byte) ([]byte, error) {
	if compiler.Catalog != nil {
		return compiler.compileDynamic(ctx, language, version, provenance, input)
	}
	expected, err := compiler.Resolve(ctx, language, version)
	if err != nil {
		return nil, err
	}
	if !provenance.valid() || provenance != expected {
		return nil, ErrCompilerProvenanceConflict
	}
	return compiler.run(ctx, []string{
		"--compile", strconv.Itoa(compiler.maxInputBytes()), strconv.Itoa(compiler.maxOutputBytes()),
	}, input)
}
func (compiler *VyperCompiler) timeout() time.Duration {
	if compiler.Timeout > 0 {
		return compiler.Timeout
	}
	return defaultCompilerTimeout
}
func (compiler *VyperCompiler) maxInputBytes() int {
	if compiler.MaxInputBytes > 0 {
		return compiler.MaxInputBytes
	}
	return defaultCompilerInputBytes
}

func (compiler *VyperCompiler) maxOutputBytes() int {
	if compiler.MaxOutputBytes > 0 {
		return compiler.MaxOutputBytes
	}
	return defaultCompilerOutputBytes
}

func validateVyperHelper(path string) (vyperRuntimeIdentity, error) {
	return validateVyperTree(path, VyperCompilerVersion, VyperCompilerSHA256, [sha256.Size]byte{})
}
func validateVyperTree(path, version, compilerSHA string, expectedManifest [sha256.Size]byte) (vyperRuntimeIdentity, error) {
	var identity vyperRuntimeIdentity
	invalid := errors.New("vyper runtime manifest is invalid")
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "etherview-vyper" {
		return identity, invalid
	}
	root := filepath.Dir(path)
	manifestPath := filepath.Join(root, "runtime-manifest.json")
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 || info.Size() > maxRuntimeManifestBytes {
		return identity, invalid
	}
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		return identity, invalid
	}
	var manifest vyperRuntimeManifest
	if validateUniqueJSON(encoded) != nil || json.Unmarshal(encoded, &manifest) != nil || manifest.Vyper != version || manifest.CompilerSHA256 != compilerSHA || len(manifest.Files) == 0 || len(manifest.Files) > maxRuntimeManifestFiles {
		return identity, invalid
	}
	if expectedManifest != [sha256.Size]byte{} {
		if sha256.Sum256(encoded) != expectedManifest || manifest.Schema != vyperDynamicSchema || manifest.Platform != runtime.GOOS+"-"+runtime.GOARCH || !validVyperExecutable(path) {
			return identity, invalid
		}
	} else if manifest.Schema != vyperRuntimeSchema || manifest.Python != "3.13.15" || manifest.PyInstaller != "6.22.2" || manifest.LockSHA256 != vyperDependencyLockSHA256 {
		return identity, invalid
	}
	expected := map[string]string{}
	for _, file := range manifest.Files {
		if !fs.ValidPath(file.Path) || strings.Contains(file.Path, "\\") || file.Path == "runtime-manifest.json" || expected[file.Path] != "" || len(file.SHA256) != 64 {
			return identity, invalid
		}
		expected[file.Path] = file.SHA256
	}
	if expected["etherview-vyper"] == "" {
		return identity, invalid
	}
	var total int64
	seen := 0
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return invalid
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil || info.Mode().Perm()&0o222 != 0 {
				return invalid
			}
			return nil
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return invalid
		}
		relative = filepath.ToSlash(relative)
		if relative == "runtime-manifest.json" {
			return nil
		}
		stat, statErr := entry.Info()
		if statErr != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0o222 != 0 || stat.Size() > maxRuntimeFileBytes || expected[relative] == "" {
			return invalid
		}
		if relative == "etherview-vyper" && stat.Mode().Perm()&0o111 == 0 {
			return invalid
		}
		total += stat.Size()
		if total > maxRuntimeTotalBytes {
			return invalid
		}
		contents, readErr := os.ReadFile(current)
		if readErr != nil {
			return invalid
		}
		hash := sha256.Sum256(contents)
		if hex.EncodeToString(hash[:]) != expected[relative] {
			return invalid
		}
		seen++
		return nil
	})
	if err != nil || seen != len(expected) {
		return identity, invalid
	}
	compilerDigest, _ := hex.DecodeString(compilerSHA)
	copy(identity.compiler[:], compilerDigest)
	identity.executor = sha256.Sum256(encoded)
	return identity, nil
}

func (compiler *VyperCompiler) run(
	ctx context.Context,
	arguments []string,
	input []byte,
) ([]byte, error) {
	if len(input) > compiler.maxInputBytes() {
		return nil, errors.New("vyper compiler input exceeds size limit")
	}
	expected, ready := compiler.runtimeIdentity()
	current, err := compiler.validateHelper()
	if !ready || err != nil || current != expected {
		compiler.markUnavailable()
		return nil, errors.New("vyper helper identity changed")
	}
	temporaryDirectory, err := os.MkdirTemp("", "etherview-vyper-*")
	if err != nil {
		return nil, errors.New("create Vyper temporary directory")
	}
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return nil, errors.New("secure Vyper temporary directory")
	}
	cleanup := func() error {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			return ErrCompilerCleanup
		}
		return nil
	}
	runContext, cancel := context.WithTimeout(ctx, compiler.timeout())
	defer cancel()
	command := exec.CommandContext(runContext, compiler.Path, arguments...)
	command.Dir = temporaryDirectory
	command.Env = []string{
		"HOME=/nonexistent",
		"TMPDIR=" + temporaryDirectory,
		"LANG=C",
		"LC_ALL=C",
	}
	command.Stdin = bytes.NewReader(input)
	maximumOutput := compiler.maxOutputBytes()
	stdout, stderr := newLimitedBuffer(maximumOutput), newLimitedBuffer(1<<20)
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
	if cleanupErr := cleanup(); cleanupErr != nil {
		return nil, cleanupErr
	}
	current, identityErr := compiler.validateHelper()
	if identityErr != nil || current != expected {
		compiler.markUnavailable()
		return nil, errors.New("vyper helper identity changed")
	}
	if runErr != nil || lingering {
		if errors.Is(runErr, exec.ErrWaitDelay) {
			return nil, ErrCompilerCleanup
		}
		if runContext.Err() != nil {
			return nil, runContext.Err()
		}
		if string(stderr.Bytes()) == "compiler runtime invariant failed\n" {
			return nil, ErrCompilerRuntime
		}
		return nil, errors.New("vyper compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("vyper compiler output exceeds size limit")
	}
	output := append([]byte(nil), stdout.Bytes()...)
	if !json.Valid(output) {
		return nil, errors.New("vyper compiler returned invalid JSON")
	}
	return output, nil
}

func (compiler *VyperCompiler) version() string {
	if compiler.Version != "" {
		return compiler.Version
	}
	return VyperCompilerVersion
}
func (compiler *VyperCompiler) schema() string {
	if compiler.Version != "" {
		return vyperDynamicSchema
	}
	return vyperRuntimeSchema
}

func (compiler *VyperCompiler) resolveDynamic(ctx context.Context, language Language, version string) (CompilerProvenance, error) {
	if language != LanguageVyper || !stableVyperVersion(normalizeCompilerVersion(version)) {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	entry, err := compiler.Catalog.Lookup(ctx, LanguageVyper, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	_, artifact, err := compiler.Catalog.vyperArtifact(ctx, entry.GenerationID, entry.Version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	executor, err := decodeCatalogDigest(artifact.ManifestSHA256)
	if err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{Kind: CompilerVyper, Digest: entry.ArtifactSHA256, ExecutorDigest: executor, ExecutorKind: VyperDynamicExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy, Platform: CompilerPlatformPythonWheel, CatalogGeneration: entry.GenerationID}, nil
}
func (compiler *VyperCompiler) compileDynamic(ctx context.Context, language Language, version string, provenance CompilerProvenance, input []byte) ([]byte, error) {
	if language != LanguageVyper || !provenance.valid() || provenance.ExecutorKind != VyperDynamicExecutorKind {
		return nil, ErrCompilerProvenanceConflict
	}
	entry, artifact, err := compiler.Catalog.vyperArtifact(ctx, provenance.CatalogGeneration, normalizeCompilerVersion(version))
	if err != nil {
		return nil, err
	}
	executor, _ := decodeCatalogDigest(artifact.ManifestSHA256)
	if entry.ArtifactSHA256 != provenance.Digest || executor != provenance.ExecutorDigest {
		return nil, ErrCompilerProvenanceConflict
	}
	child, err := compiler.ensureRuntime(ctx, entry.Version, entry, artifact)
	if err != nil {
		return nil, err
	}
	if err := child.ValidateRuntime(ctx); err != nil {
		return nil, err
	}
	return child.run(ctx, []string{"--compile", strconv.Itoa(child.maxInputBytes()), strconv.Itoa(child.maxOutputBytes())}, input)
}
