package wasmcompiler

import (
	"context"
	"strings"
	"time"
)

// These are known, single-threaded Emscripten libc imports. Unsupported host
// capabilities trap or return an explicit errno; they never access the host OS.
var legacyHosts = map[string]bool{
	"getTempRet0": true, "setTempRet0": true, "___buildEnvironment": true, "___lock": true, "___unlock": true,
	"___map_file": true, "___wasi_fd_close": true, "___wasi_fd_fdstat_get": true, "___wasi_fd_read": true, "___wasi_fd_write": true, "___wasi_fd_seek": true,
	"_clock_gettime": true, "_emscripten_get_heap_size": true, "_getenv": true, "_llvm_stackrestore": true, "_llvm_stacksave": true, "_llvm_trap": true,
	"_pathconf": true, "_pthread_cond_destroy": true, "_pthread_cond_init": true, "_pthread_cond_timedwait": true, "_pthread_detach": true, "_pthread_equal": true, "_pthread_join": true, "_sched_yield": true, "_sysconf": true,
}
var legacySyscalls = map[string]bool{
	"10": true, "15": true, "183": true, "194": true, "195": true, "196": true, "197": true, "220": true, "221": true, "295": true, "3": true, "300": true, "306": true, "340": true, "38": true, "39": true, "40": true, "5": true, "54": true, "75": true, "83": true, "85": true, "9": true, "91": true, "94": true,
}

func (s *solcState) legacyHost(ctx context.Context, name string, stack []uint64) {
	switch name {
	case "getTempRet0":
		stack[0] = s.temp
	case "setTempRet0":
		s.temp = stack[0]
	case "___lock", "___unlock": // guest memory is non-shared and execution is single-threaded
	case "___buildEnvironment":
		pool := s.alloc(ctx, 1024)
		p := s.alloc(ctx, 256)
		s.write32(p, pool)
		s.write32(uint32(stack[0]), p)
		s.write32(p, 0)
	case "_getenv":
		stack[0] = 0 // absent from the deliberately empty environment
	case "_emscripten_get_heap_size":
		stack[0] = uint64(s.guest.Memory().Size())
	case "_llvm_stacksave":
		stack[0] = s.call(ctx, "stackSave")[0]
	case "_llvm_stackrestore":
		s.call(ctx, "stackRestore", stack[0])
	case "_clock_gettime":
		if stack[0] > 1 {
			stack[0] = ^uint64(0)
			return
		}
		now := time.Now()
		s.write32(uint32(stack[1]), uint32(now.Unix()))
		s.write32(uint32(stack[1])+4, uint32(now.Nanosecond()))
		stack[0] = 0
	case "_pthread_equal":
		a, b := stack[0], stack[1]
		stack[0] = 0
		if a == b {
			stack[0] = 1
		}
	case "_pthread_cond_init":
		s.write32(uint32(stack[0]), 0)
		stack[0] = 0
	case "_pthread_cond_destroy", "_sched_yield":
		stack[0] = 0 // no other guest thread can be waiting
	case "_sysconf":
		switch stack[0] {
		case 30:
			stack[0] = 65536
		case 83, 84:
			stack[0] = 1
		default:
			stack[0] = ^uint64(0)
		}
	case "___wasi_fd_write":
		s.fdWrite(stack)
	case "___wasi_fd_fdstat_get":
		if stack[0] > 2 {
			stack[0] = 8
			return
		}
		ptr := uint32(stack[1])
		if !s.guest.Memory().Write(ptr, make([]byte, 24)) {
			panic(ErrExecution)
		}
		if !s.guest.Memory().WriteByte(ptr, 2) {
			panic(ErrExecution)
		}
		stack[0] = 0
	default:
		if strings.HasPrefix(name, "___wasi_fd_") {
			stack[0] = 8
			return
		}
		if strings.HasPrefix(name, "___syscall") {
			stack[0] = uint64(uint32(0xffffffcc))
			return
		}
		panic(ErrExecution)
	}
}
