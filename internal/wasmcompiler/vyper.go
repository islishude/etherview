package wasmcompiler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/compilerbundle"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func CompileVyper(ctx context.Context, root string, input []byte, maxInput, maxOutput int64, selftest bool) (result []byte, err error) {
	ctx = compilationContext(ctx)
	executable, err := os.Executable()
	if err != nil {
		return nil, ErrExecution
	}
	actual, err := os.Stat(executable)
	if err != nil {
		return nil, ErrExecution
	}
	selected, err := os.Stat(filepath.Join(root, filepath.Base(executable)))
	if err != nil || !os.SameFile(actual, selected) {
		return nil, ErrArtifact
	}
	identity, err := compilerbundle.Validate(filepath.Join(root, filepath.Base(executable)))
	if err != nil {
		return nil, ErrArtifact
	}
	binary, err := compilerbundle.ReadFile(identity, "python.wasm")
	if err != nil {
		return nil, ErrArtifact
	}
	archive, err := compilerbundle.ReadFile(identity, "python.zip")
	if err != nil {
		return nil, ErrArtifact
	}
	library, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, ErrArtifact
	}
	if err = validatePythonArchive(library); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(8192).WithCloseOnContextDone(true))
	defer func() {
		if e := r.Close(context.Background()); e != nil {
			result = nil
			err = ErrExecution
		}
	}()
	if _, err = wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return nil, ErrExecution
	}
	if err = InstantiateKeccak(ctx, r); err != nil {
		return nil, ErrExecution
	}
	args := []string{"/runtime/python.wasm", "-S", "-B", "/runtime/entry.py", "--compile", strconv.FormatInt(maxInput, 10), strconv.FormatInt(maxOutput, 10)}
	if selftest {
		args = args[:4]
		args = append(args, "--self-test")
	}
	stdout := &guestOutput{limit: maxOutput, cancel: cancel}
	stderr := &guestOutput{limit: 1 << 20, cancel: cancel}
	cfg := wazero.NewModuleConfig().WithArgs(args...).WithFSConfig(wazero.NewFSConfig().WithFSMount(library, "/runtime")).WithStdin(bytes.NewReader(input)).WithStdout(stdout).WithStderr(stderr).WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader).WithEnv("PYTHONHOME", "/runtime").WithEnv("PYTHONPATH", "/runtime/site-packages:/runtime").WithEnv("PYTHONHASHSEED", "0").WithEnv("PYTHONDONTWRITEBYTECODE", "1")
	_, err = r.InstantiateWithConfig(ctx, binary, cfg)
	if stdout.exceeded || stderr.exceeded {
		return nil, ErrOutputLimit
	}
	if err != nil || ctx.Err() != nil {
		return nil, ErrExecution
	}
	return bytes.Clone(stdout.buffer.Bytes()), nil
}
func validatePythonArchive(reader *zip.Reader) error {
	if len(reader.File) == 0 || len(reader.File) > 8192 {
		return ErrArtifact
	}
	total := uint64(0)
	seen := map[string]bool{}
	for _, file := range reader.File {
		name := strings.TrimSuffix(file.Name, "/")
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || file.Mode()&os.ModeSymlink != 0 || seen[name] {
			return ErrArtifact
		}
		seen[name] = true
		total += file.UncompressedSize64
		if file.UncompressedSize64 > 64<<20 || total > 256<<20 {
			return ErrArtifact
		}
	}
	if !seen["entry.py"] || !seen["compiler_crypto.py"] || !seen["site-packages/vyper/__init__.py"] {
		return ErrArtifact
	}
	return nil
}

type guestOutput struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
	cancel   context.CancelFunc
}

func (b *guestOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > b.limit-int64(b.buffer.Len()) {
		b.exceeded = true
		b.cancel()
		return 0, ErrOutputLimit
	}
	return b.buffer.Write(p)
}

var _ io.Writer = (*guestOutput)(nil)
