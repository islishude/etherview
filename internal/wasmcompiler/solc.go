package wasmcompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

var ErrExecution = errors.New("compiler execution failed")
var ErrVersion = errors.New("compiler version mismatch")

type solcState struct {
	artifact         *Artifact
	guest            api.Module
	indirectBridge   api.Module
	temp             uint64
	last             uint32
	caught           []uint32
	exceptions       map[uint32]*exceptionInfo
	uncaught         uint32
	callback         uint32
	maxOutput        uint32
	diagnostics      int
	modernExceptions bool
	alarmHandler     uint32
}

func (s *solcState) function(name string) api.Function {
	key := s.artifact.Exports[name]
	if key == "" {
		key = name
	}
	f := s.guest.ExportedFunction(key)
	if f == nil {
		panic(ErrUnsupported)
	}
	return f
}
func (s *solcState) call(ctx context.Context, name string, args ...uint64) []uint64 {
	v, err := s.function(name).Call(ctx, args...)
	if err != nil {
		panic(err)
	}
	return v
}
func (s *solcState) optional(name string) bool { _, ok := s.artifact.Exports[name]; return ok }
func (s *solcState) read32(p uint32) uint32 {
	v, ok := s.guest.Memory().ReadUint32Le(p)
	if !ok {
		panic(ErrExecution)
	}
	return v
}
func (s *solcState) write32(p, v uint32) {
	if !s.guest.Memory().WriteUint32Le(p, v) {
		panic(ErrExecution)
	}
}
func (s *solcState) cstring(p uint32, limit uint32) string {
	if p == 0 {
		return ""
	}
	mem := s.guest.Memory()
	if p >= mem.Size() {
		panic(ErrExecution)
	}
	n := min(limit, mem.Size()-p)
	b, ok := mem.Read(p, n)
	if !ok {
		panic(ErrExecution)
	}
	for i, v := range b {
		if v == 0 {
			return string(b[:i])
		}
	}
	panic(ErrExecution)
}
func (s *solcState) alloc(ctx context.Context, n uint32) uint32 {
	name := "_malloc"
	if s.optional("_solidity_alloc") {
		name = "_solidity_alloc"
	}
	p := uint32(s.call(ctx, name, uint64(n))[0])
	if p == 0 {
		panic(ErrExecution)
	}
	return p
}
func (s *solcState) writeString(ctx context.Context, value string) uint32 {
	p := s.alloc(ctx, uint32(len(value)+1))
	if !s.guest.Memory().Write(p, append([]byte(value), 0)) {
		panic(ErrExecution)
	}
	return p
}
func normalizeVersion(v string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".Emscripten.clang")
}

// CompileSolc runs one input in a fresh runtime without a persistent machine-code
// cache. The caller owns the enclosing process timeout and whole-process cleanup.
func CompileSolc(ctx context.Context, a *Artifact, version string, input []byte, maxOutput uint32) (output []byte, err error) {
	ctx = compilationContext(ctx)
	defer func() {
		if recovered := recover(); recovered != nil {
			output = nil
			err = fmt.Errorf("%w: %v", ErrExecution, recovered)
		}
	}()
	if maxOutput == 0 {
		return nil, ErrExecution
	}
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(8192).WithCloseOnContextDone(true))
	defer func() {
		if closeErr := r.Close(context.Background()); closeErr != nil {
			output = nil
			err = ErrExecution
		}
	}()
	s := &solcState{artifact: a, exceptions: map[uint32]*exceptionInfo{}, maxOutput: maxOutput, modernExceptions: a.ModernExceptions}
	if err = s.instantiateHosts(ctx, r, nil); err != nil {
		return nil, err
	}
	compiled, err := r.CompileModule(ctx, a.Binary)
	if err != nil {
		return nil, ErrArtifact
	}
	s.guest, err = r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("compiler").WithStartFunctions())
	if err != nil {
		return nil, ErrExecution
	}
	s.installIndirectBridge(ctx, r)
	s.initialize(ctx)
	versionName := "_solidity_version"
	if !s.optional(versionName) {
		versionName = "_version"
	}
	actual := s.cstring(uint32(s.call(ctx, versionName)[0]), 512)
	if normalizeVersion(actual) != normalizeVersion(version) {
		return nil, ErrVersion
	}
	s.callback = s.installCallback(ctx, r)
	name := "_solidity_compile"
	if !s.optional(name) {
		name = "_compileStandard"
	}
	if !s.optional(name) {
		return s.legacyCompile(ctx, input)
	}
	saved := s.call(ctx, "stackSave")[0]
	defer s.call(ctx, "stackRestore", saved)
	p := s.stackString(ctx, string(input))
	args := []uint64{uint64(p), uint64(s.callback)}
	if len(s.function(name).Definition().ParamTypes()) == 3 {
		args = append(args, 0)
	}
	result := s.call(ctx, name, args...)
	output, err = s.outputBytes(uint32(result[0]))
	if err != nil {
		return nil, err
	}
	if s.optional("_solidity_reset") {
		s.call(ctx, "_solidity_reset")
	}
	return output, nil
}
func (s *solcState) initialize(ctx context.Context) {
	if top := s.artifact.Constants["DYNAMICTOP_PTR"]; top != 0 {
		s.write32(top, s.artifact.Constants["DYNAMIC_BASE"])
	}
	if s.optional("establishStackSpace") {
		s.call(ctx, "establishStackSpace", uint64(s.artifact.Constants["STACK_BASE"]), uint64(s.artifact.Constants["STACK_MAX"]))
	}
	for _, name := range []string{"___wasm_call_ctors", "globalCtors"} {
		if s.optional(name) {
			s.call(ctx, name)
		}
	}
}
func (s *solcState) invoke(ctx context.Context, name string, params, results []api.ValueType, stack []uint64) {
	saved := s.call(ctx, "stackSave")[0]
	var values []uint64
	var err error
	if s.optional("dynCall_" + strings.TrimPrefix(name, "invoke_")) {
		values, err = s.function("dynCall_"+strings.TrimPrefix(name, "invoke_")).Call(ctx, stack[:len(params)]...)
	} else {
		f := s.indirectBridge.ExportedFunction(name)
		if f == nil {
			panic(ErrUnsupported)
		}
		values, err = f.Call(ctx, stack[:len(params)]...)
	}
	if err != nil {
		if _, ok := errors.AsType[thrownException](err); !ok {
			panic(err)
		}
		s.call(ctx, "stackRestore", saved)
		s.call(ctx, "_setThrew", 1, 0)
		clear(stack[:len(results)])
		return
	}
	copy(stack, values)
}

// A typed throw is caught only by the Emscripten invoke frame, never by an
// unrelated runtime trap or cancellation.
type thrownException uint32

func (e thrownException) Error() string { return fmt.Sprintf("compiler exception %d", uint32(e)) }

func (s *solcState) stackString(ctx context.Context, value string) uint32 {
	p := uint32(s.call(ctx, "stackAlloc", uint64(len(value)+1))[0])
	if !s.guest.Memory().Write(p, append([]byte(value), 0)) {
		panic(ErrExecution)
	}
	return p
}

func (s *solcState) outputBytes(p uint32) ([]byte, error) {
	memory := s.guest.Memory()
	if p == 0 || p >= memory.Size() {
		return nil, ErrExecution
	}
	data, ok := memory.Read(p, min(s.maxOutput+1, memory.Size()-p))
	if !ok {
		return nil, ErrExecution
	}
	end := bytes.IndexByte(data, 0)
	if end < 0 {
		if uint32(len(data)) > s.maxOutput {
			return nil, ErrOutputLimit
		}
		return nil, ErrExecution
	}
	if uint32(end) > s.maxOutput {
		return nil, ErrOutputLimit
	}
	return bytes.Clone(data[:end]), nil
}
