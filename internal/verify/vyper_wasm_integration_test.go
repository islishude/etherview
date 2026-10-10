//go:build integration

package verify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/testpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newVyperWASMDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("run through cmd/testintegration")
	}
	admin := openCompilerCacheLockTestDatabase(t, url, "vyper-wasm-admin", 2)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"vyper_wasm_" + hex.EncodeToString(suffix[:])}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := testpgx.Pool(t, config, 4)
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if err := store.RunMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func packageVyperWASMFixture(t *testing.T, compiler *VyperCompiler) ([]byte, VyperRuntimeArtifact) {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, name := range []string{"package-manifest.json", "packages.zip"} {
		data, err := os.ReadFile(filepath.Join(compiler.PackagePath, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0444, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buffer.Bytes())
	identity, err := compiler.validateHelper()
	if err != nil {
		t.Fatal(err)
	}
	executor := hex.EncodeToString(identity.executor[:])
	executors := map[string]string{"linux-amd64": executor, "linux-arm64": executor}
	executors[runtime.GOOS+"-"+runtime.GOARCH] = executor
	return buffer.Bytes(), VyperRuntimeArtifact{
		Platform: CompilerPlatformEmscriptenWASM32, SHA256: hex.EncodeToString(sum[:]), ManifestSHA256: hex.EncodeToString(compiler.ManifestDigest[:]),
		SharedSHA256: vyperSharedManifestSHA256, Protocol: vyperPackageSchema, MaxBytes: int64(buffer.Len()),
		ExecutorDigests: executors,
	}
}

func TestVyperWASMSignedCatalogToRealCompilation(t *testing.T) {
	db := newVyperWASMDatabase(t)
	fixture := newVyperTestCompiler(t)
	archive, artifact := packageVyperWASMFixture(t, fixture)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var catalogBytes []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			_, _ = w.Write(catalogBytes)
		case "/package.tar.gz":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	artifact.URL = server.URL + "/package.tar.gz"
	payload, err := json.Marshal(vyperCatalogDocument{Schema: "etherview-vyper-catalog-v2", ExpiresAt: time.Now().Add(time.Hour), Builds: []vyperCatalogBuild{{Version: "0.4.3", CompilerSHA256: VyperCompilerSHA256, Runtimes: []VyperRuntimeArtifact{artifact}}}})
	if err != nil {
		t.Fatal(err)
	}
	catalogBytes, err = json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCompilerCatalog(db, CompilerCatalogOptions{Sources: map[Language]string{LanguageVyper: server.URL + "/catalog.json"}, VyperPublicKey: public, AllowedOrigins: []string{server.URL}, UnsafeAllowPrivateNetworks: true, unsafeHTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	parent := &VyperCompiler{Catalog: catalog, Path: fixture.Path, SharedPath: fixture.SharedPath, Cache: &CompilerCache{Root: t.TempDir(), InstallLocker: newCompilerCacheLockTestLocker(t, db), unsafeHTTPClient: server.Client()}}
	if err := parent.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	generation, err := catalog.Refresh(t.Context(), LanguageVyper)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := parent.Resolve(t.Context(), LanguageVyper, "0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if provenance.CatalogGeneration != generation || provenance.ExecutorKind != VyperDynamicExecutorKind {
		t.Fatal("wrong runtime binding")
	}
	repository, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile("testdata/compiler/vyper/versions/0.4.3/plain.input.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.SubmitV2(t.Context(), SubmissionV2{Kind: JobVyperStandardJSON, Language: LanguageVyper, CompilerVersion: "0.4.3", TargetFile: "A.vy", StandardJSON: input, Bytecodes: []BytecodePair{{Runtime: "0x6000"}}})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(t.Context(), "wasm-integration", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim: %t %v", found, err)
	}
	changed := provenance
	changed.ExecutorDigest[0] ^= 1
	if err := repository.BindCompiler(t.Context(), lease, changed); err == nil {
		t.Fatal("database accepted executor outside signed catalog")
	}
	if err := repository.BindCompiler(t.Context(), lease, provenance); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct{ input, output string }{{"input", "output"}, {"modified", "modified_output"}} {
		raw, err := os.ReadFile("testdata/compiler/vyper/versions/0.4.3/plain." + variant.input + ".json")
		if err != nil {
			t.Fatal(err)
		}
		actual, err := parent.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, raw)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile("testdata/compiler/vyper/versions/0.4.3/plain." + variant.output + ".json")
		if err != nil {
			t.Fatal(err)
		}
		assertVyperReference(t, actual, expected, false)
	}
	server.Close()
	if _, err := db.Exec(t.Context(), `UPDATE compiler_catalog_entries SET expires_at=now()-interval '1 second' WHERE generation_id=$1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Resolve(t.Context(), LanguageVyper, "0.4.3"); !errors.Is(err, ErrCompilerCatalogStale) {
		t.Fatalf("expired new binding: %v", err)
	}
	if _, err := parent.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, input); err != nil {
		t.Fatalf("offline bound compile: %v", err)
	}
	if err := removeVyperStaging(filepath.Join(parent.Cache.Root, "vyper-runtime-"+artifact.ManifestSHA256)); err != nil {
		t.Fatal(err)
	}
	// Also remove the authenticated transport cache: no missing-package fallback.
	if err := os.Remove(filepath.Join(parent.Cache.Root, "vyper-archive-"+artifact.SHA256+".tar.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, input); err == nil {
		t.Fatal("missing package compiled offline")
	}
}
