// Command testintegration runs the integration-tagged Go suite against either
// an explicitly supplied disposable database or a PostgreSQL 18 Compose
// project that it creates and owns for the duration of the test.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/islishude/etherview/internal/testcompose"
)

type options struct {
	root          string
	packages      string
	race          bool
	benchmark     string
	benchmarkTime string
	run           string
}

func main() {
	var opts options
	flag.StringVar(&opts.root, "root", ".", "repository root")
	flag.StringVar(&opts.packages, "packages", "", "optional space-separated Go package patterns")
	flag.BoolVar(&opts.race, "race", false, "enable the Go race detector")
	flag.StringVar(&opts.benchmark, "benchmark", "", "optional Go benchmark regexp; skips ordinary tests")
	flag.StringVar(&opts.benchmarkTime, "benchtime", "1s", "Go benchmark duration or iteration count")
	flag.StringVar(&opts.run, "run", "", "optional Go test regexp")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "test-integration: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options) error {
	databaseURL := strings.TrimSpace(os.Getenv("INTEGRATION_DATABASE_URL"))
	var project *testcompose.Project
	if databaseURL == "" {
		project = testcompose.New(
			opts.root,
			testcompose.UniqueProjectName("etherview-integration"),
			"e2e/integration/compose.yaml",
		)
		fmt.Printf("test-integration: starting owned PostgreSQL 18 project %s\n", project.Name)
		if err := project.Up(ctx, "postgres"); err != nil {
			return err
		}
		defer cleanupProject(project)

		binding, err := project.Port(ctx, "postgres", 5432)
		if err != nil {
			return err
		}
		databaseURL = "postgres://etherview:etherview-integration@" + binding + "/etherview?sslmode=disable"
	} else {
		fmt.Println("test-integration: using caller-supplied disposable PostgreSQL database")
	}

	goCommand := strings.TrimSpace(os.Getenv("GO"))
	if goCommand == "" {
		goCommand = "go"
	}
	if err := runCommand(ctx, opts.root, []string{"ETHERVIEW_DATABASE_URL=" + databaseURL},
		goCommand, "run", "./cmd/etherview", "migrate", "up"); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	if err := runCommand(ctx, opts.root, []string{"ETHERVIEW_DATABASE_URL=" + databaseURL},
		goCommand, "run", "./cmd/etherview", "migrate", "status"); err != nil {
		return fmt.Errorf("migrate status: %w", err)
	}

	packages := strings.Fields(opts.packages)
	if len(packages) == 0 {
		discovered, err := discoverIntegrationPackages(opts.root)
		if err != nil {
			return err
		}
		packages = discovered
	}
	if len(packages) == 0 {
		return errors.New("no integration-tagged Go test packages were found")
	}
	arguments := []string{"test", "-count=1", "-tags=integration"}
	if opts.race {
		arguments = append(arguments, "-race")
	}
	if opts.benchmark != "" {
		if opts.race {
			return errors.New("integration benchmark does not support the race detector")
		}
		if strings.TrimSpace(opts.benchmark) != opts.benchmark || strings.TrimSpace(opts.benchmarkTime) == "" ||
			strings.TrimSpace(opts.benchmarkTime) != opts.benchmarkTime {
			return errors.New("integration benchmark arguments must be non-empty and trimmed")
		}
		arguments = append(arguments, "-run", "^$", "-bench", opts.benchmark, "-benchmem", "-benchtime", opts.benchmarkTime)
	} else if opts.run != "" {
		if strings.TrimSpace(opts.run) != opts.run {
			return errors.New("integration run regexp must be non-empty and trimmed")
		}
		arguments = append(arguments, "-run", opts.run)
	}
	arguments = append(arguments, packages...)
	if err := runCommand(ctx, opts.root, []string{"ETHERVIEW_TEST_DATABASE_URL=" + databaseURL},
		goCommand, arguments...); err != nil {
		return fmt.Errorf("integration suite: %w", err)
	}
	return nil
}

func discoverIntegrationPackages(root string) ([]string, error) {
	found := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "dist":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !hasIntegrationBuildTag(contents) {
			return nil
		}
		directory, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		found["./"+filepath.ToSlash(directory)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover integration packages: %w", err)
	}
	packages := make([]string, 0, len(found))
	for pkg := range found {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	return packages, nil
}

func hasIntegrationBuildTag(contents []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "//go:build integration" {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

func runCommand(ctx context.Context, directory string, environment []string, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	fmt.Printf("test-integration: running %s %s\n", name, strings.Join(arguments, " "))
	return command.Run()
}

func cleanupProject(project *testcompose.Project) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := project.Down(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "test-integration: cleanup failed: %v\n", err)
	}
}
