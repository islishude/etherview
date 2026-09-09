package wasmcompiler

import (
	"bytes"
	"context"
	"encoding/hex"
	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
	"github.com/tetratelabs/wazero"
	"golang.org/x/crypto/sha3"
	"testing"
)

func TestKeccakGuestABI(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer func() { _ = r.Close(ctx) }()
	if err := InstantiateKeccak(ctx, r); err != nil {
		t.Fatal(err)
	}
	module := &wasm.Module{TypeSection: []*wasm.FunctionType{{Params: []wasm.ValueType{wasm.ValueTypeI32, wasm.ValueTypeI32, wasm.ValueTypeI32}, Results: []wasm.ValueType{wasm.ValueTypeI32}}}, ImportSection: []*wasm.Import{{Type: wasm.ExternTypeFunc, Module: "etherview", Name: "keccak256"}}, FunctionSection: []uint32{0}, MemorySection: &wasm.Memory{Min: 1}, CodeSection: []*wasm.Code{{Body: []byte{wasm.OpcodeLocalGet, 0, wasm.OpcodeLocalGet, 1, wasm.OpcodeLocalGet, 2, wasm.OpcodeCall, 0, wasm.OpcodeEnd}}}, ExportSection: []*wasm.Export{{Type: wasm.ExternTypeFunc, Name: "hash", Index: 1}, {Type: wasm.ExternTypeMemory, Name: "memory"}}}
	guest, err := r.Instantiate(ctx, wasmbin.EncodeModule(module))
	if err != nil {
		t.Fatal(err)
	}
	hash := guest.ExportedFunction("hash")
	for _, size := range []int{0, 1, 3, 31, 32, 135, 136, 137, 271, 272, 273, 4096} {
		input := bytes.Repeat([]byte{0xa5}, size)
		if !guest.Memory().Write(1024, input) {
			t.Fatal("memory write")
		}
		status, err := hash.Call(ctx, 1024, uint64(size), 8192)
		if err != nil || status[0] != 0 {
			t.Fatalf("size %d: %v %v", size, status, err)
		}
		result, ok := guest.Memory().Read(8192, 32)
		if !ok {
			t.Fatal("memory read")
		}
		h := sha3.NewLegacyKeccak256()
		_, _ = h.Write(input)
		if !bytes.Equal(result, h.Sum(nil)) {
			t.Fatalf("hash mismatch at size %d", size)
		}
	}
	if !guest.Memory().Write(1024, []byte("abc")) {
		t.Fatal("memory write")
	}
	if _, err = hash.Call(ctx, 1024, 3, 1024); err != nil {
		t.Fatal(err)
	}
	digest, _ := guest.Memory().Read(1024, 32)
	if hex.EncodeToString(digest) != "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45" {
		t.Fatal("Keccak known vector/overlap mismatch")
	}
	for _, args := range [][]uint64{{65535, 2, 8192}, {1024, 3, 65520}, {0xffffffff, 2, 8192}} {
		status, err := hash.Call(ctx, args...)
		if err != nil || status[0] != 1 {
			t.Fatalf("invalid memory accepted: %v %v", status, err)
		}
	}
}
