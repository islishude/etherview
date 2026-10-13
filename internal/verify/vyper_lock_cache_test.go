package verify

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	vyper "github.com/islishude/etherview/compiler/vyper"
)

type vyperTestTransport func(*http.Request) (*http.Response, error)

func (f vyperTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func lockedVyperClient(t *testing.T, downloads *atomic.Int32) *http.Client {
	t.Helper()
	recipe, err := vyper.Lookup("0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{}
	for _, r := range []vyper.Recipe{recipe} {
		for _, a := range r.Archives {
			if a.URL == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join("../../.local/vyper-wasm/downloads", a.SHA256))
			if err != nil {
				t.Fatal("run make compiler-install and Vyper matrix preparation: ", err)
			}
			bodies[a.URL] = data
		}
	}
	return &http.Client{Transport: vyperTestTransport(func(r *http.Request) (*http.Response, error) {
		data, ok := bodies[r.URL.String()]
		if !ok {
			t.Fatalf("unexpected download %s", r.URL.Host)
		}
		downloads.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: make(http.Header)}, nil
	})}
}

func TestVyperLockedCacheAuthenticatesAndRepairs(t *testing.T) {
	fixture := newVyperTestCompiler(t)
	recipe, err := vyper.Lookup("0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	var manifest bytes.Buffer
	if err = json.Compact(&manifest, recipe.Manifest); err != nil {
		t.Fatal(err)
	}
	digest := vyper.Digest(append(manifest.Bytes(), '\n'))
	artifact := VyperRuntimeArtifact{ManifestSHA256: digest, RecipeSHA256: recipe.Digest()}
	compilerDigest, _ := decodeCatalogDigest(recipe.CompilerSHA256)
	var downloads atomic.Int32
	parent := &VyperCompiler{Path: fixture.Path, SharedPath: fixture.SharedPath, Cache: &CompilerCache{Root: t.TempDir(), InstallLocker: testCompilerCacheInstallLocker, unsafeHTTPClient: lockedVyperClient(t, &downloads)}}
	entry := CatalogEntry{Version: recipe.Version, ArtifactSHA256: compilerDigest}
	child, err := parent.ensureRuntime(t.Context(), recipe.Version, entry, artifact)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeVyperStaging(child.PackagePath) })
	if downloads.Load() != int32(len(recipe.Archives)) {
		t.Fatalf("downloads %d", downloads.Load())
	}
	// A complete authenticated cache hit must work without any network transport.
	parent.Cache.unsafeHTTPClient = &http.Client{Transport: vyperTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("warm cache used network"); return nil, nil })}
	if _, err = parent.ensureRuntime(t.Context(), recipe.Version, entry, artifact); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(child.PackagePath, "packages.zip")
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = child.validateHelper(); err == nil {
		t.Fatal("corrupt cache accepted")
	}
	if _, err = parent.ensureRuntime(t.Context(), recipe.Version, entry, artifact); err != nil {
		t.Fatal("repair from raw cache: ", err)
	}
	artifact.RecipeSHA256 = "wrong"
	if _, err = parent.ensureRuntime(t.Context(), recipe.Version, entry, artifact); err == nil {
		t.Fatal("changed recipe accepted through warm cache")
	}
}
