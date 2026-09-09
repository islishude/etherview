package wasmcompiler

import (
	"bytes"
	"context"
	"fmt"
	"github.com/tetratelabs/wabin/leb128"
	"github.com/tetratelabs/wabin/wasm"
	"math"
)

// callDestructor uses the original table's exact function signature. C++
// destructors can return a pointer that the JavaScript glue deliberately ignores.
func (s *solcState) callDestructor(ctx context.Context, index, pointer uint32) {
	if s.artifact.DestructorTrampoline != "" {
		s.call(ctx, s.artifact.DestructorTrampoline, uint64(index), uint64(pointer))
		return
	}
	for _, segment := range s.artifact.Module.ElementSection {
		if segment.Mode != wasm.ElementModeActive || segment.TableIndex != 0 {
			continue
		}
		offset := int32(0)
		if segment.OffsetExpr.Opcode == wasm.OpcodeI32Const {
			var err error
			offset, _, err = leb128.DecodeInt32(bytes.NewReader(segment.OffsetExpr.Data))
			if err != nil {
				panic(ErrArtifact)
			}
		} else if segment.OffsetExpr.Opcode != wasm.OpcodeGlobalGet {
			panic(ErrUnsupported)
		}
		if offset < 0 || index < uint32(offset) || index-uint32(offset) >= uint32(len(segment.Init)) {
			continue
		}
		function := segment.Init[index-uint32(offset)]
		if function == nil {
			panic(ErrExecution)
		}
		typ := s.functionType(*function)
		if len(typ.Params) < 1 || typ.Params[0] != wasm.ValueTypeI32 {
			panic(ErrUnsupported)
		}
		typeIndex := uint32(0)
		for i, t := range s.artifact.Module.TypeSection {
			if t == typ {
				typeIndex = uint32(i)
				break
			}
		}
		f := s.indirectBridge.ExportedFunction(fmt.Sprintf("destructor_%d", typeIndex))
		if f == nil {
			panic(ErrUnsupported)
		}
		args := make([]uint64, len(typ.Params)+1)
		args[0] = uint64(index)
		args[1] = uint64(pointer)
		for i, t := range typ.Params[1:] {
			switch t {
			case wasm.ValueTypeI32:
			case wasm.ValueTypeF32:
				args[i+2] = uint64(math.Float32bits(float32(math.NaN())))
			case wasm.ValueTypeF64:
				args[i+2] = math.Float64bits(math.NaN())
			default:
				panic(ErrUnsupported)
			}
		}
		if _, err := f.Call(ctx, args...); err != nil {
			panic(err)
		}
		return
	}
	panic(ErrUnsupported)
}
func (s *solcState) functionType(index uint32) *wasm.FunctionType {
	for _, im := range s.artifact.Module.ImportSection {
		if im.Type == wasm.ExternTypeFunc {
			if index == 0 {
				return s.artifact.Module.TypeSection[im.DescFunc]
			}
			index--
		}
	}
	if index >= uint32(len(s.artifact.Module.FunctionSection)) {
		panic(ErrExecution)
	}
	return s.artifact.Module.TypeSection[s.artifact.Module.FunctionSection[index]]
}
