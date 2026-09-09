//go:build !windows

package compilerbundle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunnerRejectsAndTerminatesDescendants(t *testing.T) {
	path, _ := fixtureBundle(t)
	directory := t.TempDir()
	runner := Runner{Path: path, Timeout: 2 * time.Minute, MaxInputBytes: 128, MaxOutputBytes: 128}
	out, err := runner.execute(context.Background(), directory, []string{"-test.run=TestBundleProcess", "--", "spawn"}, nil)
	if err == nil || len(out) != 0 {
		t.Fatalf("accepted descendant output: %q %v", out, err)
	}
	raw, readErr := os.ReadFile(filepath.Join(directory, "child.pid"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(string(raw))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if signalErr := syscall.Kill(pid, 0); errors.Is(signalErr, syscall.ESRCH) {
		return
	}
	// A non-reaping PID 1 may retain a zombie; that must remain a cleanup failure.
	state, psErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if psErr == nil && strings.HasPrefix(strings.TrimSpace(string(state)), "Z") && errors.Is(err, ErrCleanup) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant remains live: state=%q error=%v", state, err)
}
