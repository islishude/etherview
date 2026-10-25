package config

import (
	"strings"
	"testing"
)

func TestTurnstileConfiguration(t *testing.T) {
	if Default().Turnstile.Enabled() {
		t.Fatal("must default off")
	}
	for _, verification := range []bool{false, true} {
		for _, csv := range []bool{false, true} {
			c := Default()
			c.Features.UserAuth = true
			c.Server.PublicURL = "https://explorer.example"
			c.Turnstile = TurnstileConfig{VerificationEnabled: verification, CSVExportEnabled: csv, SiteKey: "public"}
			if err := validateTurnstileConfig(c); err != nil {
				t.Fatal(err)
			}
			c.Database.URL = "postgres://db/db"
			c.UserAuth.SessionPepper = strings.Repeat("p", 32)
			err := c.ValidateForRoles([]string{"api"})
			if c.Turnstile.Enabled() && (err == nil || !strings.Contains(err.Error(), "Turnstile requires a secret")) {
				t.Fatalf("missing secret accepted: %v", err)
			}
			c.Turnstile.SecretKey = "private"
			if err := c.ValidateForRoles([]string{"api"}); err != nil {
				t.Fatal(err)
			}
			c.Turnstile.SiteKey = ""
			if c.Turnstile.Enabled() && validateTurnstileConfig(c) == nil {
				t.Fatal("missing site key accepted")
			}
		}
	}
}

func TestTurnstileSecretRoleIsolation(t *testing.T) {
	for _, api := range []bool{false, true} {
		c := Default()
		reads := 0
		lookup := func(k string) (string, bool) {
			switch k {
			case "ETHERVIEW_TURNSTILE_VERIFICATION_ENABLED":
				return "true", true
			case "ETHERVIEW_TURNSTILE_SITE_KEY":
				return "public", true
			case "ETHERVIEW_TURNSTILE_SECRET_KEY_FILE":
				return "secret-file", true
			}
			return "", false
		}
		read := func(string) ([]byte, error) { reads++; return []byte("private"), nil }
		if err := applySecretEnvironment(&c, lookup, read, api); err != nil {
			t.Fatal(err)
		}
		if api && (reads != 1 || c.Turnstile.SecretKey != "private") || !api && (reads != 0 || c.Turnstile.SecretKey != "") {
			t.Fatal("secret role isolation failed")
		}
		if !c.Turnstile.VerificationEnabled || c.Turnstile.CSVExportEnabled || c.Turnstile.SiteKey != "public" {
			t.Fatal("incorrect overrides")
		}
	}
	for _, key := range []string{"ETHERVIEW_TURNSTILE_VERIFICATION_ENABLED", "ETHERVIEW_TURNSTILE_CSV_EXPORT_ENABLED"} {
		c := Default()
		if applySecretEnvironment(&c, func(k string) (string, bool) { return "invalid", k == key }, nil, false) == nil {
			t.Fatal("invalid boolean accepted")
		}
	}
}
