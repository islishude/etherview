package wasmcompiler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/islishude/etherview/internal/compilerbundle"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const ExecutorKind = "etherview_wazero_v1"
const ExecutionPolicy = "wasm_subprocess_v1"
const WazeroVersion = "v1.12.0"
const SelfTestSchema = "etherview-wazero-self-test-v1"

var ErrInvocation = errors.New("invalid compiler invocation")
var ErrInputLimit = errors.New("compiler input exceeds limit")
var ErrOutputLimit = errors.New("compiler output exceeds limit")

// Run accepts only a fixed self-test or compiler protocol. It exposes no general
// script, filesystem, environment, shell or WASI command-line interface.
// --compile solc ARTIFACT VERSION SHA256 MAX_INPUT MAX_OUTPUT TIMEOUT_MS
// Standard JSON is supplied on stdin and returned on stdout.
func Run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 1 && args[0] == "--self-test" {
		if err := selfTest(ctx); err != nil {
			return err
		}
		if err := json.NewEncoder(output).Encode(struct {
			Schema      string `json:"schema"`
			Wazero      string `json:"wazero"`
			MemoryPages int    `json:"memory_pages"`
		}{SelfTestSchema, WazeroVersion, 8192}); err != nil {
			return ErrExecution
		}
		return nil
	}
	if len(args) == 2 && args[0] == "--self-test" && args[1] == "vyper" {
		executable, err := os.Executable()
		if err != nil {
			return ErrExecution
		}
		result, err := CompileVyper(ctx, filepath.Dir(executable), nil, 5<<20, 1<<20, true)
		if err != nil {
			return publicError(err)
		}
		if _, err = output.Write(result); err != nil {
			return ErrExecution
		}
		return nil
	}
	if len(args) != 8 || args[0] != "--compile" || (args[1] != "solc" && args[1] != "vyper") {
		return ErrInvocation
	}
	path := args[2]
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvocation
	}
	if len(args[3]) == 0 || len(args[3]) > 128 {
		return ErrInvocation
	}
	digest, err := hex.DecodeString(args[4])
	if err != nil || len(digest) != 32 {
		return ErrInvocation
	}
	maxInput, err := protocolLimit(args[5], math.MaxUint32-1)
	if err != nil {
		return err
	}
	maxOutput, err := protocolLimit(args[6], math.MaxUint32-1)
	if err != nil {
		return err
	}
	timeout, err := protocolLimit(args[7], int64(time.Duration(math.MaxInt64)/time.Millisecond))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	request, err := io.ReadAll(io.LimitReader(input, maxInput+1))
	if err != nil {
		return ErrInvocation
	}
	if int64(len(request)) > maxInput {
		return ErrInputLimit
	}
	if args[1] == "vyper" {
		if args[3] != compilerbundle.VyperVersion || args[4] != compilerbundle.VyperWheelSHA256 {
			return ErrVersion
		}
		result, err := CompileVyper(ctx, path, request, maxInput, maxOutput, false)
		if err != nil {
			return publicError(err)
		}
		if !json.Valid(result) {
			return ErrExecution
		}
		if _, err = output.Write(result); err != nil {
			return ErrExecution
		}
		return nil
	}
	raw, err := readArtifact(path)
	if err != nil {
		return ErrArtifact
	}
	artifact, err := Extract(ctx, raw, [32]byte(digest))
	if err != nil {
		return publicError(err)
	}
	result, err := CompileSolc(ctx, artifact, args[3], request, uint32(maxOutput))
	if err != nil {
		return publicError(err)
	}
	if int64(len(result)) > maxOutput {
		return ErrOutputLimit
	}
	if !json.Valid(result) {
		return ErrExecution
	}
	if _, err = output.Write(result); err != nil {
		return ErrExecution
	}
	return nil
}
func protocolLimit(raw string, max int64) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > max || strconv.FormatInt(value, 10) != raw {
		return 0, ErrInvocation
	}
	return value, nil
}
func readArtifact(path string) (raw []byte, err error) {
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > MaxArtifactBytes {
		return nil, ErrArtifact
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrArtifact
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			raw = nil
			err = ErrArtifact
		}
	}()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(stat, opened) {
		return nil, ErrArtifact
	}
	raw, err = io.ReadAll(io.LimitReader(file, MaxArtifactBytes+1))
	if err != nil || len(raw) > MaxArtifactBytes {
		return nil, ErrArtifact
	}
	return raw, nil
}
func publicError(err error) error {
	for _, known := range []error{ErrArtifact, ErrUnsupported, ErrVersion, ErrInputLimit, ErrOutputLimit} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrExecution
}
