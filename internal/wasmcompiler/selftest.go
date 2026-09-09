package wasmcompiler

import (
	"context"
	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	"github.com/tetratelabs/wabin/wasm"
	"github.com/tetratelabs/wazero"
)

func selfTest(ctx context.Context) (err error) {
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(8192).WithCloseOnContextDone(true))
	defer func() {
		if e := r.Close(context.Background()); e != nil {
			err = ErrExecution
		}
	}()
	body := append([]byte{wasm.OpcodeI32Const}, leb128.EncodeInt32(8192)...)
	body = append(body, wasm.OpcodeMemoryGrow, 0, wasm.OpcodeI32Const, 0x7f, wasm.OpcodeI32Eq, wasm.OpcodeEnd)
	module := &wasm.Module{TypeSection: []*wasm.FunctionType{{Results: []wasm.ValueType{wasm.ValueTypeI32}}}, FunctionSection: []uint32{0}, MemorySection: &wasm.Memory{Min: 1}, CodeSection: []*wasm.Code{{Body: body}}, ExportSection: []*wasm.Export{{Type: wasm.ExternTypeFunc, Name: "check", Index: 0}}}
	guest, e := r.Instantiate(ctx, wasmbin.EncodeModule(module))
	if e != nil {
		return ErrExecution
	}
	out, e := guest.ExportedFunction("check").Call(ctx)
	if e != nil || len(out) != 1 || out[0] != 1 {
		return ErrExecution
	}
	return nil
}
