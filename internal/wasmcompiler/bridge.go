package wasmcompiler

import (
	"context"
	"fmt"
	"strings"

	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	"github.com/tetratelabs/wabin/wasm"
	"github.com/tetratelabs/wazero"
)

// Build guest-side call_indirect thunks. This supports table entries referring
// to either WASM or imported Go functions without depending on wazero internals.
func (s *solcState) installIndirectBridge(ctx context.Context, r wazero.Runtime) {
	module, name := "compiler", ""
	for _, ex := range s.artifact.Module.ExportSection {
		if ex.Type == wasm.ExternTypeTable && ex.Index == 0 {
			name = ex.Name
		}
	}
	if name == "" {
		for _, im := range s.artifact.Module.ImportSection {
			if im.Type == wasm.ExternTypeTable {
				module, name = im.Module, im.Name
				break
			}
		}
	}
	if name == "" {
		// Some historical builds keep the table private and export every required
		// dynCall trampoline instead. Use those original compiler trampolines.
		if !s.optional("dynCall_vi") || s.artifact.Constants["jsCallStartIndex"] == 0 {
			panic(ErrUnsupported)
		}
		for _, im := range s.artifact.Module.ImportSection {
			if im.Type != wasm.ExternTypeFunc {
				continue
			}
			semantic := s.artifact.Imports[im.Name]
			if semantic == "" {
				semantic = im.Name
			}
			if suffix, ok := strings.CutPrefix(semantic, "invoke_"); ok && !s.optional("dynCall_"+suffix) {
				panic(ErrUnsupported)
			}
		}
		return
	}
	bridge := &wasm.Module{ImportSection: []*wasm.Import{{Type: wasm.ExternTypeTable, Module: module, Name: name, DescTable: &wasm.Table{Type: wasm.RefTypeFuncref}}}}
	add := func(name string, typ *wasm.FunctionType) {
		index := uint32(len(bridge.TypeSection))
		bridge.TypeSection = append(bridge.TypeSection, typ, &wasm.FunctionType{Params: append([]wasm.ValueType{wasm.ValueTypeI32}, typ.Params...), Results: typ.Results})
		function := uint32(len(bridge.FunctionSection))
		bridge.FunctionSection = append(bridge.FunctionSection, index+1)
		body := []byte{}
		for p := range typ.Params {
			body = append(body, wasm.OpcodeLocalGet)
			body = append(body, leb128.EncodeUint32(uint32(p+1))...)
		}
		body = append(body, wasm.OpcodeLocalGet, 0, wasm.OpcodeCallIndirect)
		body = append(body, leb128.EncodeUint32(index)...)
		body = append(body, 0, wasm.OpcodeEnd)
		bridge.CodeSection = append(bridge.CodeSection, &wasm.Code{Body: body})
		bridge.ExportSection = append(bridge.ExportSection, &wasm.Export{Type: wasm.ExternTypeFunc, Name: name, Index: function})
	}
	for _, im := range s.artifact.Module.ImportSection {
		if im.Type != wasm.ExternTypeFunc {
			continue
		}
		name := s.artifact.Imports[im.Name]
		if name == "" {
			name = im.Name
		}
		if !strings.HasPrefix(name, "invoke_") {
			continue
		}
		typ := s.artifact.Module.TypeSection[im.DescFunc]
		if len(typ.Params) == 0 {
			panic(ErrUnsupported)
		}
		add(name, &wasm.FunctionType{Params: typ.Params[1:], Results: typ.Results})
	}
	for i, typ := range s.artifact.Module.TypeSection {
		if len(typ.Params) >= 1 && typ.Params[0] == wasm.ValueTypeI32 {
			add(fmt.Sprintf("destructor_%d", i), typ)
		}
	}
	var err error
	s.indirectBridge, err = r.InstantiateWithConfig(ctx, wasmbin.EncodeModule(bridge), wazero.NewModuleConfig().WithName("compiler-indirect"))
	if err != nil {
		panic(err)
	}
}
