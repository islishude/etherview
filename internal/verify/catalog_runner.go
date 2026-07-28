package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CatalogRunnerCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	Runtime        string
	RunnerImage    string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	Memory         string
	CPUs           string
	PIDs           int

	sandbox *ContainerCompiler
}

type PinnedCompiler interface {
	Resolve(context.Context, Language, string) (CompilerProvenance, error)
	CompilePinned(context.Context, Language, string, CompilerProvenance, []byte) ([]byte, error)
}

func (compiler *CatalogRunnerCompiler) ValidateRuntime(ctx context.Context) error {
	if compiler == nil || compiler.Catalog == nil || compiler.Cache == nil {
		return errors.New("catalog runner compiler requires catalog and cache")
	}
	if _, err := parseContainerImage(compiler.RunnerImage); err != nil {
		return err
	}
	sandbox := &ContainerCompiler{
		Runtime: compiler.Runtime,
		Images: map[Language]map[string]string{
			LanguageSolidity: {"runner": compiler.RunnerImage},
		},
		Timeout: compiler.Timeout, MaxInputBytes: compiler.MaxInputBytes,
		MaxOutputBytes: compiler.MaxOutputBytes, Memory: compiler.Memory,
		CPUs: compiler.CPUs, PIDs: compiler.PIDs,
	}
	if err := sandbox.ValidateRuntime(ctx); err != nil {
		return err
	}
	runtimePath, ok := sandbox.validatedRuntime()
	if !ok {
		return errors.New("catalog runner runtime is not validated")
	}
	platform, err := inspectRunnerPlatform(ctx, runtimePath, compiler.RunnerImage)
	if err != nil {
		return err
	}
	if err := compiler.Catalog.SetPlatform(platform); err != nil {
		return err
	}
	compiler.sandbox = sandbox
	return nil
}

func inspectRunnerPlatform(ctx context.Context, runtimePath, image string) (string, error) {
	inspectContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(
		inspectContext,
		runtimePath,
		"image", "inspect", "--format={{.Os}}/{{.Architecture}}", image,
	)
	command.WaitDelay = time.Second
	stdout, stderr := newLimitedBuffer(1<<20), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(inspectContext.Err(), context.DeadlineExceeded) {
			return "", errors.New("compiler runner platform inspection timed out")
		}
		return "", errors.New("compiler runner platform is unavailable")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return "", errors.New("compiler runner platform inspection output exceeded its limit")
	}
	platform := strings.TrimSpace(stdout.String())
	switch platform {
	case "linux/amd64":
		platform = CompilerPlatformLinuxAMD64
	case "linux/arm64":
		platform = CompilerPlatformLinuxARM64
	default:
		return "", errors.New("compiler runner platform is unsupported")
	}
	return platform, nil
}

func (compiler *CatalogRunnerCompiler) HardIsolated() bool {
	return compiler != nil && compiler.sandbox != nil && compiler.sandbox.HardIsolated()
}

func (compiler *CatalogRunnerCompiler) Resolve(
	ctx context.Context,
	language Language,
	version string,
) (CompilerProvenance, error) {
	entry, err := compiler.Catalog.Lookup(ctx, language, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	runnerDigest, err := parseContainerImage(compiler.RunnerImage)
	if err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{
		Kind: CompilerRunner, Digest: entry.ArtifactSHA256, RunnerDigest: runnerDigest,
		CatalogGeneration: entry.GenerationID, Platform: entry.Platform, ArtifactURL: entry.ArtifactURL,
		ArtifactMaxBytes: entry.MaxBytes, HardIsolated: compiler.HardIsolated(),
	}, nil
}

func (compiler *CatalogRunnerCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return compiler.Resolve(ctx, language, version)
}

func (compiler *CatalogRunnerCompiler) Compile(
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

func (compiler *CatalogRunnerCompiler) CompilePinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	input []byte,
) (result []byte, resultErr error) {
	if !provenance.valid() || provenance.Kind != CompilerRunner ||
		provenance.RunnerDigest != mustRunnerDigest(compiler.RunnerImage) {
		return nil, errors.New("compiler provenance is invalid")
	}
	maxInput := compiler.MaxInputBytes
	if maxInput <= 0 {
		maxInput = defaultCompilerInputBytes
	}
	if len(input) == 0 || len(input) > maxInput {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	entry := CatalogEntry{
		GenerationID: provenance.CatalogGeneration, Language: language,
		Version: normalizeCompilerVersion(version), Platform: provenance.Platform,
		ArtifactURL:    provenance.ArtifactURL,
		ArtifactSHA256: provenance.Digest, MaxBytes: provenance.ArtifactMaxBytes,
	}
	if language == LanguageYul {
		entry.Language = LanguageSolidity
	}
	path, err := compiler.Cache.EnsureCatalogEntry(ctx, entry)
	if err != nil {
		return nil, err
	}
	artifactPlatform, err := executablePlatform(path)
	if err != nil || !compilerPlatformMatches(artifactPlatform, provenance.Platform) {
		return nil, errors.New("compiler artifact does not match its runner platform")
	}
	containerPlatform, err := ociPlatform(provenance.Platform)
	if err != nil {
		return nil, err
	}
	artifact, err := os.ReadFile(path)
	if err != nil || int64(len(artifact)) > provenance.ArtifactMaxBytes ||
		sha256.Sum256(artifact) != provenance.Digest {
		return nil, errors.New("cached compiler artifact is invalid")
	}
	var framed bytes.Buffer
	if err := WriteRunnerFrame(&framed, RunnerFrame{
		Compiler: artifact, Input: input, Version: normalizeCompilerVersion(version),
		Digest: provenance.Digest,
	}); err != nil {
		return nil, err
	}
	if compiler.sandbox == nil {
		return nil, errors.New("catalog runner runtime is not validated")
	}
	runtimePath, ok := compiler.sandbox.validatedRuntime()
	if !ok {
		return nil, errors.New("catalog runner runtime is not validated")
	}
	name, err := randomCompilerContainerName()
	if err != nil {
		return nil, err
	}
	memory, cpus, pids := compiler.sandbox.resources()
	args := []string{
		"run", "--pull=never", "--platform=" + containerPlatform, "--name=" + name,
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65532:65532",
		"--memory=" + memory, "--memory-swap=" + memory, "--cpus=" + cpus,
		fmt.Sprintf("--pids-limit=%d", pids), "--ulimit=nofile=64:64", "--ulimit=core=0",
		"--tmpfs=/tmp:rw,exec,nosuid,nodev,size=272m,mode=0700",
		compiler.RunnerImage,
	}
	timeout := compiler.Timeout
	if timeout <= 0 {
		timeout = defaultCompilerTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runContext, runtimePath, args...)
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(framed.Bytes())
	maxOutput := compiler.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultCompilerOutputBytes
	}
	stdout, stderr := newLimitedBuffer(maxOutput), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	defer func() {
		panicked := recover() != nil
		if cleanupErr := compiler.sandbox.removeContainerSafely(runtimePath, name); cleanupErr != nil {
			result, resultErr = nil, ErrCompilerCleanup
			return
		}
		if panicked {
			result, resultErr = nil, ErrCompilerRuntime
		}
	}()
	if err := compiler.sandbox.runContainerCommand(command); err != nil {
		if errors.Is(runContext.Err(), context.DeadlineExceeded) {
			return nil, errors.New("sandboxed compiler timed out")
		}
		if errors.Is(runContext.Err(), context.Canceled) {
			return nil, errors.New("sandboxed compiler cancelled")
		}
		return nil, errors.New("sandboxed compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func mustRunnerDigest(image string) [sha256.Size]byte {
	digest, _ := parseContainerImage(image)
	return digest
}
