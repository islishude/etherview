package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
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

	got, err := discoverIntegrationPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./internal/alpha", "./internal/beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %#v, want %#v", got, want)
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
