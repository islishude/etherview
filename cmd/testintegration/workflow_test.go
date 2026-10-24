package main

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIntegrationWorkflowRequiresEveryShard(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/ci-checks.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Name, Needs, If string
			Strategy        struct {
				FailFast *bool `yaml:"fail-fast"`
				Matrix   struct{ Shard []int }
			}
			Steps []struct {
				Run, If string
				Env     map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		t.Fatal(err)
	}
	shards := workflow.Jobs["integration-shards"]
	if !slices.Equal(shards.Strategy.Matrix.Shard, []int{1, 2, 3, 4}) ||
		shards.Strategy.FailFast == nil || *shards.Strategy.FailFast {
		t.Fatal("integration matrix must run all four shards without fail-fast cancellation")
	}
	if len(shards.Steps) != 4 ||
		shards.Steps[2].Run != "make test-integration INTEGRATION_SHARD=${{ matrix.shard }} INTEGRATION_SHARDS=4" ||
		shards.Steps[3].Run != "make test-rustfs" || shards.Steps[3].If != "matrix.shard == 1" {
		t.Fatal("matrix must cover every integration shard and run RustFS exactly once")
	}
	gate := workflow.Jobs["integration"]
	if gate.Name != "PostgreSQL integration" || gate.Needs != "integration-shards" || gate.If != "always()" ||
		len(gate.Steps) != 1 || gate.Steps[0].Env["RESULT"] != "${{ needs.integration-shards.result }}" {
		t.Fatal("stable integration gate must depend on the complete matrix even when it fails")
	}
	for _, result := range []string{"success", "failure", "cancelled", "skipped", ""} {
		command := exec.Command("sh", "-c", gate.Steps[0].Run)
		command.Env = append(os.Environ(), "RESULT="+result)
		if err := command.Run(); (err == nil) != (result == "success") {
			t.Errorf("aggregate gate with result %q: %v", result, err)
		}
	}
}
