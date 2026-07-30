package verify

import (
	"context"
	"crypto/sha256"
	"errors"
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
		cache := CompilerCache{Root: root}
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
