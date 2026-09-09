package wasmcompiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var helperDirectory string
var helperBuildOnce sync.Once
var helperBuildError error

func TestMain(m *testing.M) {
	var err error
	helperDirectory, err = os.MkdirTemp("", "etherview-wasm-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err = os.RemoveAll(helperDirectory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
func compilerHelper(t *testing.T) string {
	t.Helper()
	path := filepath.Join(helperDirectory, "etherview-wasm")
	helperBuildOnce.Do(func() {
		cmd := exec.Command("go", "build", "-o", path, "../../cmd/etherview-wasm")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			helperBuildError = fmt.Errorf("build compiler helper: %w: %s", err, out)
		}
	})
	if helperBuildError != nil {
		t.Fatal(helperBuildError)
	}
	return path
}

// Large reference compilers run in the real non-race production subprocess.
// The test process (including its I/O and cancellation) remains race instrumented;
// small synthetic guest/host ABI tests exercise the VM directly under -race.
func runSolcHelper(t *testing.T, ctx context.Context, path, version string, input []byte, maxOutput int) ([]byte, error) {
	t.Helper()
	helper := compilerHelper(t)
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	cmd := exec.CommandContext(ctx, helper, "--compile", "solc", path, version, hex.EncodeToString(digest[:]), "5242880", strconv.Itoa(maxOutput), "60000")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		for _, known := range []error{ErrVersion, ErrArtifact, ErrUnsupported, ErrInputLimit, ErrOutputLimit} {
			if message == known.Error() {
				return nil, known
			}
		}
		return nil, fmt.Errorf("%w: helper %v: %s", ErrExecution, err, message)
	}
	return result, nil
}

func TestSolcHelperOutputLimit(t *testing.T) {
	path := "../../compiler/node_modules/solc/soljson.js"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("reference compiler not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := runSolcHelper(t, ctx, path, "0.8.36+commit.8a079791", []byte(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}}}`), 16)
	if !errors.Is(err, ErrOutputLimit) || len(out) != 0 {
		t.Fatalf("output limit: %q %v", out, err)
	}
}
