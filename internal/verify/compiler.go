package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Compiler interface {
	Compile(context.Context, Language, string, []byte) ([]byte, error)
	HardIsolated() bool
}

// RuntimeValidator is implemented by compiler backends whose security
// boundary depends on an external runtime. Applications must call it before
// accepting work so a configured-but-unreachable sandbox cannot be advertised
// as hard isolation.
type RuntimeValidator interface {
	ValidateRuntime(context.Context) error
}

type CompilerArtifact struct {
	URL      string
	SHA256   string
	MaxBytes int64
}

type CompilerCache struct {
	Root       string
	Artifacts  map[Language]map[string]CompilerArtifact
	HTTPClient *http.Client
	AllowHTTP  bool
	mu         sync.Mutex
	locks      map[string]*sync.Mutex
}

func (c *CompilerCache) Ensure(ctx context.Context, language Language, version string) (string, error) {
	if c.Root == "" {
		return "", errors.New("compiler cache root is required")
	}
	if !versionPattern.MatchString(version) {
		return "", errors.New("invalid compiler version")
	}
	versions, ok := c.Artifacts[language]
	if !ok {
		return "", fmt.Errorf("language %q is not allowlisted", language)
	}
	artifact, ok := versions[version]
	if !ok {
		return "", fmt.Errorf("compiler %s %s is not allowlisted", language, version)
	}
	if len(artifact.SHA256) != 64 {
		return "", errors.New("compiler artifact SHA-256 is invalid")
	}
	key := string(language) + "-" + version
	lock := c.lock(key)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(c.Root, key+executableSuffix())
	if validFileDigest(path, artifact.SHA256) {
		return path, nil
	}
	parsed, err := url.Parse(artifact.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !(c.AllowHTTP && parsed.Scheme == "http")) {
		return "", errors.New("compiler artifact URL is not allowed")
	}
	maxBytes := artifact.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 200 << 20
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download compiler: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("compiler server returned HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(c.Root, 0o750); err != nil {
		return "", fmt.Errorf("create compiler cache: %w", err)
	}
	temporary, err := os.CreateTemp(c.Root, ".compiler-*")
	if err != nil {
		return "", fmt.Errorf("create compiler temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(response.Body, maxBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	if written > maxBytes {
		return "", errors.New("compiler artifact exceeds size limit")
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, artifact.SHA256) {
		return "", fmt.Errorf("compiler checksum mismatch: got %s", actual)
	}
	if err := os.Chmod(temporaryPath, 0o500); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("install compiler: %w", err)
	}
	return path, nil
}

func (c *CompilerCache) lock(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = make(map[string]*sync.Mutex)
	}
	if c.locks[key] == nil {
		c.locks[key] = &sync.Mutex{}
	}
	return c.locks[key]
}

type ProcessCompiler struct {
	Cache          *CompilerCache
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	Public         bool
}

func (c ProcessCompiler) HardIsolated() bool { return false }

func (c ProcessCompiler) Compile(ctx context.Context, language Language, version string, input []byte) ([]byte, error) {
	if c.Public {
		return nil, ErrSandboxRequired
	}
	if c.Cache == nil {
		return nil, errors.New("compiler cache is required")
	}
	maxInput := c.MaxInputBytes
	if maxInput <= 0 {
		maxInput = 5 << 20
	}
	if len(input) == 0 || len(input) > maxInput {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	path, err := c.Cache.Ensure(ctx, language, version)
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"--standard-json"}
	if language == LanguageVyper {
		args = []string{"--standard-json"}
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = bytes.NewReader(input)
	maxOutput := c.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 64 << 20
	}
	stdout := newLimitedBuffer(maxOutput)
	stderr := newLimitedBuffer(1 << 20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("compiler timed out")
		}
		return nil, fmt.Errorf("compiler failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	return stdout.Bytes(), nil
}

type ContainerCompiler struct {
	Runtime        string
	Images         map[Language]map[string]string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	Memory         string
	CPUs           string
	PIDs           int
}

func (c ContainerCompiler) HardIsolated() bool {
	runtimeName := c.runtimeName()
	if runtimeName != "docker" && runtimeName != "podman" {
		return false
	}
	_, err := exec.LookPath(runtimeName)
	return err == nil
}

// ValidateRuntime checks both the allowlisted executable and its service
// connection. Finding a CLI alone is insufficient: an unreachable daemon
// cannot enforce the network, capability, PID, memory, or filesystem limits.
func (c ContainerCompiler) ValidateRuntime(ctx context.Context) error {
	runtimeName := c.runtimeName()
	if runtimeName != "docker" && runtimeName != "podman" {
		return fmt.Errorf("container runtime %q is not allowlisted", runtimeName)
	}
	path, err := exec.LookPath(runtimeName)
	if err != nil {
		return fmt.Errorf("container runtime %q is unavailable", runtimeName)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, path, "version")
	stdout, stderr := newLimitedBuffer(1<<20), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("container runtime %q readiness check timed out", runtimeName)
		}
		return fmt.Errorf("container runtime %q cannot enforce compiler isolation: %w", runtimeName, err)
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return fmt.Errorf("container runtime %q readiness output exceeded its limit", runtimeName)
	}
	return nil
}

func (c ContainerCompiler) runtimeName() string {
	if c.Runtime == "" {
		return "docker"
	}
	return c.Runtime
}

func (c ContainerCompiler) Compile(ctx context.Context, language Language, version string, input []byte) ([]byte, error) {
	if len(input) == 0 || c.MaxInputBytes > 0 && len(input) > c.MaxInputBytes {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	image := c.Images[language][version]
	if !strings.Contains(image, "@sha256:") {
		return nil, errors.New("compiler container image must be pinned by digest")
	}
	runtimeName := c.runtimeName()
	memory := c.Memory
	if memory == "" {
		memory = "512m"
	}
	cpus := c.CPUs
	if cpus == "" {
		cpus = "1"
	}
	pids := c.PIDs
	if pids <= 0 {
		pids = 64
	}
	args := []string{"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65532:65532", "--memory=" + memory, "--cpus=" + cpus, fmt.Sprintf("--pids-limit=%d", pids), "--tmpfs=/tmp:rw,noexec,nosuid,size=64m", image, "--standard-json"}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, runtimeName, args...)
	command.Stdin = bytes.NewReader(input)
	maxOutput := c.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 64 << 20
	}
	stdout, stderr := newLimitedBuffer(maxOutput), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("sandboxed compiler timed out")
		}
		return nil, fmt.Errorf("sandboxed compiler failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	return stdout.Bytes(), nil
}

func validFileDigest(path, expected string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected)
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return original, nil
	}
	if len(data) > remaining {
		b.exceeded = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return original, nil
}

func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }
func (b *limitedBuffer) Exceeded() bool { return b.exceeded }
