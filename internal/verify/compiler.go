package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Compiler interface {
	Provenance(Language, string) (CompilerProvenance, error)
	Compile(context.Context, Language, string, []byte) ([]byte, error)
}

type PinnedCompiler interface {
	Resolve(context.Context, Language, string) (CompilerProvenance, error)
	CompilePinned(context.Context, Language, string, CompilerProvenance, []byte) ([]byte, error)
}

type PinnedPairCompiler interface {
	CompilePairPinned(
		context.Context,
		Language,
		string,
		CompilerProvenance,
		[]byte,
		[]byte,
	) ([]byte, []byte, error)
}

var (
	// ErrCompilerCleanup means a compiler invocation could not prove that its
	// complete process group and private temporary directory were removed.
	ErrCompilerCleanup = errors.New("verification compiler cleanup failed")
	// ErrCompilerRuntime means the compiler execution boundary panicked. The
	// panic value is intentionally discarded at this stable fatal boundary.
	ErrCompilerRuntime = errors.New("verification compiler runtime invariant failed")
)

type CompilerKind string

const (
	GeasExecutorKind                   = "etherview_geas_v1"
	CompilerSolcJS        CompilerKind = "node_solcjs_v1"
	CompilerGeas          CompilerKind = "go_geas_v1"
	CompilerLegacyRunner  CompilerKind = "legacy_runner"
	CompilerLegacyProcess CompilerKind = "legacy_process"
)

// CompilerProvenance identifies one exact catalog artifact and execution
// runtime. Platform is artifact format identity, never host CPU selection.
type CompilerProvenance struct {
	Kind              CompilerKind
	Digest            [sha256.Size]byte
	ExecutorDigest    [sha256.Size]byte
	ExecutorKind      string
	ExecutionPolicy   string
	CatalogGeneration int64
	CatalogDigest     [sha256.Size]byte
	CatalogSource     string
	CatalogEntryCount int
	Platform          string
	ArtifactURL       string
	ArtifactMaxBytes  int64
}

func (provenance CompilerProvenance) valid() bool {
	if provenance.Digest == [sha256.Size]byte{} {
		return false
	}
	switch provenance.Kind {
	case CompilerSolcJS:
		return validCompilerPlatform(provenance.Platform) &&
			provenance.CatalogGeneration > 0 &&
			provenance.ExecutorDigest != [sha256.Size]byte{} &&
			provenance.ExecutorKind == SolcJSExecutorKind &&
			provenance.ExecutionPolicy == TrustedSubprocessPolicy &&
			provenance.Platform == CompilerPlatformEmscriptenWASM32
	case CompilerGeas:
		return provenance.CatalogGeneration == 0 &&
			provenance.CatalogDigest == [sha256.Size]byte{} &&
			provenance.CatalogSource == "" && provenance.CatalogEntryCount == 0 &&
			provenance.ArtifactURL == "" && provenance.ArtifactMaxBytes == 0 &&
			provenance.ExecutorDigest != [sha256.Size]byte{} &&
			provenance.ExecutorKind == GeasExecutorKind &&
			provenance.ExecutionPolicy == TrustedSubprocessPolicy &&
			provenance.Platform == CompilerPlatformGoModule
	case CompilerLegacyRunner:
		return validCompilerPlatform(provenance.Platform) &&
			provenance.CatalogGeneration > 0 &&
			provenance.ExecutorDigest != [sha256.Size]byte{} &&
			provenance.ExecutorKind == "legacy_runner" &&
			provenance.ExecutionPolicy == "legacy_hard_isolation"
	case CompilerLegacyProcess:
		return validCompilerPlatform(provenance.Platform) &&
			provenance.CatalogGeneration > 0 &&
			provenance.ExecutorDigest == [sha256.Size]byte{} &&
			provenance.ExecutorKind == "legacy_process" &&
			provenance.ExecutionPolicy == "legacy_trusted_process"
	default:
		return false
	}
}

// RuntimeValidator is implemented by compiler backends with a build-time
// runtime manifest that must be verified before workers are registered.
type RuntimeValidator interface {
	ValidateRuntime(context.Context) error
}

type CompilerArtifact struct {
	URL      string
	SHA256   string
	MaxBytes int64
}

type CompilerCache struct {
	Root                       string
	Timeout                    time.Duration
	UnsafeAllowPrivateNetworks bool
	InstallLocker              CompilerCacheInstallLocker
	unsafeHTTPClient           *http.Client
	unsafeAllowHTTP            bool
	resolver                   outboundResolver
	mu                         sync.Mutex
	locks                      map[string]*sync.Mutex
}

func (cache *CompilerCache) EnsureCatalogEntry(
	ctx context.Context,
	entry CatalogEntry,
) (string, error) {
	if cache == nil || entry.Language != LanguageSolidity ||
		entry.Platform != CompilerPlatformEmscriptenWASM32 ||
		!versionPattern.MatchString(entry.Version) ||
		entry.ArtifactSHA256 == [sha256.Size]byte{} {
		return "", errors.New("catalog compiler entry is invalid")
	}
	if cache.InstallLocker == nil {
		return "", errors.New("compiler cache install locker is unavailable")
	}
	artifact := CompilerArtifact{
		URL:      entry.ArtifactURL,
		SHA256:   hex.EncodeToString(entry.ArtifactSHA256[:]),
		MaxBytes: entry.MaxBytes,
	}
	key := string(entry.Language) + "-sha256-" +
		hex.EncodeToString(entry.ArtifactSHA256[:]) + ".js"
	return cache.ensureArtifact(ctx, entry.Language, entry.Version, artifact, key)
}

func (cache *CompilerCache) ensureArtifact(
	ctx context.Context,
	language Language,
	version string,
	artifact CompilerArtifact,
	key string,
) (string, error) {
	parsed, digest, maximum, err := validateCompilerArtifact(
		language, version, artifact, cache.unsafeAllowHTTP,
	)
	if err != nil {
		return "", err
	}
	lock := cache.lock(key)
	lock.Lock()
	defer lock.Unlock()

	if err := secureCompilerCacheRoot(cache.Root); err != nil {
		return "", err
	}
	path := filepath.Join(cache.Root, key)
	if validCompilerCacheFile(path, digest, maximum) {
		return path, nil
	}
	client := restrictedOutboundClient(
		cache.unsafeHTTPClient,
		cache.Timeout,
		cache.resolver,
		cache.UnsafeAllowPrivateNetworks,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errors.New("create compiler artifact request")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("download compiler artifact")
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("compiler server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maximum {
		return "", errors.New("compiler artifact exceeds size limit")
	}
	temporary, err := os.CreateTemp(cache.Root, ".compiler-*")
	if err != nil {
		return "", errors.New("create compiler temporary file")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath) //nolint:errcheck
	hasher := sha256.New()
	written, copyErr := io.Copy(
		io.MultiWriter(temporary, hasher),
		io.LimitReader(response.Body, maximum+1),
	)
	if copyErr != nil {
		_ = temporary.Close()
		return "", errors.New("read compiler artifact")
	}
	if written > maximum {
		_ = temporary.Close()
		return "", errors.New("compiler artifact exceeds size limit")
	}
	if !bytes.Equal(hasher.Sum(nil), digest[:]) {
		_ = temporary.Close()
		return "", errors.New("compiler checksum mismatch")
	}
	if err := temporary.Chmod(0o400); err != nil {
		_ = temporary.Close()
		return "", errors.New("secure compiler artifact")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", errors.New("sync compiler artifact")
	}
	if err := temporary.Close(); err != nil {
		return "", errors.New("close compiler artifact")
	}
	if err := cache.InstallLocker.WithCompilerCacheInstallLock(ctx, digest, func() error {
		// Independent API replicas may have downloaded the same cold miss while
		// this process staged its authenticated temporary file. Revalidate after
		// acquiring the writer-backed install lock and reuse the winner.
		if validCompilerCacheFile(path, digest, maximum) {
			return nil
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			// Retain a defensive winner check for an operator mounting one cache
			// through different PostgreSQL lock domains or a lock session lost
			// during installation. Only fully authenticated content is accepted.
			if validCompilerCacheFile(path, digest, maximum) {
				return nil
			}
			return errors.New("install compiler artifact")
		}
		if !validCompilerCacheFile(path, digest, maximum) {
			return errors.New("installed compiler artifact is unsafe")
		}
		return nil
	}); err != nil {
		return "", err
	}
	return path, nil
}

func (cache *CompilerCache) lock(key string) *sync.Mutex {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.locks == nil {
		cache.locks = make(map[string]*sync.Mutex)
	}
	if cache.locks[key] == nil {
		cache.locks[key] = &sync.Mutex{}
	}
	return cache.locks[key]
}

func decodeCompilerDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != sha256.Size*2 {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	copy(digest[:], decoded)
	if digest == [sha256.Size]byte{} {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	return digest, nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return original, nil
	}
	if len(data) > remaining {
		buffer.exceeded = true
		data = data[:remaining]
	}
	_, _ = buffer.buffer.Write(data)
	return original, nil
}

func (buffer *limitedBuffer) Bytes() []byte  { return buffer.buffer.Bytes() }
func (buffer *limitedBuffer) Exceeded() bool { return buffer.exceeded }
