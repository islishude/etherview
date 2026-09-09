package wasmcompiler

import (
	"context"
	"strings"
)

type exceptionInfo struct {
	typ, destructor, refs, adjusted, ptr uint32
	caught, rethrown                     bool
}

func (s *solcState) exception(ptr uint32) *exceptionInfo {
	info := s.exceptions[ptr]
	if info == nil {
		panic(ErrExecution)
	}
	return info
}
func (s *solcState) freeException(ctx context.Context, p uint32) {
	info := s.exception(p)
	p = info.ptr
	for key, value := range s.exceptions {
		if value == info {
			delete(s.exceptions, key)
		}
	}
	if s.modernExceptions {
		p -= s.artifact.ExceptionHeader
	}
	s.call(ctx, "_free", uint64(p))
}
func (s *solcState) decrement(ctx context.Context, p uint32) {
	if p == 0 {
		return
	}
	info := s.exception(p)
	p = info.ptr
	if info.refs == 0 {
		panic(ErrExecution)
	}
	info.refs--
	if info.refs == 0 && !info.rethrown {
		if info.destructor != 0 {
			s.callDestructor(ctx, info.destructor, p)
		}
		s.freeException(ctx, p)
	}
}
func (s *solcState) handleException(ctx context.Context, name string, stack []uint64, params int) {
	switch {
	case name == "___cxa_get_exception_ptr": // legacy Emscripten identity conversion

	case name == "___cxa_allocate_exception":
		n := uint32(stack[0])
		if n > MaxWasmBytes-24 {
			panic(ErrExecution)
		}
		extra := uint32(0)
		if s.modernExceptions {
			extra = s.artifact.ExceptionHeader
		}
		stack[0] = s.call(ctx, "_malloc", uint64(n+extra))[0] + uint64(extra)
	case name == "___cxa_throw":
		p := uint32(stack[0])
		if len(s.exceptions) >= 4096 {
			panic(ErrExecution)
		}
		info := &exceptionInfo{typ: uint32(stack[1]), destructor: uint32(stack[2]), adjusted: p, ptr: p}
		s.exceptions[p] = info
		s.last = p
		if s.modernExceptions {
			s.uncaught++
		} else {
			s.uncaught = 1
		}
		if s.modernExceptions {
			base := p - s.artifact.ExceptionHeader
			if !s.guest.Memory().Write(base, make([]byte, s.artifact.ExceptionHeader)) {
				panic(ErrExecution)
			}
			if s.artifact.ExceptionAttrs {
				s.write32(base, info.destructor)
				s.write32(base+8, info.typ)
			} else {
				s.write32(base+4, info.typ)
				s.write32(base+8, info.destructor)
			}
		}

		panic(thrownException(p))
	case name == "___resumeException":
		p := uint32(stack[0])
		if s.artifact.CatchObject {
			base := s.read32(p)
			s.call(ctx, "_free", uint64(p))
			p = base
		}
		if s.last == 0 {
			s.last = p
		}
		panic(thrownException(p))
	case strings.HasPrefix(name, "___cxa_find_matching_catch_"):
		s.findCatch(ctx, stack, params)
	case name == "___cxa_begin_catch":
		p := uint32(stack[0])
		catchPtr := p
		if s.artifact.CatchObject {
			p = s.read32(p)
		}
		info := s.exception(p)
		if !info.caught {
			info.caught = true
			s.uncaught--
		}
		info.rethrown = false
		info.refs++
		if s.artifact.CatchObject {
			s.caught = append(s.caught, catchPtr)
		} else {
			s.caught = append(s.caught, info.ptr)
		}
		if s.modernExceptions && s.call(ctx, "___cxa_is_pointer_type", uint64(info.typ))[0] != 0 {
			stack[0] = uint64(s.read32(p))
		} else {
			stack[0] = uint64(info.adjusted)
		}
	case name == "___cxa_end_catch":
		s.call(ctx, "_setThrew", 0, 0)
		if len(s.caught) == 0 {
			panic(ErrExecution)
		}
		p := s.caught[len(s.caught)-1]
		s.caught = s.caught[:len(s.caught)-1]
		if s.artifact.CatchObject {
			base := s.read32(p)
			s.decrement(ctx, base)
			s.call(ctx, "_free", uint64(p))
		} else {
			s.decrement(ctx, p)
		}
		s.last = 0
	case name == "___cxa_rethrow_primary_exception":
		p := uint32(stack[0])
		if p == 0 {
			return
		}
		if s.artifact.CatchObject {
			catch := uint32(s.call(ctx, "_malloc", 8)[0])
			s.write32(catch, p)
			s.write32(catch+4, p)
			s.caught = append(s.caught, catch)
		} else {
			s.caught = append(s.caught, p)
		}
		s.exception(p).rethrown = true
		s.handleException(ctx, "___cxa_rethrow", stack, 0)
	case name == "___cxa_call_unexpected":
		panic(ErrExecution)
	case name == "___cxa_rethrow":
		if len(s.caught) == 0 {
			panic(ErrExecution)
		}
		p := s.caught[len(s.caught)-1]
		s.caught = s.caught[:len(s.caught)-1]
		base := p
		if s.artifact.CatchObject {
			base = s.read32(p)
		}
		info := s.exception(base)
		if !info.rethrown {
			s.caught = append(s.caught, p)
			info.rethrown = true
			if s.modernExceptions {
				info.caught = false
				s.uncaught++
			}
		} else if s.artifact.CatchObject {
			s.call(ctx, "_free", uint64(p))
		}
		s.last = base
		panic(thrownException(base))
	case name == "___cxa_free_exception":
		s.freeException(ctx, uint32(stack[0]))
	case name == "___cxa_uncaught_exceptions" || name == "__ZSt18uncaught_exceptionv":
		stack[0] = uint64(s.uncaught)
	case name == "___cxa_current_primary_exception":
		stack[0] = 0
		if len(s.caught) > 0 {
			p := s.caught[len(s.caught)-1]
			if s.artifact.CatchObject {
				p = s.read32(p)
			}
			s.exception(p).refs++
			stack[0] = uint64(p)
		}
	case name == "___cxa_increment_exception_refcount" || name == "___exception_addRef":
		if stack[0] != 0 {
			s.exception(uint32(stack[0])).refs++
		}
	case name == "___cxa_decrement_exception_refcount" || name == "___exception_decRef":
		s.decrement(ctx, uint32(stack[0]))
	default:
		panic(ErrUnsupported)
	}
}
func (s *solcState) findCatch(ctx context.Context, stack []uint64, params int) {
	p := s.last
	if p == 0 {
		s.temp = 0
		stack[0] = 0
		return
	}
	info := s.exception(p)
	scratch := s.artifact.Constants["catchBuffer"]
	catchPtr := uint32(0)
	if s.artifact.CatchObject {
		catchPtr = uint32(s.call(ctx, "_malloc", 8)[0])
		s.write32(catchPtr, p)
		scratch = catchPtr + 4
	} else if scratch == 0 {
		scratch = uint32(s.call(ctx, "_malloc", 4)[0])
		defer s.call(ctx, "_free", uint64(scratch))
	}
	s.write32(scratch, p)
	s.temp = uint64(info.typ)
	for _, typ := range stack[:params] {
		if s.modernExceptions && (typ == 0 || typ == uint64(info.typ)) {
			break
		}
		if typ != 0 && s.call(ctx, "___cxa_can_catch", typ, uint64(info.typ), uint64(scratch))[0] != 0 {
			s.temp = typ
			break
		}
	}
	info.adjusted = s.read32(scratch)
	stack[0] = uint64(p)
	if s.artifact.CatchObject {
		stack[0] = uint64(catchPtr)
	}
	if !s.modernExceptions && info.adjusted != p {
		s.exceptions[info.adjusted] = info
		stack[0] = uint64(info.adjusted)
	}
}
