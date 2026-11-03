//go:build runtimee2e

package runtimee2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Docker populates a new named volume before the application starts. Concurrent
// copy-up of an image-provided cache child used to fail with mkdir: file exists.
// Exercise that boundary directly, without PostgreSQL or an application lock.
func TestCompilerCacheReplicasColdStart(t *testing.T) {
	for round := range 3 {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			volume := fmt.Sprintf("etherview-cache-replicas-%d-%d", os.Getpid(), time.Now().UnixNano())
			command := func(ctx context.Context, args ...string) error {
				output, err := exec.CommandContext(ctx, dockerCommand(), args...).CombinedOutput()
				if err != nil {
					return fmt.Errorf("docker %s: %w: %s", args[0], err, output)
				}
				return nil
			}
			if err := command(ctx, "volume", "create", volume); err != nil {
				t.Fatal(err)
			}
			var created []string
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
				defer stop()
				if len(created) > 0 {
					if err := command(cleanup, append([]string{"rm", "-f"}, created...)...); err != nil {
						t.Error(err)
					}
				}
				if err := command(cleanup, "volume", "rm", volume); err != nil {
					t.Error(err)
				}
			})
			type result struct {
				name string
				err  error
			}
			start := make(chan struct{})
			results := make(chan result, 8)
			for replica := range 8 {
				name := fmt.Sprintf("%s-%d", volume, replica)
				go func() {
					<-start
					err := command(ctx, "create", "--name", name, "--network", "none",
						"--mount", "type=volume,src="+volume+",dst=/var/lib/etherview/compilers",
						valueOrDefault("IMAGE", "etherview:local"), "version")
					results <- result{name, err}
				}()
			}
			close(start)
			var failures []string
			for range 8 {
				result := <-results
				if result.err != nil {
					failures = append(failures, result.err.Error())
				} else {
					created = append(created, result.name)
				}
			}
			if len(failures) > 0 {
				t.Fatal(strings.Join(failures, "\n"))
			}
			if err := command(ctx, "start", "--attach", created[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}
