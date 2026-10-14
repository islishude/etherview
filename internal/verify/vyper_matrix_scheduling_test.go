package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A failed version must release its slot and must not hide the remaining
// versions or turn a partial matrix into a passing test process.
func TestVyperMatrixSchedulingFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var profiles map[string]VyperCapabilities
	if err := json.Unmarshal(vyperCapabilitiesJSON, &profiles); err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 2, 4, 8} {
		t.Run(strconv.Itoa(parallel), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable,
				"-test.run=^TestVyperDynamicRuntimeMatrix$", "-test.count=1",
				"-test.v", "-test.parallel="+strconv.Itoa(parallel))
			// Missing packages fail before starting any compiler; this probe
			// requires neither the SEA nor the local package installation.
			cmd.Env = append(os.Environ(), "ETHERVIEW_TEST_VYPER_MATRIX_ROOT="+t.TempDir())
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("matrix failure did not finish normally: %v\n%s", err, output)
			}
			seen := make(map[string]bool, len(profiles))
			for line := range strings.SplitSeq(string(output), "\n") {
				version, ok := strings.CutPrefix(strings.TrimSpace(line), "--- FAIL: TestVyperDynamicRuntimeMatrix/")
				if !ok {
					continue
				}
				version, _, _ = strings.Cut(version, " ")
				if _, ok := profiles[version]; !ok || seen[version] {
					t.Fatalf("unexpected or duplicate version %q\n%s", version, output)
				}
				seen[version] = true
			}
			if len(seen) != len(profiles) {
				t.Fatalf("only %d/%d versions reported failure\n%s", len(seen), len(profiles), output)
			}
		})
	}
}
