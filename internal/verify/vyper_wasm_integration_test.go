//go:build integration

package verify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
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
		t.Skip("run through e2e/cmd/testintegration")
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

func TestVyperApplicationLockToRealCompilation(t *testing.T) {
	db := newVyperWASMDatabase(t)
	fixture := newVyperTestCompiler(t)
	catalog, err := NewCompilerCatalog(db, CompilerCatalogOptions{Sources: map[Language]string{LanguageSolidity: "https://binaries.soliditylang.org/wasm/list.json"}, AllowedOrigins: []string{"https://binaries.soliditylang.org"}})
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	parent := &VyperCompiler{Catalog: catalog, Path: fixture.Path, SharedPath: fixture.SharedPath, Cache: &CompilerCache{Root: t.TempDir(), InstallLocker: newCompilerCacheLockTestLocker(t, db), unsafeHTTPClient: lockedVyperClient(t, &downloads)}}
	if err := parent.ValidateRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup(t.Context(), LanguageVyper, "0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	generation := entry.GenerationID
	if _, _, err := catalog.vyperArtifact(t.Context(), generation+1, "0.4.3"); !errors.Is(err, ErrCompilerProvenanceConflict) {
		t.Fatalf("another application generation accepted: %v", err)
	}
	_, artifact, err := catalog.vyperArtifact(t.Context(), generation, "0.4.3")
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
		t.Fatal("database accepted executor outside application binding")
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
	parent.Cache.unsafeHTTPClient = &http.Client{Transport: vyperTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	if _, err := db.Exec(t.Context(), `UPDATE compiler_catalog_entries SET expires_at=now()-interval '1 second' WHERE generation_id=$1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Resolve(t.Context(), LanguageVyper, "0.4.3"); err != nil {
		t.Fatalf("embedded binding depends on remote expiry: %v", err)
	}
	if _, err := parent.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, input); err != nil {
		t.Fatalf("offline bound compile: %v", err)
	}
	if err := removeVyperStaging(filepath.Join(parent.Cache.Root, "vyper-runtime-"+artifact.ManifestSHA256)); err != nil {
		t.Fatal(err)
	}
	// Also remove the authenticated transport cache: no missing-package fallback.
	if err := os.Remove(filepath.Join(parent.Cache.Root, "pypi-"+VyperCompilerSHA256+".whl")); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.CompilePinned(t.Context(), LanguageVyper, "0.4.3", provenance, input); err == nil {
		t.Fatal("missing package compiled offline")
	}
}
