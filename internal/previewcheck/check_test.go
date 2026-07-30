package previewcheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/testcompose"
)

type fakeExecutor struct {
	inspectCalls int
	snapshots    [][]container
	commands     []testcompose.Command
	innerDigest  string
	probeCalls   int
	probeFailsAt int
}

func (e *fakeExecutor) Run(_ context.Context, command testcompose.Command) ([]byte, error) {
	e.commands = append(e.commands, command)
	if slices.Contains(command.Args, "ps") {
		return []byte("container-ids\n"), nil
	}
	if len(command.Args) > 0 && command.Args[0] == "inspect" {
		if slices.Contains(command.Args, "present") ||
			slices.ContainsFunc(command.Args, func(value string) bool {
				return strings.Contains(value, `println "present"`)
			}) {
			return []byte("present\n"), nil
		}
		index := min(e.inspectCalls, len(e.snapshots)-1)
		e.inspectCalls++
		var output strings.Builder
		for _, value := range e.snapshots[index] {
			encoded, _ := json.Marshal(value)
			output.Write(encoded)
			output.WriteByte('\n')
		}
		return []byte(output.String()), nil
	}
	if len(command.Args) > 0 && command.Args[0] == "exec" {
		if e.innerDigest == "" {
			return nil, errors.New("runner absent")
		}
		return []byte(e.innerDigest + "\n"), nil
	}
	if len(command.Args) > 0 && command.Args[0] == "run" {
		e.probeCalls++
		if e.probeFailsAt == e.probeCalls {
			return nil, errors.New("role not ready")
		}
		return nil, nil
	}
	return nil, errors.New("unexpected command")
}

func TestCheckAcceptsCompleteStablePreview(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:   [][]container{healthyContainers(0)},
		innerDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	err := Check(context.Background(), Options{
		Root:            "/repo",
		ProjectName:     "preview",
		ComposeFile:     "compose.preview.yaml",
		ComposeCommand:  "/repo/.github/scripts/compose.sh",
		DockerCommand:   "/usr/bin/docker",
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.inspectCalls < 2 {
		t.Fatalf("inspect calls = %d, want readiness plus stability inspection", executor.inspectCalls)
	}
	if !slices.ContainsFunc(executor.commands, func(command testcompose.Command) bool {
		return command.Name == "/repo/.github/scripts/compose.sh" &&
			slices.Contains(command.Args, "-p") &&
			slices.Contains(command.Args, "preview") &&
			slices.Contains(command.Env, "ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="+runner)
	}) {
		t.Fatal("Compose command did not preserve project, wrapper, and exact runner environment")
	}
	if !slices.ContainsFunc(executor.commands, secureRoleProbeCommand) {
		t.Fatal("checker did not issue the pinned, bounded application-role readiness probe")
	}
}

func TestCheckAcceptsExactReferenceWhenDaemonImageIDDiffers(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:abababababababababababababababababababababababababababababababab"
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:   [][]container{healthyContainers(0)},
		innerDigest: "sha256:cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd",
	}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsFailedCompilerPreflightImmediately(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	containers := healthyContainers(0)
	for index := range containers {
		if containers[index].Service == "compiler-preflight" {
			containers[index].ExitCode = 17
		}
	}
	executor := &fakeExecutor{snapshots: [][]container{containers}}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       "https://etherview.localhost:8080/api/v1/config",
		Timeout:         time.Second,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
	})
	if err == nil || !strings.Contains(err.Error(), `one-shot service "compiler-preflight" exited nonzero`) {
		t.Fatalf("error = %v, want failed preflight", err)
	}
}

func TestCheckRejectsRestartCountChangeDuringStability(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	server := featureServer(t, true, true)
	before := healthyContainers(0)
	after := healthyContainers(0)
	for index := range after {
		if after[index].Service == "verify" {
			after[index].RestartCount = 1
		}
	}
	executor := &fakeExecutor{
		snapshots:    [][]container{before, after},
		innerDigest:  "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		inspectCalls: 0,
	}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), `service "verify" restart count changed`) {
		t.Fatalf("error = %v, want restart-count change", err)
	}
}

func TestCheckRejectsDisabledPublicFeatures(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	server := featureServer(t, true, false)
	executor := &fakeExecutor{
		snapshots:   [][]container{healthyContainers(0)},
		innerDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "does not enable NFT metadata") {
		t.Fatalf("error = %v, want NFT metadata mismatch", err)
	}
}

func TestCheckRetriesRoleReadinessBeforeBaseline(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:    [][]container{healthyContainers(0)},
		innerDigest:  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		probeFailsAt: 1,
	}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.probeCalls < 3 {
		t.Fatalf("role probe calls = %d, want retry plus stability probe", executor.probeCalls)
	}
}

func TestCheckRejectsRoleReadinessLossDuringStability(t *testing.T) {
	const runner = "etherview-compiler-runner@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:    [][]container{healthyContainers(0)},
		innerDigest:  "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		probeFailsAt: 2,
	}
	err := Check(context.Background(), Options{
		RunnerImage:     runner,
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "role readiness failed during the stability window") {
		t.Fatalf("error = %v, want stability readiness loss", err)
	}
}

func TestValidateRunnerImageRequiresExactDigest(t *testing.T) {
	for _, value := range []string{
		"",
		"etherview-compiler-runner:preview",
		"etherview-compiler-runner@sha256:abc",
		"etherview-compiler-runner@sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if err := validateRunnerImage(value); err == nil {
			t.Fatalf("validateRunnerImage(%q) succeeded", value)
		}
	}
}

func featureServer(t *testing.T, verification, nftMetadata bool) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/config" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"data": map[string]any{
				"features": map[string]bool{
					"verification": verification,
					"nft_metadata": nftMetadata,
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func healthyContainers(restartCount int) []container {
	values := make([]container, 0, len(runningServices)+len(oneShotServices))
	for _, service := range runningServices {
		health := ""
		networks := map[string]json.RawMessage{
			"preview_default": json.RawMessage(`{}`),
		}
		if service == "postgres" || service == "compiler-runtime" {
			health = "healthy"
		}
		if service == "compiler-runtime" {
			networks = map[string]json.RawMessage{
				"preview_compiler-runtime": json.RawMessage(`{}`),
			}
		}
		if service == "verify" {
			networks["preview_compiler-runtime"] = json.RawMessage(`{}`)
		}
		values = append(values, container{
			ID:           service + "-id",
			Service:      service,
			Status:       "running",
			Running:      true,
			Health:       health,
			RestartCount: restartCount,
			Networks:     networks,
		})
	}
	for _, service := range oneShotServices {
		values = append(values, container{
			ID:           service + "-id",
			Service:      service,
			Status:       "exited",
			ExitCode:     0,
			RestartCount: restartCount,
		})
	}
	return values
}

func secureRoleProbeCommand(command testcompose.Command) bool {
	if command.Name != "/usr/bin/docker" || len(command.Args) == 0 || command.Args[0] != "run" {
		return false
	}
	for _, argument := range []string{
		"--rm",
		"--pull=never",
		"--network=preview_default",
		"--read-only",
		"--user=65532:65532",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=32",
		"--memory=32m",
		"--memory-swap=32m",
		"--cpus=0.25",
		roleProbeImage,
	} {
		if !slices.Contains(command.Args, argument) {
			return false
		}
	}
	if slices.ContainsFunc(command.Args, func(argument string) bool {
		return argument == "-p" || strings.HasPrefix(argument, "--publish")
	}) {
		return false
	}
	return slices.ContainsFunc(command.Args, func(argument string) bool {
		return strings.Contains(argument, "http://${role}:9090/health/ready")
	})
}
