package verify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"runtime"
	"strings"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"golang.org/x/mod/semver"
)

const VyperDynamicExecutorKind = "etherview_vyper_v3"
const vyperDynamicSchema = "etherview-vyper-runtime-v3"

// VyperRuntimeArtifact binds a complete, platform-specific executable tree.
// SHA256 authenticates the transport archive; ManifestSHA256 is executor identity.
type VyperRuntimeArtifact struct {
	Platform       string `json:"platform"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	MaxBytes       int64  `json:"max_bytes"`
	Protocol       string `json:"protocol"`
}

type vyperCatalogBuild struct {
	Version        string                 `json:"version"`
	CompilerSHA256 string                 `json:"compiler_sha256"`
	Withdrawn      bool                   `json:"withdrawn"`
	Runtimes       []VyperRuntimeArtifact `json:"runtimes"`
}

type vyperCatalogDocument struct {
	Schema    string              `json:"schema"`
	ExpiresAt time.Time           `json:"expires_at"`
	Builds    []vyperCatalogBuild `json:"builds"`
}

func stableVyperVersion(version string) bool {
	value := "v" + version
	return semver.IsValid(value) && semver.Canonical(value) == value && semver.Prerelease(value) == "" && semver.Build(value) == ""
}

func (catalog *CompilerCatalog) parseVyper(source string, raw []byte) ([]CatalogEntry, error) {
	invalid := errors.New("invalid signed Vyper catalog")
	var envelope struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if strictVyperJSON(raw, &envelope) != nil {
		return nil, invalid
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil {
		return nil, invalid
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(catalog.options.VyperPublicKey) != ed25519.PublicKeySize || !ed25519.Verify(catalog.options.VyperPublicKey, payload, signature) {
		return nil, invalid
	}
	var document vyperCatalogDocument
	if strictVyperJSON(payload, &document) != nil || document.Schema != "etherview-vyper-catalog-v1" || !document.ExpiresAt.After(time.Now()) || len(document.Builds) == 0 || len(document.Builds) > catalog.options.MaxEntries {
		return nil, invalid
	}
	entries := make([]CatalogEntry, 0, len(document.Builds))
	seen := map[string]bool{}
	for _, build := range document.Builds {
		_, supported := VyperVersionCapabilities(build.Version)
		if !supported || !stableVyperVersion(build.Version) || seen[build.Version] {
			return nil, invalid
		}
		seen[build.Version] = true
		digest, err := decodeCatalogDigest(build.CompilerSHA256)
		if err != nil {
			return nil, invalid
		}
		platforms := map[string]bool{}
		for _, artifact := range build.Runtimes {
			if catalog.validateVyperArtifact(artifact) != nil || platforms[artifact.Platform] {
				return nil, invalid
			}
			platforms[artifact.Platform] = true
		}
		if !platforms["linux-amd64"] || !platforms["linux-arm64"] {
			return nil, invalid
		}
		if build.Withdrawn {
			continue
		}
		runtimes, err := json.Marshal(build.Runtimes)
		if err != nil {
			return nil, invalid
		}
		entries = append(entries, CatalogEntry{Language: LanguageVyper, Version: build.Version, Platform: CompilerPlatformPythonWheel, ArtifactURL: source, ArtifactSHA256: digest, MaxBytes: catalog.options.MaxArtifactBytes, VyperRuntimes: runtimes, ExpiresAt: document.ExpiresAt})
	}
	if len(entries) == 0 {
		return nil, invalid
	}
	return entries, nil
}

func strictVyperJSON(raw []byte, target any) error {
	if validateUniqueJSON(raw) != nil {
		return errors.New("invalid Vyper JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (catalog *CompilerCatalog) validateVyperArtifact(artifact VyperRuntimeArtifact) error {
	invalid := errors.New("invalid Vyper runtime artifact")
	if artifact.Platform != "linux-amd64" && artifact.Platform != "linux-arm64" && artifact.Platform != "darwin-arm64" && artifact.Platform != "darwin-amd64" {
		return invalid
	}
	if artifact.Protocol != vyperDynamicSchema || artifact.MaxBytes <= 0 || artifact.MaxBytes > catalog.options.MaxArtifactBytes {
		return invalid
	}
	if _, err := decodeCatalogDigest(artifact.SHA256); err != nil {
		return invalid
	}
	if _, err := decodeCatalogDigest(artifact.ManifestSHA256); err != nil {
		return invalid
	}
	u, err := url.Parse(artifact.URL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(artifact.URL) > 4096 {
		return invalid
	}
	origin, err := canonicalCatalogOrigin(u.Scheme+"://"+u.Host, catalog.options.unsafeAllowHTTP)
	if err != nil {
		return invalid
	}
	if _, ok := catalog.origins[origin]; !ok {
		return invalid
	}
	return nil
}

func (catalog *CompilerCatalog) vyperArtifact(ctx context.Context, generation int64, version string) (CatalogEntry, VyperRuntimeArtifact, error) {
	var entry CatalogEntry
	var digest, encoded []byte
	err := catalog.db.QueryRowContext(ctx, dbgen.VerifyVyperRuntime, generation, version).Scan(&entry.GenerationID, &entry.Version, &digest, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, VyperRuntimeArtifact{}, ErrCompilerVersionUnavailable
	}
	if err != nil {
		return entry, VyperRuntimeArtifact{}, errors.New("read Vyper runtime catalog")
	}
	if len(digest) != sha256.Size {
		return entry, VyperRuntimeArtifact{}, ErrCompilerProvenanceConflict
	}
	copy(entry.ArtifactSHA256[:], digest)
	entry.Language, entry.Platform = LanguageVyper, CompilerPlatformPythonWheel
	var artifacts []VyperRuntimeArtifact
	if strictVyperJSON(encoded, &artifacts) != nil {
		return entry, VyperRuntimeArtifact{}, ErrCompilerProvenanceConflict
	}
	for _, artifact := range artifacts {
		if artifact.Platform == runtime.GOOS+"-"+runtime.GOARCH {
			if catalog.validateVyperArtifact(artifact) != nil {
				return entry, artifact, ErrCompilerProvenanceConflict
			}
			return entry, artifact, nil
		}
	}
	return entry, VyperRuntimeArtifact{}, ErrCompilerVersionUnavailable
}

func (catalog *CompilerCatalog) vyperConfigured() bool {
	if catalog == nil {
		return false
	}
	catalog.sourceMu.RLock()
	defer catalog.sourceMu.RUnlock()
	return strings.TrimSpace(catalog.options.Sources[LanguageVyper]) != "" && len(catalog.options.VyperPublicKey) == ed25519.PublicKeySize
}

func vyperHostSupported(raw []byte) bool {
	var artifacts []VyperRuntimeArtifact
	if json.Unmarshal(raw, &artifacts) != nil {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.Platform == runtime.GOOS+"-"+runtime.GOARCH {
			return true
		}
	}
	return false
}
