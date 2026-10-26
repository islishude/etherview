package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTurnstileConfiguration(t *testing.T) {
	if Default().Turnstile.Enabled {
		t.Fatal("must default off")
	}
	for _, enabled := range []bool{false, true} {
		c := Default()
		c.Features.UserAuth = true
		c.Server.PublicURL = "https://explorer.example"
		c.Turnstile = TurnstileConfig{Enabled: enabled, SiteKey: "public"}
		if err := validateTurnstileConfig(c); err != nil {
			t.Fatal(err)
		}
		c.Database.URL = "postgres://db/db"
		c.UserAuth.SessionPepper = strings.Repeat("p", 32)
		err := c.ValidateForRoles([]string{"api"})
		if c.Turnstile.Enabled && (err == nil || !strings.Contains(err.Error(), "Turnstile requires a secret")) {
			t.Fatalf("missing secret accepted: %v", err)
		}
		c.Turnstile.SecretKey = "private"
		if err := c.ValidateForRoles([]string{"api"}); err != nil {
			t.Fatal(err)
		}
		c.Turnstile.SiteKey = ""
		if c.Turnstile.Enabled && validateTurnstileConfig(c) == nil {
			t.Fatal("missing site key accepted")
		}
	}
}

func TestTurnstileSecretRoleIsolation(t *testing.T) {
	for _, api := range []bool{false, true} {
		c := Default()
		reads := 0
		lookup := func(k string) (string, bool) {
			switch k {
			case "ETHERVIEW_TURNSTILE_ENABLED":
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
		if !c.Turnstile.Enabled || c.Turnstile.SiteKey != "public" {
			t.Fatal("incorrect overrides")
		}
	}
	c := Default()
	if applySecretEnvironment(&c, func(k string) (string, bool) { return "invalid", k == "ETHERVIEW_TURNSTILE_ENABLED" }, nil, false) == nil {
		t.Fatal("invalid boolean accepted")
	}
}

func TestTurnstileSharedConfiguration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c := Default()
		decoder := yaml.NewDecoder(strings.NewReader("turnstile:\n  enabled: true\n  site_key: public\n"))
		decoder.KnownFields(true)
		if err := decoder.Decode(&c); err != nil {
			t.Fatal(err)
		}
		if !c.Turnstile.Enabled || c.Turnstile.SiteKey != "public" {
			t.Fatal("YAML not applied")
		}
		value := "false"
		if enabled {
			value = "true"
		}
		if err := applyEnvironment(&c, func(k string) (string, bool) {
			return value, k == "ETHERVIEW_TURNSTILE_ENABLED"
		}, nil); err != nil {
			t.Fatal(err)
		}
		if c.Turnstile.Enabled != enabled {
			t.Fatal("environment did not override YAML")
		}
	}
	for _, key := range []string{"verification_enabled", "csv_export_enabled"} {
		c := Default()
		decoder := yaml.NewDecoder(strings.NewReader("turnstile:\n  " + key + ": true\n"))
		decoder.KnownFields(true)
		if err := decoder.Decode(&c); err == nil {
			t.Fatalf("removed YAML key %s accepted", key)
		}
		name := "ETHERVIEW_TURNSTILE_" + strings.ToUpper(key)
		if err := applyEnvironment(&c, func(k string) (string, bool) { return "true", k == name }, nil); err == nil || !strings.Contains(err.Error(), "no longer supported") {
			t.Fatalf("removed environment key %s accepted: %v", name, err)
		}
	}
}

func TestTurnstileEnabledRequirements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"auth", func(c *Config) { c.Features.UserAuth = false }},
		{"site key", func(c *Config) { c.Turnstile.SiteKey = "" }},
		{"secret format", func(c *Config) { c.Turnstile.SecretKey = "invalid key" }},
		{"public URL", func(c *Config) { c.Server.PublicURL = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Turnstile = TurnstileConfig{Enabled: true, SiteKey: "public", SecretKey: "private"}
			c.Features.UserAuth = true
			c.Server.PublicURL = "https://explorer.example"
			tc.mutate(&c)
			if validateTurnstileConfig(c) == nil {
				t.Fatal("missing requirement accepted")
			}
			c.Turnstile.Enabled = false
			if err := validateTurnstileConfig(c); err != nil {
				t.Fatal(err)
			}
		})
	}
}
