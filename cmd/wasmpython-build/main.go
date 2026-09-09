// Command wasmpython-build is a build-only CPython HOSTRUNNER. It is never copied
// into production, whose etherview-wasm exposes only fixed compiler protocols.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/islishude/etherview/internal/wasmcompiler"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"os"
	"path/filepath"
	"time"
)

func run() error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: wasmpython-build SOURCE_ROOT PYTHON_WASM [PYTHON_ARGS]")
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		return err
	}
	binary, err := filepath.Abs(os.Args[2])
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, binary)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(8192).WithCloseOnContextDone(true))
	defer func() { _ = r.Close(context.Background()) }()
	if _, err = wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return err
	}
	if err = wasmcompiler.InstantiateKeccak(ctx, r); err != nil {
		return err
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	cfg := wazero.NewModuleConfig().WithArgs(append([]string{"/" + filepath.ToSlash(rel)}, os.Args[3:]...)...).WithFSConfig(wazero.NewFSConfig().WithDirMount(root, "/")).WithStdout(os.Stdout).WithStderr(os.Stderr).WithStdin(os.Stdin).WithRandSource(rand.Reader).WithSysWalltime().WithSysNanotime().WithEnv("PYTHONPATH", "/cross-build/wasm32-wasip1/build/lib.wasi-wasm32-3.13").WithEnv("PYTHONHASHSEED", "0")
	_, err = r.InstantiateWithConfig(ctx, raw, cfg)
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
