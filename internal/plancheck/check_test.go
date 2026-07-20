package plancheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckValidFixture(t *testing.T) {
	t.Parallel()

	report := Check(filepath.Join("testdata", "valid"))
	if !report.OK() {
		t.Fatalf("valid fixture failed:\n%s", diagnosticText(report))
	}
	if report.Plans != 2 {
		t.Fatalf("Plans = %d, want 2", report.Plans)
	}
	if report.WorkItems != 3 {
		t.Fatalf("WorkItems = %d, want 3", report.WorkItems)
	}
	if report.Links != 6 {
		t.Fatalf("Links = %d, want 6", report.Links)
	}
}

func TestCheckCompletedPlanFixture(t *testing.T) {
	t.Parallel()

	root := copyFixture(t, "valid")
	replaceInFile(t, filepath.Join(root, "PLAN.md"),
		"| P00 | [Foundation](docs/plans/P00-foundation.md) | in_progress |",
		"| P00 | [Foundation](docs/plans/P00-foundation.md) | done |",
	)
	planPath := filepath.Join(root, "docs", "plans", "P00-foundation.md")
	replaceInFile(t, planPath, "Status: `in_progress`", "Status: `done`")
	replaceInFile(t, planPath, "| P00-T01 | in_progress |", "| P00-T01 | done |")
	replaceInFile(t, planPath, "| P00-T02 | todo |", "| P00-T02 | done |")
	replaceInFile(t, planPath, "- [ ] Plan drift fails validation.", "- [x] Plan drift fails validation.")
	replaceInFile(t, planPath, "None yet.", "- P00-T01: `go test ./...` passed.\n- P00-T02: `make check` passed.")

	report := Check(root)
	if !report.OK() {
		t.Fatalf("completed fixture failed:\n%s", diagnosticText(report))
	}
}

func TestCheckInvalidFixture(t *testing.T) {
	t.Parallel()

	report := Check(filepath.Join("testdata", "invalid"))
	if report.OK() {
		t.Fatal("invalid fixture unexpectedly passed")
	}
	for _, want := range []string{
		`local link target "../architecture/missing.md" does not exist`,
		"dependency P99 does not resolve",
		"does not match child status",
		"duplicate work-item ID P00-T01",
		`malformed work-item ID "P00-ONE"`,
		`has malformed dependency ID "P0"`,
		"must use parent prefix P00-T",
		`unsupported status "started"`,
		"done work item P00-T01 must have non-placeholder evidence",
	} {
		if !strings.Contains(diagnosticText(report), want) {
			t.Errorf("diagnostics do not contain %q:\n%s", want, diagnosticText(report))
		}
	}
}

func TestCheckRejectsIncompleteAndCyclicDependencies(t *testing.T) {
	t.Parallel()

	root := copyFixture(t, "valid")
	planPath := filepath.Join(root, "docs", "plans", "P00-foundation.md")
	replaceInFile(t, planPath,
		"| P00-T01 | in_progress | — |",
		"| P00-T01 | in_progress | P00-T02 |",
	)

	report := Check(root)
	text := diagnosticText(report)
	for _, want := range []string{
		"work item P00-T01 is in_progress but dependency P00-T02 is todo",
		"work-item dependency cycle: P00-T01 -> P00-T02 -> P00-T01",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostics do not contain %q:\n%s", want, text)
		}
	}
}

func TestCheckRejectsEscapingLink(t *testing.T) {
	t.Parallel()

	root := copyFixture(t, "valid")
	planPath := filepath.Join(root, "docs", "plans", "P00-foundation.md")
	replaceInFile(t, planPath,
		"[Architecture](../architecture/overview.md)",
		"[Architecture](../../../../outside.md)",
	)

	report := Check(root)
	if !strings.Contains(diagnosticText(report), "escapes the repository") {
		t.Fatalf("missing escaping-link diagnostic:\n%s", diagnosticText(report))
	}
}

func TestDiagnosticString(t *testing.T) {
	t.Parallel()

	diagnostic := Diagnostic{Path: "PLAN.md", Line: 12, Message: "broken"}
	if got, want := diagnostic.String(), "PLAN.md:12: broken"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()

	destination := t.TempDir()
	source := os.DirFS(filepath.Join("testdata", name))
	if err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(destination, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return destination
}

func replaceInFile(t *testing.T, path, old, replacement string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	updated := strings.Replace(string(data), old, replacement, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func diagnosticText(report Report) string {
	lines := make([]string, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		lines = append(lines, diagnostic.String())
	}
	return strings.Join(lines, "\n")
}
