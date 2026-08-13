package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
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

	"github.com/islishude/etherview/internal/geascompiler"
)

const (
	GeasModulePath = "github.com/fjl/geas"
	GeasModuleSum  = "h1:CtVkRXysF+1gf1L0MgisG4vcr/Zv/uf8ukq/uqiUEUs="
	geasHeapLimit  = "384MiB"

	maximumGeasHelperBytes = int64(256 << 20)
)

var ErrCompilerNondeterministic = errors.New("verification compiler output is nondeterministic")

type GeasCompiler struct {
	Path           string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int

	mu       sync.RWMutex
	identity *geasRuntimeIdentity
}

type geasRuntimeIdentity struct {
	compiler [sha256.Size]byte
	executor [sha256.Size]byte
}

func (compiler *GeasCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil {
		return errors.New("geas compiler is nil")
	}
	compiler.markUnavailable()
	identity, err := validateGeasHelper(compiler.Path)
	if err != nil {
		return err
	}
	compiler.mu.Lock()
	compiler.identity = &identity
	compiler.mu.Unlock()
	output, err := compiler.run(ctx, []string{"--self-test"}, nil)
	if err != nil {
		compiler.markUnavailable()
		return errors.New("geas helper self-test failed")
	}
	var response geascompiler.Response
	if json.Unmarshal(output, &response) != nil || !response.Successful ||
		response.Schema != geascompiler.ProtocolSchema || response.Bytecode != "0x6001" ||
		len(response.Sources) != 1 || response.Sources[0] != "selftest.eas" {
		compiler.markUnavailable()
		return errors.New("geas helper self-test response is invalid")
	}
	return nil
}

func (compiler *GeasCompiler) markUnavailable() {
	compiler.mu.Lock()
	compiler.identity = nil
	compiler.mu.Unlock()
}

func (compiler *GeasCompiler) Ready() bool {
	if compiler == nil {
		return false
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	return compiler.identity != nil
}

func (compiler *GeasCompiler) CompilerAvailable(context.Context) bool {
	return compiler.Ready()
}

func (compiler *GeasCompiler) Resolve(
	_ context.Context,
	language Language,
	version string,
) (CompilerProvenance, error) {
	if compiler == nil || language != LanguageGeas ||
		normalizeCompilerVersion(version) != GeasCompilerVersion {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	if compiler.identity == nil {
		return CompilerProvenance{}, ErrCompilerCatalogUnavailable
	}
	return CompilerProvenance{
		Kind:            CompilerGeas,
		Digest:          compiler.identity.compiler,
		ExecutorDigest:  compiler.identity.executor,
		ExecutorKind:    GeasExecutorKind,
		ExecutionPolicy: TrustedSubprocessPolicy,
		Platform:        CompilerPlatformGoModule,
	}, nil
}

func (compiler *GeasCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	return compiler.Resolve(context.Background(), language, version)
}

func (compiler *GeasCompiler) Compile(
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

func (compiler *GeasCompiler) CompilePinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	input []byte,
) ([]byte, error) {
	if language != LanguageGeas || normalizeCompilerVersion(version) != GeasCompilerVersion {
		return nil, ErrCompilerVersionUnavailable
	}
	if err := compiler.validatePinned(provenance); err != nil {
		return nil, err
	}
	var request geascompiler.Request
	if len(input) == 0 || len(input) > compiler.maxInputBytes() ||
		json.Unmarshal(input, &request) != nil {
		return nil, errors.New("geas compiler input is invalid")
	}
	response, err := compiler.compileRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func (compiler *GeasCompiler) CompileGeasEntrypointPinned(
	ctx context.Context,
	version string,
	provenance CompilerProvenance,
	sources map[string]string,
	entrypoint string,
) (geascompiler.Response, error) {
	if normalizeCompilerVersion(version) != GeasCompilerVersion {
		return geascompiler.Response{}, ErrCompilerVersionUnavailable
	}
	if err := compiler.validatePinned(provenance); err != nil {
		return geascompiler.Response{}, err
	}
	return compiler.compileRequest(ctx, geascompiler.Request{
		Schema: geascompiler.ProtocolSchema, Sources: sources, Entrypoint: entrypoint,
	})
}

func (compiler *GeasCompiler) compileRequest(
	ctx context.Context,
	request geascompiler.Request,
) (geascompiler.Response, error) {
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > compiler.maxInputBytes() {
		return geascompiler.Response{}, errors.New("geas compiler input exceeds size limit")
	}
	compileOnce := func() (geascompiler.Response, error) {
		output, runErr := compiler.run(ctx, []string{"--compile"}, encoded)
		if runErr != nil {
			return geascompiler.Response{}, runErr
		}
		var response geascompiler.Response
		if json.Unmarshal(output, &response) != nil || response.Validate(request) != nil {
			return geascompiler.Response{}, errors.New("geas compiler returned invalid JSON")
		}
		return response, nil
	}
	first, err := compileOnce()
	if err != nil {
		return geascompiler.Response{}, err
	}
	second, err := compileOnce()
	if err != nil {
		return geascompiler.Response{}, err
	}
	if !first.Equal(second) {
		return geascompiler.Response{}, ErrCompilerNondeterministic
	}
	return first, nil
}

func (compiler *GeasCompiler) validatePinned(provenance CompilerProvenance) error {
	if !provenance.valid() || provenance.Kind != CompilerGeas ||
		provenance.ExecutorKind != GeasExecutorKind {
		return ErrCompilerProvenanceConflict
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	if compiler.identity == nil || provenance.Digest != compiler.identity.compiler ||
		provenance.ExecutorDigest != compiler.identity.executor {
		return ErrCompilerProvenanceConflict
	}
	return nil
}

func (compiler *GeasCompiler) timeout() time.Duration {
	if compiler.Timeout > 0 {
		return compiler.Timeout
	}
	return defaultCompilerTimeout
}

func (compiler *GeasCompiler) maxInputBytes() int {
	if compiler.MaxInputBytes > 0 {
		return compiler.MaxInputBytes
	}
	return defaultCompilerInputBytes
}

func (compiler *GeasCompiler) run(
	ctx context.Context,
	arguments []string,
	input []byte,
) ([]byte, error) {
	if len(input) > compiler.maxInputBytes() {
		return nil, errors.New("geas compiler input exceeds size limit")
	}
	expected, ready := compiler.runtimeIdentity()
	current, err := validateGeasHelper(compiler.Path)
	if !ready || err != nil || current != expected {
		compiler.markUnavailable()
		return nil, errors.New("geas helper identity changed")
	}
	temporaryDirectory, err := os.MkdirTemp("", "etherview-geas-*")
	if err != nil {
		return nil, errors.New("create Geas temporary directory")
	}
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		_ = os.RemoveAll(temporaryDirectory)
		return nil, errors.New("secure Geas temporary directory")
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
		"GOMEMLIMIT=" + geasHeapLimit,
	}
	command.Stdin = bytes.NewReader(input)
	maximumOutput := compiler.MaxOutputBytes
	if maximumOutput <= 0 {
		maximumOutput = defaultCompilerOutputBytes
	}
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
	current, identityErr := validateGeasHelper(compiler.Path)
	if identityErr != nil || current != expected {
		compiler.markUnavailable()
		return nil, errors.New("geas helper identity changed")
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
		return nil, errors.New("geas compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("geas compiler output exceeds size limit")
	}
	output := append([]byte(nil), stdout.Bytes()...)
	if !json.Valid(output) {
		return nil, errors.New("geas compiler returned invalid JSON")
	}
	return output, nil
}

func (compiler *GeasCompiler) runtimeIdentity() (geasRuntimeIdentity, bool) {
	if compiler == nil {
		return geasRuntimeIdentity{}, false
	}
	compiler.mu.RLock()
	defer compiler.mu.RUnlock()
	if compiler.identity == nil {
		return geasRuntimeIdentity{}, false
	}
	return *compiler.identity, true
}

func validateGeasHelper(path string) (geasRuntimeIdentity, error) {
	var identity geasRuntimeIdentity
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return identity, errors.New("geas helper path must be absolute and clean")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o222 != 0 || info.Size() < 1 || info.Size() > maximumGeasHelperBytes {
		return identity, errors.New("geas helper file is invalid")
	}
	build, err := buildinfo.ReadFile(path)
	if err != nil {
		return identity, errors.New("read Geas helper build info")
	}
	var dependencyFound bool
	for _, dependency := range build.Deps {
		if dependency.Path != GeasModulePath {
			continue
		}
		if dependencyFound || dependency.Version != "v"+GeasCompilerVersion ||
			dependency.Sum != GeasModuleSum || dependency.Replace != nil {
			return identity, errors.New("geas helper module identity is invalid")
		}
		decoded, decodeErr := decodeModuleSum(dependency.Sum)
		if decodeErr != nil {
			return identity, decodeErr
		}
		identity.compiler = decoded
		dependencyFound = true
	}
	if !dependencyFound {
		return identity, errors.New("geas helper module is missing")
	}
	file, err := os.Open(path)
	if err != nil {
		return identity, errors.New("open Geas helper")
	}
	defer file.Close() //nolint:errcheck
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return identity, errors.New("geas helper identity changed")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maximumGeasHelperBytes+1)); err != nil ||
		opened.Size() > maximumGeasHelperBytes {
		return identity, errors.New("hash Geas helper")
	}
	copy(identity.executor[:], hasher.Sum(nil))
	return identity, nil
}

func decodeModuleSum(sum string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if !strings.HasPrefix(sum, "h1:") {
		return digest, errors.New("geas module sum is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sum, "h1:"))
	if err != nil || len(decoded) != sha256.Size {
		return digest, errors.New("geas module sum is invalid")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func (identity geasRuntimeIdentity) String() string {
	return fmt.Sprintf("geas:%s helper:%s", hex.EncodeToString(identity.compiler[:]), hex.EncodeToString(identity.executor[:]))
}
