package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type trackingCompilerCacheInstallLocker struct {
	mu           sync.Mutex
	locks        map[[sha256.Size]byte]*sync.Mutex
	path         string
	calls        int
	active       int
	maximum      int
	observations []os.FileInfo
}

func (locker *trackingCompilerCacheInstallLocker) WithCompilerCacheInstallLock(
	_ context.Context,
	digest [sha256.Size]byte,
	action func() error,
) error {
	locker.mu.Lock()
	if locker.locks == nil {
		locker.locks = make(map[[sha256.Size]byte]*sync.Mutex)
	}
	keyLock := locker.locks[digest]
	if keyLock == nil {
		keyLock = &sync.Mutex{}
		locker.locks[digest] = keyLock
	}
	locker.mu.Unlock()

	keyLock.Lock()
	defer keyLock.Unlock()
	locker.mu.Lock()
	locker.calls++
	locker.active++
	locker.maximum = max(locker.maximum, locker.active)
	locker.mu.Unlock()
	err := action()
	locker.mu.Lock()
	defer locker.mu.Unlock()
	locker.active--
	if err == nil && locker.path != "" {
		if info, statErr := os.Lstat(locker.path); statErr == nil {
			locker.observations = append(locker.observations, info)
		}
	}
	return err
}

type failingCompilerCacheInstallLocker struct{ calls atomic.Int32 }

func (locker *failingCompilerCacheInstallLocker) WithCompilerCacheInstallLock(
	context.Context,
	[sha256.Size]byte,
	func() error,
) error {
	locker.calls.Add(1)
	return errors.New("install lock should not be used")
}

type fixedOutboundResolver struct {
	addresses []net.IPAddr
	err       error
}

func (resolver fixedOutboundResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, resolver.err
}

func compilerCatalogEntry(url string, payload []byte, maximum int64) CatalogEntry {
	return CatalogEntry{
		Language:       LanguageSolidity,
		Version:        "0.8.30",
		Platform:       CompilerPlatformEmscriptenWASM32,
		ArtifactURL:    url,
		ArtifactSHA256: sha256.Sum256(payload),
		MaxBytes:       maximum,
	}
}

func TestCompilerDownloadRejectsRedirectsAndPrivateDNS(t *testing.T) {
	payload := []byte("compiler JavaScript")
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write(payload)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	cache := CompilerCache{
		Root:                       t.TempDir(),
		InstallLocker:              testCompilerCacheInstallLocker,
		unsafeAllowHTTP:            true,
		UnsafeAllowPrivateNetworks: true,
	}
	entry := compilerCatalogEntry(redirect.URL, payload, 1<<20)
	if _, err := cache.EnsureCatalogEntry(context.Background(), entry); err == nil ||
		err.Error() != "download compiler artifact" {
		t.Fatalf("unexpected redirect error: %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests", targetHits.Load())
	}

	cache.Root = t.TempDir()
	cache.unsafeAllowHTTP = false
	cache.UnsafeAllowPrivateNetworks = false
	cache.resolver = fixedOutboundResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	entry.ArtifactURL = "https://compiler.example/artifact"
	if _, err := cache.EnsureCatalogEntry(context.Background(), entry); err == nil ||
		err.Error() != "download compiler artifact" {
		t.Fatalf("unexpected private DNS error: %v", err)
	}

	client := restrictedOutboundClient(nil, time.Second, fixedOutboundResolver{}, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("production compiler downloader can use an environment proxy")
	}
}

func TestCompilerCacheBoundsDeclaredAndStreamingArtifacts(t *testing.T) {
	payload := []byte("artifact larger than its configured limit")
	for _, test := range []struct {
		name   string
		stream bool
	}{
		{name: "content length"},
		{name: "streaming body", stream: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.stream {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			cache := CompilerCache{
				Root:                       t.TempDir(),
				InstallLocker:              testCompilerCacheInstallLocker,
				unsafeAllowHTTP:            true,
				UnsafeAllowPrivateNetworks: true,
			}
			entry := compilerCatalogEntry(server.URL, payload, int64(len(payload)-1))
			if _, err := cache.EnsureCatalogEntry(context.Background(), entry); err == nil ||
				err.Error() != "compiler artifact exceeds size limit" {
				t.Fatalf("unexpected size error: %v", err)
			}
		})
	}
}

func TestCompilerCacheRejectsUnsafeRootAndInstallsReadOnlyEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX cache ownership and symlink semantics")
	}
	payload := []byte("compiler JavaScript")

	t.Run("symlink root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "cache")
		if err := os.Symlink(t.TempDir(), root); err != nil {
			t.Fatal(err)
		}
		cache := CompilerCache{Root: root, InstallLocker: testCompilerCacheInstallLocker}
		entry := compilerCatalogEntry("https://compiler.example/artifact", payload, 1<<20)
		if _, err := cache.EnsureCatalogEntry(context.Background(), entry); err == nil ||
			!strings.Contains(err.Error(), "non-symlink directory") {
			t.Fatalf("unexpected symlink root error: %v", err)
		}
	})

	t.Run("downloaded entry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		defer server.Close()
		cache := CompilerCache{
			Root:                       t.TempDir(),
			InstallLocker:              testCompilerCacheInstallLocker,
			unsafeAllowHTTP:            true,
			UnsafeAllowPrivateNetworks: true,
		}
		installed, err := cache.EnsureCatalogEntry(
			context.Background(), compilerCatalogEntry(server.URL, payload, 1<<20),
		)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(installed)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
			t.Fatalf("installed mode=%v error=%v", info, err)
		}
	})
}

func TestCompilerCacheSerializesConcurrentInstall(t *testing.T) {
	payload := []byte("compiler JavaScript")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	cache := CompilerCache{
		Root:                       t.TempDir(),
		InstallLocker:              testCompilerCacheInstallLocker,
		unsafeAllowHTTP:            true,
		UnsafeAllowPrivateNetworks: true,
	}
	entry := compilerCatalogEntry(server.URL, payload, 1<<20)
	errorsFound := make(chan error, 16)
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			_, err := cache.EnsureCatalogEntry(context.Background(), entry)
			errorsFound <- err
		})
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("compiler downloaded %d times", hits.Load())
	}
}

func TestCompilerCachePersistsAcrossInstances(t *testing.T) {
	payload := []byte("persistent compiler JavaScript")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(payload)
	}))
	root := t.TempDir()
	entry := compilerCatalogEntry(server.URL, payload, 1<<20)
	first := CompilerCache{
		Root:                       root,
		InstallLocker:              testCompilerCacheInstallLocker,
		unsafeAllowHTTP:            true,
		UnsafeAllowPrivateNetworks: true,
	}
	installed, err := first.EnsureCatalogEntry(context.Background(), entry)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Close()

	second := CompilerCache{
		Root:                       root,
		InstallLocker:              testCompilerCacheInstallLocker,
		unsafeAllowHTTP:            true,
		UnsafeAllowPrivateNetworks: true,
	}
	reused, err := second.EnsureCatalogEntry(context.Background(), entry)
	if err != nil {
		t.Fatalf("reuse persisted compiler with unavailable origin: %v", err)
	}
	if reused != installed || hits.Load() != 1 {
		t.Fatalf("reused path=%q installed=%q downloads=%d", reused, installed, hits.Load())
	}
}

func TestCompilerCacheValidHitDoesNotAcquireInstallLock(t *testing.T) {
	payload := []byte("persistent compiler JavaScript")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	root := t.TempDir()
	entry := compilerCatalogEntry(server.URL, payload, 1<<20)
	first := CompilerCache{
		Root: root, InstallLocker: testCompilerCacheInstallLocker,
		unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true,
	}
	installed, err := first.EnsureCatalogEntry(context.Background(), entry)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Close()

	locker := &failingCompilerCacheInstallLocker{}
	second := CompilerCache{
		Root: root, InstallLocker: locker,
		unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true,
	}
	reused, err := second.EnsureCatalogEntry(context.Background(), entry)
	if err != nil || reused != installed || locker.calls.Load() != 0 {
		t.Fatalf("reused=%q installed=%q lock calls=%d error=%v",
			reused, installed, locker.calls.Load(), err)
	}
}

func TestCompilerCacheCleansTemporaryFileAfterInstallLockFailure(t *testing.T) {
	payload := []byte("compiler JavaScript")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	root := t.TempDir()
	locker := &failingCompilerCacheInstallLocker{}
	cache := CompilerCache{
		Root: root, InstallLocker: locker,
		unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true,
	}
	entry := compilerCatalogEntry(server.URL, payload, 1<<20)
	if _, err := cache.EnsureCatalogEntry(context.Background(), entry); err == nil ||
		err.Error() != "install lock should not be used" {
		t.Fatalf("install lock failure = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || locker.calls.Load() != 1 {
		t.Fatalf("cache entries=%d lock calls=%d", len(entries), locker.calls.Load())
	}
}

func TestCompilerCacheAllowsIndependentConcurrentInstall(t *testing.T) {
	payload := []byte("shared persistent compiler JavaScript")
	var hits atomic.Int32
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 2 {
			close(bothStarted)
		}
		<-release
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	root := t.TempDir()
	entry := compilerCatalogEntry(server.URL, payload, 1<<20)
	locker := &trackingCompilerCacheInstallLocker{
		path: filepath.Join(root, string(entry.Language)+"-sha256-"+
			hex.EncodeToString(entry.ArtifactSHA256[:])+".js"),
	}
	results := make(chan string, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			cache := CompilerCache{
				Root:                       root,
				InstallLocker:              locker,
				unsafeAllowHTTP:            true,
				UnsafeAllowPrivateNetworks: true,
			}
			path, err := cache.EnsureCatalogEntry(context.Background(), entry)
			results <- path
			errorsFound <- err
		}()
	}
	select {
	case <-bothStarted:
		close(release)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("independent cache downloads did not overlap")
	}

	var installed string
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
		path := <-results
		if installed == "" {
			installed = path
		} else if path != installed {
			t.Fatalf("cache paths differ: %q != %q", path, installed)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("independent cold misses downloaded %d times", hits.Load())
	}
	if !validCompilerCacheFile(installed, entry.ArtifactSHA256, entry.MaxBytes) {
		t.Fatal("concurrent installation did not leave one valid compiler artifact")
	}
	locker.mu.Lock()
	defer locker.mu.Unlock()
	if locker.calls != 2 || locker.maximum != 1 || len(locker.observations) != 2 ||
		!os.SameFile(locker.observations[0], locker.observations[1]) {
		t.Fatalf("install lock calls=%d maximum=%d observations=%d",
			locker.calls, locker.maximum, len(locker.observations))
	}
}

func TestCompilerCacheFileValidationRetriesIdentityChanges(t *testing.T) {
	payload := []byte("compiler JavaScript")
	digest := sha256.Sum256(payload)

	t.Run("between lstat and open", func(t *testing.T) {
		path := installCompilerCacheTestFile(t, t.TempDir(), "artifact.js", payload)
		replacement := installCompilerCacheTestFile(t, filepath.Dir(path), "replacement.js", payload)
		operations := operatingSystemCompilerCacheFileOperations
		var opens atomic.Int32
		operations.open = func(name string) (*os.File, error) {
			if opens.Add(1) == 1 {
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			}
			return os.Open(name)
		}
		if !validCompilerCacheFileUsing(operations, path, digest, 1<<20) || opens.Load() != 2 {
			t.Fatalf("validation result=false opens=%d", opens.Load())
		}
	})

	t.Run("during digest read", func(t *testing.T) {
		path := installCompilerCacheTestFile(t, t.TempDir(), "artifact.js", payload)
		replacement := installCompilerCacheTestFile(t, filepath.Dir(path), "replacement.js", payload)
		operations := operatingSystemCompilerCacheFileOperations
		var hashes atomic.Int32
		operations.hash = func(file *os.File, maximum int64) ([sha256.Size]byte, error) {
			got, err := hashCompilerCacheFile(file, maximum)
			if hashes.Add(1) == 1 {
				if renameErr := os.Rename(replacement, path); renameErr != nil {
					t.Fatal(renameErr)
				}
			}
			return got, err
		}
		if !validCompilerCacheFileUsing(operations, path, digest, 1<<20) || hashes.Load() != 2 {
			t.Fatalf("validation result=false hashes=%d", hashes.Load())
		}
	})

	t.Run("bounded repeated replacement", func(t *testing.T) {
		root := t.TempDir()
		path := installCompilerCacheTestFile(t, root, "artifact.js", payload)
		operations := operatingSystemCompilerCacheFileOperations
		var opens atomic.Int32
		operations.open = func(name string) (*os.File, error) {
			attempt := opens.Add(1)
			replacement := installCompilerCacheTestFile(
				t, root, fmt.Sprintf("replacement-%d.js", attempt), payload,
			)
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			return os.Open(name)
		}
		if validCompilerCacheFileUsing(operations, path, digest, 1<<20) ||
			opens.Load() != compilerCacheValidationTries {
			t.Fatalf("validation result=true opens=%d", opens.Load())
		}
	})

	t.Run("stable digest mismatch does not retry", func(t *testing.T) {
		path := installCompilerCacheTestFile(t, t.TempDir(), "artifact.js", payload)
		operations := operatingSystemCompilerCacheFileOperations
		var opens atomic.Int32
		operations.open = func(name string) (*os.File, error) {
			opens.Add(1)
			return os.Open(name)
		}
		if validCompilerCacheFileUsing(
			operations, path, sha256.Sum256([]byte("different")), 1<<20,
		) || opens.Load() != 1 {
			t.Fatalf("validation result=true opens=%d", opens.Load())
		}
	})
}

func installCompilerCacheTestFile(
	t *testing.T,
	root string,
	name string,
	payload []byte,
) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestrictedOutboundDialRejectsResolverFailureWithoutDetails(t *testing.T) {
	_, err := dialRestrictedOutboundHost(
		context.Background(), "tcp", "compiler.example:443",
		fixedOutboundResolver{err: errors.New("resolver-secret")}, false, time.Second,
	)
	if err == nil || err.Error() != "resolve restricted outbound host" ||
		strings.Contains(err.Error(), "resolver-secret") {
		t.Fatalf("unexpected resolver error: %v", err)
	}
}
