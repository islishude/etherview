package compilerbundle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var ErrExecution = errors.New("WASM compiler execution failed")
var ErrChanged = errors.New("WASM compiler runtime changed")
var ErrLimit = errors.New("WASM compiler stream limit exceeded")
var ErrCleanup = errors.New("WASM compiler process cleanup failed")

type Runner struct {
	Path                          string
	Timeout                       time.Duration
	MaxInputBytes, MaxOutputBytes int
}

func (r Runner) Execute(ctx context.Context, expected [32]byte, args []string, input []byte) ([]byte, error) {
	if r.Timeout <= 0 || r.MaxInputBytes <= 0 || r.MaxOutputBytes <= 0 || len(input) > r.MaxInputBytes || expected == [32]byte{} {
		return nil, ErrLimit
	}
	identity, err := Validate(r.Path)
	if err != nil {
		return nil, err
	}
	if identity.Digest != expected {
		return nil, ErrChanged
	}
	directory, err := os.MkdirTemp("", "etherview-wasm-")
	if err != nil {
		return nil, ErrExecution
	}
	output, runErr := r.execute(ctx, directory, args, input)
	cleanupErr := os.RemoveAll(directory)
	if cleanupErr != nil {
		return nil, ErrCleanup
	}
	if runErr != nil {
		return nil, runErr
	}
	identity, err = Validate(r.Path)
	if err != nil || identity.Digest != expected {
		return nil, ErrChanged
	}
	return output, nil
}
func (r Runner) execute(ctx context.Context, directory string, args []string, input []byte) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, r.Path, args...)
	command.Dir = directory
	command.Env = []string{"HOME=/nonexistent", "TMPDIR=" + directory, "LANG=C", "LC_ALL=C"}
	command.Stdin = bytes.NewReader(input)
	stdout := &boundedOutput{limit: r.MaxOutputBytes, cancel: cancel}
	stderr := &boundedOutput{limit: 1 << 20, cancel: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	configureProcess(command)
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return killGroup(command.Process)
	}
	command.WaitDelay = 2 * time.Second
	runErr := command.Run()
	if !groupTerminated(command.Process) {
		_ = killGroup(command.Process)
		deadline := time.Now().Add(2 * time.Second)
		for !groupTerminated(command.Process) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !groupTerminated(command.Process) {
			return nil, ErrCleanup
		}
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, ErrLimit
	}
	if runErr != nil || ctx.Err() != nil || runCtx.Err() != nil {
		return nil, ErrExecution
	}
	return bytes.Clone(stdout.buffer.Bytes()), nil
}

// Kept local to one execution; os/exec joins writer goroutines before inspection.
type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		b.exceeded = true
		if remaining > 0 {
			_, _ = b.buffer.Write(p[:remaining])
		}
		b.cancel()
		return remaining, ErrLimit
	}
	return b.buffer.Write(p)
}

var _ io.Writer = (*boundedOutput)(nil)

// RuntimeFile selects fixed sibling data only; source requests never select it.
func RuntimeFile(executable, name string) string {
	return filepath.Join(filepath.Dir(executable), name)
}
