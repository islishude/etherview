package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"

	vyper "github.com/islishude/etherview/compiler/vyper"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"golang.org/x/mod/semver"
)

const VyperDynamicExecutorKind = "node_vyper_wasm_v1"

// Persisted application provenance, never a remotely supplied catalog.
type VyperRuntimeArtifact struct {
	ExecutorDigests map[string]string `json:"executor_digests"`
	SharedSHA256    string            `json:"shared_sha256"`
	Platform        string            `json:"platform"`
	ManifestSHA256  string            `json:"manifest_sha256"`
	RecipeSHA256    string            `json:"recipe_sha256"`
	Protocol        string            `json:"protocol"`
}

func stableVyperVersion(version string) bool {
	value := "v" + version
	return semver.IsValid(value) && semver.Canonical(value) == value && semver.Prerelease(value) == "" && semver.Build(value) == ""
}

func strictVyperJSON(raw []byte, target any) error {
	if validateUniqueJSON(raw) != nil {
		return errors.New("invalid Vyper JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// installVyperLock registers an immutable application snapshot after local
// runtime self-tests. No download, signature, expiry or remote head is involved.
func (catalog *CompilerCatalog) installVyperLock(ctx context.Context, compiler *VyperCompiler) error {
	recipes, err := vyper.Recipes()
	if err != nil {
		return err
	}
	entries := make([]CatalogEntry, 0, len(recipes))
	for _, recipe := range recipes {
		host, shared, err := compiler.hostIdentity(recipe.Version)
		if err != nil {
			return err
		}
		spec, ok := vyperRuntimeFor(recipe.Version)
		if !ok || recipe.Pyodide != spec.Pyodide || recipe.Python != spec.Python {
			return ErrCompilerProvenanceConflict
		}
		var compact bytes.Buffer
		if json.Compact(&compact, recipe.Manifest) != nil {
			return ErrCompilerProvenanceConflict
		}
		manifest := sha256.Sum256(append(compact.Bytes(), '\n'))
		executor := vyperExecutorDigest(host, shared, manifest)
		artifact := VyperRuntimeArtifact{Platform: CompilerPlatformEmscriptenWASM32, Protocol: vyperPackageSchema, SharedSHA256: hex.EncodeToString(shared[:]), ManifestSHA256: hex.EncodeToString(manifest[:]), RecipeSHA256: recipe.Digest(), ExecutorDigests: map[string]string{runtime.GOOS + "-" + runtime.GOARCH: hex.EncodeToString(executor[:])}}
		encoded, _ := json.Marshal([]VyperRuntimeArtifact{artifact})
		digest, err := decodeCatalogDigest(recipe.CompilerSHA256)
		if err != nil {
			return err
		}
		entries = append(entries, CatalogEntry{Language: LanguageVyper, Version: recipe.Version, Platform: CompilerPlatformPythonWheel, ArtifactURL: "embedded:vyper/" + recipe.Digest(), ArtifactSHA256: digest, MaxBytes: vyper.MaxArchive, VyperRuntimes: encoded})
	}
	raw, _ := json.Marshal(entries)
	digest := sha256.Sum256(raw)
	generation, err := catalog.persist(ctx, LanguageVyper, "embedded:vyper/"+hex.EncodeToString(digest[:]), digest, entries)
	if err != nil {
		return err
	}
	catalog.sourceMu.Lock()
	defer catalog.sourceMu.Unlock()
	catalog.vyperEntries = make(map[string]CatalogEntry, len(entries))
	for _, entry := range entries {
		entry.GenerationID = generation
		catalog.vyperEntries[entry.Version] = entry
	}
	return nil
}

func (catalog *CompilerCatalog) vyperArtifact(ctx context.Context, generation int64, version string) (CatalogEntry, VyperRuntimeArtifact, error) {
	entry, err := catalog.Lookup(ctx, LanguageVyper, version)
	if err != nil {
		return entry, VyperRuntimeArtifact{}, err
	}
	if entry.GenerationID != generation {
		return entry, VyperRuntimeArtifact{}, ErrCompilerProvenanceConflict
	}
	row, err := dbgen.New(catalog.db).VerifyVyperRuntime(ctx, generation, version)
	if err != nil {
		return entry, VyperRuntimeArtifact{}, errors.New("read application Vyper binding")
	}
	var actual, expected []VyperRuntimeArtifact
	if strictVyperJSON(row.VyperRuntimes, &actual) != nil || strictVyperJSON(entry.VyperRuntimes, &expected) != nil || len(actual) != 1 || len(expected) != 1 || !equalJSONValue(row.VyperRuntimes, entry.VyperRuntimes) || !bytes.Equal(row.ArtifactSha256, entry.ArtifactSHA256[:]) {
		return entry, VyperRuntimeArtifact{}, ErrCompilerProvenanceConflict
	}
	return entry, actual[0], nil
}
