package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadAPIKey(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     string
		envSet  bool
		file    string
		want    string
		wantErr string
	}{
		{name: "conflicting sources", env: "environment-key", envSet: true, file: "file-key\n", wantErr: "-api-key-file and ETHERVIEW_LOAD_API_KEY are mutually exclusive"},
		{name: "oversized file", file: strings.Repeat("x", 4_097), wantErr: "API key exceeds 4096 bytes"},
		{name: "oversized whitespace file", file: strings.Repeat(" ", 4_097), wantErr: "API key exceeds 4096 bytes"},
		{name: "empty file", file: " \n\t", wantErr: "API key source is empty"},
		{name: "trimmed file", file: "  file-key\n", want: "file-key"},
		{name: "explicitly empty environment", env: " \n", envSet: true, wantErr: "API key source is empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Setenv registers restoration even when this case needs an absent variable.
			t.Setenv("ETHERVIEW_LOAD_API_KEY", test.env)
			if !test.envSet {
				if err := os.Unsetenv("ETHERVIEW_LOAD_API_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			var path string
			if test.file != "" {
				path = filepath.Join(t.TempDir(), "api-key")
				if err := os.WriteFile(path, []byte(test.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			value, err := readAPIKey(path)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("readAPIKey error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value != test.want {
				t.Fatalf("API key = %q, want %q", value, test.want)
			}
		})
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
