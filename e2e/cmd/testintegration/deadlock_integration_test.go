//go:build integration

package main

import (
	"context"
	"encoding/json"
	"github.com/islishude/etherview/internal/testdbguard"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The child succeeds after swallowing a real server-detected deadlock. The
// managed runner must still fail, independently of the go test exit status.
func TestManagedRunnerRejectsSwallowedDeadlock(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	shim := filepath.Join(directory, "go-fixture")
	script := "#!/bin/sh\ncase \"$1\" in\nrun) exit 0;;\ntest) exec \"$DEADLOCK_TEST_EXECUTABLE\" -test.run='^TestManagedRunnerDeadlockChild$';;\n*) exit 2;;\nesac\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO", shim)
	t.Setenv("DEADLOCK_TEST_EXECUTABLE", executable)
	t.Setenv("DEADLOCK_TEST_CHILD", "1")
	t.Setenv("INTEGRATION_DATABASE_URL", "")
	t.Setenv("TMPDIR", directory)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	err = run(ctx, options{root: root, packages: "./fixture", shard: 1, shards: 1})
	if err == nil || !strings.Contains(err.Error(), "new database deadlocks") || strings.Contains(err.Error(), "integration suite:") {
		t.Fatalf("runner did not independently reject deadlock: %v", err)
	}
	reports, globErr := filepath.Glob(filepath.Join(directory, "etherview-integration-evidence-*", "deadlocks.json"))
	if globErr != nil || len(reports) != 1 {
		t.Fatalf("missing runner evidence: %v %v", reports, globErr)
	}
	raw, readErr := os.ReadFile(reports[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	var report testdbguard.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Final.Deadlocks <= report.Baseline.Deadlocks {
		t.Fatalf("incorrect failure report: %+v", report)
	}
	logs, readErr := os.ReadFile(filepath.Join(filepath.Dir(reports[0]), "postgres.log"))
	if readErr != nil || !strings.Contains(string(logs), "deadlock detected") || strings.Contains(string(logs), "etherview-integration") {
		t.Fatalf("missing or unredacted database diagnostics: %v", readErr)
	}
}

func TestManagedRunnerDeadlockChild(t *testing.T) {
	if os.Getenv("DEADLOCK_TEST_CHILD") != "1" {
		t.Skip("subprocess fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	url := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	a, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background()) //nolint:errcheck
	b, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background()) //nolint:errcheck
	if _, err := a.Exec(ctx, "CREATE TABLE runner_deadlock (id int PRIMARY KEY); INSERT INTO runner_deadlock VALUES (1),(2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, "BEGIN; SELECT * FROM runner_deadlock WHERE id=1 FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec(ctx, "BEGIN; SELECT * FROM runner_deadlock WHERE id=2 FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := a.Exec(ctx, "SELECT * FROM runner_deadlock WHERE id=2 FOR UPDATE"); done <- err }()
	_, secondErr := b.Exec(ctx, "SELECT * FROM runner_deadlock WHERE id=1 FOR UPDATE")
	_, _ = b.Exec(ctx, "ROLLBACK")
	firstErr := <-done
	_, _ = a.Exec(ctx, "ROLLBACK")
	if firstErr == nil && secondErr == nil {
		t.Fatal("fixture did not produce a deadlock")
	}
	// Deliberately report success; the parent checks PostgreSQL independently.
}
