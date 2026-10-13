package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	vyper "github.com/islishude/etherview/compiler/vyper"
)

func cancellationVyperRuntime(t *testing.T, transport vyperTestTransport) (*VyperCompiler, CatalogEntry, VyperRuntimeArtifact) {
	t.Helper()
	recipe, err := vyper.Lookup("0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	var manifest bytes.Buffer
	if err := json.Compact(&manifest, recipe.Manifest); err != nil {
		t.Fatal(err)
	}
	digest, err := decodeCatalogDigest(recipe.CompilerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &VyperCompiler{Cache: &CompilerCache{Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker, unsafeHTTPClient: &http.Client{Transport: transport}}}
	return compiler, CatalogEntry{Version: recipe.Version, ArtifactSHA256: digest}, VyperRuntimeArtifact{RecipeSHA256: recipe.Digest(), ManifestSHA256: vyper.Digest(append(manifest.Bytes(), '\n'))}
}

func TestVyperAssemblyPreservesCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			compiler, entry, artifact := cancellationVyperRuntime(t, func(r *http.Request) (*http.Response, error) {
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			compiler.Timeout = 20 * time.Millisecond
			_, err := compiler.ensureRuntime(ctx, entry.Version, entry, artifact)
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
			}
			if err != want {
				t.Fatalf("error = %v, want sentinel %v", err, want)
			}
		})
	}
}

type vyperAssemblyLeaseRepository struct {
	verifyMemoryRepository
	started <-chan struct{}
}

func (r *vyperAssemblyLeaseRepository) Renew(ctx context.Context, _ VerificationLease, _ time.Duration) error {
	select {
	case <-r.started:
		return ErrLeaseLost
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestVyperAssemblyLeaseLossRemainsRecoverable(t *testing.T) {
	started := make(chan struct{})
	compiler, entry, artifact := cancellationVyperRuntime(t, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	repository := &vyperAssemblyLeaseRepository{started: started}
	worker := newVerifyTestWorker(t, repository, &verifyTestCompiler{})
	worker.options.LeaseDuration = 3 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := runWithLeaseHeartbeat(ctx, worker, VerificationLease{}, func(ctx context.Context) (*VyperCompiler, error) {
		return compiler.ensureRuntime(ctx, entry.Version, entry, artifact)
	})
	if !errors.Is(err, context.Canceled) || !recoverableVerificationLeaseLoss(err) {
		t.Fatalf("assembly lease loss must remain recoverable: %v", err)
	}
}

func TestVyperAssemblyFailureRemainsSanitized(t *testing.T) {
	compiler, entry, artifact := cancellationVyperRuntime(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream credentials must not escape")
	})
	_, err := compiler.ensureRuntime(t.Context(), entry.Version, entry, artifact)
	if err == nil || err.Error() != "assemble locked Vyper package" || errors.Unwrap(err) != nil {
		t.Fatalf("unsanitized assembly error: %v", err)
	}
}
