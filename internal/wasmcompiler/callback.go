package wasmcompiler

import (
	"context"
	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func (s *solcState) installCallback(ctx context.Context, r wazero.Runtime) uint32 {
	if value := s.artifact.Constants["jsCallStartIndex"]; value != 0 {
		return value
	}
	tableModule, tableName := "compiler", ""
	for _, ex := range s.artifact.Module.ExportSection {
		if ex.Type == wasm.ExternTypeTable && ex.Index == 0 {
			tableName = ex.Name
		}
	}
	if tableName == "" {
		for _, im := range s.artifact.Module.ImportSection {
			if im.Type == wasm.ExternTypeTable {
				tableModule, tableName = im.Module, im.Name
				break
			}
		}
	}
	if tableName == "" {
		panic(ErrUnsupported)
	}
	count := 3
	if s.optional("_solidity_compile") && len(s.function("_solidity_compile").Definition().ParamTypes()) == 3 {
		count = 5
	}
	params := make([]api.ValueType, count)
	for i := range params {
		params[i] = api.ValueTypeI32
	}
	_, err := r.NewHostModuleBuilder("compiler-callback").NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, _ api.Module, stack []uint64) { s.importCallback(ctx, stack[:count]) }), params, nil).Export("callback").Instantiate(ctx)
	if err != nil {
		panic(err)
	}
	bridge := &wasm.Module{
		TypeSection:     []*wasm.FunctionType{{Params: params}, {Results: []wasm.ValueType{wasm.ValueTypeI32}}},
		ImportSection:   []*wasm.Import{{Type: wasm.ExternTypeFunc, Module: "compiler-callback", Name: "callback", DescFunc: 0}, {Type: wasm.ExternTypeTable, Module: tableModule, Name: tableName, DescTable: &wasm.Table{Type: wasm.RefTypeFuncref}}},
		FunctionSection: []uint32{1},
		ExportSection:   []*wasm.Export{{Type: wasm.ExternTypeFunc, Name: "install", Index: 1}, {Type: wasm.ExternTypeFunc, Name: "callback", Index: 0}},
		CodeSection:     []*wasm.Code{{LocalTypes: []wasm.ValueType{wasm.ValueTypeI32}, Body: []byte{0xd0, 0x70, 0x41, 1, 0xfc, 15, 0, 0x22, 0, 0xd2, 0, 0x26, 0, 0x20, 0, 0x0b}}},
	}
	m, err := r.InstantiateWithConfig(ctx, wasmbin.EncodeModule(bridge), wazero.NewModuleConfig().WithName("callback-bridge"))
	if err != nil {
		panic(err)
	}
	value, err := m.ExportedFunction("install").Call(ctx)
	if err != nil {
		panic(err)
	}
	if uint32(value[0]) == ^uint32(0) {
		panic(ErrUnsupported)
	}
	return uint32(value[0])
}
func (s *solcState) importCallback(ctx context.Context, args []uint64) {
	if err := ctx.Err(); err != nil {
		panic(err)
	}
	message := "File import callback not supported"
	var out uint32
	if len(args) == 5 {
		if args[0] != 0 {
			panic(ErrExecution)
		}
		switch s.cstring(uint32(args[1]), 64) {
		case "source":
		case "smt-query":
			message = "SMT solver callback not supported"
		default:
			panic(ErrUnsupported)
		}
		out = uint32(args[4])
	} else if len(args) == 3 {
		out = uint32(args[2])
	} else {
		panic(ErrUnsupported)
	}
	s.write32(out, s.writeString(ctx, message))
}
