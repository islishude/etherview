package wasmcompiler

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	"github.com/tetratelabs/wabin/wasm"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func (s *solcState) instantiateHosts(ctx context.Context, r wazero.Runtime, _ wazero.CompiledModule) error {
	groups := map[string][]*wasm.Import{}
	for _, im := range s.artifact.Module.ImportSection {
		groups[im.Module] = append(groups[im.Module], im)
	}
	for name, imports := range groups {
		needsModule := false
		for _, im := range imports {
			if im.Type != wasm.ExternTypeFunc {
				needsModule = true
			}
		}
		hostName := name
		if needsModule {
			hostName = name + "-functions"
		}
		host := r.NewHostModuleBuilder(hostName)
		env := &wasm.Module{}
		for _, im := range imports {
			if im.Type == wasm.ExternTypeFunc {
				semantic := s.artifact.Imports[im.Name]
				if semantic == "" {
					semantic = im.Name
				}
				typ := s.artifact.Module.TypeSection[im.DescFunc]
				if !knownHost(semantic) {
					return fmt.Errorf("%w: host %s", ErrUnsupported, semantic)
				}
				host.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) {
					if err := ctx.Err(); err != nil {
						panic(err)
					}
					if s.guest == nil {
						s.guest = m
					}
					s.hostCall(ctx, semantic, typ.Params, typ.Results, stack)
				}), typ.Params, typ.Results).Export(im.Name)
				index := uint32(len(env.ImportSection))
				env.TypeSection = append(env.TypeSection, typ)
				env.ImportSection = append(env.ImportSection, &wasm.Import{Type: wasm.ExternTypeFunc, Module: hostName, Name: im.Name, DescFunc: index})
				env.ExportSection = append(env.ExportSection, &wasm.Export{Type: wasm.ExternTypeFunc, Name: im.Name, Index: index})
			} else {
				if err := s.addEnvironment(env, im); err != nil {
					return err
				}
			}
		}
		if _, err := host.Instantiate(ctx); err != nil {
			return ErrUnsupported
		}
		if needsModule {
			if _, err := r.InstantiateWithConfig(ctx, wasmbin.EncodeModule(env), wazero.NewModuleConfig().WithName(name)); err != nil {
				return ErrUnsupported
			}
		}
	}
	return nil
}
func (s *solcState) addEnvironment(m *wasm.Module, im *wasm.Import) error {
	switch im.Type {
	case wasm.ExternTypeMemory:
		mem := *im.DescMem
		mem.Min = max(mem.Min, s.artifact.Constants["TOTAL_MEMORY"]/65536)
		if mem.Min > 8192 {
			return ErrUnsupported
		}
		mem.Max = 8192
		mem.IsMaxEncoded = true
		m.MemorySection = &mem
		m.ExportSection = append(m.ExportSection, &wasm.Export{Type: im.Type, Name: im.Name, Index: 0})
	case wasm.ExternTypeTable:
		tab := *im.DescTable
		m.TableSection = append(m.TableSection, &tab)
		m.ExportSection = append(m.ExportSection, &wasm.Export{Type: im.Type, Name: im.Name, Index: uint32(len(m.TableSection) - 1)})
	case wasm.ExternTypeGlobal:
		if im.Module == "global" && (im.Name == "NaN" || im.Name == "Infinity") && im.DescGlobal.ValType == wasm.ValueTypeF64 {
			value := math.NaN()
			if im.Name == "Infinity" {
				value = math.Inf(1)
			}
			data := make([]byte, 8)
			binary.LittleEndian.PutUint64(data, math.Float64bits(value))
			m.GlobalSection = append(m.GlobalSection, &wasm.Global{Type: im.DescGlobal, Init: &wasm.ConstantExpression{Opcode: wasm.OpcodeF64Const, Data: data}})
			m.ExportSection = append(m.ExportSection, &wasm.Export{Type: im.Type, Name: im.Name, Index: uint32(len(m.GlobalSection) - 1)})
			return nil
		}
		name := s.artifact.Imports[im.Name]
		if name == "" {
			name = im.Name
		}
		value, ok := s.artifact.Constants[name]
		if !ok {
			n, e := strconv.ParseUint(name, 10, 32)
			if e == nil {
				value = uint32(n)
				ok = true
			}
		}
		if !ok {
			switch name {
			case "memoryBase", "tableBase", "__memory_base", "__table_base":
				ok = true
			case "STACKTOP":
				value = s.artifact.Constants["STACK_BASE"]
				ok = true
			}
		}
		if !ok || im.DescGlobal.ValType != wasm.ValueTypeI32 {
			return ErrUnsupported
		}
		m.GlobalSection = append(m.GlobalSection, &wasm.Global{Type: im.DescGlobal, Init: &wasm.ConstantExpression{Opcode: wasm.OpcodeI32Const, Data: leb128.EncodeInt32(int32(value))}})
		m.ExportSection = append(m.ExportSection, &wasm.Export{Type: im.Type, Name: im.Name, Index: uint32(len(m.GlobalSection) - 1)})
	default:
		return ErrUnsupported
	}
	return nil
}

var simpleHosts = map[string]bool{
	"_getTempRet0": true, "_setTempRet0": true, "_emscripten_resize_heap": true, "_emscripten_memcpy_big": true,
	"_environ_get": true, "_environ_sizes_get": true, "_fd_write": true, "_fd_read": true, "_fd_seek": true, "_fd_close": true,
	"_raise": true, "_pthread_create": true, "___sys_munmap": true, "___sys_ioctl": true, "___sys_open": true, "___sys_fcntl64": true, "_llvm_exp2_f64": true, "__Exit": true, "_signal": true, "_abort": true, "abort": true, "_exit": true, "_proc_exit": true, "_llvm_eh_typeid_for": true,
	"__emscripten_date_now": true, "_emscripten_get_now": true, "__emscripten_get_now_is_monotonic": true,
	"___syscall_open": true, "___syscall_stat64": true, "___syscall_faccessat": true, "___syscall_fcntl64": true, "___syscall_ioctl": true, "___syscall_newfstatat": true, "___syscall_openat": true,
	"___call_sighandler": true, "_strftime_l": true,
}

var indirectHostPattern = regexp.MustCompile(`^(?:invoke|jsCall)_[vifdj][ifdj]*$`)
var exceptionHosts = map[string]bool{
	"___cxa_get_exception_ptr": true, "___cxa_allocate_exception": true, "___cxa_throw": true, "___resumeException": true,
	"___cxa_begin_catch": true, "___cxa_end_catch": true, "___cxa_rethrow": true,
	"___cxa_free_exception": true, "___cxa_uncaught_exceptions": true,
	"___cxa_current_primary_exception": true, "___cxa_increment_exception_refcount": true,
	"___cxa_decrement_exception_refcount": true, "___exception_addRef": true, "___exception_decRef": true,
	"___cxa_rethrow_primary_exception": true, "___cxa_call_unexpected": true,
	"___cxa_find_matching_catch_2": true, "___cxa_find_matching_catch_3": true,
	"___cxa_find_matching_catch_4": true, "___cxa_find_matching_catch_5": true,
	"___cxa_find_matching_catch_6": true, "___cxa_find_matching_catch_7": true,
	"___cxa_find_matching_catch_8": true, "___cxa_find_matching_catch_9": true,
	"___cxa_find_matching_catch_10": true,
}

func knownLegacySyscall(name string) bool {
	return strings.HasPrefix(name, "___syscall") && legacySyscalls[strings.TrimPrefix(name, "___syscall")]
}
func knownHost(name string) bool {
	if suffix, ok := strings.CutPrefix(name, "___cxa_find_matching_catch_"); ok {
		n, err := strconv.Atoi(suffix)
		return err == nil && n >= 2 && n <= 66
	}
	return simpleHosts[name] || legacyHosts[name] || knownLegacySyscall(name) || exceptionHosts[name] || indirectHostPattern.MatchString(name)
}

func (s *solcState) hostCall(ctx context.Context, name string, params, results []api.ValueType, stack []uint64) {
	switch {
	case legacyHosts[name] || knownLegacySyscall(name):
		s.legacyHost(ctx, name, stack)
	case strings.HasPrefix(name, "invoke_"):
		s.invoke(ctx, name, params, results, stack)
	case strings.HasPrefix(name, "___cxa_") || strings.HasPrefix(name, "___exception_") || name == "___resumeException":
		s.handleException(ctx, name, stack, len(params))
	case strings.HasPrefix(name, "jsCall_"):
		s.importCallback(ctx, stack[1:len(params)])
	case name == "_llvm_exp2_f64":
		stack[0] = math.Float64bits(math.Exp2(math.Float64frombits(stack[0])))
	case name == "_pthread_create":
		stack[0] = 6
	case name == "_raise":
		if s.optional("___errno_location") {
			s.write32(uint32(s.call(ctx, "___errno_location")[0]), 52)
		}
		stack[0] = uint64(uint32(0xffffffff))
	case name == "_signal":
		if stack[0] == 14 {
			s.alarmHandler = uint32(stack[1])
		}
		stack[0] = 0
	case name == "_getTempRet0":
		stack[0] = s.temp
	case name == "_setTempRet0":
		s.temp = stack[0]
	case name == "_llvm_eh_typeid_for": // identity conversion, no memory or host access
	case name == "_emscripten_resize_heap":
		want := uint64(uint32(stack[0]))
		pages, _ := s.guest.Memory().Grow(0)
		ok := true
		if want > uint64(pages)*65536 {
			_, ok = s.guest.Memory().Grow(uint32((want+65535)/65536) - pages)
		}
		stack[0] = 0
		if ok {
			stack[0] = 1
		}
	case name == "_emscripten_memcpy_big":
		dst, src, n := uint32(stack[0]), uint32(stack[1]), uint32(stack[2])
		data, ok := s.guest.Memory().Read(src, n)
		if !ok || !s.guest.Memory().Write(dst, data) {
			panic(ErrExecution)
		}
	case name == "_environ_sizes_get":
		s.write32(uint32(stack[0]), 0)
		s.write32(uint32(stack[1]), 0)
		stack[0] = 0
	case name == "_environ_get":
		stack[0] = 0 // the guest environment is intentionally empty
	case name == "__emscripten_get_now_is_monotonic":
		stack[0] = 1
	case name == "__emscripten_date_now" || name == "_emscripten_get_now":
		stack[0] = math.Float64bits(float64(time.Now().UnixNano()) / 1e6)
	case name == "_fd_write":
		s.fdWrite(stack)
	case name == "_fd_read" || name == "_fd_seek" || name == "_fd_close":
		stack[0] = 8 // WASI EBADF: no guest-opened descriptors
	case strings.HasPrefix(name, "___syscall_") || strings.HasPrefix(name, "___sys_"):
		stack[0] = uint64(uint32(0xffffffcc)) // -ENOSYS: host filesystem is unavailable
	default:
		panic(ErrExecution)
	}
}
func (s *solcState) fdWrite(stack []uint64) {
	if stack[0] != 1 && stack[0] != 2 {
		stack[0] = 8
		return
	}
	ptr, n := uint32(stack[1]), uint32(stack[2])
	if n > 1024 {
		panic(ErrExecution)
	}
	count := uint32(0)
	for i := range n {
		p := s.read32(ptr + i*8)
		size := s.read32(ptr + i*8 + 4)
		if size > 1<<20 || s.diagnostics+int(size) > 1<<20 {
			panic(ErrExecution)
		}
		if _, ok := s.guest.Memory().Read(p, size); !ok {
			panic(ErrExecution)
		}
		count += size
		s.diagnostics += int(size)
	}
	s.write32(uint32(stack[3]), count)
	stack[0] = 0
}
