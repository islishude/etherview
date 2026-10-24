package sourcecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedSQLRejectsProductionLiterals(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/repository.go", "package example\nconst query = `SELECT id FROM jobs`\n")
	diagnostics, err := checkSQLSources(root)
	if err != nil || len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v err=%v", diagnostics, err)
	}
}

func TestEmbeddedSQLScansBuildTaggedAndUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/tagged file.go", "//go:build sourcecheckfixture\n\npackage example\nconst query = `SELECT id FROM jobs`\n")
	diagnostics, err := checkSQLSources(root)
	if err != nil || len(diagnostics) != 1 || !strings.HasPrefix(diagnostics[0], "internal/example/tagged file.go:4:") {
		t.Fatalf("diagnostics=%v err=%v", diagnostics, err)
	}
}

func TestEmbeddedSQLRejectsParseAndWalkFailures(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "broken.go", "not Go source")
	if _, err := checkSQLSources(root); err == nil {
		t.Fatal("expected a parse error")
	}
	if _, err := checkSQLSources(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected a filesystem error")
	}
}

func TestEmbeddedSQLAllowsGeneratedSourcesTestsAndRawExecutors(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/db/gen/query.sql.go", "package dbgen\nconst query = `SELECT id FROM jobs`\n")
	writeFixture(t, root, "internal/api/gen/query.go", "not parsed")
	writeFixture(t, root, "nested/node_modules/query.go", "not parsed")
	writeFixture(t, root, ".git/query.go", "not parsed")
	writeFixture(t, root, "internal/example/repository_test.go", "package example\nconst query = `SELECT id FROM jobs`\n")
	writeFixture(t, root, "internal/store/migrate.go", "package store\nconst query = `CREATE TABLE jobs (id bigint)`\n")
	writeFixture(t, root, "internal/store/partition.go", "package store\nconst query = `SELECT current_schema()`\n")
	diagnostics, err := checkSQLSources(root)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v err=%v", diagnostics, err)
	}
}

func TestLooksLikeSQLRejectsFragmentsWithoutMatchingErrorText(t *testing.T) {
	for _, value := range []string{
		"SELECT pg_advisory_lock(1)", "INSERT INTO jobs (id) VALUES (1)",
		"UPDATE jobs SET state = 'done'", "DELETE FROM jobs", "ORDER BY id",
		"SAVEPOINT work",
	} {
		if !looksLikeSQL(value) {
			t.Fatalf("expected SQL: %q", value)
		}
	}
	for _, value := range []string{"select verification job", "update failed", "delete request"} {
		if looksLikeSQL(value) {
			t.Fatalf("unexpected SQL classification: %q", value)
		}
	}
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSQLSourceLocations(t *testing.T) {
	for _, test := range []struct {
		path    string
		allowed bool
	}{
		{"internal/db/queries/query with spaces.sql", true},
		{"internal/db/queries/nested/query.sql", true},
		{"internal/store/migrations/schema with spaces.sql", true},
		{".git/ignored.sql", true},
		{"nested/.git/ignored.sql", true},
		{"node_modules/ignored.sql", true},
		{"nested/node_modules/ignored.sql", true},
		{"internal/db/gen/ignored.sql", true},
		{"internal/api/gen/ignored.sql", true},
		{"scripts/not-sql.txt", true},
		{"scripts/query with spaces.sql", false},
		{"scripts/query\nwith newline.sql", false},
		{"-leading.sql", false},
		{"internal/db/queries-other/query.sql", false},
		{"internal/store/migrations-other/query.sql", false},
		{"nested/internal/db/queries/query.sql", false},
		{"internal/db/generation/query.sql", false},
		{"nested/internal/db/gen/query.sql", false},
		{"nested/node_modules-other/query.sql", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			// These files are outside any Git index; location checks must not rely
			// on tracked files or shell word splitting.
			root := filepath.Join(t.TempDir(), "source tree")
			writeFixture(t, root, test.path, "SELECT 1;\n")
			diagnostics, err := checkSQLSources(root)
			if err != nil {
				t.Fatal(err)
			}
			if test.allowed {
				if len(diagnostics) != 0 {
					t.Fatalf("unexpected diagnostics: %v", diagnostics)
				}
			} else if len(diagnostics) != 1 || diagnostics[0] != test.path+": SQL source must live under internal/db/queries or internal/store/migrations" {
				t.Fatalf("unexpected diagnostics: %v", diagnostics)
			}
		})
	}
}
