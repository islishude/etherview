package testcompose

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const executorOutputLines = 4096

func TestOSExecutorOutputHelper(t *testing.T) {
	if os.Getenv("ETHERVIEW_EXECUTOR_HELPER") != "1" {
		return
	}
	var workers sync.WaitGroup
	for _, stream := range []struct {
		writer io.Writer
		text   string
	}{{os.Stdout, "stdout\n"}, {os.Stderr, "stderr\n"}} {
		workers.Go(func() {
			for range executorOutputLines {
				if _, err := io.WriteString(stream.writer, stream.text); err != nil {
					os.Exit(2)
				}
			}
		})
	}
	workers.Wait()
	code, _ := strconv.Atoi(os.Getenv("ETHERVIEW_EXECUTOR_EXIT"))
	os.Exit(code)
}

func TestOSExecutorCapturesConcurrentOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"quiet", "separate", "shared"} {
		for _, exitCode := range []int{0, 7} {
			t.Run(mode+"/exit="+strconv.Itoa(exitCode), func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				executor := OSExecutor{}
				switch mode {
				case "separate":
					executor.Stdout, executor.Stderr = &stdout, &stderr
				case "shared":
					executor.Stdout, executor.Stderr = &stdout, &stdout
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				output, err := executor.Run(ctx, Command{
					Name: executable,
					Args: []string{"-test.run=^TestOSExecutorOutputHelper$"},
					Env:  append(os.Environ(), "ETHERVIEW_EXECUTOR_HELPER=1", "ETHERVIEW_EXECUTOR_EXIT="+strconv.Itoa(exitCode)),
				})
				if exitCode == 0 && err != nil {
					t.Fatal(err)
				}
				if exitCode != 0 {
					var exitError *exec.ExitError
					if !errors.As(err, &exitError) || exitError.ExitCode() != exitCode {
						t.Fatalf("error = %v, want exit code %d", err, exitCode)
					}
				}
				if len(output) != executorOutputLines*len("stdout\nstderr\n") ||
					bytes.Count(output, []byte("stdout\n")) != executorOutputLines ||
					bytes.Count(output, []byte("stderr\n")) != executorOutputLines {
					t.Fatalf("captured output lost or corrupted: %d bytes", len(output))
				}
				switch mode {
				case "separate":
					if stdout.String() != strings.Repeat("stdout\n", executorOutputLines) || stderr.String() != strings.Repeat("stderr\n", executorOutputLines) {
						t.Fatal("streamed output lost, corrupted, or routed to the wrong stream")
					}
				case "shared":
					if !bytes.Equal(stdout.Bytes(), output) {
						t.Fatal("shared stream differs from captured output")
					}
				}
			})
		}
	}
}
