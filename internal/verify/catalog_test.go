package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestCompilerCatalogParsesArchitectureNeutralSolcJSList(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("compiler"))
	catalog := &CompilerCatalog{
		options: CompilerCatalogOptions{
			MaxEntries: 4096, MaxArtifactBytes: 200 << 20,
		},
		origins: map[string]struct{}{
			"https://binaries.soliditylang.org": {},
		},
	}
	raw := []byte(`{
		"builds":[{
			"path":"soljson-v0.8.30+commit.73712a01.js",
			"version":"0.8.30",
			"longVersion":"v0.8.30+commit.73712a01",
			"sha256":"` + hex.EncodeToString(digest[:]) + `",
			"ignored":"safe"
		}],
		"releases":{}
	}`)
	entries, err := catalog.parse(
		LanguageSolidity,
		"https://binaries.soliditylang.org/emscripten-wasm32/list.json",
		raw,
	)
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Version != "0.8.30+commit.73712a01" ||
		entry.ArtifactSHA256 != digest ||
		entry.Platform != CompilerPlatformEmscriptenWASM32 {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestCompilerCatalogRejectsUnsafeOrAmbiguousLists(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("11", sha256.Size)
	base := &CompilerCatalog{
		options: CompilerCatalogOptions{MaxEntries: 1, MaxArtifactBytes: 200 << 20},
		origins: map[string]struct{}{"https://allowed.example": {}},
	}
	tests := map[string]string{
		"duplicate key": `{"builds":[],"builds":[]}`,
		"empty":         `{"builds":[]}`,
		"unknown origin": `{"builds":[{
			"path":"https://evil.example/solc",
			"longVersion":"0.8.30+commit.73712a01",
			"sha256":"` + digest + `"
		}]}`,
		"zero digest": `{"builds":[{
			"path":"solc",
			"longVersion":"0.8.30+commit.73712a01",
			"sha256":"` + strings.Repeat("00", sha256.Size) + `"
		}]}`,
		"duplicate version": `{"builds":[{
			"path":"a","longVersion":"0.8.30+commit.73712a01","sha256":"` + digest + `"
		},{
			"path":"b","longVersion":"0.8.30+commit.73712a01","sha256":"` + digest + `"
		}]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := base.parse(
				LanguageSolidity,
				"https://allowed.example/emscripten-wasm32/list.json",
				[]byte(raw),
			); err == nil {
				t.Fatal("expected catalog rejection")
			}
		})
	}
}

func TestCompilerCatalogOptionsAreClosed(t *testing.T) {
	t.Parallel()
	options := CompilerCatalogOptions{
		Sources: map[Language]string{
			LanguageSolidity: "https://binaries.soliditylang.org/emscripten-wasm32/list.json",
		},
		Platform:       CompilerPlatformEmscriptenWASM32,
		AllowedOrigins: []string{"https://binaries.soliditylang.org"},
		Freshness:      24 * time.Hour,
	}
	if _, err := NewCompilerCatalog(nil, options); err == nil {
		t.Fatal("expected nil database rejection")
	}
	if _, err := canonicalCatalogOrigin("https://user@example.com", false); err == nil {
		t.Fatal("expected credential-bearing origin rejection")
	}
	if _, err := canonicalCatalogOrigin("http://example.com", false); err == nil {
		t.Fatal("expected plaintext origin rejection")
	}
}

func TestAutomaticCompilerCatalogNeverSelectsCPUPlatform(t *testing.T) {
	t.Parallel()
	want := "https://binaries.soliditylang.org/emscripten-wasm32/list.json"
	got, err := resolveCatalogSource(
		LanguageSolidity, automaticCatalogSource, CompilerPlatformEmscriptenWASM32,
	)
	if err != nil || got != want {
		t.Fatalf("architecture-neutral catalog = %q, error=%v", got, err)
	}
	for _, platform := range []string{
		"linux-amd64",
		"linux-arm64",
		"macosx-amd64",
		"windows-amd64",
		"linux-riscv64",
	} {
		if _, err := resolveCatalogSource(LanguageSolidity, automaticCatalogSource, platform); err == nil {
			t.Fatalf("CPU platform %q selected a compiler catalog", platform)
		}
	}
}

func TestCatalogArtifactIdentityIsAlwaysEmscriptenWASM32(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"https://binaries.soliditylang.org/emscripten-wasm32/list.json",
		"https://compiler.example/list.json",
	} {
		got, err := catalogArtifactPlatform(LanguageSolidity, source)
		if err != nil || got != CompilerPlatformEmscriptenWASM32 {
			t.Fatalf("source %q platform=%q error=%v", source, got, err)
		}
	}
}
