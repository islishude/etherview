package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultCatalogBytes       = int64(8 << 20)
	defaultCatalogEntries     = 4096
	defaultCompilerArtifactMB = int64(200 << 20)
	defaultCatalogFreshness   = 24 * time.Hour
)

var (
	ErrCompilerCatalogStale       = errors.New("compiler catalog is stale")
	ErrCompilerCatalogUnavailable = errors.New("compiler catalog is unavailable")
	ErrCompilerVersionUnavailable = errors.New("compiler version is unavailable")
)

// CatalogEntry is an immutable compiler artifact identity from one persisted
// catalog generation. Yul uses the Solidity compiler catalog.
type CatalogEntry struct {
	GenerationID   int64
	Language       Language
	Version        string
	Platform       string
	ArtifactURL    string
	ArtifactSHA256 [sha256.Size]byte
	MaxBytes       int64
	FetchedAt      time.Time
}

type CompilerCatalogOptions struct {
	Sources                    map[Language]string
	Platform                   string
	AllowedOrigins             []string
	Timeout                    time.Duration
	MaxCatalogBytes            int64
	MaxEntries                 int
	MaxArtifactBytes           int64
	Freshness                  time.Duration
	UnsafeAllowPrivateNetworks bool
	unsafeHTTPClient           *http.Client
	unsafeAllowHTTP            bool
	resolver                   outboundResolver
}

// CompilerCatalog refreshes immutable PostgreSQL generations and resolves
// versions only from the current, sufficiently fresh generation.
type CompilerCatalog struct {
	db               *sql.DB
	options          CompilerCatalogOptions
	origins          map[string]struct{}
	automaticSources map[Language]bool
	sourceMu         sync.RWMutex
}

func NewCompilerCatalog(db *sql.DB, options CompilerCatalogOptions) (*CompilerCatalog, error) {
	if db == nil {
		return nil, errors.New("compiler catalog requires a database")
	}
	catalog, err := newCompilerCatalogParser(options)
	if err != nil {
		return nil, err
	}
	catalog.db = db
	return catalog, nil
}

func newCompilerCatalogParser(options CompilerCatalogOptions) (*CompilerCatalog, error) {
	if options.MaxCatalogBytes <= 0 {
		options.MaxCatalogBytes = defaultCatalogBytes
	}
	if options.MaxCatalogBytes > defaultCatalogBytes {
		return nil, errors.New("compiler catalog limit exceeds 8 MiB")
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = defaultCatalogEntries
	}
	if options.MaxEntries > defaultCatalogEntries {
		return nil, errors.New("compiler catalog entry limit exceeds 4096")
	}
	if options.MaxArtifactBytes <= 0 {
		options.MaxArtifactBytes = defaultCompilerArtifactMB
	}
	if options.MaxArtifactBytes > defaultCompilerArtifactMB {
		return nil, errors.New("compiler artifact limit exceeds 200 MiB")
	}
	if options.Freshness <= 0 {
		options.Freshness = defaultCatalogFreshness
	}
	if options.Timeout <= 0 {
		options.Timeout = 20 * time.Second
	}
	if options.Platform == "" {
		options.Platform = CompilerPlatformEmscriptenWASM32
	}
	if options.Platform != CompilerPlatformEmscriptenWASM32 {
		return nil, errors.New("compiler catalog platform must be emscripten-wasm32")
	}
	sources := make(map[Language]string, len(options.Sources))
	for language, source := range options.Sources {
		sources[language] = source
	}
	options.Sources = sources
	origins := make(map[string]struct{}, len(options.AllowedOrigins))
	for _, raw := range options.AllowedOrigins {
		origin, err := canonicalCatalogOrigin(raw, options.unsafeAllowHTTP)
		if err != nil {
			return nil, err
		}
		origins[origin] = struct{}{}
	}
	if len(options.Sources) == 0 {
		return nil, errors.New("compiler catalog sources are required")
	}
	automaticSources := make(map[Language]bool, len(options.Sources))
	for language, raw := range options.Sources {
		if language != LanguageSolidity {
			return nil, fmt.Errorf("compiler catalog language %q is unsupported", language)
		}
		automaticSources[language] = strings.TrimSpace(raw) == "" ||
			strings.TrimSpace(raw) == automaticCatalogSource
		raw, err := resolveCatalogSource(language, raw, options.Platform)
		if err != nil {
			return nil, err
		}
		options.Sources[language] = raw
		parsed, err := url.Parse(raw)
		if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("compiler catalog source URL is invalid")
		}
		origin, err := canonicalCatalogOrigin(parsed.Scheme+"://"+parsed.Host, options.unsafeAllowHTTP)
		if err != nil {
			return nil, err
		}
		if _, allowed := origins[origin]; !allowed {
			return nil, errors.New("compiler catalog source origin is not allowlisted")
		}
	}
	return &CompilerCatalog{
		options: options, origins: origins, automaticSources: automaticSources,
	}, nil
}

// SetPlatform accepts only the architecture-neutral solc-js artifact identity.
// It remains available for callers that construct a catalog before deciding
// whether the source is automatic or an approved mirror.
func (catalog *CompilerCatalog) SetPlatform(platform string) error {
	if catalog == nil || platform != CompilerPlatformEmscriptenWASM32 {
		return errors.New("compiler catalog platform is unsupported")
	}
	catalog.sourceMu.Lock()
	defer catalog.sourceMu.Unlock()
	for language, automatic := range catalog.automaticSources {
		if !automatic {
			continue
		}
		source, err := resolveCatalogSource(language, automaticCatalogSource, platform)
		if err != nil {
			return err
		}
		parsed, err := url.Parse(source)
		if err != nil {
			return errors.New("compiler catalog source URL is invalid")
		}
		origin, err := canonicalCatalogOrigin(parsed.Scheme+"://"+parsed.Host, false)
		if err != nil {
			return err
		}
		if _, allowed := catalog.origins[origin]; !allowed {
			return errors.New("automatic compiler catalog origin is not allowlisted")
		}
		catalog.options.Sources[language] = source
	}
	catalog.options.Platform = platform
	return nil
}

func canonicalCatalogOrigin(raw string, allowHTTP bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("compiler catalog origin is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && (!allowHTTP || scheme != "http") {
		return "", errors.New("compiler catalog origin must use HTTPS")
	}
	host := strings.ToLower(parsed.Host)
	if parsed.Hostname() == "" {
		return "", errors.New("compiler catalog origin is invalid")
	}
	return scheme + "://" + host, nil
}

func (catalog *CompilerCatalog) Refresh(ctx context.Context, language Language) (int64, error) {
	if catalog == nil || catalog.db == nil {
		return 0, errors.New("refresh using nil compiler catalog")
	}
	catalog.sourceMu.RLock()
	source, ok := catalog.options.Sources[language]
	catalog.sourceMu.RUnlock()
	if !ok {
		return 0, ErrCompilerVersionUnavailable
	}
	sources := []string{source}
	byVersion := make(map[string]CatalogEntry)
	hasher := sha256.New()
	for _, candidateSource := range sources {
		raw, err := catalog.fetch(ctx, candidateSource)
		if err != nil {
			return 0, err
		}
		entries, err := catalog.parse(language, candidateSource, raw)
		if err != nil {
			return 0, err
		}
		_, _ = hasher.Write([]byte(candidateSource))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(raw)
		for _, entry := range entries {
			if _, exists := byVersion[entry.Version]; !exists {
				byVersion[entry.Version] = entry
			}
		}
	}
	if len(byVersion) == 0 || len(byVersion) > catalog.options.MaxEntries {
		return 0, errors.New("merged compiler catalog entry count is outside configured bounds")
	}
	entries := make([]CatalogEntry, 0, len(byVersion))
	for _, entry := range byVersion {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Version < entries[j].Version })
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return catalog.persist(ctx, language, strings.Join(sources, ","), digest, entries)
}

func (catalog *CompilerCatalog) fetch(ctx context.Context, source string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, errors.New("create compiler catalog request")
	}
	client := restrictedOutboundClient(
		catalog.options.unsafeHTTPClient,
		catalog.options.Timeout,
		catalog.options.resolver,
		catalog.options.UnsafeAllowPrivateNetworks,
	)
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("download compiler catalog")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("compiler catalog server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > catalog.options.MaxCatalogBytes {
		return nil, errors.New("compiler catalog exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, catalog.options.MaxCatalogBytes+1))
	if err != nil {
		return nil, errors.New("read compiler catalog")
	}
	if int64(len(raw)) > catalog.options.MaxCatalogBytes {
		return nil, errors.New("compiler catalog exceeds size limit")
	}
	return raw, nil
}

type compilerCatalogDocument struct {
	Builds []struct {
		Path        string `json:"path"`
		Version     string `json:"version"`
		LongVersion string `json:"longVersion"`
		SHA256      string `json:"sha256"`
	} `json:"builds"`
}

func (catalog *CompilerCatalog) parse(language Language, source string, raw []byte) ([]CatalogEntry, error) {
	if err := validateUniqueJSON(raw); err != nil {
		return nil, fmt.Errorf("invalid compiler catalog JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var document compilerCatalogDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("invalid compiler catalog shape")
	}
	if len(document.Builds) == 0 || len(document.Builds) > catalog.options.MaxEntries {
		return nil, errors.New("compiler catalog entry count is outside configured bounds")
	}
	base, _ := url.Parse(source)
	platform, err := catalogArtifactPlatform(language, source)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(document.Builds))
	entries := make([]CatalogEntry, 0, len(document.Builds))
	for _, build := range document.Builds {
		version := normalizeCompilerVersion(build.LongVersion)
		if version == "" {
			version = normalizeCompilerVersion(build.Version)
		}
		if !versionPattern.MatchString(version) {
			return nil, errors.New("compiler catalog contains an invalid version")
		}
		if _, duplicate := seen[version]; duplicate {
			return nil, errors.New("compiler catalog contains a duplicate version")
		}
		seen[version] = struct{}{}
		digest, err := decodeCatalogDigest(build.SHA256)
		if err != nil {
			return nil, err
		}
		reference, err := url.Parse(build.Path)
		if err != nil || build.Path == "" {
			return nil, errors.New("compiler catalog contains an invalid artifact path")
		}
		artifact := base.ResolveReference(reference)
		if artifact.User != nil || artifact.RawQuery != "" || artifact.Fragment != "" {
			return nil, errors.New("compiler catalog contains an invalid artifact URL")
		}
		origin, err := canonicalCatalogOrigin(artifact.Scheme+"://"+artifact.Host, catalog.options.unsafeAllowHTTP)
		if err != nil {
			return nil, err
		}
		if _, allowed := catalog.origins[origin]; !allowed {
			return nil, errors.New("compiler artifact origin is not allowlisted")
		}
		entries = append(entries, CatalogEntry{
			Language: language, Version: version, Platform: platform, ArtifactURL: artifact.String(),
			ArtifactSHA256: digest, MaxBytes: catalog.options.MaxArtifactBytes,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Version < entries[j].Version })
	return entries, nil
}

func normalizeCompilerVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

func decodeCatalogDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" || value != strings.ToLower(value) {
		return digest, errors.New("compiler catalog SHA-256 is invalid")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return digest, errors.New("compiler catalog SHA-256 is invalid")
	}
	copy(digest[:], decoded)
	if digest == [sha256.Size]byte{} {
		return digest, errors.New("compiler catalog SHA-256 must be non-zero")
	}
	return digest, nil
}

func (catalog *CompilerCatalog) persist(
	ctx context.Context,
	language Language,
	source string,
	digest [sha256.Size]byte,
	entries []CatalogEntry,
) (int64, error) {
	tx, err := catalog.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin compiler catalog refresh: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var generationID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO compiler_catalog_generations
			(language, source_url, catalog_digest, entry_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (language, catalog_digest) DO UPDATE
		SET source_url = compiler_catalog_generations.source_url
		RETURNING id
	`, language, source, digest[:], len(entries)).Scan(&generationID)
	if err != nil {
		return 0, fmt.Errorf("persist compiler catalog generation: %w", err)
	}
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO compiler_catalog_entries
				(generation_id, language, version, platform, artifact_url, artifact_sha256, max_bytes)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (generation_id, version) DO NOTHING
		`, generationID, language, entry.Version, entry.Platform, entry.ArtifactURL,
			entry.ArtifactSHA256[:], entry.MaxBytes); err != nil {
			return 0, fmt.Errorf("persist compiler catalog entry: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compiler_catalog_heads (language, generation_id)
		VALUES ($1, $2)
		ON CONFLICT (language) DO UPDATE
		SET generation_id = EXCLUDED.generation_id, updated_at = now()
	`, language, generationID); err != nil {
		return 0, fmt.Errorf("activate compiler catalog generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit compiler catalog refresh: %w", err)
	}
	return generationID, nil
}

func (catalog *CompilerCatalog) Lookup(ctx context.Context, language Language, version string) (CatalogEntry, error) {
	if language == LanguageYul {
		language = LanguageSolidity
	}
	version = normalizeCompilerVersion(version)
	var entry CatalogEntry
	var digest []byte
	err := catalog.db.QueryRowContext(ctx, `
		SELECT entry.generation_id, entry.language, entry.version,
		       entry.platform, entry.artifact_url, entry.artifact_sha256,
		       entry.max_bytes, head.updated_at
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1 AND entry.version = $2
	`, language, version).Scan(
		&entry.GenerationID, &entry.Language, &entry.Version, &entry.Platform,
		&entry.ArtifactURL, &digest, &entry.MaxBytes, &entry.FetchedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		headErr := catalog.db.QueryRowContext(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM compiler_catalog_heads WHERE language = $1)`,
			language,
		).Scan(&exists)
		if headErr != nil {
			return CatalogEntry{}, fmt.Errorf("check compiler catalog availability: %w", headErr)
		}
		if !exists {
			return CatalogEntry{}, ErrCompilerCatalogUnavailable
		}
		return CatalogEntry{}, ErrCompilerVersionUnavailable
	}
	if err != nil {
		return CatalogEntry{}, fmt.Errorf("lookup compiler catalog entry: %w", err)
	}
	if len(digest) != sha256.Size {
		return CatalogEntry{}, errors.New("stored compiler catalog digest is invalid")
	}
	copy(entry.ArtifactSHA256[:], digest)
	if !validCompilerPlatform(entry.Platform) {
		return CatalogEntry{}, errors.New("stored compiler catalog platform is invalid")
	}
	if time.Since(entry.FetchedAt) > catalog.options.Freshness {
		return CatalogEntry{}, ErrCompilerCatalogStale
	}
	return entry, nil
}

func (catalog *CompilerCatalog) Versions(ctx context.Context, language Language) ([]string, error) {
	if language == LanguageYul {
		language = LanguageSolidity
	}
	rows, err := catalog.db.QueryContext(ctx, `
		SELECT entry.version, head.updated_at
		FROM compiler_catalog_heads AS head
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = head.generation_id AND generation.language = head.language
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = head.generation_id AND entry.language = head.language
		WHERE head.language = $1
		ORDER BY entry.version
	`, language)
	if err != nil {
		return nil, fmt.Errorf("list compiler catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var versions []string
	var fetchedAt time.Time
	for rows.Next() {
		var version string
		if err := rows.Scan(&version, &fetchedAt); err != nil {
			return nil, fmt.Errorf("scan compiler catalog: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read compiler catalog: %w", err)
	}
	if len(versions) == 0 {
		return nil, ErrCompilerCatalogUnavailable
	}
	if time.Since(fetchedAt) > catalog.options.Freshness {
		return nil, ErrCompilerCatalogStale
	}
	return versions, nil
}
