package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDiscoverIntegrationPackages(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/alpha/database_integration_test.go",
		"//go:build integration\n\npackage alpha\n")
	writeTestFile(t, root, "internal/beta/stage_test.go",
		"//go:build integration\n\npackage beta\n")
	writeTestFile(t, root, "internal/beta/unit_test.go", "package beta\n")
	writeTestFile(t, root, "web/node_modules/ignored/integration_test.go",
		"//go:build integration\n\npackage ignored\n")
	writeTestFile(t, root, ".local/baseline/internal/verify/integration_test.go",
		"//go:build integration\n\npackage verify\n")

	got, err := discoverIntegrationPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./internal/alpha", "./internal/beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %#v, want %#v", got, want)
	}
}

func TestIntegrationTestArguments(t *testing.T) {
	t.Parallel()
	if integrationPackageTimeout != 15*time.Minute {
		t.Fatalf("integration package timeout = %s", integrationPackageTimeout)
	}
	for _, test := range []struct {
		name  string
		opts  options
		extra []string
	}{
		{name: "default"},
		{name: "focused race", opts: options{race: true, run: "^TestFocused$"}, extra: []string{"-race", "-run", "^TestFocused$"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			arguments, err := integrationTestArguments(test.opts, []string{"./internal/integration"})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"test", "-count=1", "-timeout=15m0s", "-tags=integration"}
			want = append(want, test.extra...)
			want = append(want, "./internal/integration")
			if !slices.Equal(arguments, want) {
				t.Fatalf("integration arguments = %q, want %q", arguments, want)
			}
		})
	}
}

func writeTestFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
