//go:build !windows

package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSolcJSCancellationTerminatesWholeProcessGroup(t *testing.T) {
	root := t.TempDir()
	childPIDPath := filepath.Join(root, "child-pid")
	fakeExecutor := writeFakeExecutor(t, fmt.Sprintf(
		"#!/bin/sh\n/bin/sleep 30 &\nchild=$!\nprintf '%%s' \"$child\" > %s\nwait \"$child\"\n",
		shellQuote(childPIDPath),
	))
	compiler := fakeSolcJSCompiler(fakeExecutor)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := compiler.run(
			ctx, filepath.Dir(fakeExecutor), fakeExecutor, "0.8.36", []byte(`{}`), false,
		)
		result <- err
	}()
	waitForTestFile(t, childPIDPath)
	raw, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID=%q error=%v", raw, err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation result: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("compiler child process %d is still alive: %v", childPID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
