package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadVyperCatalogEnvironment(t *testing.T) {
	public := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	publicKey := base64.StdEncoding.EncodeToString(public)
	for _, role := range []string{"all", "api"} {
		for _, test := range []struct {
			name      string
			url       string
			key       string
			wantError string
		}{
			{name: "configured", url: "https://compilers.example/vyper/catalog.json", key: publicKey},
			{name: "unconfigured"},
			{name: "missing key", url: "https://compilers.example/vyper/catalog.json", wantError: "base64 Ed25519 public key"},
			{name: "malformed key", url: "https://compilers.example/vyper/catalog.json", key: "invalid", wantError: "base64 Ed25519 public key"},
			{name: "non HTTPS", url: "http://compilers.example/vyper/catalog.json", key: publicKey, wantError: "HTTPS URL"},
			{name: "unlisted origin", url: "https://unlisted.example/catalog.json", key: publicKey, wantError: "origin is not allowlisted"},
		} {
			t.Run(role+"/"+test.name, func(t *testing.T) {
				t.Setenv("ETHERVIEW_DATABASE_URL", "postgres://example/etherview")
				t.Setenv("ETHERVIEW_RPC_URLS", "https://rpc.example")
				t.Setenv("ETHERVIEW_API_KEY_PEPPER", strings.Repeat("p", 32))
				t.Setenv("ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL", test.url)
				t.Setenv("ETHERVIEW_VERIFICATION_VYPER_CATALOG_PUBLIC_KEY", test.key)
				t.Setenv("ETHERVIEW_VERIFICATION_ALLOWED_DOWNLOAD_ORIGINS", "https://binaries.soliditylang.org,https://compilers.example")
				cfg, err := LoadForRoles("", []string{role})
				if test.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantError) {
						t.Fatalf("want %q, got %v", test.wantError, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Verification.VyperCatalogURL != test.url || cfg.Verification.VyperCatalogPublicKey != test.key {
					t.Fatal("Vyper environment configuration was not loaded")
				}
				if err := validateCompilerCatalogConfig(cfg.Verification); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
