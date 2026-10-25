package verify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompilerDownloadFailureClassification(t *testing.T) {
	for _, test := range []struct {
		status int
		code   ErrorCode
		retry  bool
	}{
		{403, ErrorCompilerDownloadRejected, false}, {404, ErrorCompilerDownloadRejected, false},
		{408, ErrorCompilerDownloadTimeout, true}, {429, ErrorCompilerDownloadRateLimited, true},
		{500, ErrorCompilerDownloadUnavailable, true}, {503, ErrorCompilerDownloadUnavailable, true},
	} {
		t.Run(string(test.code)+http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "9999999999999")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("secret upstream diagnostics"))
			}))
			defer server.Close()
			cache := CompilerCache{Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker, unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true}
			_, err := cache.EnsureCatalogEntry(t.Context(), compilerCatalogEntry(server.URL, []byte("compiler"), 1024))
			var failure CompilerDownloadError
			if !errors.As(err, &failure) || failure.Code != test.code || failure.retryable() != test.retry || failure.RetryAfter != 30*time.Second || strings.Contains(err.Error(), "secret") {
				t.Fatalf("failure=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compilerDownloadFailure(ctx, errors.New("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

type downloadTestCompiler struct {
	*verifyTestCompiler
	cache            *CompilerCache
	entry            CatalogEntry
	resolveDownloads bool
}

func (compiler *downloadTestCompiler) Resolve(ctx context.Context, language Language, version string) (CompilerProvenance, error) {
	if compiler.resolveDownloads {
		if _, err := compiler.cache.EnsureCatalogEntry(ctx, compiler.entry); err != nil {
			return CompilerProvenance{}, err
		}
	}
	return compiler.verifyTestCompiler.Resolve(ctx, language, version)
}

func (compiler *downloadTestCompiler) CompilePinned(ctx context.Context, language Language, version string, provenance CompilerProvenance, input []byte) ([]byte, error) {
	if _, err := compiler.cache.EnsureCatalogEntry(ctx, compiler.entry); err != nil {
		return nil, err
	}
	return compiler.verifyTestCompiler.CompilePinned(ctx, language, version, provenance, input)
}

func TestWorkerCompilerDownloadRecoveryAndBudget(t *testing.T) {
	for _, test := range []struct {
		name             string
		recover, resolve bool
	}{
		{"compile_recovery", true, false}, {"compile_exhaustion", false, false},
		{"resolve_recovery", true, true}, {"resolve_exhaustion", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			payload := []byte("authenticated compiler fixture")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				call := calls.Add(1)
				if !test.recover || call == 1 {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			compiler := &downloadTestCompiler{
				verifyTestCompiler: &verifyTestCompiler{provenance: testSolcJSProvenance(), output: candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{"A.sol": {"A": {creation: []byte{0x60, 1}, runtime: []byte{0x60, 1}}}})},
				cache:              &CompilerCache{Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker, unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true},
				entry:              compilerCatalogEntry(server.URL, payload, 1024),
				resolveDownloads:   test.resolve,
			}
			repository := &verifyMemoryRepository{lease: verifyV2Lease(), claimFound: true}
			found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
			if err != nil || !found {
				t.Fatalf("found=%t err=%v", found, err)
			}
			if test.recover {
				if calls.Load() != 2 || compiler.compileCalls != 2 || len(repository.failures) != 0 || len(repository.outcomes) != 1 || repository.outcomes[0] != "verification_success" {
					t.Fatalf("calls=%d compile=%d failures=%v outcomes=%v", calls.Load(), compiler.compileCalls, repository.failures, repository.outcomes)
				}
			} else if calls.Load() != 3 || compiler.compileCalls != 0 || len(repository.failures) != 1 || repository.failures[0] != ErrorCompilerDownloadUnavailable {
				t.Fatalf("calls=%d failures=%v", calls.Load(), repository.failures)
			}
		})
	}
}

func TestWorkerCompilerDownloadRetryLosesLease(t *testing.T) {
	repository := &verifyMemoryRepository{lease: verifyV2Lease(), claimFound: true, renewError: ErrLeaseLost}
	compiler := &verifyTestCompiler{provenance: testSolcJSProvenance(), compileError: CompilerDownloadError{Code: ErrorCompilerDownloadUnavailable}}
	worker := newVerifyTestWorker(t, repository, compiler)
	worker.options.LeaseDuration = 3 * time.Millisecond
	_, err := worker.ProcessOne(t.Context())
	if !errors.Is(err, ErrLeaseLost) || compiler.compileCalls != 1 || len(repository.failures) != 0 {
		t.Fatalf("err=%v calls=%d failures=%v", err, compiler.compileCalls, repository.failures)
	}
}

func TestWorkerCompilerDownloadPermanentFailures(t *testing.T) {
	for _, code := range []ErrorCode{ErrorCompilerChecksum, ErrorCompilerDownloadRejected, ErrorCompilerArtifactTooLarge} {
		t.Run(string(code), func(t *testing.T) {
			repository := &verifyMemoryRepository{lease: verifyV2Lease(), claimFound: true}
			compiler := &verifyTestCompiler{provenance: testSolcJSProvenance(), compileError: CompilerDownloadError{Code: code}}
			if _, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if compiler.compileCalls != 1 || len(repository.failures) != 1 || repository.failures[0] != code {
				t.Fatalf("calls=%d failures=%v", compiler.compileCalls, repository.failures)
			}
		})
	}
}

func TestCompilerDownloadTimeoutAndTruncatedBody(t *testing.T) {
	for _, timeout := range []bool{true, false} {
		t.Run(map[bool]string{true: "timeout", false: "truncated"}[timeout], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if timeout {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("short"))
			}))
			defer server.Close()
			deadline := time.Second
			if timeout {
				deadline = 50 * time.Millisecond
			}
			cache := CompilerCache{Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker, Timeout: deadline, unsafeAllowHTTP: true, UnsafeAllowPrivateNetworks: true}
			_, err := cache.EnsureCatalogEntry(t.Context(), compilerCatalogEntry(server.URL, []byte("compiler"), 1024))
			want := ErrorCompilerDownloadUnavailable
			if timeout {
				want = ErrorCompilerDownloadTimeout
			}
			if err == nil || err.Error() != string(want) {
				t.Fatalf("error=%v want=%s", err, want)
			}
		})
	}
}
