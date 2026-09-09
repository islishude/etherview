package compilerbundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixtureBundle(t *testing.T) (string, Identity) {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"etherview-wasm": raw, "python.wasm": []byte("test guest bytes"), "python.zip": []byte("test archive bytes"), "runtime.lock.json": []byte("{}\n")} {
		if err = os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err = Seal(root, "etherview-wasm"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "etherview-wasm")
	identity, err := Validate(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, identity
}
func TestManifestRejectsTampering(t *testing.T) {
	path, _ := fixtureBundle(t)
	target := filepath.Join(filepath.Dir(path), "python.wasm")
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("different guest"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampering: %v", err)
	}
}
func TestManifestRejectsUnlistedAndSymlinkFiles(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "extra", true: "symlink"}[link], func(t *testing.T) {
			path, _ := fixtureBundle(t)
			root := filepath.Dir(path)
			if err := os.Chmod(root, 0o755); err != nil {
				t.Fatal(err)
			}
			extra := filepath.Join(root, "extra")
			var err error
			if link {
				err = os.Symlink("python.wasm", extra)
			} else {
				err = os.WriteFile(extra, []byte("data"), 0o444)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(root, 0o555); err != nil {
				t.Fatal(err)
			}
			if _, err = Validate(path); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unexpected file: %v", err)
			}
		})
	}
}
func TestRunnerIsolationAndLimits(t *testing.T) {
	path, identity := fixtureBundle(t)
	t.Setenv("WASM_TEST_SECRET", "must-not-inherit")
	runner := Runner{Path: path, Timeout: 2 * time.Minute, MaxInputBytes: 128, MaxOutputBytes: 128}
	args := func(mode string) []string { return []string{"-test.run=TestBundleProcess", "--", mode} }
	out, err := runner.Execute(context.Background(), identity.Digest, args("environment"), nil)
	if err != nil || string(out) != "ok" {
		t.Fatalf("environment: %q %v", out, err)
	}
	if _, err = runner.Execute(context.Background(), identity.Digest, args("large"), nil); !errors.Is(err, ErrLimit) {
		t.Fatalf("output bound: %v", err)
	}
	if _, err = runner.Execute(context.Background(), identity.Digest, args("environment"), bytes.Repeat([]byte("x"), 129)); !errors.Is(err, ErrLimit) {
		t.Fatalf("input bound: %v", err)
	}
	changed := identity.Digest
	changed[0] ^= 1
	if _, err = runner.Execute(context.Background(), changed, args("environment"), nil); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed runtime: %v", err)
	}
	runner.Timeout = 100 * time.Millisecond
	start := time.Now()
	if out, err = runner.Execute(context.Background(), identity.Digest, args("sleep"), nil); !errors.Is(err, ErrExecution) || len(out) != 0 {
		t.Fatalf("timeout: %q %v", out, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("subprocess timeout was not bounded")
	}
}
func TestBundleProcess(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	if len(os.Args) != marker+2 {
		os.Exit(2)
	}
	switch os.Args[marker+1] {
	case "environment":
		cwd, err := os.Getwd()
		cwdInfo, e1 := os.Stat(cwd)
		tmpInfo, e2 := os.Stat(os.Getenv("TMPDIR"))
		if err != nil || e1 != nil || e2 != nil || !os.SameFile(cwdInfo, tmpInfo) || os.Getenv("HOME") != "/nonexistent" || os.Getenv("WASM_TEST_SECRET") != "" {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("ok")
		os.Exit(0)
	case "large":
		for {
			if _, err := os.Stdout.WriteString(strings.Repeat("x", 4096)); err != nil {
				os.Exit(4)
			}
		}
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "spawn":
		command := exec.Command(os.Args[0], "-test.run=TestBundleProcess", "--", "sleep")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if command.Start() != nil {
			os.Exit(5)
		}
		if os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "child.pid"), []byte(strconv.Itoa(command.Process.Pid)), 0o600) != nil {
			os.Exit(6)
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
