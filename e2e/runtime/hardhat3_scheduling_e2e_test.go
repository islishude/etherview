//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Subprocesses let expected testing.T failures exercise the production group
// without requiring Docker or converting a real E2E failure into a success.
func TestHardhat3TopologyScheduling(t *testing.T) {
	if scenario := os.Getenv("ETHERVIEW_HARDHAT_SCHEDULING_PROBE"); scenario != "" {
		probeHardhatScheduling(t, scenario)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, suite := range []string{"hardhat", "vyper"} {
		for _, parallel := range []int{1, 2} {
			for _, scenario := range []string{"success", "monolith", "distributed", "missing", "parity", "deadline", "cleanup"} {
				t.Run(suite+"/"+strconv.Itoa(parallel)+"/"+scenario, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHardhat3TopologyScheduling$", "-test.count=1", "-test.v", "-test.parallel="+strconv.Itoa(parallel))
					cmd.Env = append(os.Environ(), "ETHERVIEW_HARDHAT_SCHEDULING_PROBE="+scenario, "ETHERVIEW_SCHEDULING_SUITE="+suite)
					output, err := cmd.CombinedOutput()
					wantCode := 1
					if scenario == "success" {
						wantCode = 0
					}
					code := 0
					if err != nil {
						var exit *exec.ExitError
						if !errors.As(err, &exit) {
							t.Fatalf("probe did not exit normally: %v\n%s", err, output)
						}
						code = exit.ExitCode()
					}
					if ctx.Err() != nil || code != wantCode || !strings.Contains(string(output), "scheduling probe complete") {
						t.Fatalf("probe exit=%d, want=%d: %v\n%s", code, wantCode, ctx.Err(), output)
					}
				})
			}
		}
	}
}

func probeHardhatScheduling(t *testing.T, scenario string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if scenario == "deadline" {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer deadlineCancel()
	}
	parallel, err := strconv.Atoi(flag.Lookup("test.parallel").Value.String())
	if err != nil {
		t.Fatal(err)
	}
	var started, cleaned, active, maximum atomic.Int32
	bothStarted := make(chan struct{})
	run := func(t *testing.T, mode string) hardhatProxySnapshot {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		t.Cleanup(func() {
			active.Add(-1)
			cleaned.Add(1)
			if scenario == "cleanup" && mode == "monolith" {
				t.Error("injected cleanup failure")
			}
		})
		if started.Add(1) == 2 {
			close(bothStarted)
		}
		if parallel == 2 {
			select {
			case <-bothStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("topologies did not overlap")
			}
		}
		if scenario == mode {
			t.Fatal("injected topology failure")
		}
		if scenario == "missing" && mode == "monolith" {
			t.Skip("injected missing snapshot")
		}
		result := hardhatProxySnapshot{AddressJobs: 1}
		if scenario == "parity" && mode == "distributed" {
			result.AddressJobs++
		}
		return result
	}
	var accepted bool
	if os.Getenv("ETHERVIEW_SCHEDULING_SUITE") == "vyper" {
		_, accepted = runVerificationTopologies(t, ctx, func(t *testing.T, mode string) vyperApplicationSnapshot {
			result := run(t, mode)
			return vyperApplicationSnapshot{Jobs: result.AddressJobs}
		})
	} else {
		_, accepted = runVerificationTopologies(t, ctx, run)
	}
	if cleaned.Load() != 2 || started.Load() != 2 || active.Load() != 0 || maximum.Load() != int32(parallel) {
		t.Fatalf("group returned before both topologies and cleanups completed: started=%d cleaned=%d active=%d maximum=%d", started.Load(), cleaned.Load(), active.Load(), maximum.Load())
	}
	if accepted != (scenario == "success") {
		t.Fatalf("acceptance=%t for %s", accepted, scenario)
	}
	t.Log("scheduling probe complete")
}
