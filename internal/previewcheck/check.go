// Package previewcheck validates the live Preview Compose topology without
// relying on Docker Compose's --wait result. It inspects the actual containers,
// proves the production application image matches the Docker host architecture,
// checks the public HTTPS feature contract, and observes a final stability
// window.
package previewcheck

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/testcompose"
)

const (
	DefaultTimeout         = 3 * time.Minute
	DefaultPollInterval    = 2 * time.Second
	DefaultStabilityWindow = 15 * time.Second
	maxConfigBodyBytes     = 1 << 20
	roleProbeTimeout       = 20 * time.Second
	roleProbeImage         = "docker:29.6.2-cli@sha256:be132a9f282288de4afaf63379dff75711fda0147c6b72a9df44e51841402144"
	compilerCachePath      = "/var/lib/etherview/compilers"
	compilerLibraryPath    = "/opt/etherview/compiler/lib"
	expectedNodeVersion    = "v26.5.0"
)

var (
	runningServices = []string{
		"api",
		"sync",
		"enrich",
		"trace",
		"metadata",
		"maintenance",
		"reth",
		"postgres",
	}
	oneShotServices = []string{
		"migration",
	}
)

// Executor is the process boundary used by Check. Implementations must return
// stdout only; stderr can contain local daemon details and must not be surfaced
// as checker diagnostics.
type Executor interface {
	Run(context.Context, testcompose.Command) ([]byte, error)
}

// OSExecutor runs local Compose and Docker commands while keeping stderr out
// of returned diagnostics.
type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, command testcompose.Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = command.Env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

type Options struct {
	Root               string
	ProjectName        string
	DockerCommand      string
	ConfigURL          string
	CAFile             string
	InsecureSkipVerify bool
	Timeout            time.Duration
	PollInterval       time.Duration
	StabilityWindow    time.Duration
	Executor           Executor
	HTTPClient         *http.Client
}

type runtime struct {
	options Options
	client  *http.Client
}

type snapshot struct {
	containers map[string]container
}

type container struct {
	ID           string                     `json:"id"`
	ImageID      string                     `json:"image_id"`
	Service      string                     `json:"service"`
	Status       string                     `json:"status"`
	Running      bool                       `json:"running"`
	Restarting   bool                       `json:"restarting"`
	ExitCode     int                        `json:"exit_code"`
	Health       string                     `json:"health"`
	RestartCount int                        `json:"restart_count"`
	Networks     map[string]json.RawMessage `json:"networks"`
	Environment  []string                   `json:"environment"`
	Tmpfs        map[string]string          `json:"tmpfs"`
}

type publicConfigResponse struct {
	Data struct {
		Features map[string]bool `json:"features"`
	} `json:"data"`
}

type pendingError struct {
	err error
}

func (e *pendingError) Error() string { return e.err.Error() }
func (e *pendingError) Unwrap() error { return e.err }

// Check waits for the complete Preview topology, then proves it remains stable.
// A failed one-shot service, a public feature mismatch, or a changed container
// identity/restart count is terminal and returns immediately.
func Check(ctx context.Context, options Options) error {
	instance, err := newRuntime(options)
	if err != nil {
		return err
	}
	runContext, cancel := context.WithTimeout(ctx, instance.options.Timeout)
	defer cancel()

	baseline, err := instance.waitReady(runContext)
	if err != nil {
		return err
	}
	if err := instance.observeStability(runContext, baseline); err != nil {
		return err
	}
	return nil
}

func newRuntime(options Options) (*runtime, error) {
	if strings.TrimSpace(options.Root) == "" {
		options.Root = "."
	}
	if strings.TrimSpace(options.ProjectName) == "" {
		options.ProjectName = "etherview-preview"
	}
	if strings.TrimSpace(options.DockerCommand) == "" {
		options.DockerCommand = "docker"
	}
	if strings.TrimSpace(options.ConfigURL) == "" {
		options.ConfigURL = "https://etherview.localhost:8080/api/v1/config"
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.PollInterval == 0 {
		options.PollInterval = DefaultPollInterval
	}
	if options.StabilityWindow == 0 {
		options.StabilityWindow = DefaultStabilityWindow
	}
	if options.Executor == nil {
		options.Executor = OSExecutor{}
	}
	if options.Timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}
	if options.PollInterval <= 0 {
		return nil, errors.New("poll interval must be greater than zero")
	}
	if options.StabilityWindow <= 0 || options.StabilityWindow >= options.Timeout {
		return nil, errors.New("stability window must be greater than zero and shorter than the timeout")
	}
	parsedURL, err := url.Parse(options.ConfigURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" ||
		parsedURL.Path != "/api/v1/config" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, errors.New("config URL must be an absolute HTTPS /api/v1/config URL without query or fragment")
	}
	if options.CAFile != "" && options.InsecureSkipVerify {
		return nil, errors.New("CA file and insecure TLS mode are mutually exclusive")
	}

	client := options.HTTPClient
	if client == nil {
		client, err = previewHTTPClient(options.Root, options.CAFile, options.InsecureSkipVerify)
		if err != nil {
			return nil, err
		}
	}
	return &runtime{options: options, client: client}, nil
}

func previewHTTPClient(root, caFile string, insecure bool) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecure {
		// The caller must opt into this explicitly for a local Preview
		// certificate. It is never selected by default.
		tlsConfig.InsecureSkipVerify = true //nolint:gosec
	}
	if caFile != "" {
		if !filepath.IsAbs(caFile) {
			caFile = filepath.Join(root, caFile)
		}
		pemBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Preview CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("preview CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func (r *runtime) waitReady(ctx context.Context) (snapshot, error) {
	var lastPending error
	for {
		current, err := r.inspect(ctx)
		if err == nil {
			err = assess(current)
		}
		if err == nil {
			err = r.verifyCompilerRuntime(ctx, current)
		}
		if err == nil {
			if probeErr := r.verifyRoleReadiness(ctx, current); probeErr != nil {
				err = &pendingError{err: probeErr}
			}
		}
		if err == nil {
			err = r.verifyPublicConfig(ctx)
		}
		if err == nil {
			return current, nil
		}
		var pending *pendingError
		if !errors.As(err, &pending) {
			return snapshot{}, err
		}
		lastPending = err
		if err := waitInterval(ctx, r.options.PollInterval); err != nil {
			if lastPending != nil {
				return snapshot{}, fmt.Errorf("preview did not become ready before timeout: %w", lastPending)
			}
			return snapshot{}, fmt.Errorf("preview readiness wait: %w", err)
		}
	}
}

func (r *runtime) observeStability(ctx context.Context, baseline snapshot) error {
	stableUntil := time.Now().Add(r.options.StabilityWindow)
	for {
		delay := min(r.options.PollInterval, time.Until(stableUntil))
		if delay > 0 {
			if err := waitInterval(ctx, delay); err != nil {
				return fmt.Errorf("preview stability wait: %w", err)
			}
		}

		current, err := r.inspect(ctx)
		if err != nil {
			return fmt.Errorf("preview lost stability: %w", err)
		}
		if err := assess(current); err != nil {
			return fmt.Errorf("preview lost stability: %w", err)
		}
		if err := compareSnapshots(baseline, current); err != nil {
			return err
		}
		if err := r.verifyRoleReadiness(ctx, current); err != nil {
			return fmt.Errorf("preview role readiness failed during the stability window: %w", err)
		}
		if time.Now().Before(stableUntil) {
			continue
		}
		if err := r.verifyCompilerRuntime(ctx, current); err != nil {
			return fmt.Errorf("preview lost native compiler runtime: %w", err)
		}
		if err := r.verifyPublicConfig(ctx); err != nil {
			return fmt.Errorf("preview lost public feature contract: %w", err)
		}
		return nil
	}
}

func (r *runtime) inspect(ctx context.Context) (snapshot, error) {
	output, err := r.options.Executor.Run(ctx, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: []string{
			"container", "ls", "--all", "--quiet", "--filter",
			"label=com.docker.compose.project=" + r.options.ProjectName,
		},
		Dir: r.options.Root,
	})
	if err != nil {
		return snapshot{}, &pendingError{err: errors.New("docker compose project enumeration failed")}
	}
	ids := strings.Fields(string(output))
	if len(ids) == 0 {
		return snapshot{}, &pendingError{err: errors.New("compose project has no containers")}
	}

	const inspectFormat = `{"id":{{json .Id}},"image_id":{{json .Image}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"status":{{json .State.Status}},"running":{{.State.Running}},"restarting":{{.State.Restarting}},"exit_code":{{.State.ExitCode}},"health":{{with (index .State "Health")}}{{json .Status}}{{else}}""{{end}},"restart_count":{{.RestartCount}},"networks":{{json .NetworkSettings.Networks}},"environment":{{json .Config.Env}},"tmpfs":{{json (index .HostConfig "Tmpfs")}}}`
	arguments := []string{"inspect", "--format", inspectFormat}
	arguments = append(arguments, ids...)
	output, err = r.options.Executor.Run(ctx, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: arguments,
		Dir:  r.options.Root,
	})
	if err != nil {
		return snapshot{}, &pendingError{err: errors.New("docker container inspection failed")}
	}
	containers := make(map[string]container, len(ids))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for decoder.More() {
		var value container
		if err := decoder.Decode(&value); err != nil {
			return snapshot{}, errors.New("docker container inspection returned invalid JSON")
		}
		if value.Service == "" || value.ID == "" || value.ImageID == "" {
			return snapshot{}, errors.New("docker container inspection omitted Compose identity")
		}
		if _, exists := containers[value.Service]; exists {
			return snapshot{}, fmt.Errorf("compose service %q has more than one container", value.Service)
		}
		containers[value.Service] = value
	}
	if len(containers) == 0 {
		return snapshot{}, errors.New("docker container inspection returned no records")
	}
	return snapshot{containers: containers}, nil
}

func assess(current snapshot) error {
	expected := make(map[string]struct{}, len(runningServices)+len(oneShotServices))
	for _, service := range runningServices {
		expected[service] = struct{}{}
	}
	for _, service := range oneShotServices {
		expected[service] = struct{}{}
	}
	for service := range current.containers {
		if _, ok := expected[service]; !ok {
			return fmt.Errorf("unexpected Compose service %q is still present", service)
		}
	}
	for _, service := range runningServices {
		value, exists := current.containers[service]
		if !exists {
			return &pendingError{err: fmt.Errorf("required service %q is absent", service)}
		}
		if !value.Running || value.Restarting || value.Status != "running" {
			return &pendingError{err: fmt.Errorf("required service %q is not running", service)}
		}
		if value.Health != "" && value.Health != "healthy" {
			return &pendingError{err: fmt.Errorf("required service %q is not healthy", service)}
		}
	}
	for _, service := range oneShotServices {
		value, exists := current.containers[service]
		if !exists {
			return &pendingError{err: fmt.Errorf("required one-shot service %q is absent", service)}
		}
		if value.Status == "exited" {
			if value.ExitCode != 0 {
				return fmt.Errorf("required one-shot service %q exited nonzero", service)
			}
			continue
		}
		if value.Running || value.Status == "created" {
			return &pendingError{err: fmt.Errorf("required one-shot service %q has not completed", service)}
		}
		return fmt.Errorf("required one-shot service %q did not exit successfully", service)
	}
	return nil
}

func (r *runtime) verifyCompilerRuntime(ctx context.Context, current snapshot) error {
	api, exists := current.containers["api"]
	if !exists {
		return &pendingError{err: errors.New("API service is absent")}
	}
	for _, service := range []string{"api", "sync", "enrich", "trace", "metadata", "maintenance"} {
		value, exists := current.containers[service]
		if !exists {
			return &pendingError{err: fmt.Errorf("required service %q is absent", service)}
		}
		if value.ImageID != api.ImageID {
			return fmt.Errorf("application service %q does not use the API production image", service)
		}
		_, hasCompilerCache := value.Tmpfs[compilerCachePath]
		if service == "api" && !hasCompilerCache {
			return errors.New("API must receive exactly one private compiler cache")
		}
		if service != "api" && hasCompilerCache {
			return fmt.Errorf("service %q must not receive the compiler cache", service)
		}
		unsafeDownloadException := false
		for _, entry := range value.Environment {
			key, environmentValue, _ := strings.Cut(entry, "=")
			switch key {
			case "ETHERVIEW_COMPILER_SANDBOX",
				"ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT",
				"ETHERVIEW_VERIFICATION_RUNNER_IMAGE":
				return fmt.Errorf("service %q still receives removed compiler configuration", service)
			}
			if key == "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS" {
				if service != "api" || environmentValue != "true" {
					return fmt.Errorf("service %q has an invalid compiler download exception", service)
				}
				unsafeDownloadException = true
			}
		}
		if service == "api" && !unsafeDownloadException {
			return errors.New("API must receive the scoped Preview compiler download exception")
		}
	}

	imageArchitecture, err := r.options.Executor.Run(ctx, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: []string{
			"exec", "--env", "LD_LIBRARY_PATH=" + compilerLibraryPath,
			api.ID, "/usr/local/bin/node", "--print", "process.arch",
		},
		Dir: r.options.Root,
	})
	if err != nil {
		return &pendingError{err: errors.New("application runtime architecture is unavailable")}
	}
	hostArchitecture, err := r.options.Executor.Run(ctx, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: []string{"info", "--format", "{{.Architecture}}"},
		Dir:  r.options.Root,
	})
	if err != nil {
		return &pendingError{err: errors.New("docker host architecture is unavailable")}
	}
	imageArch := normalizeArchitecture(string(imageArchitecture))
	hostArch := normalizeArchitecture(string(hostArchitecture))
	if imageArch == "" || hostArch == "" || imageArch != hostArch {
		return fmt.Errorf(
			"preview application image architecture %q does not match docker host architecture %q",
			imageArch, hostArch,
		)
	}
	nodeVersion, err := r.options.Executor.Run(ctx, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: []string{
			"exec", "--env", "LD_LIBRARY_PATH=" + compilerLibraryPath,
			api.ID, "/usr/local/bin/node", "--version",
		},
		Dir: r.options.Root,
	})
	if err != nil {
		return &pendingError{err: errors.New("preview Node runtime is unavailable")}
	}
	if strings.TrimSpace(string(nodeVersion)) != expectedNodeVersion {
		return errors.New("preview Node runtime version is invalid")
	}
	return nil
}

func normalizeArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x86_64", "x86-64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (r *runtime) verifyRoleReadiness(ctx context.Context, current snapshot) error {
	network, err := sharedApplicationNetwork(current)
	if err != nil {
		return err
	}
	probeContext, cancel := context.WithTimeout(ctx, roleProbeTimeout)
	defer cancel()
	const script = `for role in api sync enrich trace metadata maintenance; do
	wget -q -T 2 -O /dev/null "http://${role}:9090/health/ready"
done`
	_, err = r.options.Executor.Run(probeContext, testcompose.Command{
		Name: r.options.DockerCommand,
		Args: []string{
			"run",
			"--rm",
			"--pull=never",
			"--network=" + network,
			"--read-only",
			"--user=65532:65532",
			"--cap-drop=ALL",
			"--security-opt=no-new-privileges",
			"--pids-limit=32",
			"--memory=32m",
			"--memory-swap=32m",
			"--cpus=0.25",
			roleProbeImage,
			"sh",
			"-ec",
			script,
		},
		Dir: r.options.Root,
	})
	if err != nil {
		return errors.New("one or more application roles are not ready")
	}
	return nil
}

func sharedApplicationNetwork(current snapshot) (string, error) {
	services := []string{"api", "sync", "enrich", "trace", "metadata", "maintenance", "reth", "postgres"}
	api, exists := current.containers["api"]
	if !exists || len(api.Networks) == 0 {
		return "", errors.New("API service has no inspected application network")
	}
	candidates := make(map[string]struct{}, len(api.Networks))
	for network := range api.Networks {
		candidates[network] = struct{}{}
	}
	for _, service := range services[1:] {
		value, exists := current.containers[service]
		if !exists {
			return "", fmt.Errorf("required service %q is absent while resolving the application network", service)
		}
		for candidate := range candidates {
			if _, connected := value.Networks[candidate]; !connected {
				delete(candidates, candidate)
			}
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("expected one shared application network, found %d", len(candidates))
	}
	for network := range candidates {
		return network, nil
	}
	panic("unreachable")
}

func (r *runtime) verifyPublicConfig(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.options.ConfigURL, nil)
	if err != nil {
		return err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return &pendingError{err: errors.New("preview HTTPS config endpoint is unavailable")}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return &pendingError{err: fmt.Errorf("preview HTTPS config endpoint returned status %d", response.StatusCode)}
	}
	var payload publicConfigResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxConfigBodyBytes+1))
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("preview HTTPS config endpoint returned invalid JSON")
	}
	if !payload.Data.Features["verification"] {
		return errors.New("preview public config does not enable verification")
	}
	if !payload.Data.Features["nft_metadata"] {
		return errors.New("preview public config does not enable NFT metadata")
	}
	return nil
}

func compareSnapshots(baseline, current snapshot) error {
	for _, service := range append(append([]string(nil), runningServices...), oneShotServices...) {
		before := baseline.containers[service]
		after, exists := current.containers[service]
		if !exists {
			return fmt.Errorf("preview service %q disappeared during the stability window", service)
		}
		if before.ID != after.ID {
			return fmt.Errorf("preview service %q was replaced during the stability window", service)
		}
		if before.RestartCount != after.RestartCount {
			return fmt.Errorf("preview service %q restart count changed during the stability window", service)
		}
	}
	return nil
}

func waitInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
