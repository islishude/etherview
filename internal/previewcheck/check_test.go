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
	inspectCalls      int
	snapshots         [][]container
	commands          []testcompose.Command
	hostArchitecture  string
	imageArchitecture string
	nodeVersion       string
	probeCalls        int
	probeFailsAt      int
}

func (e *fakeExecutor) Run(_ context.Context, command testcompose.Command) ([]byte, error) {
	e.commands = append(e.commands, command)
	if len(command.Args) >= 2 &&
		command.Args[0] == "container" && command.Args[1] == "ls" {
		return []byte("container-ids\n"), nil
	}
	if len(command.Args) > 0 && command.Args[0] == "inspect" {
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
	if len(command.Args) > 0 && command.Args[0] == "info" {
		architecture := e.hostArchitecture
		if architecture == "" {
			architecture = "aarch64"
		}
		return []byte(architecture + "\n"), nil
	}
	if len(command.Args) > 0 && command.Args[0] == "exec" {
		if slices.Contains(command.Args, "process.arch") {
			architecture := e.imageArchitecture
			if architecture == "" {
				architecture = "arm64"
			}
			return []byte(architecture + "\n"), nil
		}
		version := e.nodeVersion
		if version == "" {
			version = expectedNodeVersion
		}
		return []byte(version + "\n"), nil
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

func TestCheckAcceptsCompleteStableNativePreview(t *testing.T) {
	server := featureServer(t, true, true)
	executor := &fakeExecutor{snapshots: [][]container{healthyContainers(0)}}
	err := Check(context.Background(), Options{
		Root:            "/repo",
		ProjectName:     "preview",
		DockerCommand:   "/usr/bin/docker",
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
		return command.Name == "/usr/bin/docker" &&
			slices.Contains(command.Args, "label=com.docker.compose.project=preview")
	}) {
		t.Fatal("checker did not enumerate every container in the exact Compose project")
	}
	if !slices.ContainsFunc(executor.commands, func(command testcompose.Command) bool {
		return command.Name == "/usr/bin/docker" &&
			slices.Equal(command.Args, []string{
				"exec", "--env", "LD_LIBRARY_PATH=" + compilerLibraryPath,
				"api-id", "/usr/local/bin/node", "--print", "process.arch",
			})
	}) {
		t.Fatal("checker did not execute the bundled Node runtime architecture probe")
	}
	if !slices.ContainsFunc(executor.commands, func(command testcompose.Command) bool {
		return command.Name == "/usr/bin/docker" &&
			slices.Equal(command.Args, []string{
				"exec", "--env", "LD_LIBRARY_PATH=" + compilerLibraryPath,
				"api-id", "/usr/local/bin/node", "--version",
			})
	}) {
		t.Fatal("checker did not execute the bundled Node runtime")
	}
	if !slices.ContainsFunc(executor.commands, secureRoleProbeCommand) {
		t.Fatal("checker did not issue the pinned, bounded application-role readiness probe")
	}
}

func TestCheckRejectsNonNativeApplicationImage(t *testing.T) {
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:         [][]container{healthyContainers(0)},
		hostArchitecture:  "aarch64",
		imageArchitecture: "amd64",
	}
	err := Check(context.Background(), Options{
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match docker host architecture") {
		t.Fatalf("error = %v, want native-architecture mismatch", err)
	}
}

func TestCheckRejectsOrphanService(t *testing.T) {
	containers := healthyContainers(0)
	containers = append(containers, container{
		ID: "orphan-id", ImageID: "sha256:orphan", Service: "compiler-runner",
		Status: "running", Running: true,
	})
	executor := &fakeExecutor{snapshots: [][]container{containers}}
	err := Check(context.Background(), Options{
		ConfigURL:       "https://etherview.localhost:8080/api/v1/config",
		Timeout:         time.Second,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
	})
	if err == nil || !strings.Contains(err.Error(), `unexpected Compose service "compiler-runner"`) {
		t.Fatalf("error = %v, want orphan service rejection", err)
	}
}

func TestCheckRejectsFailedMigrationImmediately(t *testing.T) {
	containers := healthyContainers(0)
	for index := range containers {
		if containers[index].Service == "migration" {
			containers[index].ExitCode = 17
		}
	}
	executor := &fakeExecutor{snapshots: [][]container{containers}}
	err := Check(context.Background(), Options{
		ConfigURL:       "https://etherview.localhost:8080/api/v1/config",
		Timeout:         time.Second,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
	})
	if err == nil || !strings.Contains(err.Error(), `one-shot service "migration" exited nonzero`) {
		t.Fatalf("error = %v, want failed migration", err)
	}
}

func TestCheckRejectsCompilerCacheLeak(t *testing.T) {
	containers := healthyContainers(0)
	for index := range containers {
		if containers[index].Service == "sync" {
			containers[index].Tmpfs = map[string]string{
				compilerCachePath: "rw,nosuid,nodev,noexec",
			}
		}
	}
	executor := &fakeExecutor{snapshots: [][]container{containers}}
	err := Check(context.Background(), Options{
		ConfigURL:       "https://etherview.localhost:8080/api/v1/config",
		Timeout:         time.Second,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
	})
	if err == nil || !strings.Contains(err.Error(), `"sync" must not receive the compiler cache`) {
		t.Fatalf("error = %v, want compiler-cache scope rejection", err)
	}
}

func TestCheckRejectsRemovedCompilerEnvironment(t *testing.T) {
	containers := healthyContainers(0)
	for index := range containers {
		if containers[index].Service == "api" {
			containers[index].Environment = append(
				containers[index].Environment,
				"ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT=http://legacy",
			)
		}
	}
	executor := &fakeExecutor{snapshots: [][]container{containers}}
	err := Check(context.Background(), Options{
		ConfigURL:       "https://etherview.localhost:8080/api/v1/config",
		Timeout:         time.Second,
		PollInterval:    time.Millisecond,
		StabilityWindow: time.Millisecond,
		Executor:        executor,
	})
	if err == nil || !strings.Contains(err.Error(), "removed compiler configuration") {
		t.Fatalf("error = %v, want removed compiler environment rejection", err)
	}
}

func TestCheckRejectsRestartCountChangeDuringStability(t *testing.T) {
	server := featureServer(t, true, true)
	before := healthyContainers(0)
	after := healthyContainers(0)
	for index := range after {
		if after[index].Service == "api" {
			after[index].RestartCount = 1
		}
	}
	executor := &fakeExecutor{snapshots: [][]container{before, after}}
	err := Check(context.Background(), Options{
		ConfigURL:       server.URL + "/api/v1/config",
		Timeout:         250 * time.Millisecond,
		PollInterval:    time.Millisecond,
		StabilityWindow: 3 * time.Millisecond,
		Executor:        executor,
		HTTPClient:      server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), `service "api" restart count changed`) {
		t.Fatalf("error = %v, want restart-count change", err)
	}
}

func TestCheckRejectsDisabledPublicFeatures(t *testing.T) {
	server := featureServer(t, true, false)
	executor := &fakeExecutor{snapshots: [][]container{healthyContainers(0)}}
	err := Check(context.Background(), Options{
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
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:    [][]container{healthyContainers(0)},
		probeFailsAt: 1,
	}
	err := Check(context.Background(), Options{
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
	server := featureServer(t, true, true)
	executor := &fakeExecutor{
		snapshots:    [][]container{healthyContainers(0)},
		probeFailsAt: 2,
	}
	err := Check(context.Background(), Options{
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

func TestNormalizeArchitecture(t *testing.T) {
	for input, expected := range map[string]string{
		"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64",
	} {
		if actual := normalizeArchitecture(input); actual != expected {
			t.Fatalf("normalizeArchitecture(%q) = %q, want %q", input, actual, expected)
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
	applicationServices := map[string]struct{}{
		"api": {}, "sync": {}, "enrich": {}, "trace": {}, "metadata": {}, "maintenance": {},
	}
	for _, service := range runningServices {
		health := ""
		if service == "postgres" {
			health = "healthy"
		}
		imageID := "sha256:" + service
		environment := []string(nil)
		tmpfs := map[string]string(nil)
		if _, application := applicationServices[service]; application {
			imageID = "sha256:application"
			environment = []string{"ETHERVIEW_ROLES=" + service}
		}
		if service == "api" {
			tmpfs = map[string]string{
				compilerCachePath: "rw,nosuid,nodev,noexec",
			}
			environment = append(
				environment,
				"ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS=true",
			)
		}
		values = append(values, container{
			ID:           service + "-id",
			ImageID:      imageID,
			Service:      service,
			Status:       "running",
			Running:      true,
			Health:       health,
			RestartCount: restartCount,
			Networks: map[string]json.RawMessage{
				"preview_default": json.RawMessage(`{}`),
			},
			Environment: environment,
			Tmpfs:       tmpfs,
		})
	}
	for _, service := range oneShotServices {
		values = append(values, container{
			ID:           service + "-id",
			ImageID:      "sha256:application",
			Service:      service,
			Status:       "exited",
			ExitCode:     0,
			RestartCount: restartCount,
		})
	}
	return values
}

func secureRoleProbeCommand(command testcompose.Command) bool {
	if command.Name != "docker" && command.Name != "/usr/bin/docker" {
		return false
	}
	if len(command.Args) == 0 || command.Args[0] != "run" {
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
