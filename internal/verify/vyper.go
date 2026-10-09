package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

const (
	VyperCompilerVersion        = "0.4.3"
	VyperExecutorKind           = "etherview_vyper_v1"
	CompilerPlatformPythonWheel = "python-wheel"
	VyperCompilerSHA256         = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd"
)

// A bundled helper cannot recover through a remote catalog refresh.
var ErrVyperRuntimeUnavailable = errors.New("vyper runtime unavailable")

type VyperCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	Version        string
	CompilerDigest [sha256.Size]byte
	ManifestDigest [sha256.Size]byte
	PackagePath    string
	SharedPath     string
	Path           string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	mu             sync.RWMutex
	identity       *vyperRuntimeIdentity
}
type vyperRuntimeIdentity struct{ compiler, executor [sha256.Size]byte }

func (compiler *VyperCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil {
		return errors.New("vyper compiler is nil")
	}
	compiler.markUnavailable()
	if compiler.Catalog != nil {
		if compiler.PackagePath != "" || compiler.Cache == nil || compiler.Cache.InstallLocker == nil {
			return ErrVyperRuntimeUnavailable
		}
		if err := secureCompilerCacheRoot(compiler.Cache.Root); err != nil {
			return err
		}
	}
	identity, err := compiler.validateHelper()
	if err != nil {
		return err
	}
	output, err := compiler.runExpected(ctx, []string{"--self-test"}, nil, identity)
	if err != nil {
		compiler.markUnavailable()
		return errors.New("vyper helper self-test failed")
	}
	var response struct {
		Schema       string `json:"schema"`
		Pyodide      string `json:"pyodide"`
		Python       string `json:"python"`
		AccessDenied bool   `json:"access_denied"`
		Limits       bool   `json:"limits"`
	}
	if json.Unmarshal(output, &response) != nil || response.Schema != "etherview-vyper-wasm-self-test-v1" || response.Python != "3.13.2" || response.Pyodide != "0.29.3" || !response.AccessDenied || !response.Limits {
		compiler.markUnavailable()
		return errors.New("vyper helper self-test response is invalid")
	}
	compiler.mu.Lock()
	compiler.identity = &identity
	compiler.mu.Unlock()
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
		_, ready := compiler.runtimeIdentity()
		return ready && compiler.Cache != nil && compiler.Cache.InstallLocker != nil
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
	return CompilerProvenance{Kind: CompilerVyper, Digest: identity.compiler, ExecutorDigest: identity.executor, ExecutorKind: VyperDynamicExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy, Platform: CompilerPlatformPythonWheel}, nil
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
	// Catalog-free compilers are used for local differential verification. Their
	// exact runtime identity is required, but they cannot create durable jobs
	// because persistent provenance requires a signed catalog generation.
	if provenance != expected {
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

func (compiler *VyperCompiler) run(
	ctx context.Context,
	arguments []string,
	input []byte,
) ([]byte, error) {
	expected, ready := compiler.runtimeIdentity()
	if !ready {
		return nil, ErrVyperRuntimeUnavailable
	}
	return compiler.runExpected(ctx, arguments, input, expected)
}

func (compiler *VyperCompiler) runExpected(ctx context.Context, arguments []string, input []byte, expected vyperRuntimeIdentity) ([]byte, error) {
	if len(input) > compiler.maxInputBytes() {
		return nil, errors.New("vyper compiler input exceeds size limit")
	}
	current, err := compiler.validateHelper()
	if err != nil || current != expected {
		compiler.markUnavailable()
		return nil, ErrVyperRuntimeUnavailable
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
	nodeArguments, err := compiler.wasmArguments(arguments)
	if err != nil {
		_ = cleanup()
		return nil, err
	}
	command := exec.CommandContext(runContext, compiler.Path, nodeArguments...)
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
		return nil, ErrVyperRuntimeUnavailable
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
func (compiler *VyperCompiler) resolveDynamic(ctx context.Context, language Language, version string) (CompilerProvenance, error) {
	if language != LanguageVyper || !stableVyperVersion(normalizeCompilerVersion(version)) {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	host, shared, err := compiler.checkedHostIdentity()
	if err != nil {
		return CompilerProvenance{}, err
	}
	entry, err := compiler.Catalog.Lookup(ctx, LanguageVyper, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	_, artifact, err := compiler.Catalog.vyperArtifact(ctx, entry.GenerationID, entry.Version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	pkg, err := decodeCatalogDigest(artifact.ManifestSHA256)
	if err != nil {
		return CompilerProvenance{}, err
	}
	executor := vyperExecutorDigest(host, shared, pkg)
	if artifact.ExecutorDigests[runtime.GOOS+"-"+runtime.GOARCH] != hex.EncodeToString(executor[:]) {
		return CompilerProvenance{}, ErrCompilerProvenanceConflict
	}
	child, err := compiler.ensureRuntime(ctx, entry.Version, entry, artifact)
	if err != nil {
		return CompilerProvenance{}, err
	}
	identity, err := child.validateHelper()
	if err != nil || identity.executor != executor || identity.compiler != entry.ArtifactSHA256 {
		return CompilerProvenance{}, ErrCompilerProvenanceConflict
	}
	if _, _, err := compiler.checkedHostIdentity(); err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{Kind: CompilerVyper, Digest: entry.ArtifactSHA256, ExecutorDigest: executor, ExecutorKind: VyperDynamicExecutorKind, ExecutionPolicy: TrustedSubprocessPolicy, Platform: CompilerPlatformPythonWheel, CatalogGeneration: entry.GenerationID}, nil
}
func (compiler *VyperCompiler) compileDynamic(ctx context.Context, language Language, version string, provenance CompilerProvenance, input []byte) ([]byte, error) {
	if language != LanguageVyper || !provenance.valid() || provenance.ExecutorKind != VyperDynamicExecutorKind {
		return nil, ErrCompilerProvenanceConflict
	}
	host, shared, err := compiler.checkedHostIdentity()
	if err != nil {
		return nil, err
	}
	entry, artifact, err := compiler.Catalog.vyperArtifact(ctx, provenance.CatalogGeneration, normalizeCompilerVersion(version))
	if err != nil {
		return nil, err
	}
	pkg, _ := decodeCatalogDigest(artifact.ManifestSHA256)
	executor := vyperExecutorDigest(host, shared, pkg)
	if entry.ArtifactSHA256 != provenance.Digest || executor != provenance.ExecutorDigest || artifact.ExecutorDigests[runtime.GOOS+"-"+runtime.GOARCH] != hex.EncodeToString(executor[:]) {
		return nil, ErrCompilerProvenanceConflict
	}
	child, err := compiler.ensureRuntime(ctx, entry.Version, entry, artifact)
	if err != nil {
		return nil, err
	}
	// The shared host passed startup self-tests. Each compilation needs one
	// fresh process; its package and complete identity are verified on both sides.
	expected := vyperRuntimeIdentity{compiler: provenance.Digest, executor: provenance.ExecutorDigest}
	output, compileErr := child.runExpected(ctx, []string{"--compile", strconv.Itoa(child.maxInputBytes()), strconv.Itoa(child.maxOutputBytes())}, input, expected)
	if _, _, err := compiler.checkedHostIdentity(); err != nil {
		return nil, err
	}
	return output, compileErr
}

func (compiler *VyperCompiler) wasmArguments(arguments []string) ([]string, error) {
	shared, err := solcJSArtifactNodeOptions(compiler.sharedPath())
	if err != nil {
		return nil, err
	}
	options := "--node-options=--max-old-space-size=128 --wasm-max-mem-pages=6144 " + shared[len("--node-options="):]
	if len(arguments) == 1 && arguments[0] == "--self-test" {
		return []string{options, "--vyper-self-test", compiler.sharedPath()}, nil
	}
	if len(arguments) != 3 || arguments[0] != "--compile" || compiler.PackagePath == "" {
		return nil, ErrCompilerRuntime
	}
	pkg, err := solcJSArtifactNodeOptions(compiler.PackagePath)
	if err != nil {
		return nil, err
	}
	options += " " + pkg[len("--node-options="):]
	return []string{options, "--vyper-compile", compiler.sharedPath(), compiler.PackagePath, compiler.version(), arguments[1], arguments[2]}, nil
}
