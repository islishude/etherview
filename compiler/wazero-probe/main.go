// P30-T100: diagnostic runner, deliberately not a production compiler executor.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/table"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	if len(os.Args) < 3 {
		panic("usage: probe solc DIR | python ROOT [python arguments]")
	}
	timeout := 60 * time.Second
	if value := os.Getenv("PROBE_TIMEOUT"); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		must(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(filepath.Dir(os.Args[2]), "compiled-cache"))
	must(err)
	defer cache.Close(ctx)
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(cache).WithMemoryLimitPages(8192).WithCloseOnContextDone(true))
	defer r.Close(ctx)
	mode, root := os.Args[1], os.Args[2]
	if mode == "python" {
		_, err := wasi_snapshot_preview1.Instantiate(ctx, r)
		must(err)
		b, err := os.ReadFile(filepath.Join(root, "python.wasm"))
		must(err)
		cfg := wazero.NewModuleConfig().WithArgs(append([]string{"python"}, os.Args[3:]...)...).WithStdout(os.Stdout).WithStderr(os.Stderr).WithStdin(os.Stdin).WithEnv("PYTHONPATH", "/probe/site").WithFSConfig(wazero.NewFSConfig().WithReadOnlyDirMount(root, "/").WithReadOnlyDirMount(filepath.Join(filepath.Dir(root), "python-probe"), "/probe")).WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader)
		_, err = r.InstantiateWithConfig(ctx, b, cfg)
		must(err)
		return
	}
	var mapping struct {
		Imports map[string]string
		Exports map[string]string
	}
	b, err := os.ReadFile(filepath.Join(root, "solc.json"))
	must(err)
	must(json.Unmarshal(b, &mapping))
	b, err = os.ReadFile(filepath.Join(root, "solc.wasm"))
	must(err)
	compiled, err := r.CompileModule(ctx, b)
	must(err)
	var temp uint64
	builders := map[string]wazero.HostModuleBuilder{}
	call := func(ctx context.Context, m api.Module, name string, args ...uint64) []uint64 {
		f := m.ExportedFunction(mapping.Exports[name])
		if f == nil {
			panic("missing export " + name)
		}
		v, e := f.Call(ctx, args...)
		must(e)
		return v
	}
	for _, def := range compiled.ImportedFunctions() {
		mod, name, _ := def.Import()
		semantic := mapping.Imports[name]
		params, results := def.ParamTypes(), def.ResultTypes()
		if builders[mod] == nil {
			builders[mod] = r.NewHostModuleBuilder(mod)
		}
		builders[mod].NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, s []uint64) {
			switch {
			case strings.HasPrefix(semantic, "invoke_"):
				if strings.Contains(strings.TrimPrefix(semantic, "invoke_"), "j") {
					v := call(ctx, m, "dynCall_"+strings.TrimPrefix(semantic, "invoke_"), s[:len(params)]...)
					copy(s, v)
					return
				}
				f := table.LookupFunction(m, 0, uint32(s[0]), params[1:], results)
				v, e := f.Call(ctx, s[1:len(params)]...)
				must(e)
				copy(s, v)
			case semantic == "_getTempRet0":
				s[0] = temp
			case semantic == "_setTempRet0":
				temp = s[0]
			case semantic == "_emscripten_resize_heap":
				pages, _ := m.Memory().Grow(0)
				want := (uint32(s[0]) + 65535) / 65536
				ok := true
				if want > pages {
					_, ok = m.Memory().Grow(want - pages)
				}
				s[0] = 0
				if ok {
					s[0] = 1
				}
			case semantic == "_emscripten_memcpy_big":
				dst, src, n := uint32(s[0]), uint32(s[1]), uint32(s[2])
				v, ok := m.Memory().Read(src, n)
				if !ok || !m.Memory().Write(dst, v) {
					panic("memory bounds")
				}
			case semantic == "_environ_sizes_get":
				m.Memory().WriteUint32Le(uint32(s[0]), 0)
				m.Memory().WriteUint32Le(uint32(s[1]), 0)
				s[0] = 0
			case semantic == "_environ_get":
				s[0] = 0
			default:
				panic("unimplemented host import: " + semantic)
			}
		}), params, results).Export(name)
	}
	for _, b := range builders {
		_, err = b.Instantiate(ctx)
		must(err)
	}
	m, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions())
	must(err)
	call(ctx, m, "___wasm_call_ctors")
	str := func(p uint64) string {
		v, ok := m.Memory().Read(uint32(p), m.Memory().Size()-uint32(p))
		if !ok {
			panic("pointer")
		}
		for i, c := range v {
			if c == 0 {
				return string(v[:i])
			}
		}
		panic("unterminated string")
	}
	fmt.Fprintln(os.Stderr, "compiler:", str(call(ctx, m, "_solidity_version")[0]))
	input, err := io.ReadAll(io.LimitReader(os.Stdin, (8<<20)+1))
	must(err)
	if len(input) > 8<<20 {
		panic("input exceeds probe limit")
	}
	p := call(ctx, m, "_solidity_alloc", uint64(len(input)+1))[0]
	if !m.Memory().Write(uint32(p), append(input, 0)) {
		panic("input bounds")
	}
	output := call(ctx, m, "_solidity_compile", p, 0, 0)[0]
	fmt.Println(str(output))
}
