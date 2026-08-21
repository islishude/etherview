package sourcecheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRejectsProductionSQLAndUnexpectedSQLFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/repository.go", "package example\nconst query = `SELECT id FROM jobs`\n")
	writeFixture(t, root, "scripts/query.sql", "SELECT 1;\n")
	report := Check(root)
	if report.OK() || len(report.Diagnostics) != 2 {
		t.Fatalf("report=%+v", report)
	}
}

func TestCheckAllowsGeneratedSourcesTestsAndRawExecutors(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/db/gen/query.sql.go", "package dbgen\nconst query = `SELECT id FROM jobs`\n")
	writeFixture(t, root, "internal/example/repository_test.go", "package example\nconst query = `SELECT id FROM jobs`\n")
	writeFixture(t, root, "internal/store/migrate.go", "package store\nconst query = `CREATE TABLE jobs (id bigint)`\n")
	writeFixture(t, root, "internal/store/partition.go", "package store\nconst query = `SELECT current_schema()`\n")
	writeFixture(t, root, "internal/db/queries/jobs.sql", "-- name: Jobs :many\nSELECT id FROM jobs;\n")
	writeFixture(t, root, "internal/store/migrations/0001.sql", "CREATE TABLE jobs (id bigint);\n")
	report := Check(root)
	if !report.OK() {
		t.Fatalf("report=%+v", report)
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
