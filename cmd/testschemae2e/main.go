// Command testschemae2e validates a fresh PostgreSQL schema lifecycle through
// the production migration image and production Compose definition.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/islishude/etherview/internal/testcompose"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *root); err != nil {
		fmt.Fprintf(os.Stderr, "test-schema-e2e: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root string) error {
	project := testcompose.New(root, testcompose.UniqueProjectName("etherview-schema"), "compose.yaml")
	project.Profiles = []string{"distributed"}
	project.Env = map[string]string{
		"POSTGRES_PASSWORD":     "etherview-schema-e2e",
		"ETHERVIEW_IMAGE":       valueOrDefault("IMAGE", "etherview:local"),
		"ETHERVIEW_CONFIG_FILE": root + "/deploy/config.example.yaml",
	}
	defer cleanupProject(project)

	if err := project.Up(ctx, "postgres"); err != nil {
		return err
	}
	if _, err := project.Run(ctx, "run", "--rm", "--no-deps", "migration"); err != nil {
		return fmt.Errorf("fresh migration: %w", err)
	}
	if _, err := project.Run(
		ctx,
		"run", "--rm", "--no-deps", "migration",
		"migrate", "status", "--config=/etc/etherview/config.yaml",
	); err != nil {
		return fmt.Errorf("migration status: %w", err)
	}
	fmt.Println("test-schema-e2e: PASS (fresh production-image migration and status)")
	return nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func cleanupProject(project *testcompose.Project) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := project.Down(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "test-schema-e2e: cleanup failed: %v\n", err)
	}
}
