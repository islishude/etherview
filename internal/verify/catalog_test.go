package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestCompilerCatalogParsesSolidityAndVyperLists(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("compiler"))
	checksum := hex.EncodeToString(digest[:])
	for _, test := range []struct {
		name     string
		language Language
		source   string
		path     string
		origin   string
	}{
		{
			name: "solidity", language: LanguageSolidity,
			source: "https://binaries.soliditylang.org/linux-amd64/list.json",
			path:   "solc-linux-amd64-v0.8.30+commit.73712a01",
			origin: "https://binaries.soliditylang.org",
		},
		{
			name: "vyper", language: LanguageVyper,
			source: "https://raw.githubusercontent.com/blockscout/solc-bin/main/vyper.list.json",
			path:   "https://github.com/vyperlang/vyper/releases/download/v0.4.3/vyper.linux",
			origin: "https://github.com",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := &CompilerCatalog{
				options: CompilerCatalogOptions{
					MaxEntries: 4096, MaxArtifactBytes: 200 << 20,
				},
				origins: map[string]struct{}{
					test.origin:                         {},
					"https://raw.githubusercontent.com": {},
					"https://binaries.soliditylang.org": {},
				},
			}
			raw := []byte(`{
				"builds":[{
					"path":` + quotedJSON(test.path) + `,
					"version":"0.8.30",
					"longVersion":"v0.8.30+commit.73712a01",
					"sha256":"` + checksum + `",
					"ignored":"safe"
				}],
				"releases":{}
			}`)
			entries, err := catalog.parse(test.language, test.source, raw)
			if err != nil {
				t.Fatalf("parse catalog: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}
			entry := entries[0]
			if entry.Version != "0.8.30+commit.73712a01" {
				t.Fatalf("version = %q", entry.Version)
			}
			if entry.ArtifactSHA256 != digest {
				t.Fatal("artifact digest changed")
			}
			wantPlatform := CompilerPlatformLinuxAMD64
			if entry.Platform != wantPlatform {
				t.Fatalf("platform = %q, want %q", entry.Platform, wantPlatform)
			}
			if !strings.HasPrefix(entry.ArtifactURL, test.origin+"/") {
				t.Fatalf("artifact URL = %q", entry.ArtifactURL)
			}
		})
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
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := base.parse(LanguageSolidity, "https://allowed.example/list.json", []byte(raw)); err == nil {
				t.Fatal("expected catalog rejection")
			}
		})
	}
}

func TestCompilerCatalogOptionsAreClosed(t *testing.T) {
	t.Parallel()
	options := CompilerCatalogOptions{
		Sources: map[Language]string{
			LanguageSolidity: "https://binaries.soliditylang.org/linux-amd64/list.json",
		},
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

func TestAutomaticCompilerCatalogMatchesRunnerArchitecture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		platform string
		want     string
	}{
		{
			platform: CompilerPlatformBin,
			want:     "https://binaries.soliditylang.org/bin/list.json",
		},
		{
			platform: CompilerPlatformEmscriptenASMJS,
			want:     "https://binaries.soliditylang.org/emscripten-asmjs/list.json",
		},
		{
			platform: CompilerPlatformEmscriptenWASM32,
			want:     "https://binaries.soliditylang.org/emscripten-wasm32/list.json",
		},
		{
			platform: CompilerPlatformLinuxAMD64,
			want:     "https://binaries.soliditylang.org/linux-amd64/list.json",
		},
		{
			platform: CompilerPlatformLinuxARM64,
			want:     "https://binaries.soliditylang.org/linux-arm64/list.json",
		},
		{
			platform: CompilerPlatformMacOSAMD64,
			want:     "https://binaries.soliditylang.org/macosx-amd64/list.json",
		},
		{
			platform: CompilerPlatformWASM,
			want:     "https://binaries.soliditylang.org/wasm/list.json",
		},
		{
			platform: CompilerPlatformWindowsAMD64,
			want:     "https://binaries.soliditylang.org/windows-amd64/list.json",
		},
	}
	for _, test := range tests {
		got, err := resolveCatalogSource(LanguageSolidity, "auto", test.platform)
		if err != nil || got != test.want {
			t.Fatalf("platform %q resolved to %q, error=%v", test.platform, got, err)
		}
	}
	if _, err := resolveCatalogSource(LanguageSolidity, "auto", "linux-riscv64"); err == nil {
		t.Fatal("unsupported runner architecture selected a compiler catalog")
	}
}

func TestCatalogArtifactPlatformIsUnambiguous(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		"https://binaries.soliditylang.org/linux-amd64/list.json": CompilerPlatformLinuxAMD64,
		"https://binaries.soliditylang.org/linux-arm64/list.json": CompilerPlatformLinuxARM64,
	} {
		got, err := catalogArtifactPlatform(LanguageSolidity, source)
		if err != nil || got != want {
			t.Fatalf("source %q platform=%q error=%v", source, got, err)
		}
	}
	if _, err := catalogArtifactPlatform(
		LanguageSolidity,
		"https://compiler.example/list.json",
	); err == nil {
		t.Fatal("ambiguous Solidity mirror platform was accepted")
	}
}

func quotedJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
