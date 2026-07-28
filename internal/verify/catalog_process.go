package verify

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// CatalogProcessCompiler is the private-development backend for native
// solc-bin platforms. Public verification never permits process mode.
type CatalogProcessCompiler struct {
	Catalog        *CompilerCatalog
	Cache          *CompilerCache
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
}

func (compiler *CatalogProcessCompiler) Resolve(
	ctx context.Context,
	language Language,
	version string,
) (CompilerProvenance, error) {
	if compiler == nil || compiler.Catalog == nil || compiler.Cache == nil {
		return CompilerProvenance{}, errors.New("catalog process compiler is unavailable")
	}
	entry, err := compiler.Catalog.Lookup(ctx, language, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{
		Kind: CompilerProcess, Digest: entry.ArtifactSHA256,
		CatalogGeneration: entry.GenerationID, Platform: entry.Platform,
		ArtifactURL: entry.ArtifactURL, ArtifactMaxBytes: entry.MaxBytes,
	}, nil
}

func (compiler *CatalogProcessCompiler) Provenance(
	language Language,
	version string,
) (CompilerProvenance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return compiler.Resolve(ctx, language, version)
}

func (compiler *CatalogProcessCompiler) Compile(
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

func (compiler *CatalogProcessCompiler) CompilePinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	input []byte,
) ([]byte, error) {
	if !provenance.valid() || provenance.Kind != CompilerProcess ||
		provenance.CatalogGeneration <= 0 || !validCompilerPlatform(provenance.Platform) ||
		provenance.ArtifactURL == "" || provenance.ArtifactMaxBytes <= 0 {
		return nil, errors.New("compiler provenance is invalid")
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
	path, err := compiler.Cache.EnsureCatalogEntry(ctx, CatalogEntry{
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
	platform, err := executablePlatform(path)
	if err != nil || !compilerPlatformMatches(platform, provenance.Platform) {
		return nil, errors.New("compiler artifact does not match the process platform")
	}
	timeout := compiler.Timeout
	if timeout <= 0 {
		timeout = defaultCompilerTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runContext, path, "--standard-json")
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(input)
	maxOutput := compiler.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultCompilerOutputBytes
	}
	stdout, stderr := newLimitedBuffer(maxOutput), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(runContext.Err(), context.DeadlineExceeded) {
			return nil, errors.New("compiler timed out")
		}
		if errors.Is(runContext.Err(), context.Canceled) {
			return nil, errors.New("compiler cancelled")
		}
		return nil, errors.New("compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func (*CatalogProcessCompiler) HardIsolated() bool {
	return false
}
