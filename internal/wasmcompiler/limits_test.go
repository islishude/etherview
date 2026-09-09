package wasmcompiler

import (
	"context"
	"testing"
	"time"

	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	"github.com/tetratelabs/wabin/wasm"
)

func fixtureArtifact(body []byte) *Artifact {
	i32 := wasm.ValueTypeI32
	module := &wasm.Module{
		TypeSection:     []*wasm.FunctionType{{Results: []wasm.ValueType{i32}}, {Params: []wasm.ValueType{i32}, Results: []wasm.ValueType{i32}}, {Params: []wasm.ValueType{i32}}, {Params: []wasm.ValueType{i32, i32, i32}, Results: []wasm.ValueType{i32}}},
		FunctionSection: []uint32{0, 1, 2, 1, 3},
		MemorySection:   &wasm.Memory{Min: 1}, TableSection: []*wasm.Table{{Type: wasm.RefTypeFuncref, Min: 1}},
		CodeSection: []*wasm.Code{{Body: append(append([]byte{wasm.OpcodeI32Const}, leb128.EncodeInt32(1024)...), wasm.OpcodeEnd)}, {Body: append(append([]byte{wasm.OpcodeI32Const}, leb128.EncodeInt32(2048)...), wasm.OpcodeEnd)}, {Body: []byte{wasm.OpcodeEnd}}, {Body: append(append([]byte{wasm.OpcodeI32Const}, leb128.EncodeInt32(4096)...), wasm.OpcodeEnd)}, {Body: body}},
		DataSection: []*wasm.DataSegment{{OffsetExpression: &wasm.ConstantExpression{Opcode: wasm.OpcodeI32Const, Data: leb128.EncodeInt32(1024)}, Init: []byte("fixture\x00{}\x00")}},
	}
	mapping := map[string]string{"_solidity_version": "version", "_malloc": "alloc", "stackSave": "version", "stackRestore": "restore", "stackAlloc": "stack", "_solidity_compile": "compile"}
	for i, name := range []string{"version", "alloc", "restore", "stack", "compile"} {
		module.ExportSection = append(module.ExportSection, &wasm.Export{Type: wasm.ExternTypeFunc, Name: name, Index: uint32(i)})
	}
	module.ExportSection = append(module.ExportSection, &wasm.Export{Type: wasm.ExternTypeMemory, Name: "memory"}, &wasm.Export{Type: wasm.ExternTypeTable, Name: "table"})
	return &Artifact{Binary: wasmbin.EncodeModule(module), Module: module, Exports: mapping, Imports: map[string]string{}, Constants: map[string]uint32{}}
}
func TestGuestInfiniteLoopCancellation(t *testing.T) {
	// loop { br 0 }; unreachable result keeps the function's return type valid.
	a := fixtureArtifact([]byte{wasm.OpcodeLoop, 0x40, wasm.OpcodeBr, 0, wasm.OpcodeEnd, wasm.OpcodeI32Const, 0, wasm.OpcodeEnd})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err := CompileSolc(ctx, a, "fixture", []byte("{}"), 1024)
	if err == nil || len(out) != 0 || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("cancellation: %q %v %v", out, err, ctx.Err())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("guest did not terminate promptly")
	}
}
func TestGuestMemoryLimit(t *testing.T) {
	body := append([]byte{wasm.OpcodeI32Const}, leb128.EncodeInt32(8192)...)
	body = append(body, wasm.OpcodeMemoryGrow, 0, wasm.OpcodeI32Const, 0x7f, wasm.OpcodeI32Eq, wasm.OpcodeIf, wasm.ValueTypeI32, wasm.OpcodeI32Const)
	body = append(body, leb128.EncodeInt32(1032)...)
	body = append(body, wasm.OpcodeElse, wasm.OpcodeI32Const, 0, wasm.OpcodeEnd, wasm.OpcodeEnd)
	out, err := CompileSolc(context.Background(), fixtureArtifact(body), "fixture", []byte("{}"), 1024)
	if err != nil || string(out) != "{}" {
		t.Fatalf("memory cap: %q %v", out, err)
	}
}
