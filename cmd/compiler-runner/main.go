// Command compiler-runner is the minimal entrypoint of the digest-pinned
// generic compiler sandbox image. It accepts exactly one framed compiler and
// Standard JSON input on stdin.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/verify"
)

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}

func run(input io.Reader, output, diagnostics io.Writer) error {
	frame, err := verify.ReadRunnerFrame(input)
	if err != nil {
		return err
	}
	compiler, err := os.CreateTemp("/tmp", "compiler-*")
	if err != nil {
		return errors.New("create compiler")
	}
	path := compiler.Name()
	defer os.Remove(path) //nolint:errcheck
	if err := compiler.Chmod(0o500); err != nil {
		compiler.Close() //nolint:errcheck
		return errors.New("secure compiler")
	}
	if _, err := compiler.Write(frame.Compiler); err != nil {
		compiler.Close() //nolint:errcheck
		return errors.New("write compiler")
	}
	if err := compiler.Sync(); err != nil {
		compiler.Close() //nolint:errcheck
		return errors.New("sync compiler")
	}
	if err := compiler.Close(); err != nil {
		return errors.New("close compiler")
	}
	if _, err := verify.ExecutablePlatform(path); err != nil {
		return err
	}
	versionContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	versionCommand := exec.CommandContext(versionContext, path, "--version")
	versionOutput := &boundedWriter{limit: 1 << 20}
	versionCommand.Stdout, versionCommand.Stderr = versionOutput, versionOutput
	err = versionCommand.Run()
	cancel()
	if err != nil || versionOutput.exceeded ||
		!strings.Contains(versionOutput.buffer.String(), baseCompilerVersion(frame.Version)) {
		return errors.New("compiler version check failed")
	}
	command := exec.Command(path, "--standard-json")
	command.Stdin = bytes.NewReader(frame.Input)
	command.Stdout = output
	command.Stderr = diagnostics
	command.Env = []string{"HOME=/tmp", "PATH=/usr/bin:/bin"}
	if err := command.Run(); err != nil {
		return errors.New("compiler failed")
	}
	return nil
}

func baseCompilerVersion(version string) string {
	version = strings.TrimPrefix(version, "v")
	if separator := strings.IndexByte(version, '+'); separator >= 0 {
		return version[:separator]
	}
	return version
}

type boundedWriter struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	original := len(value)
	remaining := writer.limit - writer.buffer.Len()
	if remaining <= 0 {
		writer.exceeded = true
		return original, nil
	}
	if len(value) > remaining {
		writer.exceeded = true
		value = value[:remaining]
	}
	_, _ = writer.buffer.Write(value)
	return original, nil
}
