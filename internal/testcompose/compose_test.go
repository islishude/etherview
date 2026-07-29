package testcompose

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type recordingExecutor struct {
	command Command
	output  []byte
	err     error
}

func (e *recordingExecutor) Run(_ context.Context, command Command) ([]byte, error) {
	e.command = command
	return e.output, e.err
}

func TestProjectCommandKeepsFilesProfilesAndEnvironment(t *testing.T) {
	t.Setenv("COMPOSE", "docker compose")
	executor := &recordingExecutor{}
	project := New("/repo", "runtime", "compose.yaml", "/tmp/overlay.yaml")
	project.Profiles = []string{"distributed"}
	project.Env = map[string]string{"POSTGRES_PASSWORD": "test-password"}
	project.Executor = executor

	if _, err := project.Run(context.Background(), "up", "-d", "postgres"); err != nil {
		t.Fatal(err)
	}

	if executor.command.Name != "docker" {
		t.Fatalf("command name = %q, want docker", executor.command.Name)
	}
	wantArguments := []string{
		"compose", "-p", "runtime",
		"-f", filepath.Join("/repo", "compose.yaml"),
		"-f", "/tmp/overlay.yaml",
		"--profile", "distributed",
		"up", "-d", "postgres",
	}
	if !slices.Equal(executor.command.Args, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", executor.command.Args, wantArguments)
	}
	if !slices.Contains(executor.command.Env, "POSTGRES_PASSWORD=test-password") {
		t.Fatalf("environment does not contain project override: %#v", executor.command.Env)
	}
}

func TestProjectPortNormalizesIPv4AndIPv6Bindings(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "ipv4", output: "0.0.0.0:49152\n", want: "127.0.0.1:49152"},
		{name: "ipv6", output: "[::]:49153\n", want: "127.0.0.1:49153"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{output: []byte(test.output)}
			project := New("/repo", "runtime", "compose.yaml")
			project.Compose = "compose"
			project.Executor = executor

			got, err := project.Port(context.Background(), "postgres", 5432)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("port = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewQuietCapturesWithoutStreaming(t *testing.T) {
	project := NewQuiet("/repo", "runtime", "compose.yaml")
	executor, ok := project.Executor.(OSExecutor)
	if !ok {
		t.Fatalf("quiet executor type = %T, want testcompose.OSExecutor", project.Executor)
	}
	if executor.Stdout != nil || executor.Stderr != nil {
		t.Fatalf("quiet executor streams output: %#v", executor)
	}

	recorder := &recordingExecutor{output: []byte("captured output")}
	project.Executor = recorder
	output, err := project.Run(context.Background(), "ps", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "captured output" {
		t.Fatalf("captured output = %q", output)
	}
}

func TestProjectFailureIncludesCapturedOutputOnce(t *testing.T) {
	project := NewQuiet("/repo", "runtime", "compose.yaml")
	project.Executor = &recordingExecutor{
		output: []byte("compose diagnostic"),
		err:    errors.New("compose failed"),
	}

	_, err := project.Run(context.Background(), "up", "-d")
	if err == nil {
		t.Fatal("expected Compose failure")
	}
	if count := strings.Count(err.Error(), "compose diagnostic"); count != 1 {
		t.Fatalf("captured output occurrences = %d, want 1: %v", count, err)
	}
}
