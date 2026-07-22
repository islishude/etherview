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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

type Compiler interface {
	Provenance(Language, string) (CompilerProvenance, error)
	Compile(context.Context, Language, string, []byte) ([]byte, error)
	HardIsolated() bool
}

var (
	// ErrCompilerCleanup means a compiler invocation could not prove that its
	// exact container was force-removed. Workers must stop without terminalizing
	// the leased job.
	ErrCompilerCleanup = errors.New("verification compiler container cleanup failed")
	// ErrCompilerRuntime means the compiler execution boundary panicked. The
	// panic value is intentionally discarded at this stable fatal boundary.
	ErrCompilerRuntime = errors.New("verification compiler runtime invariant failed")
)

type CompilerKind string

const (
	CompilerProcess   CompilerKind = "process"
	CompilerContainer CompilerKind = "container"
)

// CompilerProvenance identifies the exact allowlisted compiler artifact used
// by a worker. Digest is the artifact or container image SHA-256, not a mutable
// version label.
type CompilerProvenance struct {
	Kind         CompilerKind
	Digest       [sha256.Size]byte
	HardIsolated bool
}

func (provenance CompilerProvenance) valid() bool {
	return (provenance.Kind == CompilerProcess || provenance.Kind == CompilerContainer) &&
		provenance.Digest != [sha256.Size]byte{}
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
	Root                       string
	Artifacts                  map[Language]map[string]CompilerArtifact
	Timeout                    time.Duration
	unsafeHTTPClient           *http.Client
	unsafeAllowHTTP            bool
	unsafeAllowPrivateNetworks bool
	resolver                   outboundResolver
	mu                         sync.Mutex
	locks                      map[string]*sync.Mutex
}

func (c *CompilerCache) Ensure(ctx context.Context, language Language, version string) (string, error) {
	versions, ok := c.Artifacts[language]
	if !ok {
		return "", fmt.Errorf("language %q is not allowlisted", language)
	}
	artifact, ok := versions[version]
	if !ok {
		return "", fmt.Errorf("compiler %s %s is not allowlisted", language, version)
	}
	parsed, digest, maximum, err := validateCompilerArtifact(language, version, artifact, c.unsafeAllowHTTP)
	if err != nil {
		return "", err
	}
	key := string(language) + "-" + version
	lock := c.lock(key)
	lock.Lock()
	defer lock.Unlock()

	if err := secureCompilerCacheRoot(c.Root); err != nil {
		return "", err
	}
	path := filepath.Join(c.Root, key+executableSuffix())
	if validCompilerCacheFile(path, digest, maximum) {
		return path, nil
	}
	client := restrictedOutboundClient(c.unsafeHTTPClient, c.Timeout, c.resolver, c.unsafeAllowPrivateNetworks)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errors.New("create compiler artifact request")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("download compiler artifact")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("compiler server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maximum {
		return "", errors.New("compiler artifact exceeds size limit")
	}
	temporary, err := os.CreateTemp(c.Root, ".compiler-*")
	if err != nil {
		return "", errors.New("create compiler temporary file")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(response.Body, maximum+1))
	if copyErr != nil {
		_ = temporary.Close()
		return "", errors.New("read compiler artifact")
	}
	if written > maximum {
		_ = temporary.Close()
		return "", errors.New("compiler artifact exceeds size limit")
	}
	if string(hasher.Sum(nil)) != string(digest[:]) {
		_ = temporary.Close()
		return "", errors.New("compiler checksum mismatch")
	}
	if err := temporary.Chmod(0o500); err != nil {
		_ = temporary.Close()
		return "", errors.New("secure compiler artifact")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", errors.New("sync compiler artifact")
	}
	if err := temporary.Close(); err != nil {
		return "", errors.New("close compiler artifact")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", errors.New("install compiler artifact")
	}
	if !validCompilerCacheFile(path, digest, maximum) {
		return "", errors.New("installed compiler artifact is unsafe")
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

func (c ProcessCompiler) ValidateRuntime(context.Context) error {
	if c.Public {
		return ErrSandboxRequired
	}
	if c.Cache == nil {
		return errors.New("compiler cache is required")
	}
	if err := validateProcessManifest(c.Cache.Artifacts); err != nil {
		return err
	}
	return secureCompilerCacheRoot(c.Cache.Root)
}

func (c ProcessCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	if c.Cache == nil {
		return CompilerProvenance{}, errors.New("compiler cache is required")
	}
	versions, ok := c.Cache.Artifacts[language]
	if !ok {
		return CompilerProvenance{}, fmt.Errorf("language %q is not allowlisted", language)
	}
	artifact, ok := versions[version]
	if !ok {
		return CompilerProvenance{}, fmt.Errorf("compiler %s %s is not allowlisted", language, version)
	}
	_, digest, _, err := validateCompilerArtifact(language, version, artifact, c.Cache.unsafeAllowHTTP)
	if err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{Kind: CompilerProcess, Digest: digest}, nil
}

func (c ProcessCompiler) Compile(ctx context.Context, language Language, version string, input []byte) ([]byte, error) {
	if c.Public {
		return nil, ErrSandboxRequired
	}
	if c.Cache == nil {
		return nil, errors.New("compiler cache is required")
	}
	maxInput := c.MaxInputBytes
	if maxInput <= 0 {
		maxInput = defaultCompilerInputBytes
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
		timeout = defaultCompilerTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"--standard-json"}
	if language == LanguageVyper {
		args = []string{"--standard-json"}
	}
	command := exec.CommandContext(ctx, path, args...)
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(input)
	maxOutput := c.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultCompilerOutputBytes
	}
	stdout := newLimitedBuffer(maxOutput)
	stderr := newLimitedBuffer(1 << 20)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("compiler timed out")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, errors.New("compiler cancelled")
		}
		return nil, errors.New("compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
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

	stateMu         sync.RWMutex
	resolvedRuntime string
	validatedImages map[Language]map[string]string
	cleanupTimeout  time.Duration
	runContainer    func(*exec.Cmd) error
}

func (c *ContainerCompiler) HardIsolated() bool {
	if c == nil {
		return false
	}
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.resolvedRuntime != "" && len(c.validatedImages) > 0
}

func (c *ContainerCompiler) Provenance(language Language, version string) (CompilerProvenance, error) {
	image, ok := c.provenanceImage(language, version)
	if !ok {
		return CompilerProvenance{}, fmt.Errorf("compiler %s %s is not allowlisted", language, version)
	}
	digest, err := parseContainerImage(image)
	if err != nil {
		return CompilerProvenance{}, err
	}
	return CompilerProvenance{
		Kind: CompilerContainer, Digest: digest, HardIsolated: c.HardIsolated(),
	}, nil
}

// ValidateRuntime checks both the allowlisted executable and its service
// connection. Finding a CLI alone is insufficient: an unreachable daemon
// cannot enforce the network, capability, PID, memory, or filesystem limits.
func (c *ContainerCompiler) ValidateRuntime(ctx context.Context) error {
	if c == nil {
		return errors.New("container compiler is required")
	}
	c.stateMu.Lock()
	c.resolvedRuntime = ""
	c.validatedImages = nil
	c.stateMu.Unlock()
	runtimeName := c.runtimeName()
	if runtimeName != "docker" && runtimeName != "podman" {
		return fmt.Errorf("container runtime %q is not allowlisted", runtimeName)
	}
	memory, cpus, pids := c.resources()
	if err := validateContainerResources(memory, cpus, pids); err != nil {
		return err
	}
	images, err := validateContainerManifest(c.Images)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(runtimeName)
	if err != nil {
		return fmt.Errorf("container runtime %q is unavailable", runtimeName)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("container runtime %q is unavailable", runtimeName)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	command := exec.CommandContext(probeCtx, path, "version")
	command.WaitDelay = time.Second
	stdout, stderr := newLimitedBuffer(1<<20), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	cancel()
	if err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("container runtime %q readiness check timed out", runtimeName)
		}
		return fmt.Errorf("container runtime %q cannot enforce compiler isolation", runtimeName)
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return fmt.Errorf("container runtime %q readiness output exceeded its limit", runtimeName)
	}
	for _, language := range sortedCompilerLanguages(images) {
		versions := make([]string, 0, len(images[language]))
		for version := range images[language] {
			versions = append(versions, version)
		}
		sort.Strings(versions)
		for _, version := range versions {
			inspectCtx, inspectCancel := context.WithTimeout(ctx, 10*time.Second)
			command = exec.CommandContext(inspectCtx, path, "image", "inspect", images[language][version])
			command.WaitDelay = time.Second
			stdout, stderr = newLimitedBuffer(1<<20), newLimitedBuffer(1<<20)
			command.Stdout, command.Stderr = stdout, stderr
			err = command.Run()
			inspectCancel()
			if err != nil {
				if errors.Is(inspectCtx.Err(), context.DeadlineExceeded) {
					return errors.New("compiler container image inspection timed out")
				}
				return errors.New("compiler container image is unavailable")
			}
			if stdout.Exceeded() || stderr.Exceeded() {
				return errors.New("compiler container image inspection output exceeded its limit")
			}
		}
	}
	c.stateMu.Lock()
	c.resolvedRuntime = path
	c.validatedImages = images
	c.stateMu.Unlock()
	return nil
}

func (c *ContainerCompiler) runtimeName() string {
	if c.Runtime == "" {
		return "docker"
	}
	return c.Runtime
}

func (c *ContainerCompiler) Compile(ctx context.Context, language Language, version string, input []byte) (result []byte, resultErr error) {
	maxInput := c.MaxInputBytes
	if maxInput <= 0 {
		maxInput = defaultCompilerInputBytes
	}
	if len(input) == 0 || len(input) > maxInput {
		return nil, errors.New("compiler input exceeds configured bounds")
	}
	configuredImage := c.Images[language][version]
	if _, err := parseContainerImage(configuredImage); err != nil {
		return nil, err
	}
	image, ok := c.image(language, version, true)
	if !ok || image != configuredImage {
		return nil, errors.New("container compiler runtime is not validated")
	}
	memory, cpus, pids := c.resources()
	if err := validateContainerResources(memory, cpus, pids); err != nil {
		return nil, err
	}
	runtimePath, ok := c.validatedRuntime()
	if !ok {
		return nil, errors.New("container compiler runtime is not validated")
	}
	containerName, err := randomCompilerContainerName()
	if err != nil {
		return nil, err
	}
	args := []string{
		"run", "--pull=never", "--name=" + containerName,
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65532:65532",
		"--memory=" + memory, "--memory-swap=" + memory, "--cpus=" + cpus,
		fmt.Sprintf("--pids-limit=%d", pids), "--ulimit=nofile=64:64", "--ulimit=core=0",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,mode=0700",
		image, "--standard-json",
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultCompilerTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, runtimePath, args...)
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(input)
	maxOutput := c.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultCompilerOutputBytes
	}
	stdout, stderr := newLimitedBuffer(maxOutput), newLimitedBuffer(1<<20)
	command.Stdout, command.Stderr = stdout, stderr
	// Register cleanup before crossing the runtime boundary. The deferred
	// cleanup controls every normal return and every panic once a container may
	// have started. Cleanup failure takes priority over all other outcomes.
	defer func() {
		panicked := recover() != nil
		if cleanupErr := c.removeContainerSafely(runtimePath, containerName); cleanupErr != nil {
			result, resultErr = nil, ErrCompilerCleanup
			return
		}
		if panicked {
			result, resultErr = nil, ErrCompilerRuntime
		}
	}()
	runErr := c.runContainerCommand(command)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("sandboxed compiler timed out")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, errors.New("sandboxed compiler cancelled")
		}
		return nil, errors.New("sandboxed compiler failed")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("compiler output exceeds size limit")
	}
	return stdout.Bytes(), nil
}

func (c *ContainerCompiler) runContainerCommand(command *exec.Cmd) error {
	if c.runContainer != nil {
		return c.runContainer(command)
	}
	return command.Run()
}

func (c *ContainerCompiler) image(language Language, version string, validated bool) (string, bool) {
	if c == nil {
		return "", false
	}
	if !validated {
		image, ok := c.Images[language][version]
		return image, ok
	}
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	image, ok := c.validatedImages[language][version]
	return image, ok
}

func (c *ContainerCompiler) provenanceImage(language Language, version string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.stateMu.RLock()
	image, ok := c.validatedImages[language][version]
	validated := c.resolvedRuntime != "" && len(c.validatedImages) > 0
	c.stateMu.RUnlock()
	if validated {
		return image, ok
	}
	image, ok = c.Images[language][version]
	return image, ok
}

func (c *ContainerCompiler) validatedRuntime() (string, bool) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.resolvedRuntime, c.resolvedRuntime != ""
}

func (c *ContainerCompiler) resources() (string, string, int) {
	memory, cpus, pids := c.Memory, c.CPUs, c.PIDs
	if memory == "" {
		memory = "512m"
	}
	if cpus == "" {
		cpus = "1"
	}
	if pids <= 0 {
		pids = 64
	}
	return memory, cpus, pids
}

func (c *ContainerCompiler) removeContainer(runtimePath, name string) error {
	if !validCompilerContainerName(name) {
		return ErrCompilerCleanup
	}
	timeout := c.cleanupTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(cleanupCtx, runtimePath, "rm", "-f", name)
	command.WaitDelay = time.Second
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return ErrCompilerCleanup
	}
	return nil
}

func (c *ContainerCompiler) removeContainerSafely(runtimePath, name string) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrCompilerCleanup
		}
	}()
	return c.removeContainer(runtimePath, name)
}

func decodeCompilerDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != sha256.Size*2 {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	copy(digest[:], decoded)
	if digest == [sha256.Size]byte{} {
		return digest, errors.New("compiler artifact SHA-256 is invalid")
	}
	return digest, nil
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
