package facilitator

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearCredentialEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"X402_DATABASE_URL", "X402_DATABASE_URL_FILE", "X402_BEARER_TOKEN", "X402_BEARER_TOKEN_FILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCredentialSources(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fallback := write("yaml", "yaml-value\n")
	file := write("env", "env-value\n")
	blank := write("blank", " \n")
	empty := write("empty", "")
	oversized := write("oversized", strings.Repeat("x", 17))
	missing := filepath.Join(dir, "marker_SECRET")
	for _, tc := range []struct {
		name                 string
		env                  map[string]string
		fallback, want, code string
	}{
		{name: "yaml", fallback: fallback, want: "yaml-value"},
		{name: "direct overrides missing yaml", env: map[string]string{"VALUE": " env-value\n"}, fallback: missing, want: "env-value"},
		{name: "file overrides missing yaml", env: map[string]string{"VALUE_FILE": file}, fallback: missing, want: "env-value"},
		{name: "direct without yaml", env: map[string]string{"VALUE": "env-value"}, want: "env-value"},
		{name: "file without yaml", env: map[string]string{"VALUE_FILE": file}, want: "env-value"},
		{name: "empty direct fallback", env: map[string]string{"VALUE": ""}, fallback: fallback, want: "yaml-value"},
		{name: "blank direct fallback", env: map[string]string{"VALUE": " \n"}, fallback: fallback, want: "yaml-value"},
		{name: "empty file fallback", env: map[string]string{"VALUE_FILE": empty}, fallback: fallback, want: "yaml-value"},
		{name: "blank file fallback", env: map[string]string{"VALUE_FILE": blank}, fallback: fallback, want: "yaml-value"},
		{name: "conflict", env: map[string]string{"VALUE": "marker_SECRET", "VALUE_FILE": missing}, fallback: fallback, code: "conflicting_sources"},
		{name: "empty conflict", env: map[string]string{"VALUE": "", "VALUE_FILE": ""}, fallback: fallback, code: "conflicting_sources"},
		{name: "missing env file", env: map[string]string{"VALUE_FILE": missing}, fallback: fallback, code: "file_unavailable"},
		{name: "empty env path", env: map[string]string{"VALUE_FILE": ""}, fallback: fallback, code: "file_unavailable"},
		{name: "oversized direct", env: map[string]string{"VALUE": strings.Repeat("x", 17)}, fallback: fallback, code: "invalid_secret"},
		{name: "oversized env file", env: map[string]string{"VALUE_FILE": oversized}, fallback: fallback, code: "invalid_file"},
		{name: "missing source", code: "missing_secret"},
		{name: "blank without fallback", env: map[string]string{"VALUE": " \n"}, code: "missing_secret"},
		{name: "missing yaml", fallback: missing, code: "file_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := resolveCredential("VALUE", tc.fallback, 16, "auth.bearer_token_file", func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok })
			defer clear(value)
			if tc.code != "" {
				ce, ok := errors.AsType[*ConfigError](err)
				if !ok || ce.Code != tc.code || ce.Field != "auth.bearer_token_file" || strings.Contains(err.Error(), "marker_SECRET") {
					t.Fatalf("unexpected diagnostic: %v", err)
				}
				return
			}
			if err != nil || string(bytes.TrimSpace(value)) != tc.want {
				t.Fatalf("unexpected source or error: %v", err)
			}
		})
	}
}

func TestEnvironmentCredentialLimitsAndCheckConfig(t *testing.T) {
	for _, name := range []string{"X402_DATABASE_URL", "X402_BEARER_TOKEN"} {
		t.Run(name, func(t *testing.T) {
			c := configFixture(t)
			limit := 8192
			if name == "X402_BEARER_TOKEN" {
				limit = 4096
			}
			t.Setenv(name, strings.Repeat("x", limit))
			if err := CheckConfig(c); err != nil {
				t.Fatal(err)
			}
			t.Setenv(name, strings.Repeat("x", limit+1))
			if err := CheckConfig(c); err == nil {
				t.Fatal("accepted oversized secret")
			}
		})
	}
	c := configFixture(t)
	c.Database.URLFile = ""
	c.Auth.BearerTokenFile = ""
	// Declaration-only validation does not require an injection source yet.
	if _, err := LoadConfig(writeConfig(t, c)); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfig(c); err == nil {
		t.Fatal("accepted absent runtime credentials")
	}
	t.Setenv("X402_DATABASE_URL", "postgres://localhost:1/offline")
	t.Setenv("X402_BEARER_TOKEN", strings.Repeat("t", 31))
	if err := CheckConfig(c); err == nil {
		t.Fatal("accepted short bearer")
	}
	t.Setenv("X402_BEARER_TOKEN", strings.Repeat("t", 32))
	if err := CheckConfig(c); err != nil {
		t.Fatal(err)
	}
}
