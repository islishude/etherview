package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestCompilerDownloadRejectsRedirectsAndPrivateDNS(t *testing.T) {
	payload := []byte("compiler binary")
	digest := sha256.Sum256(payload)
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
		unsafeAllowPrivateNetworks: true,
		Artifacts: map[Language]map[string]CompilerArtifact{
			LanguageSolidity: {"1.2.3": {URL: redirect.URL, SHA256: hex.EncodeToString(digest[:])}},
		},
	}
	if _, err := cache.Ensure(context.Background(), LanguageSolidity, "1.2.3"); err == nil || err.Error() != "download compiler artifact" {
		t.Fatalf("unexpected redirect error: %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests", targetHits.Load())
	}

	cache.Root = t.TempDir()
	cache.unsafeAllowHTTP = false
	cache.unsafeAllowPrivateNetworks = false
	cache.resolver = fixedOutboundResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	cache.Artifacts[LanguageSolidity]["1.2.3"] = CompilerArtifact{
		URL: "https://compiler.example/artifact", SHA256: hex.EncodeToString(digest[:]),
	}
	if _, err := cache.Ensure(context.Background(), LanguageSolidity, "1.2.3"); err == nil || err.Error() != "download compiler artifact" {
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
	digest := sha256.Sum256(payload)
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
				unsafeAllowPrivateNetworks: true,
				Artifacts: map[Language]map[string]CompilerArtifact{
					LanguageSolidity: {"1": {
						URL: server.URL, SHA256: hex.EncodeToString(digest[:]), MaxBytes: int64(len(payload) - 1),
					}},
				},
			}
			if _, err := cache.Ensure(context.Background(), LanguageSolidity, "1"); err == nil || err.Error() != "compiler artifact exceeds size limit" {
				t.Fatalf("unexpected size error: %v", err)
			}
		})
	}
}

func TestCompilerCacheRejectsUnsafeRootAndReplacesUnsafeEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX cache ownership and symlink semantics")
	}
	payload := []byte("compiler binary")
	digest := sha256.Sum256(payload)
	artifact := CompilerArtifact{URL: "https://compiler.example/artifact", SHA256: hex.EncodeToString(digest[:])}

	t.Run("symlink root", func(t *testing.T) {
		realRoot := t.TempDir()
		root := filepath.Join(t.TempDir(), "cache")
		if err := os.Symlink(realRoot, root); err != nil {
			t.Fatal(err)
		}
		cache := CompilerCache{Root: root, Artifacts: map[Language]map[string]CompilerArtifact{
			LanguageSolidity: {"1": artifact},
		}}
		if _, err := cache.Ensure(context.Background(), LanguageSolidity, "1"); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
			t.Fatalf("unexpected symlink root error: %v", err)
		}
	})

	t.Run("writable root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o770); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Chmod(root, 0o700); err != nil {
				t.Errorf("restore compiler cache root permissions: %v", err)
			}
		}()
		cache := CompilerCache{Root: root, Artifacts: map[Language]map[string]CompilerArtifact{
			LanguageSolidity: {"1": artifact},
		}}
		if _, err := cache.Ensure(context.Background(), LanguageSolidity, "1"); err == nil || !strings.Contains(err.Error(), "write access") {
			t.Fatalf("unexpected writable root error: %v", err)
		}
	})

	t.Run("symlink entry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		defer server.Close()
		root := t.TempDir()
		external := filepath.Join(t.TempDir(), "external")
		if err := os.WriteFile(external, []byte("do not replace"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "solidity-1"+executableSuffix())
		if err := os.Symlink(external, path); err != nil {
			t.Fatal(err)
		}
		cache := CompilerCache{
			Root: root, unsafeAllowHTTP: true, unsafeAllowPrivateNetworks: true,
			Artifacts: map[Language]map[string]CompilerArtifact{
				LanguageSolidity: {"1": {URL: server.URL, SHA256: hex.EncodeToString(digest[:])}},
			},
		}
		installed, err := cache.Ensure(context.Background(), LanguageSolidity, "1")
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(installed)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o500 {
			t.Fatalf("installed mode=%v error=%v", info, err)
		}
		externalData, err := os.ReadFile(external)
		if err != nil || string(externalData) != "do not replace" {
			t.Fatalf("external data=%q error=%v", externalData, err)
		}
	})

	t.Run("over-permissive entry", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write(payload)
		}))
		defer server.Close()
		root := t.TempDir()
		path := filepath.Join(root, "solidity-1"+executableSuffix())
		if err := os.WriteFile(path, payload, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
		cache := CompilerCache{
			Root: root, unsafeAllowHTTP: true, unsafeAllowPrivateNetworks: true,
			Artifacts: map[Language]map[string]CompilerArtifact{
				LanguageSolidity: {"1": {URL: server.URL, SHA256: hex.EncodeToString(digest[:])}},
			},
		}
		installed, err := cache.Ensure(context.Background(), LanguageSolidity, "1")
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(installed)
		if err != nil || info.Mode().Perm() != 0o500 || hits.Load() != 1 {
			t.Fatalf("mode=%v hits=%d error=%v", info, hits.Load(), err)
		}
	})
}

func TestCompilerCacheSerializesConcurrentInstall(t *testing.T) {
	payload := []byte("compiler binary")
	digest := sha256.Sum256(payload)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	cache := CompilerCache{
		Root: t.TempDir(), unsafeAllowHTTP: true, unsafeAllowPrivateNetworks: true,
		Artifacts: map[Language]map[string]CompilerArtifact{
			LanguageSolidity: {"1.2.3": {URL: server.URL, SHA256: hex.EncodeToString(digest[:])}},
		},
	}
	errorsFound := make(chan error, 16)
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			_, err := cache.Ensure(context.Background(), LanguageSolidity, "1.2.3")
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

func TestProcessCompilerDoesNotExposeDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	payload := []byte("#!/bin/sh\necho compiler-secret >&2\nexit 2\n")
	digest := sha256.Sum256(payload)
	root := t.TempDir()
	path := filepath.Join(root, "solidity-1"+executableSuffix())
	if err := os.WriteFile(path, payload, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	compiler := ProcessCompiler{Cache: &CompilerCache{
		Root: root,
		Artifacts: map[Language]map[string]CompilerArtifact{
			LanguageSolidity: {"1": {URL: "https://compiler.example/artifact", SHA256: hex.EncodeToString(digest[:])}},
		},
	}}
	_, err := compiler.Compile(context.Background(), LanguageSolidity, "1", []byte(`{}`))
	if err == nil || err.Error() != "compiler failed" || strings.Contains(err.Error(), "compiler-secret") {
		t.Fatalf("unexpected compiler error: %v", err)
	}
}

func TestContainerCompilerValidatesAndAppliesIsolation(t *testing.T) {
	compiler, logPath := newFakeContainerCompiler(t)
	if compiler.HardIsolated() {
		t.Fatal("unvalidated compiler reported hard isolation")
	}
	if err := compiler.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !compiler.HardIsolated() {
		t.Fatal("validated compiler did not report hard isolation")
	}
	output, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`))
	if err != nil || string(output) != `{"contracts":{}}` {
		t.Fatalf("output=%q error=%v", output, err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logData)
	for _, required := range []string{
		"version", "image inspect registry.example/solc@sha256:", "run --pull=never --name=etherview-compiler-",
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--user=65532:65532", "--memory=128m", "--memory-swap=128m", "--cpus=1.5",
		"--pids-limit=33", "--ulimit=nofile=64:64", "--ulimit=core=0",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,mode=0700",
		"rm -f etherview-compiler-",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("runtime log does not contain %q:\n%s", required, log)
		}
	}
	if strings.Contains(log, "run --rm") {
		t.Fatalf("runtime used auto-removal instead of verified cleanup:\n%s", log)
	}
	provenance, err := compiler.Provenance(LanguageSolidity, "0.8.30")
	if err != nil || !provenance.HardIsolated || hex.EncodeToString(provenance.Digest[:]) != strings.Repeat("1", 64) {
		t.Fatalf("provenance=%+v error=%v", provenance, err)
	}

	compiler.Images[LanguageSolidity]["0.8.30"] = "registry.example/solc@sha256:" + strings.Repeat("2", 64)
	provenance, err = compiler.Provenance(LanguageSolidity, "0.8.30")
	if err != nil || hex.EncodeToString(provenance.Digest[:]) != strings.Repeat("1", 64) {
		t.Fatalf("validated provenance changed: %+v error=%v", provenance, err)
	}
	if _, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`)); err == nil || err.Error() != "container compiler runtime is not validated" {
		t.Fatalf("mutated manifest compiled: %v", err)
	}
}

func TestContainerCompilerCleansUpAndRedactsFailures(t *testing.T) {
	compiler, logPath := newFakeContainerCompiler(t)
	if err := compiler.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", "fail")
	if _, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`)); err == nil || err.Error() != "sandboxed compiler failed" || strings.Contains(err.Error(), "runtime-secret") {
		t.Fatalf("unexpected failure: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(logData), "rm -f etherview-compiler-") {
		t.Fatalf("failed compiler was not cleaned up: %q error=%v", logData, err)
	}

	t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", "timeout")
	compiler.Timeout = 50 * time.Millisecond
	started := time.Now()
	if _, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`)); err == nil || err.Error() != "sandboxed compiler timed out" {
		t.Fatalf("unexpected timeout: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("compiler timeout cleanup exceeded its bound")
	}
	logData, err = os.ReadFile(logPath)
	if err != nil || strings.Count(string(logData), "rm -f etherview-compiler-") < 2 {
		t.Fatalf("timed-out compiler was not cleaned up: %q error=%v", logData, err)
	}
}

func TestContainerCompilerFailsClosedWhenCleanupFailsOrHangs(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
	}{
		{name: "force removal fails", mode: "cleanup-fail"},
		{name: "force removal hangs", mode: "cleanup-hang"},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiler, _ := newFakeContainerCompiler(t)
			if err := compiler.ValidateRuntime(context.Background()); err != nil {
				t.Fatal(err)
			}
			compiler.cleanupTimeout = 50 * time.Millisecond
			t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", test.mode)
			started := time.Now()
			_, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`))
			if !errors.Is(err, ErrCompilerCleanup) || err.Error() != ErrCompilerCleanup.Error() {
				t.Fatalf("cleanup error=%v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("failed cleanup exceeded its bound")
			}
		})
	}
}

func TestContainerCompilerCleansUpPanicsWithoutLeakingDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		want error
	}{
		{name: "panic", want: ErrCompilerRuntime},
		{name: "panic with cleanup failure", mode: "panic-cleanup-fail", want: ErrCompilerCleanup},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiler, logPath := newFakeContainerCompiler(t)
			if err := compiler.ValidateRuntime(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", test.mode)
			compiler.runContainer = func(command *exec.Cmd) error {
				if err := command.Run(); err != nil {
					return err
				}
				panic("runtime-secret")
			}
			_, err := compiler.Compile(context.Background(), LanguageSolidity, "0.8.30", []byte(`{}`))
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() || strings.Contains(err.Error(), "runtime-secret") {
				t.Fatalf("panic boundary error=%v, want=%v", err, test.want)
			}
			logData, readErr := os.ReadFile(logPath)
			if readErr != nil {
				t.Fatalf("read panicked compiler log: %v", readErr)
			}
			logText := string(logData)
			runOffset := strings.Index(logText, "run --pull=never --name=etherview-compiler-")
			rmOffset := strings.Index(logText, "rm -f etherview-compiler-")
			if runOffset < 0 || rmOffset < 0 || runOffset >= rmOffset ||
				strings.Count(logText, "run --pull=never --name=etherview-compiler-") != 1 ||
				strings.Count(logText, "rm -f etherview-compiler-") != 1 {
				t.Fatalf("panicked compiler was not cleaned up: %q error=%v", logData, readErr)
			}
		})
	}
}

func TestContainerCompilerRejectsMissingImagesAndInvalidLimits(t *testing.T) {
	compiler, _ := newFakeContainerCompiler(t)
	t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", "missing-image")
	if err := compiler.ValidateRuntime(context.Background()); err == nil || err.Error() != "compiler container image is unavailable" || strings.Contains(err.Error(), "runtime-secret") {
		t.Fatalf("unexpected image error: %v", err)
	}
	if compiler.HardIsolated() {
		t.Fatal("compiler with a missing image reported hard isolation")
	}

	t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", "")
	compiler.Memory = "unlimited"
	if err := compiler.ValidateRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "memory limit") {
		t.Fatalf("unexpected memory error: %v", err)
	}
	compiler.Memory = "128m"
	compiler.Images[LanguageSolidity]["0.8.30"] = "registry.example/solc@sha256:" + strings.Repeat("0", 64)
	if err := compiler.ValidateRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("unexpected digest error: %v", err)
	}
}

func newFakeContainerCompiler(t *testing.T) (*ContainerCompiler, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	directory := t.TempDir()
	logPath := filepath.Join(directory, "runtime.log")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$ETHERVIEW_TEST_RUNTIME_LOG"
if [ "$1" = "version" ]; then
  exit 0
fi
if [ "$1" = "image" ]; then
  if [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "missing-image" ]; then
    printf '%s\n' 'runtime-secret' >&2
    exit 3
  fi
  exit 0
fi
if [ "$1" = "rm" ]; then
	if [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "cleanup-fail" ] ||
	   [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "panic-cleanup-fail" ]; then
		printf '%s\n' 'runtime-secret' >&2
		exit 6
	fi
	if [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "cleanup-hang" ]; then
		exec /bin/sleep 10
	fi
  exit 0
fi
if [ "$1" = "run" ]; then
  if [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "timeout" ]; then
    exec /bin/sleep 10
  fi
  if [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "fail" ] ||
	 [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "cleanup-fail" ] ||
	 [ "${ETHERVIEW_TEST_RUNTIME_MODE:-}" = "cleanup-hang" ]; then
    printf '%s\n' 'runtime-secret' >&2
    exit 4
  fi
  printf '%s' '{"contracts":{}}'
  exit 0
fi
exit 5
`
	runtimePath := filepath.Join(directory, "docker")
	if err := os.WriteFile(runtimePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ETHERVIEW_TEST_RUNTIME_LOG", logPath)
	t.Setenv("ETHERVIEW_TEST_RUNTIME_MODE", "")
	return &ContainerCompiler{
		Runtime: "docker",
		Images: map[Language]map[string]string{
			LanguageSolidity: {"0.8.30": "registry.example/solc@sha256:" + strings.Repeat("1", 64)},
		},
		Timeout: time.Second, MaxInputBytes: 1024, MaxOutputBytes: 1024,
		Memory: "128m", CPUs: "1.5", PIDs: 33,
	}, logPath
}

func TestRestrictedOutboundDialRejectsResolverFailureWithoutDetails(t *testing.T) {
	_, err := dialRestrictedOutboundHost(
		context.Background(), "tcp", "compiler.example:443",
		fixedOutboundResolver{err: errors.New("resolver-secret")}, false, time.Second,
	)
	if err == nil || err.Error() != "resolve restricted outbound host" || strings.Contains(err.Error(), "resolver-secret") {
		t.Fatalf("unexpected resolver error: %v", err)
	}
}
