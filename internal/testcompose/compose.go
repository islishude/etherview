// Package testcompose owns the small Docker Compose process boundary shared by
// service-backed repository tests. It keeps lifecycle, diagnostics, and command
// construction in Go while allowing the repository Compose selector to support
// both the Docker plugin and the standalone binary.
package testcompose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Executor interface {
	Run(context.Context, Command) ([]byte, error)
}

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type OSExecutor struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (e OSExecutor) Run(ctx context.Context, command Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = command.Env
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(&captured, writerOrDiscard(e.Stdout))
	cmd.Stderr = io.MultiWriter(&captured, writerOrDiscard(e.Stderr))
	if err := cmd.Run(); err != nil {
		return captured.Bytes(), fmt.Errorf("%s %s: %w", command.Name, strings.Join(command.Args, " "), err)
	}
	return captured.Bytes(), nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

type Project struct {
	Root     string
	Name     string
	Files    []string
	Profiles []string
	Env      map[string]string
	Compose  string
	Executor Executor
}

func New(root, name string, files ...string) *Project {
	return &Project{
		Root:     root,
		Name:     name,
		Files:    files,
		Compose:  composeCommand(root),
		Executor: OSExecutor{Stdout: os.Stdout, Stderr: os.Stderr},
	}
}

// NewQuiet returns a project that captures Compose output without streaming
// successful lifecycle progress to the caller's terminal. Project.Run still
// includes the captured output once when a command fails.
func NewQuiet(root, name string, files ...string) *Project {
	project := New(root, name, files...)
	project.Executor = OSExecutor{}
	return project
}

func UniqueProjectName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func (p *Project) Run(ctx context.Context, arguments ...string) ([]byte, error) {
	if p.Executor == nil {
		return nil, fmt.Errorf("compose project %q has no executor", p.Name)
	}
	command, err := p.Command(arguments...)
	if err != nil {
		return nil, err
	}
	output, err := p.Executor.Run(ctx, command)
	if err != nil {
		return output, fmt.Errorf("compose project %q: %w\n%s", p.Name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (p *Project) Command(arguments ...string) (Command, error) {
	command := strings.Fields(strings.TrimSpace(p.Compose))
	if len(command) == 0 {
		return Command{}, fmt.Errorf("compose command is empty")
	}
	args := make([]string, 0, 2+len(p.Files)*2+len(p.Profiles)*2+len(arguments))
	if p.Name != "" {
		args = append(args, "-p", p.Name)
	}
	for _, file := range p.Files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(p.Root, file)
		}
		args = append(args, "-f", file)
	}
	for _, profile := range p.Profiles {
		args = append(args, "--profile", profile)
	}
	args = append(args, arguments...)

	env := append([]string(nil), os.Environ()...)
	for key, value := range p.Env {
		env = append(env, key+"="+value)
	}
	return Command{
		Name: command[0],
		Args: append(command[1:], args...),
		Dir:  p.Root,
		Env:  env,
	}, nil
}

func (p *Project) Up(ctx context.Context, services ...string) error {
	args := []string{"up", "-d", "--wait", "--wait-timeout", "90", "--remove-orphans"}
	args = append(args, services...)
	_, err := p.Run(ctx, args...)
	return err
}

func (p *Project) Down(ctx context.Context) error {
	_, err := p.Run(ctx, "down", "--volumes", "--remove-orphans")
	return err
}

func (p *Project) Logs(ctx context.Context) string {
	output, _ := p.Run(ctx, "logs", "--no-color", "--timestamps")
	return string(output)
}

func (p *Project) Port(ctx context.Context, service string, containerPort int) (string, error) {
	output, err := p.Run(ctx, "port", service, strconv.Itoa(containerPort))
	if err != nil {
		return "", err
	}
	lines := strings.Fields(string(output))
	if len(lines) == 0 {
		return "", fmt.Errorf("compose project %q service %q has no published port %d", p.Name, service, containerPort)
	}
	binding := lines[0]
	index := strings.LastIndexByte(binding, ':')
	if index < 0 || index == len(binding)-1 {
		return "", fmt.Errorf("invalid Compose port binding %q", binding)
	}
	if _, err := strconv.ParseUint(binding[index+1:], 10, 16); err != nil {
		return "", fmt.Errorf("invalid Compose port binding %q: %w", binding, err)
	}
	return "127.0.0.1:" + binding[index+1:], nil
}

func composeCommand(root string) string {
	if value := strings.TrimSpace(os.Getenv("COMPOSE")); value != "" {
		return value
	}
	return filepath.Join(root, ".github", "scripts", "compose.sh")
}
