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

	got, err := discoverIntegrationPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./internal/alpha", "./internal/beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %#v, want %#v", got, want)
	}
}

func TestIntegrationTestArgumentsSetExplicitPackageTimeout(t *testing.T) {
	t.Parallel()
	arguments, err := integrationTestArguments(options{}, []string{"./internal/integration"})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"-count=1",
		"-timeout=" + integrationPackageTimeout.String(),
		"-tags=integration",
		"./internal/integration",
	} {
		if !slices.Contains(arguments, required) {
			t.Fatalf("integration arguments %q omit %q", arguments, required)
		}
	}
	if integrationPackageTimeout != 15*time.Minute {
		t.Fatalf("integration package timeout = %s", integrationPackageTimeout)
	}
}

func TestIntegrationTestArgumentsPreserveFocusedAndRaceModes(t *testing.T) {
	t.Parallel()
	arguments, err := integrationTestArguments(options{race: true, run: "^TestFocused$"}, []string{"./internal/integration"})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"-race", "-run", "^TestFocused$"} {
		if !slices.Contains(arguments, required) {
			t.Fatalf("integration arguments %q omit %q", arguments, required)
		}
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
