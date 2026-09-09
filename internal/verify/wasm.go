package verify

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/islishude/etherview/internal/compilerbundle"
)

const WasmExecutorKind = compilerbundle.ExecutorKind
const WasmSubprocessPolicy = compilerbundle.ExecutionPolicy

// WasmCompiler is one compiler family over the shared, immutable subprocess
// bundle. API/all never instantiate the guest VM in their own process.
type WasmCompiler struct {
	Catalog                       *CompilerCatalog
	Cache                         *CompilerCache
	Path                          string
	Vyper                         bool
	Timeout                       time.Duration
	MaxInputBytes, MaxOutputBytes int
	mu                            sync.RWMutex
	digest                        [32]byte
	ready                         bool
}

func (c *WasmCompiler) limits() (time.Duration, int, int) {
	timeout, input, output := c.Timeout, c.MaxInputBytes, c.MaxOutputBytes
	if timeout <= 0 {
		timeout = defaultCompilerTimeout
	}
	if input <= 0 {
		input = defaultCompilerInputBytes
	}
	if output <= 0 {
		output = defaultCompilerOutputBytes
	}
	return timeout, input, output
}
func (c *WasmCompiler) ValidateRuntime(ctx context.Context) error {
	if c == nil {
		return errors.New("WASM compiler is unavailable")
	}
	c.unavailable()
	if !c.Vyper && (c.Catalog == nil || c.Cache == nil || c.Cache.InstallLocker == nil) {
		return errors.New("WASM compiler catalog/cache is incomplete")
	}
	identity, err := compilerbundle.Validate(c.Path)
	if err != nil {
		return err
	}
	if !c.Vyper {
		if err = secureCompilerCacheRoot(c.Cache.Root); err != nil {
			return err
		}
	}
	timeout, input, output := c.limits()
	args := []string{"--self-test"}
	if c.Vyper {
		args = append(args, "vyper")
	} else {
		timeout = min(timeout, 10*time.Second)
	}
	runner := compilerbundle.Runner{Path: c.Path, Timeout: timeout, MaxInputBytes: input, MaxOutputBytes: output}
	raw, err := runner.Execute(ctx, identity.Digest, args, nil)
	if err != nil {
		return err
	}
	var reply struct {
		Schema, Wazero, Version, Python string
		MemoryPages                     int  `json:"memory_pages"`
		AccessDenied                    bool `json:"access_denied"`
	}
	if json.Unmarshal(raw, &reply) != nil {
		return errors.New("invalid WASM compiler self-test")
	}
	if c.Vyper {
		if reply.Schema != "etherview-wazero-vyper-self-test-v1" || reply.Version != VyperCompilerVersion || reply.Python != compilerbundle.PythonVersion || !reply.AccessDenied {
			return errors.New("invalid WASM Vyper self-test")
		}
	} else if reply.Schema != "etherview-wazero-self-test-v1" || reply.Wazero != compilerbundle.WazeroVersion || reply.MemoryPages != 8192 {
		return errors.New("invalid WASM Solidity self-test")
	}
	c.mu.Lock()
	c.digest = identity.Digest
	c.ready = true
	c.mu.Unlock()
	return nil
}
func (c *WasmCompiler) unavailable() {
	c.mu.Lock()
	c.ready = false
	c.digest = [32]byte{}
	c.mu.Unlock()
}
func (c *WasmCompiler) runtimeDigest() ([32]byte, error) {
	if c == nil {
		return [32]byte{}, errors.New("WASM compiler unavailable")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.ready || c.digest == [32]byte{} {
		return [32]byte{}, errors.New("WASM compiler runtime unavailable")
	}
	return c.digest, nil
}
func (c *WasmCompiler) Ready() bool { _, err := c.runtimeDigest(); return err == nil }
func (c *WasmCompiler) CompilerAvailable(ctx context.Context) bool {
	if !c.Ready() {
		return false
	}
	if c.Vyper {
		return true
	}
	if c.Catalog == nil {
		return false
	}
	_, err := c.Catalog.Versions(ctx, LanguageSolidity)
	return err == nil
}
func (c *WasmCompiler) Resolve(ctx context.Context, language Language, version string) (CompilerProvenance, error) {
	digest, err := c.runtimeDigest()
	if err != nil {
		if c != nil && c.Vyper {
			return CompilerProvenance{}, ErrVyperRuntimeUnavailable
		}
		return CompilerProvenance{}, err
	}
	p := CompilerProvenance{ExecutorDigest: digest, ExecutorKind: WasmExecutorKind, ExecutionPolicy: WasmSubprocessPolicy}
	if c.Vyper {
		if language != LanguageVyper || normalizeCompilerVersion(version) != VyperCompilerVersion {
			return CompilerProvenance{}, ErrCompilerVersionUnavailable
		}
		raw, _ := hex.DecodeString(VyperCompilerSHA256)
		copy(p.Digest[:], raw)
		p.Kind = CompilerVyper
		p.Platform = CompilerPlatformPythonWheel
		return p, nil
	}
	if language != LanguageSolidity && language != LanguageYul {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	entry, err := c.Catalog.Lookup(ctx, language, version)
	if err != nil {
		return CompilerProvenance{}, err
	}
	if entry.Platform != CompilerPlatformEmscriptenWASM32 {
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
	p.Kind = CompilerSolcWasm
	p.Digest = entry.ArtifactSHA256
	p.Platform = entry.Platform
	p.CatalogGeneration = entry.GenerationID
	p.ArtifactURL = entry.ArtifactURL
	p.ArtifactMaxBytes = entry.MaxBytes
	return p, nil
}
func (c *WasmCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Resolve(ctx, language, version)
}
func (c *WasmCompiler) Compile(ctx context.Context, language Language, version string, input []byte) ([]byte, error) {
	p, err := c.Resolve(ctx, language, version)
	if err != nil {
		return nil, err
	}
	return c.CompilePinned(ctx, language, version, p, input)
}
func (c *WasmCompiler) CompilePinned(ctx context.Context, language Language, version string, p CompilerProvenance, input []byte) ([]byte, error) {
	digest, err := c.runtimeDigest()
	if err != nil {
		return nil, err
	}
	if !p.valid() || p.ExecutorKind != WasmExecutorKind || p.ExecutionPolicy != WasmSubprocessPolicy || p.ExecutorDigest != digest {
		return nil, ErrCompilerProvenanceConflict
	}
	timeout, maxInput, maxOutput := c.limits()
	if len(input) == 0 || len(input) > maxInput {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	mode, path := "solc", ""
	if c.Vyper {
		if p.Kind != CompilerVyper || language != LanguageVyper || normalizeCompilerVersion(version) != VyperCompilerVersion {
			return nil, ErrCompilerProvenanceConflict
		}
		mode = "vyper"
		path = filepath.Dir(c.Path)
	} else {
		if c.Cache == nil || p.Kind != CompilerSolcWasm || (language != LanguageSolidity && language != LanguageYul) {
			return nil, ErrCompilerProvenanceConflict
		}
		path, err = c.Cache.EnsureCatalogEntry(ctx, CatalogEntry{GenerationID: p.CatalogGeneration, Language: LanguageSolidity, Version: normalizeCompilerVersion(version), Platform: p.Platform, ArtifactURL: p.ArtifactURL, ArtifactSHA256: p.Digest, MaxBytes: p.ArtifactMaxBytes})
		if err != nil {
			return nil, err
		}
	}
	args := []string{"--compile", mode, path, normalizeCompilerVersion(version), hex.EncodeToString(p.Digest[:]), strconv.Itoa(maxInput), strconv.Itoa(maxOutput), strconv.FormatInt(max(timeout.Milliseconds(), 1), 10)}
	result, err := (compilerbundle.Runner{Path: c.Path, Timeout: timeout, MaxInputBytes: maxInput, MaxOutputBytes: maxOutput}).Execute(ctx, digest, args, input)
	if errors.Is(err, compilerbundle.ErrCleanup) {
		return nil, ErrCompilerCleanup
	}
	if errors.Is(err, compilerbundle.ErrChanged) || errors.Is(err, compilerbundle.ErrInvalid) {
		c.unavailable()
	}
	return result, err
}
