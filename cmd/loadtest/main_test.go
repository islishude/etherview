package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadAPIKeyRejectsConflictingSourcesAndOversizedFiles(t *testing.T) {
	t.Setenv("ETHERVIEW_LOAD_API_KEY", "environment-key")
	path := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(path, []byte("file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAPIKey(path); err == nil {
		t.Fatal("conflicting API key sources were accepted")
	}

	if err := os.Unsetenv("ETHERVIEW_LOAD_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4_097)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAPIKey(path); err == nil {
		t.Fatal("oversized API key file was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 4_097)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAPIKey(path); err == nil {
		t.Fatal("raw oversized whitespace API key file was accepted")
	}
	if err := os.WriteFile(path, []byte(" \n\t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAPIKey(path); err == nil {
		t.Fatal("empty API key file was accepted")
	}
}

func TestReadAPIKeyTrimsAFileValue(t *testing.T) {
	t.Setenv("ETHERVIEW_LOAD_API_KEY", "")
	if err := os.Unsetenv("ETHERVIEW_LOAD_API_KEY"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(path, []byte("  file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readAPIKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if value != "file-key" {
		t.Fatalf("API key = %q", value)
	}
}

func TestReadAPIKeyRejectsExplicitlyEmptyEnvironmentValue(t *testing.T) {
	t.Setenv("ETHERVIEW_LOAD_API_KEY", " \n")
	if _, err := readAPIKey(""); err == nil {
		t.Fatal("empty environment API key was accepted")
	}
}

func TestLoadEnvironmentDefaultsParseTypedValuesAndJSONPaths(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"ETHERVIEW_LOAD_BASE_URL":             "https://explorer.example.test",
		"ETHERVIEW_LOAD_PATHS":                `["/api/v1/blocks?limit=20&sort=desc","/api/v1/status"]`,
		"ETHERVIEW_LOAD_RATE":                 "500",
		"ETHERVIEW_LOAD_DURATION":             "30m",
		"ETHERVIEW_LOAD_CONCURRENCY":          "256",
		"ETHERVIEW_LOAD_REQUEST_TIMEOUT":      "2s",
		"ETHERVIEW_LOAD_MAX_P95":              "400ms",
		"ETHERVIEW_LOAD_MAX_ERROR_RATE":       "0.0005",
		"ETHERVIEW_LOAD_MIN_THROUGHPUT_RATIO": "0.98",
		"ETHERVIEW_LOAD_MAX_LAG":              "1",
		"ETHERVIEW_LOAD_PROFILE":              "reference",
	}
	defaults, err := loadEnvironmentDefaults(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.baseURL != values["ETHERVIEW_LOAD_BASE_URL"] ||
		defaults.rate != 500 ||
		defaults.duration != 30*time.Minute ||
		defaults.concurrency != 256 ||
		defaults.requestTimeout != 2*time.Second ||
		defaults.maximumP95 != 400*time.Millisecond ||
		defaults.maximumErrorRate != 0.0005 ||
		defaults.minimumThroughputRatio != 0.98 ||
		defaults.maximumLag != 1 ||
		defaults.profile != "reference" {
		t.Fatalf("environment defaults = %+v", defaults)
	}
	wantPaths := pathsFlag{
		"/api/v1/blocks?limit=20&sort=desc",
		"/api/v1/status",
	}
	if !reflect.DeepEqual(defaults.paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", defaults.paths, wantPaths)
	}
}

func TestLoadEnvironmentDefaultsRejectMalformedValuesWithoutEchoingThem(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		values map[string]string
		secret string
	}{
		{
			name: "integer",
			values: map[string]string{
				"ETHERVIEW_LOAD_RATE": "not-an-integer-secret",
			},
			secret: "not-an-integer-secret",
		},
		{
			name: "paths",
			values: map[string]string{
				"ETHERVIEW_LOAD_PATHS": `["/api/v1/status",secret]`,
			},
			secret: `["/api/v1/status",secret]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadEnvironmentDefaults(mapLookup(test.values))
			if err == nil {
				t.Fatal("malformed environment value was accepted")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("error echoed the malformed value: %v", err)
			}
		})
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
